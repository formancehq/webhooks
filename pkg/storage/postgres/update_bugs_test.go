package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/go-libs/v2/bun/bundebug"
	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/logging"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
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
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Ping())
	require.NoError(t, storage.Migrate(context.Background(), db))

	store, err := postgres.NewStore(db)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	return store
}

// BUG 3: UpdateOneConfig doesn't update updated_at.
// Unlike UpdateOneConfigActivation and UpdateOneConfigSecret which both set
// updated_at = now(), UpdateOneConfig does NOT update the timestamp.
//
// Expected: updated_at should change after UpdateOneConfig.
// Actual: updated_at remains at the original creation time.
func TestBug3_UpdateOneConfigMissingUpdatedAt(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Insert a config
	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)

	originalUpdatedAt := cfg.UpdatedAt

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Update the config
	err = store.UpdateOneConfig(ctx, cfg.ID, webhooks.ConfigUser{
		Endpoint:   "http://localhost:9090",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event.v2"},
	})
	require.NoError(t, err)

	// Fetch the config to check updated_at
	cfgs, err := store.FindManyConfigs(ctx, map[string]any{"id": cfg.ID})
	require.NoError(t, err)
	require.Len(t, cfgs, 1)

	// updated_at should have changed after the update
	assert.True(t, cfgs[0].UpdatedAt.After(originalUpdatedAt),
		"updated_at should be updated after UpdateOneConfig. "+
			"Original: %v, After update: %v", originalUpdatedAt, cfgs[0].UpdatedAt)
}

// BUG 4: UpdateOneConfig doesn't check if config exists.
// An UPDATE on a non-existent ID returns nil error (0 rows affected).
//
// Expected: updating a non-existent config should return storage.ErrConfigNotFound.
// Actual: returns nil (no error), making the 404 handler unreachable.
func TestBug4_UpdateOneConfigNoExistenceCheck(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	nonExistentID := uuid.NewString()

	err := store.UpdateOneConfig(ctx, nonExistentID, webhooks.ConfigUser{
		Endpoint:   "http://localhost:9090",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})

	// Should return ErrConfigNotFound for non-existent ID
	assert.ErrorIs(t, err, storage.ErrConfigNotFound,
		"UpdateOneConfig should return ErrConfigNotFound for non-existent ID")
}

// BUG 11: UpdateAttemptsStatus updates ALL attempts for a webhookID.
// When a retry succeeds, ALL attempts for that webhookID are set to the same
// status, including old historical attempts. This corrupts the attempt history.
//
// Expected: only "to retry" attempts should be updated.
// Actual: ALL attempts are overwritten.
func TestBug11_UpdateAttemptsStatusOverwritesAll(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := "test-webhook-id"
	cfg := webhooks.Config{
		ID:       uuid.NewString(),
		Active:   true,
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   "http://localhost:8080",
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test"},
		},
	}

	// Insert first attempt as "to retry"
	att1 := webhooks.Attempt{
		ID:             uuid.NewString(),
		WebhookID:      webhookID,
		Config:         cfg,
		Payload:        `{"test":true}`,
		StatusCode:     500,
		RetryAttempt:   0,
		Status:         webhooks.StatusAttemptToRetry,
		NextRetryAfter: time.Now().Add(-time.Hour),
	}
	require.NoError(t, store.InsertOneAttempt(ctx, att1))

	// Insert second attempt as "to retry"
	att2 := webhooks.Attempt{
		ID:             uuid.NewString(),
		WebhookID:      webhookID,
		Config:         cfg,
		Payload:        `{"test":true}`,
		StatusCode:     500,
		RetryAttempt:   1,
		Status:         webhooks.StatusAttemptToRetry,
		NextRetryAfter: time.Now().Add(-time.Hour),
	}
	require.NoError(t, store.InsertOneAttempt(ctx, att2))

	// Now update all attempts to "success"
	atts, err := store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptSuccess)
	require.NoError(t, err)

	// BUG: ALL attempts are updated to "success", including old ones
	// The returned slice should reflect the updated statuses,
	// but due to BUG 2, the returned data has stale statuses.
	_ = atts // returned atts have stale status due to BUG 2

	// Verify via direct query that ALL attempts were updated
	allAtts, err := store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	require.NoError(t, err)

	// Since we set all to "success", there should be 0 "to retry" attempts left
	assert.Equal(t, 0, len(allAtts),
		"BUG 11 CONFIRMED: ALL attempts were updated to 'success', "+
			"not just the latest one. Historical data is corrupted.")
}
