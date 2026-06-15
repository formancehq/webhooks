package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/stretchr/testify/require"
)

// remainingAttemptIDs returns the set of attempt IDs still present in the table.
func remainingAttemptIDs(t *testing.T, db *bun.DB) map[string]struct{} {
	t.Helper()

	atts := []webhooks.Attempt{}
	require.NoError(t, db.NewSelect().Model(&atts).Scan(context.Background()))
	ids := make(map[string]struct{}, len(atts))
	for _, att := range atts {
		ids[att.ID] = struct{}{}
	}
	return ids
}

// attemptStatusesByID returns attempt ID -> status for all rows.
func attemptStatusesByID(t *testing.T, db *bun.DB) map[string]string {
	t.Helper()

	atts := []webhooks.Attempt{}
	require.NoError(t, db.NewSelect().Model(&atts).Scan(context.Background()))
	statuses := make(map[string]string, len(atts))
	for _, att := range atts {
		statuses[att.ID] = att.Status
	}
	return statuses
}

// insertAttemptAged inserts an attempt with an explicit updated_at so retention
// cutoffs can be exercised deterministically.
func insertAttemptAged(t *testing.T, store storage.Store, cfg webhooks.Config, status string, updatedAt time.Time) string {
	t.Helper()

	payload, _ := json.Marshal(map[string]string{"type": "test.event"})
	att := webhooks.Attempt{
		ID:           uuid.NewString(),
		WebhookID:    uuid.NewString(),
		Config:       cfg,
		Payload:      string(payload),
		StatusCode:   500,
		RetryAttempt: 1,
		Status:       status,
		UpdatedAt:    updatedAt,
	}
	if status == webhooks.StatusAttemptToRetry {
		att.NextRetryAfter = time.Now().UTC().Add(time.Minute)
	}
	require.NoError(t, store.InsertOneAttempt(context.Background(), att))
	return att.ID
}

func insertConfig(t *testing.T, store storage.Store) webhooks.Config {
	t.Helper()

	cfg, err := store.InsertOneConfig(context.Background(), webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)
	return cfg
}

func countAttemptsByStatus(t *testing.T, store storage.Store, status string) int {
	t.Helper()

	// Reuse the storage API instead of raw SQL: FindAttemptsToRetryByWebhookID
	// is webhook-scoped, so go through CountAttemptsToRetry for 'to retry' and
	// a generic count via UpdateAttemptsStatus is not suitable — use the bun DB
	// from the store-level helper instead.
	switch status {
	case webhooks.StatusAttemptToRetry:
		n, err := store.CountAttemptsToRetry(context.Background())
		require.NoError(t, err)
		return int(n)
	default:
		t.Fatalf("unsupported status for countAttemptsByStatus: %s", status)
		return 0
	}
}

func TestPurgeFinishedAttempts(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()
	cfg := insertConfig(t, store)

	now := time.Now().UTC()
	old := now.Add(-72 * time.Hour)

	oldSuccess := insertAttemptAged(t, store, cfg, webhooks.StatusAttemptSuccess, old)
	recentSuccess := insertAttemptAged(t, store, cfg, webhooks.StatusAttemptSuccess, now)
	oldFailed := insertAttemptAged(t, store, cfg, webhooks.StatusAttemptFailed, old)
	recentFailed := insertAttemptAged(t, store, cfg, webhooks.StatusAttemptFailed, now)
	oldToRetry := insertAttemptAged(t, store, cfg, webhooks.StatusAttemptToRetry, old)

	deleted, err := store.PurgeFinishedAttempts(ctx, 24*time.Hour, 24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted, "only the old success and old failed attempts should be purged")

	remaining := remainingAttemptIDs(t, db)
	require.NotContains(t, remaining, oldSuccess)
	require.NotContains(t, remaining, oldFailed)
	require.Contains(t, remaining, recentSuccess)
	require.Contains(t, remaining, recentFailed)
	require.Contains(t, remaining, oldToRetry, "pending attempts must never be purged regardless of age")
}

func TestPurgeFinishedAttemptsDisabledRetention(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()
	cfg := insertConfig(t, store)

	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	oldSuccess := insertAttemptAged(t, store, cfg, webhooks.StatusAttemptSuccess, old)
	oldFailed := insertAttemptAged(t, store, cfg, webhooks.StatusAttemptFailed, old)

	// success retention disabled, failed retention active
	deleted, err := store.PurgeFinishedAttempts(ctx, 0, 24*time.Hour, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)

	remaining := remainingAttemptIDs(t, db)
	require.Contains(t, remaining, oldSuccess, "a retention <= 0 must disable purging for that status")
	require.NotContains(t, remaining, oldFailed)
}

func TestPurgeFinishedAttemptsBatches(t *testing.T) {
	store, _ := newTestStoreWithDB(t)
	ctx := context.Background()
	cfg := insertConfig(t, store)

	old := time.Now().UTC().Add(-72 * time.Hour)
	for range 5 {
		insertAttemptAged(t, store, cfg, webhooks.StatusAttemptSuccess, old)
	}

	// batchSize smaller than the backlog: each run consumes only one batch.
	deleted, err := store.PurgeFinishedAttempts(ctx, 24*time.Hour, 24*time.Hour, 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted)

	deleted, err = store.PurgeFinishedAttempts(ctx, 24*time.Hour, 24*time.Hour, 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted)

	deleted, err = store.PurgeFinishedAttempts(ctx, 24*time.Hour, 24*time.Hour, 2)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
}

func TestRetentionIndexesExist(t *testing.T) {
	_, db := newTestStoreWithDB(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		status string
	}{
		{"idx_attempts_retention_success", "success"},
		{"idx_attempts_retention_failed", "failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var indexDef string
			err := db.NewRaw(`
				SELECT indexdef
				FROM pg_indexes
				WHERE tablename = 'attempts' AND indexname = ?
			`, tc.name).Scan(ctx, &indexDef)
			require.NoError(t, err)
			require.Contains(t, indexDef, "updated_at")
			require.Contains(t, indexDef, "id")
			require.Contains(t, indexDef, "WHERE")
			require.Contains(t, indexDef, tc.status)
		})
	}

	t.Run("idx_attempts_first_attempt_lookup", func(t *testing.T) {
		var indexDef string
		err := db.NewRaw(`
			SELECT indexdef
			FROM pg_indexes
			WHERE tablename = 'attempts' AND indexname = ?
		`, "idx_attempts_first_attempt_lookup").Scan(ctx, &indexDef)
		require.NoError(t, err)
		require.Contains(t, indexDef, "webhook_id")
		require.Contains(t, indexDef, "created_at")
	})
}

func TestFailUnclaimableAttempts(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	stale := now.Add(-10 * time.Minute)

	// Live config: its pending attempts must be preserved.
	liveCfg := insertConfig(t, store)
	liveToRetry := insertAttemptAged(t, store, liveCfg, webhooks.StatusAttemptToRetry, now)

	// Inactive config: retry claim skips it, so its pending attempts must be
	// failed by retention instead of staying in the retry queue forever.
	inactiveCfg := insertConfig(t, store)
	_, err := store.UpdateOneConfigActivation(ctx, inactiveCfg.ID, false)
	require.NoError(t, err)
	inactiveToRetry := insertAttemptAged(t, store, inactiveCfg, webhooks.StatusAttemptToRetry, now)
	inactiveRetrying := insertAttemptAged(t, store, inactiveCfg, webhooks.StatusAttemptRetrying, stale)
	inactiveRetryingRecent := insertAttemptAged(t, store, inactiveCfg, webhooks.StatusAttemptRetrying, now)
	inactiveSuccess := insertAttemptAged(t, store, inactiveCfg, webhooks.StatusAttemptSuccess, now)

	// Orphan config: never persisted, simulating a deleted config snapshotted
	// in the attempt's JSONB column.
	orphanCfg := webhooks.NewConfig(webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	orphanToRetry := insertAttemptAged(t, store, orphanCfg, webhooks.StatusAttemptToRetry, now)
	orphanRetrying := insertAttemptAged(t, store, orphanCfg, webhooks.StatusAttemptRetrying, stale)
	orphanRetryingRecent := insertAttemptAged(t, store, orphanCfg, webhooks.StatusAttemptRetrying, now)
	orphanSuccess := insertAttemptAged(t, store, orphanCfg, webhooks.StatusAttemptSuccess, now)

	updated, err := store.FailUnclaimableAttempts(ctx, 100, 5*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 4, updated, "only pending to-retry and stale retrying inactive/deleted config attempts should be failed")

	statuses := attemptStatusesByID(t, db)
	require.Equal(t, webhooks.StatusAttemptToRetry, statuses[liveToRetry], "live config attempts must be preserved")
	require.Equal(t, webhooks.StatusAttemptFailed, statuses[inactiveToRetry])
	require.Equal(t, webhooks.StatusAttemptFailed, statuses[inactiveRetrying])
	require.Equal(t, webhooks.StatusAttemptRetrying, statuses[inactiveRetryingRecent], "recent retrying rows may still be in-flight")
	require.Equal(t, webhooks.StatusAttemptSuccess, statuses[inactiveSuccess], "terminal inactive-config attempts must not be rewritten")
	require.Equal(t, webhooks.StatusAttemptFailed, statuses[orphanToRetry])
	require.Equal(t, webhooks.StatusAttemptFailed, statuses[orphanRetrying])
	require.Equal(t, webhooks.StatusAttemptRetrying, statuses[orphanRetryingRecent], "recent retrying rows may still be in-flight")
	require.Equal(t, webhooks.StatusAttemptSuccess, statuses[orphanSuccess], "terminal orphan attempts must not be rewritten")

	require.Equal(t, 1, countAttemptsByStatus(t, store, webhooks.StatusAttemptToRetry))
}
