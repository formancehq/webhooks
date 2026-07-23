package postgres_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/go-libs/v2/publish"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func insertDeliveryConfig(t *testing.T, store storage.Store) webhooks.Config {
	t.Helper()
	config, err := store.InsertOneConfig(context.Background(), webhooks.ConfigUser{
		Endpoint: "https://example.com/webhooks", Secret: webhooks.NewSecret(), EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)
	return config
}

func newDelivery(configID, eventID, status string, createdAt time.Time) webhooks.Delivery {
	next := createdAt
	return webhooks.Delivery{
		ID: uuid.NewString(), EventID: eventID, ConfigID: configID, EventType: "test.event",
		Payload: `{"type":"test.event"}`, Status: status, NextAttemptAt: &next,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func TestDurableDeliveryLifecycleAndBrokerDeduplication(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	first := newDelivery(config.ID, "event-1", webhooks.StatusDeliveryPending, now)
	duplicate := newDelivery(config.ID, "event-1", webhooks.StatusDeliveryPending, now)
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{first}))
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{duplicate}))

	page, err := store.FindDeliveries(ctx, webhooks.DeliveryFilter{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, page.Data, 1, "the event/config uniqueness constraint must absorb broker redelivery")

	claimed, err := store.ClaimDeliveries(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, webhooks.StatusDeliveryDelivering, claimed[0].Status)

	completedAt := time.Now().UTC()
	statusCode := 200
	delivery := claimed[0]
	delivery.Status = webhooks.StatusDeliverySucceeded
	delivery.AttemptCount = 1
	delivery.CycleStartedAt = &now
	delivery.LastAttemptAt = &completedAt
	delivery.LastStatusCode = &statusCode
	delivery.NextAttemptAt = nil
	durationMillis := int64(12)
	attempt := webhooks.DeliveryAttempt{
		ID: uuid.NewString(), DeliveryID: delivery.ID, AttemptNumber: 1,
		Endpoint: config.Endpoint, Outcome: webhooks.OutcomeDeliverySucceeded,
		StatusCode: 200, DurationMillis: &durationMillis, CreatedAt: completedAt,
	}
	finalStatus, err := store.CompleteDelivery(ctx, delivery, attempt)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliverySucceeded, finalStatus)

	stored, err := store.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliverySucceeded, stored.Status)
	require.Equal(t, 1, stored.AttemptCount)
	attempts, _, err := store.FindDeliveryAttempts(ctx, delivery.ID, nil, 10)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, attempt.ID, attempts[0].ID)
	require.Equal(t, attempt.Outcome, attempts[0].Outcome)
	require.Equal(t, attempt.StatusCode, attempts[0].StatusCode)
	require.WithinDuration(t, attempt.CreatedAt, attempts[0].CreatedAt, time.Microsecond)
}

func TestEnqueueEventTargetsOnlyActiveConfigsAndDeduplicatesBrokerRedelivery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	activeConfig := insertDeliveryConfig(t, store)
	inactiveConfig := insertDeliveryConfig(t, store)
	_, err := store.UpdateOneConfigActivation(ctx, inactiveConfig.ID, false)
	require.NoError(t, err)
	_, err = store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint: "https://example.com/other", Secret: webhooks.NewSecret(), EventTypes: []string{"other.event"},
	})
	require.NoError(t, err)

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, store.EnqueueEvent(ctx, "event-enqueue", "event-key", "test.event", `{"type":"test.event"}`, createdAt))
	require.NoError(t, store.EnqueueEvent(ctx, "event-enqueue", "event-key", "test.event", `{"type":"test.event"}`, createdAt))
	page, err := store.FindDeliveries(ctx, webhooks.DeliveryFilter{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	require.Equal(t, activeConfig.ID, page.Data[0].ConfigID)
	require.Equal(t, "event-key", page.Data[0].IdempotencyKey)

	require.NoError(t, store.DeleteOneConfig(ctx, activeConfig.ID))
	require.NoError(t, store.EnqueueEvent(ctx, "event-after-delete", "event-key-2", "test.event", `{"type":"test.event"}`, createdAt))
	page, err = store.FindDeliveries(ctx, webhooks.DeliveryFilter{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, page.Data, 1, "deleted and inactive configs must not receive new deliveries")
	require.Equal(t, webhooks.StatusDeliveryCancelled, page.Data[0].Status)
}

func TestFailClaimedDeliveryEndsClaimWithoutCreatingAnAttempt(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	delivery := newDelivery(config.ID, "expired-delivery", webhooks.StatusDeliveryPending, time.Now().UTC().Add(-time.Second))
	delivery.CycleStartedAt = &delivery.CreatedAt
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))
	claimed, err := store.ClaimDeliveries(ctx, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NotNil(t, claimed[0].ClaimedAt)

	require.NoError(t, store.FailClaimedDelivery(ctx, claimed[0].ID, *claimed[0].ClaimedAt, "retry window elapsed"))
	stored, err := store.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryFailed, stored.Status)
	require.Equal(t, "retry window elapsed", stored.LastError)
	require.Nil(t, stored.ClaimedAt)
	require.Nil(t, stored.NextAttemptAt)
	require.Zero(t, stored.AttemptCount)
	attempts, _, err := store.FindDeliveryAttempts(ctx, delivery.ID, nil, 10)
	require.NoError(t, err)
	require.Empty(t, attempts)
}

func TestReplayDeliveryResetsFailedBudgetAndIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	delivery := newDelivery(config.ID, "event-replay", webhooks.StatusDeliveryFailed, now)
	delivery.AttemptCount = 15
	delivery.NextAttemptAt = nil
	delivery.ReplayGeneration = 2
	delivery.CycleStartedAt = &now
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))

	replayed, applied, err := store.ReplayDelivery(ctx, delivery.ID, "replay-key")
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, webhooks.StatusDeliveryPending, replayed.Status)
	require.Equal(t, 0, replayed.AttemptCount)
	require.Equal(t, 3, replayed.ReplayGeneration)
	require.Nil(t, replayed.CycleStartedAt)
	require.NotNil(t, replayed.NextAttemptAt)

	cached, applied, err := store.ReplayDelivery(ctx, delivery.ID, "replay-key")
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, replayed.ReplayGeneration, cached.ReplayGeneration)
	stored, err := store.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, 3, stored.ReplayGeneration, "retrying the HTTP command must not create another replay generation")

	other := newDelivery(config.ID, "event-other", webhooks.StatusDeliveryFailed, now)
	other.NextAttemptAt = nil
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{other}))
	_, _, err = store.ReplayDelivery(ctx, other.ID, "replay-key")
	require.ErrorIs(t, err, storage.ErrIdempotencyConflict)
}

func TestReplayIdempotencyKeyExpiresAfterTwentyFourHours(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	delivery := newDelivery(config.ID, "event-expired-key", webhooks.StatusDeliveryFailed, time.Now().UTC().Add(-time.Hour))
	delivery.NextAttemptAt = nil
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))
	first, applied, err := store.ReplayDelivery(ctx, delivery.ID, "expiring-key")
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, 1, first.ReplayGeneration)

	_, err = db.NewUpdate().Model((*webhooks.ReplayRequestRecord)(nil)).
		Where("key = ?", "expiring-key").Set("created_at = ?", time.Now().UTC().Add(-25*time.Hour)).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*webhooks.Delivery)(nil)).Where("id = ?", delivery.ID).
		Set("status = ?", webhooks.StatusDeliveryFailed).Set("next_attempt_at = NULL").Exec(ctx)
	require.NoError(t, err)

	second, applied, err := store.ReplayDelivery(ctx, delivery.ID, "expiring-key")
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, 2, second.ReplayGeneration, "an expired command key must execute again instead of returning the stale response")
}

func TestBulkReplayUsesStablePagesAndDifferentPendingSemantics(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	deliveries := []webhooks.Delivery{
		newDelivery(config.ID, "bulk-1", webhooks.StatusDeliveryFailed, created),
		newDelivery(config.ID, "bulk-2", webhooks.StatusDeliveryPending, created.Add(time.Second)),
		newDelivery(config.ID, "bulk-3", webhooks.StatusDeliveryFailed, created.Add(2*time.Second)),
	}
	for index := range deliveries {
		if deliveries[index].Status == webhooks.StatusDeliveryFailed {
			deliveries[index].NextAttemptAt = nil
			deliveries[index].AttemptCount = 15
		}
	}
	require.NoError(t, store.InsertDeliveries(ctx, deliveries))

	request := webhooks.ReplayDeliveriesRequest{
		CreatedAtFrom: created.Add(-time.Minute), CreatedAtTo: created.Add(time.Hour),
		Statuses: []string{webhooks.StatusDeliveryFailed, webhooks.StatusDeliveryPending}, PageSize: 2,
	}
	first, applied, err := store.ReplayDeliveries(ctx, request, "bulk-page-1")
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, 1, first.Replayed)
	require.Equal(t, 1, first.Expedited)
	require.True(t, first.HasMore)
	require.NotNil(t, first.NextCursor)
	cached, applied, err := store.ReplayDeliveries(ctx, request, "bulk-page-1")
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, first.Replayed, cached.Replayed)
	require.Equal(t, first.Expedited, cached.Expedited)
	require.Equal(t, first.NextCursorToken, cached.NextCursorToken)
	conflicting := request
	conflicting.PageSize = 1
	_, _, err = store.ReplayDeliveries(ctx, conflicting, "bulk-page-1")
	require.ErrorIs(t, err, storage.ErrIdempotencyConflict)

	request.Cursor = first.NextCursor
	second, applied, err := store.ReplayDeliveries(ctx, request, "bulk-page-2")
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, 1, second.Replayed)
	require.False(t, second.HasMore)

	failed, err := store.GetDelivery(ctx, deliveries[0].ID)
	require.NoError(t, err)
	require.Equal(t, 0, failed.AttemptCount)
	require.Equal(t, 1, failed.ReplayGeneration)
	pending, err := store.GetDelivery(ctx, deliveries[1].ID)
	require.NoError(t, err)
	require.Equal(t, 0, pending.ReplayGeneration, "expediting pending work must not reset its retry generation")
}

func TestSoftDeleteHidesConfigAndCancelsPendingDeliveries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	delivery := newDelivery(config.ID, "event-delete", webhooks.StatusDeliveryPending, time.Now().UTC())
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))
	require.NoError(t, store.DeleteOneConfig(ctx, config.ID))

	configs, err := store.FindManyConfigs(ctx, map[string]any{"id": config.ID})
	require.NoError(t, err)
	require.Empty(t, configs)
	stored, err := store.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryCancelled, stored.Status)
	_, _, err = store.ReplayDelivery(ctx, delivery.ID, "deleted-config")
	require.ErrorIs(t, err, storage.ErrDeliveryNotReplayable)
}

func TestBackfillDeliveriesPreservesLegacyWebhookIdentity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	webhookID := uuid.NewString()
	payload, err := json.Marshal(publish.EventMessage{Type: "test.event", IdempotencyKey: "event-key"})
	require.NoError(t, err)
	created := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, store.InsertOneAttempt(ctx, webhooks.Attempt{
		ID: uuid.NewString(), WebhookID: webhookID, Config: config, Payload: string(payload),
		StatusCode: 404, Status: webhooks.StatusAttemptFailed, CreatedAt: created, UpdatedAt: created,
	}))

	migrated, err := store.BackfillDeliveries(ctx, 30*24*time.Hour, 90*24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, migrated)
	delivery, err := store.GetDelivery(ctx, webhookID)
	require.NoError(t, err)
	require.Equal(t, webhookID, delivery.ID)
	require.Equal(t, "legacy:"+webhookID, delivery.EventID)
	require.Equal(t, "event-key", delivery.IdempotencyKey)
	require.Equal(t, webhooks.StatusDeliveryFailed, delivery.Status)

	migrated, err = store.BackfillDeliveries(ctx, 30*24*time.Hour, 90*24*time.Hour, 100)
	require.NoError(t, err)
	require.Zero(t, migrated, "backfill must be resumable and idempotent")
}

func TestFinalBackfillReconcilesLegacyRetryCompletedAfterInitialPass(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	webhookID := uuid.NewString()
	created := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, store.InsertOneAttempt(ctx, webhooks.Attempt{
		ID: uuid.NewString(), WebhookID: webhookID, Config: config, Payload: `{"type":"test.event"}`,
		StatusCode: 500, Status: webhooks.StatusAttemptRetrying, CreatedAt: created, UpdatedAt: created,
		NextRetryAfter: created.Add(time.Minute),
	}))

	migrated, err := store.BackfillDeliveries(ctx, 30*24*time.Hour, 90*24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, migrated)
	initial, err := store.GetDelivery(ctx, webhookID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryPending, initial.Status)

	_, err = store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptSuccess)
	require.NoError(t, err)
	migrated, err = store.BackfillDeliveries(ctx, 30*24*time.Hour, 90*24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, migrated)
	final, err := store.GetDelivery(ctx, webhookID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliverySucceeded, final.Status)
	require.Nil(t, final.NextAttemptAt)
}

func TestBackfillCancelsLegacyRetryForInactiveConfig(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	webhookID := uuid.NewString()
	created := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, store.InsertOneAttempt(ctx, webhooks.Attempt{
		ID: uuid.NewString(), WebhookID: webhookID, Config: config, Payload: `{"type":"test.event"}`,
		StatusCode: 500, Status: webhooks.StatusAttemptToRetry, CreatedAt: created, UpdatedAt: created,
		NextRetryAfter: created.Add(time.Minute),
	}))
	_, err := store.UpdateOneConfigActivation(ctx, config.ID, false)
	require.NoError(t, err)

	migrated, err := store.BackfillDeliveries(ctx, 30*24*time.Hour, 90*24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, migrated)
	delivery, err := store.GetDelivery(ctx, webhookID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryCancelled, delivery.Status)
	require.Nil(t, delivery.NextAttemptAt)
}

func TestBackfillCreatesNonReplayableTombstoneWithoutLegacySecret(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	legacySecret := config.Secret
	webhookID := uuid.NewString()
	require.NoError(t, store.InsertOneAttempt(ctx, webhooks.Attempt{
		ID: uuid.NewString(), WebhookID: webhookID, Config: config, Payload: `{"type":"test.event"}`,
		StatusCode: 500, Status: webhooks.StatusAttemptToRetry, NextRetryAfter: time.Now().UTC().Add(time.Minute),
	}))
	_, err := db.NewDelete().Model((*webhooks.Config)(nil)).Where("id = ?", config.ID).Exec(ctx)
	require.NoError(t, err)

	migrated, err := store.BackfillDeliveries(ctx, 30*24*time.Hour, 90*24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, migrated)
	tombstone := webhooks.Config{}
	require.NoError(t, db.NewSelect().Model(&tombstone).Where("id = ?", config.ID).Scan(ctx))
	require.False(t, tombstone.Active)
	require.NotNil(t, tombstone.DeletedAt)
	require.NotEqual(t, legacySecret, tombstone.Secret)
	delivery, err := store.GetDelivery(ctx, webhookID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryCancelled, delivery.Status)
	require.Nil(t, delivery.NextAttemptAt)
	_, _, err = store.ReplayDelivery(ctx, webhookID, "tombstone-replay")
	require.ErrorIs(t, err, storage.ErrDeliveryNotReplayable)
}

func TestConcurrentDeliveryClaimsNeverOverlap(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	now := time.Now().UTC().Add(-time.Second)
	deliveries := make([]webhooks.Delivery, 100)
	for index := range deliveries {
		deliveries[index] = newDelivery(config.ID, uuid.NewString(), webhooks.StatusDeliveryPending, now)
	}
	require.NoError(t, store.InsertDeliveries(ctx, deliveries))

	var wait sync.WaitGroup
	ids := make(chan string, len(deliveries))
	for range 10 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			claimed, err := store.ClaimDeliveries(ctx, 10)
			require.NoError(t, err)
			for _, delivery := range claimed {
				ids <- delivery.ID
			}
		}()
	}
	wait.Wait()
	close(ids)
	seen := map[string]struct{}{}
	for id := range ids {
		_, exists := seen[id]
		require.False(t, exists, "one delivery was claimed by multiple workers")
		seen[id] = struct{}{}
	}
	require.Len(t, seen, len(deliveries))
}

func TestDeliveryCompletionRollsBackAttemptWhenClaimWasLost(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	delivery := newDelivery(config.ID, "lost-claim", webhooks.StatusDeliveryPending, time.Now().UTC())
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))
	attempt := webhooks.DeliveryAttempt{
		ID: uuid.NewString(), DeliveryID: delivery.ID, AttemptNumber: 1,
		Endpoint: config.Endpoint, Outcome: webhooks.OutcomeDeliverySucceeded, StatusCode: 200,
	}
	delivery.Status = webhooks.StatusDeliverySucceeded
	_, err := store.CompleteDelivery(ctx, delivery, attempt)
	require.ErrorIs(t, err, storage.ErrDeliveryNotFound)
	attempts, _, err := store.FindDeliveryAttempts(ctx, delivery.ID, nil, 10)
	require.NoError(t, err)
	require.Empty(t, attempts, "attempt insert and delivery transition must share one transaction")
}

func TestStaleWorkerCannotCompleteAReclaimedDelivery(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	delivery := newDelivery(config.ID, "reclaimed-delivery", webhooks.StatusDeliveryPending, time.Now().UTC().Add(-time.Second))
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))
	firstClaim, err := store.ClaimDeliveries(ctx, 1)
	require.NoError(t, err)
	require.Len(t, firstClaim, 1)
	require.NotNil(t, firstClaim[0].CycleStartedAt)
	cycleStartedAt := *firstClaim[0].CycleStartedAt

	staleClaimedAt := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond)
	_, err = db.NewUpdate().Model((*webhooks.Delivery)(nil)).Where("id = ?", delivery.ID).
		Set("claimed_at = ?", staleClaimedAt).Exec(ctx)
	require.NoError(t, err)
	firstClaim[0].ClaimedAt = &staleClaimedAt
	_, err = store.RecoverStaleDeliveries(ctx, 5*time.Minute)
	require.NoError(t, err)
	secondClaim, err := store.ClaimDeliveries(ctx, 1)
	require.NoError(t, err)
	require.Len(t, secondClaim, 1)
	require.NotEqual(t, *firstClaim[0].ClaimedAt, *secondClaim[0].ClaimedAt)
	require.NotNil(t, secondClaim[0].CycleStartedAt)
	require.Equal(t, cycleStartedAt, *secondClaim[0].CycleStartedAt, "recovery and re-claim must not reset the retry window")

	completedAt := time.Now().UTC()
	stale := firstClaim[0]
	stale.Status = webhooks.StatusDeliverySucceeded
	stale.AttemptCount = 1
	stale.LastAttemptAt = &completedAt
	stale.NextAttemptAt = nil
	staleAttempt := webhooks.DeliveryAttempt{
		ID: uuid.NewString(), DeliveryID: stale.ID, AttemptNumber: 1,
		Endpoint: config.Endpoint, Outcome: webhooks.OutcomeDeliverySucceeded, StatusCode: 200,
	}
	_, err = store.CompleteDelivery(ctx, stale, staleAttempt)
	require.ErrorIs(t, err, storage.ErrDeliveryNotFound)

	fresh := secondClaim[0]
	fresh.Status = webhooks.StatusDeliverySucceeded
	fresh.AttemptCount = 1
	fresh.LastAttemptAt = &completedAt
	fresh.NextAttemptAt = nil
	freshAttempt := staleAttempt
	freshAttempt.ID = uuid.NewString()
	finalStatus, err := store.CompleteDelivery(ctx, fresh, freshAttempt)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliverySucceeded, finalStatus)
	attempts, _, err := store.FindDeliveryAttempts(ctx, delivery.ID, nil, 10)
	require.NoError(t, err)
	require.Len(t, attempts, 1, "the stale worker attempt must have rolled back")
	require.Equal(t, freshAttempt.ID, attempts[0].ID)
}

func TestInFlightFailureBecomesCancelledWhenConfigWasDeactivated(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	delivery := newDelivery(config.ID, "deactivated-in-flight", webhooks.StatusDeliveryPending, time.Now().UTC().Add(-time.Second))
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))
	claimed, err := store.ClaimDeliveries(ctx, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = store.UpdateOneConfigActivation(ctx, config.ID, false)
	require.NoError(t, err)

	now := time.Now().UTC()
	retryAt := now.Add(time.Minute)
	failed := claimed[0]
	failed.Status = webhooks.StatusDeliveryPending
	failed.AttemptCount = 1
	failed.LastAttemptAt = &now
	failed.NextAttemptAt = &retryAt
	attempt := webhooks.DeliveryAttempt{
		ID: uuid.NewString(), DeliveryID: failed.ID, AttemptNumber: 1,
		Endpoint: config.Endpoint, Outcome: webhooks.OutcomeDeliveryRetryableFailure, StatusCode: 500,
	}
	finalStatus, err := store.CompleteDelivery(ctx, failed, attempt)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryCancelled, finalStatus)
	stored, err := store.GetDelivery(ctx, failed.ID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryCancelled, stored.Status)
	require.Nil(t, stored.NextAttemptAt)
}

func TestRecoverStaleDeliveriesRestoresActiveAndCancelsInactiveClaims(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()
	activeConfig := insertDeliveryConfig(t, store)
	inactiveConfig := insertDeliveryConfig(t, store)
	recentConfig := insertDeliveryConfig(t, store)
	now := time.Now().UTC().Add(-time.Second)
	deliveries := []webhooks.Delivery{
		newDelivery(activeConfig.ID, "stale-active", webhooks.StatusDeliveryPending, now),
		newDelivery(inactiveConfig.ID, "stale-inactive", webhooks.StatusDeliveryPending, now),
		newDelivery(recentConfig.ID, "recent-active", webhooks.StatusDeliveryPending, now),
	}
	require.NoError(t, store.InsertDeliveries(ctx, deliveries))
	claimed, err := store.ClaimDeliveries(ctx, 3)
	require.NoError(t, err)
	require.Len(t, claimed, 3)
	_, err = store.UpdateOneConfigActivation(ctx, inactiveConfig.ID, false)
	require.NoError(t, err)
	staleCutoff := time.Now().UTC().Add(-10 * time.Minute)
	_, err = db.NewUpdate().Model((*webhooks.Delivery)(nil)).
		Where("event_id IN (?)", bun.List([]string{"stale-active", "stale-inactive"})).
		Set("claimed_at = ?", staleCutoff).Exec(ctx)
	require.NoError(t, err)

	recovered, err := store.RecoverStaleDeliveries(ctx, 5*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 2, recovered)
	active, err := store.GetDelivery(ctx, deliveries[0].ID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryPending, active.Status)
	inactive, err := store.GetDelivery(ctx, deliveries[1].ID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryCancelled, inactive.Status)
	recent, err := store.GetDelivery(ctx, deliveries[2].ID)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusDeliveryDelivering, recent.Status)
}

func TestDeliveryRetentionCascadesAttemptsAndPurgesDeletedConfig(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()
	config := insertDeliveryConfig(t, store)
	created := time.Now().UTC().Add(-48 * time.Hour)
	delivery := newDelivery(config.ID, "retention", webhooks.StatusDeliverySucceeded, created)
	delivery.NextAttemptAt = nil
	delivery.UpdatedAt = created
	require.NoError(t, store.InsertDeliveries(ctx, []webhooks.Delivery{delivery}))
	duration := int64(1)
	_, err := db.NewInsert().Model(&webhooks.DeliveryAttempt{
		ID: uuid.NewString(), DeliveryID: delivery.ID, AttemptNumber: 1, Endpoint: config.Endpoint,
		Outcome: webhooks.OutcomeDeliverySucceeded, StatusCode: 200, DurationMillis: &duration, CreatedAt: created,
	}).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, store.DeleteOneConfig(ctx, config.ID))

	purged, err := store.PurgeFinishedDeliveries(ctx, 24*time.Hour, 90*24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, purged)
	_, err = store.GetDelivery(ctx, delivery.ID)
	require.ErrorIs(t, err, storage.ErrDeliveryNotFound)
	var attemptCount int
	attemptCount, err = db.NewSelect().Model((*webhooks.DeliveryAttempt)(nil)).Where("delivery_id = ?", delivery.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, attemptCount)
	var configCount int
	configCount, err = db.NewSelect().Model((*webhooks.Config)(nil)).Where("id = ?", config.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, configCount)
}
