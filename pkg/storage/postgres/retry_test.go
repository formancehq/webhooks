package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/go-libs/v2/bun/bundebug"
	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) storage.Store {
	t.Helper()

	hooks := make([]bun.QueryHook, 0)
	if testing.Verbose() {
		hooks = append(hooks, bundebug.NewQueryHook())
	}

	pgDB := srv.NewDatabase(t)
	db, err := bunconnect.OpenSQLDB(logging.TestingContext(), bunconnect.ConnectionOptions{
		DatabaseSourceName: pgDB.ConnString(),
	}, hooks...)
	require.NoError(t, err)

	require.NoError(t, db.Ping())
	require.NoError(t, storage.Migrate(context.Background(), db))

	store, err := postgres.NewStore(db)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close(context.Background())
	})

	return store
}

func insertConfigAndAttempt(t *testing.T, db storage.Store, webhookID, status string, nextRetryAfter time.Time) webhooks.Config {
	t.Helper()
	ctx := context.Background()

	cfg, err := db.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)

	insertAttemptWithConfig(t, db, webhookID, cfg, status, 1, nextRetryAfter)

	return cfg
}

func insertAttemptWithConfig(t *testing.T, db storage.Store, webhookID string, cfg webhooks.Config, status string, retryAttempt int, nextRetryAfter time.Time) {
	t.Helper()
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"type": "test.event"})

	att := webhooks.Attempt{
		ID:             uuid.NewString(),
		WebhookID:      webhookID,
		Config:         cfg,
		Payload:        string(payload),
		StatusCode:     500,
		RetryAttempt:   retryAttempt,
		Status:         status,
		NextRetryAfter: nextRetryAfter,
	}
	require.NoError(t, db.InsertOneAttempt(ctx, att))
}

func TestClaimWebhookIDsToRetry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID1 := uuid.NewString()
	webhookID2 := uuid.NewString()
	webhookID3 := uuid.NewString()

	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert 3 webhooks with "to retry" status and past next_retry_after
	insertConfigAndAttempt(t, store, webhookID1, webhooks.StatusAttemptToRetry, pastTime)
	insertConfigAndAttempt(t, store, webhookID2, webhooks.StatusAttemptToRetry, pastTime)
	insertConfigAndAttempt(t, store, webhookID3, webhooks.StatusAttemptToRetry, pastTime)

	// Claim with limit 2 -- should only get 2
	ids, err := store.FindWebhookIDsToRetry(ctx, 2)
	require.NoError(t, err)
	require.Len(t, ids, 2)

	// Claimed attempts should now be in "retrying" status
	atts, err := store.FindAttemptsToRetryByWebhookID(ctx, ids[0])
	require.NoError(t, err)
	require.Len(t, atts, 1)

	// Second claim should get the remaining 1
	ids2, err := store.FindWebhookIDsToRetry(ctx, 2)
	require.NoError(t, err)
	require.Len(t, ids2, 1)

	// No overlap between first and second claim
	for _, id := range ids2 {
		require.NotContains(t, ids, id)
	}

	// Third claim should return empty
	ids3, err := store.FindWebhookIDsToRetry(ctx, 2)
	require.NoError(t, err)
	require.Len(t, ids3, 0)
}

func TestClaimRespectsNextRetryAfter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookIDPast := uuid.NewString()
	webhookIDFuture := uuid.NewString()

	pastTime := time.Now().UTC().Add(-10 * time.Minute)
	futureTime := time.Now().UTC().Add(10 * time.Minute)

	// One attempt eligible, one not yet
	insertConfigAndAttempt(t, store, webhookIDPast, webhooks.StatusAttemptToRetry, pastTime)
	insertConfigAndAttempt(t, store, webhookIDFuture, webhooks.StatusAttemptToRetry, futureTime)

	ids, err := store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	require.Equal(t, webhookIDPast, ids[0])
}

func TestUpdateAttemptsStatusOnlyUpdatesRetrying(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := uuid.NewString()
	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert two attempts: one "retrying", one "success"
	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)

	payload, _ := json.Marshal(map[string]string{"type": "test.event"})

	retryingAtt := webhooks.Attempt{
		ID:             uuid.NewString(),
		WebhookID:      webhookID,
		Config:         cfg,
		Payload:        string(payload),
		StatusCode:     500,
		RetryAttempt:   2,
		Status:         webhooks.StatusAttemptRetrying,
		NextRetryAfter: pastTime,
	}
	successAtt := webhooks.Attempt{
		ID:           uuid.NewString(),
		WebhookID:    webhookID,
		Config:       cfg,
		Payload:      string(payload),
		StatusCode:   200,
		RetryAttempt: 0,
		Status:       webhooks.StatusAttemptSuccess,
	}
	require.NoError(t, store.InsertOneAttempt(ctx, retryingAtt))
	require.NoError(t, store.InsertOneAttempt(ctx, successAtt))

	// Update retrying attempts to failed
	_, err = store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptFailed)
	require.NoError(t, err)

	// The "retrying" attempt should now be "failed"
	// The "success" attempt should still be "success"
	// We verify by trying to find retrying attempts -- should be 0
	atts, err := store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	require.NoError(t, err)
	require.Len(t, atts, 0)
}

func TestRecoverStaleRetryingAttempts(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := uuid.NewString()
	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert config and a "to retry" attempt
	insertConfigAndAttempt(t, store, webhookID, webhooks.StatusAttemptToRetry, pastTime)

	// Claim it
	ids, err := store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	// Verify it's now "retrying"
	atts, err := store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	require.NoError(t, err)
	require.Len(t, atts, 1)

	// Recovery with short duration should NOT recover (updated_at is recent)
	err = store.RecoverStaleRetryingAttempts(ctx, 5*time.Minute)
	require.NoError(t, err)

	// Still retrying
	atts, err = store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	require.NoError(t, err)
	require.Len(t, atts, 1)

	// Recovery with zero duration should recover everything
	err = store.RecoverStaleRetryingAttempts(ctx, 0)
	require.NoError(t, err)

	// No longer retrying
	atts, err = store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	require.NoError(t, err)
	require.Len(t, atts, 0)

	// Should be claimable again
	ids, err = store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 1)
}

func TestClaimMultipleAttemptsPerWebhook(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := uuid.NewString()
	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert one config, then 3 "to retry" attempts for the same webhook
	cfg := insertConfigAndAttempt(t, store, webhookID, webhooks.StatusAttemptToRetry, pastTime)
	insertAttemptWithConfig(t, store, webhookID, cfg, webhooks.StatusAttemptToRetry, 2, pastTime)
	insertAttemptWithConfig(t, store, webhookID, cfg, webhooks.StatusAttemptToRetry, 3, pastTime)

	// Claim
	ids, err := store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	// All 3 attempts should now be "retrying"
	atts, err := store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	require.NoError(t, err)
	require.Len(t, atts, 3)
}

func TestClaimOnlyToRetryNotOtherStatuses(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := uuid.NewString()
	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert config with one "to retry" and one "success" attempt for same webhook
	cfg := insertConfigAndAttempt(t, store, webhookID, webhooks.StatusAttemptToRetry, pastTime)
	insertAttemptWithConfig(t, store, webhookID, cfg, webhooks.StatusAttemptSuccess, 0, time.Time{})

	// Claim
	ids, err := store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	// Only the "to retry" attempt should be "retrying", not the "success" one
	atts, err := store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	require.NoError(t, err)
	require.Len(t, atts, 1)
}

func TestFullRetryLifecycle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := uuid.NewString()
	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Step 1: Insert a "to retry" attempt
	insertConfigAndAttempt(t, store, webhookID, webhooks.StatusAttemptToRetry, pastTime)

	// Step 2: Claim it
	ids, err := store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	// Step 3: Simulate retry failure -- update status back to "to retry"
	_, err = store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptToRetry)
	require.NoError(t, err)

	// Step 4: Should be claimable again
	ids, err = store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	// Step 5: Simulate retry success
	_, err = store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptSuccess)
	require.NoError(t, err)

	// Step 6: Should NOT be claimable anymore
	ids, err = store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 0)
}

func TestClaimIgnoresDeletedConfig(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := uuid.NewString()
	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert config + attempt, then delete the config
	cfg := insertConfigAndAttempt(t, store, webhookID, webhooks.StatusAttemptToRetry, pastTime)
	err := store.DeleteOneConfig(ctx, cfg.ID)
	require.NoError(t, err)

	// Should NOT be claimable (config no longer exists, JOIN fails)
	ids, err := store.FindWebhookIDsToRetry(ctx, 50)
	require.NoError(t, err)
	require.Len(t, ids, 0)
}

func TestConcurrentGoroutineClaimsNoDuplicates(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert 20 webhooks
	for range 20 {
		insertConfigAndAttempt(t, store, uuid.NewString(), webhooks.StatusAttemptToRetry, pastTime)
	}

	// Run 4 goroutines claiming concurrently -- verify no duplicates
	type result struct {
		ids []string
		err error
	}
	ch := make(chan result, 4)

	for range 4 {
		go func() {
			ids, err := store.FindWebhookIDsToRetry(ctx, 10)
			ch <- result{ids: ids, err: err}
		}()
	}

	allClaimed := make(map[string]bool)
	for range 4 {
		res := <-ch
		require.NoError(t, res.err)
		for _, id := range res.ids {
			require.False(t, allClaimed[id], "webhook %s was claimed by multiple goroutines", id)
			allClaimed[id] = true
		}
	}

	// At least some were claimed (concurrent workers may overlap on snapshot)
	require.Greater(t, len(allClaimed), 0)
	// No duplicates (the key invariant)
	// The rest will be claimed in subsequent ticks

	// Drain remaining by sequential claims
	for {
		ids, err := store.FindWebhookIDsToRetry(ctx, 50)
		require.NoError(t, err)
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			require.False(t, allClaimed[id], "webhook %s was claimed twice across rounds", id)
			allClaimed[id] = true
		}
	}

	// All 20 webhooks claimed exactly once across all rounds
	require.Len(t, allClaimed, 20)
}

func TestConcurrentClaimsNoOverlap(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	pastTime := time.Now().UTC().Add(-10 * time.Minute)

	// Insert 10 webhooks
	webhookIDs := make([]string, 10)
	for i := range webhookIDs {
		webhookIDs[i] = uuid.NewString()
		insertConfigAndAttempt(t, store, webhookIDs[i], webhooks.StatusAttemptToRetry, pastTime)
	}

	// Simulate concurrent claims by doing sequential claims of 3
	allClaimed := make(map[string]bool)

	for {
		ids, err := store.FindWebhookIDsToRetry(ctx, 3)
		require.NoError(t, err)
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			require.False(t, allClaimed[id], "webhook %s was claimed twice", id)
			allClaimed[id] = true
		}
	}

	// All 10 webhooks should have been claimed exactly once
	require.Len(t, allClaimed, 10)
}
