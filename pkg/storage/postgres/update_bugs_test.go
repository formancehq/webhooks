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

// TestBug11_UpdateAttemptsStatusOnlyUpdatesToRetry verifies that
// UpdateAttemptsStatus only updates attempts with status "to retry",
// leaving historical attempts (e.g. "success") untouched.
func TestBug11_UpdateAttemptsStatusOnlyUpdatesToRetry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	webhookID := "test-webhook-id"
	cfg := webhooks.Config{
		ID:     uuid.NewString(),
		Active: true,
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   "http://localhost:8080",
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test"},
		},
	}

	// Insert a historical "success" attempt (should NOT be touched)
	attSuccess := webhooks.Attempt{
		ID:           uuid.NewString(),
		WebhookID:    webhookID,
		Config:       cfg,
		Payload:      `{"test":true}`,
		StatusCode:   200,
		RetryAttempt: 0,
		Status:       webhooks.StatusAttemptSuccess,
	}
	require.NoError(t, store.InsertOneAttempt(ctx, attSuccess))

	// Insert a "to retry" attempt (should be updated)
	attRetry := webhooks.Attempt{
		ID:             uuid.NewString(),
		WebhookID:      webhookID,
		Config:         cfg,
		Payload:        `{"test":true}`,
		StatusCode:     500,
		RetryAttempt:   1,
		Status:         webhooks.StatusAttemptToRetry,
		NextRetryAfter: time.Now().Add(-time.Hour),
	}
	require.NoError(t, store.InsertOneAttempt(ctx, attRetry))

	// Update attempts status to "success" — only "to retry" should change
	atts, err := store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptSuccess)
	require.NoError(t, err)

	// Only the "to retry" attempt should be returned and updated
	require.Len(t, atts, 1, "should only return the 'to retry' attempt")
	assert.Equal(t, webhooks.StatusAttemptSuccess, atts[0].Status)
	assert.Equal(t, attRetry.ID, atts[0].ID)
}
