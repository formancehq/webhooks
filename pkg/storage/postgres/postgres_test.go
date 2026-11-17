package postgres_test

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/go-libs/v2/bun/bundebug"
	"github.com/uptrace/bun"

	"github.com/formancehq/go-libs/v2/logging"

	"github.com/formancehq/go-libs/v2/bun/bunconnect"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {

	hooks := make([]bun.QueryHook, 0)
	if testing.Verbose() {
		hooks = append(hooks, bundebug.NewQueryHook())
	}

	pgDB := srv.NewDatabase(t)
	db, err := bunconnect.OpenSQLDB(logging.TestingContext(), bunconnect.ConnectionOptions{
		DatabaseSourceName: pgDB.ConnString(),
	}, hooks...)
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	require.NoError(t, db.Ping())
	require.NoError(t, storage.Migrate(context.Background(), db))

	// Cleanup tables
	require.NoError(t, db.ResetModel(context.TODO(), (*webhooks.Config)(nil)))

	store, err := postgres.NewStore(db)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close(context.Background())
	})

	cfgs, err := store.FindManyConfigs(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Equal(t, 0, len(cfgs))

}

func TestConfigsInsert(t *testing.T) {
	hooks := make([]bun.QueryHook, 0)
	if testing.Verbose() {
		hooks = append(hooks, bundebug.NewQueryHook())
	}

	pgDB := srv.NewDatabase(t)
	db, err := bunconnect.OpenSQLDB(logging.TestingContext(), bunconnect.ConnectionOptions{
		DatabaseSourceName: pgDB.ConnString(),
	}, hooks...)
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	require.NoError(t, db.Ping())
	require.NoError(t, storage.Migrate(context.Background(), db))

	store, err := postgres.NewStore(db)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close(context.Background())
	})

	cfgUser := webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     "foo",
		EventTypes: []string{"A", "B"},
	}
	cfg, err := store.InsertOneConfig(context.Background(), cfgUser)
	require.NoError(t, err)

	cfgs, err := store.FindManyConfigs(context.Background(), map[string]any{
		"event_types": "A",
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(cfgs))

	err = store.UpdateOneConfig(context.Background(), cfg.ID, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     "foo",
		EventTypes: []string{"B"},
	})
	require.NoError(t, err)

	cfgs, err = store.FindManyConfigs(context.Background(), map[string]any{
		"event_types": "A",
	})
	require.NoError(t, err)
	require.Equal(t, 0, len(cfgs))
}

// TestUpdateOneConfigPreservesSecretWhenOmitted pins the SEC-103 update
// semantics at the SQL level: an empty ConfigUser.Secret (omitted/null/""
// in the request body) must leave the stored secret column untouched, in the
// same UPDATE statement (atomic, no read-merge race, no sentinel persisted).
func TestUpdateOneConfigPreservesSecretWhenOmitted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	original := webhooks.NewSecret()
	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "https://example.com/preserve",
		Secret:     original,
		EventTypes: []string{"A"},
	})
	require.NoError(t, err)

	// update without a secret: stored secret must be preserved
	require.NoError(t, store.UpdateOneConfig(ctx, cfg.ID, webhooks.ConfigUser{
		Endpoint:   "https://example.com/preserve2",
		EventTypes: []string{"B"},
	}))

	cfgs, err := store.FindManyConfigs(ctx, map[string]any{"id": cfg.ID})
	require.NoError(t, err)
	require.Len(t, cfgs, 1)
	require.Equal(t, original, cfgs[0].Secret, "omitted secret on update must preserve the stored secret")
	require.Equal(t, "https://example.com/preserve2", cfgs[0].Endpoint)
	require.Equal(t, []string{"B"}, cfgs[0].EventTypes)
}

// TestUpdateOneConfigReplacesSecretWhenSupplied checks that an explicit valid
// secret replaces the stored one, and that the replacement is decoded-valid
// (24 bytes after base64 decoding).
func TestUpdateOneConfigReplacesSecretWhenSupplied(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "https://example.com/replace",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"A"},
	})
	require.NoError(t, err)
	original := cfg.Secret
	require.NotEmpty(t, original)

	replacement := webhooks.NewSecret()
	require.NoError(t, store.UpdateOneConfig(ctx, cfg.ID, webhooks.ConfigUser{
		Endpoint:   "https://example.com/replace",
		Secret:     replacement,
		EventTypes: []string{"A"},
	}))

	cfgs, err := store.FindManyConfigs(ctx, map[string]any{"id": cfg.ID})
	require.NoError(t, err)
	require.Len(t, cfgs, 1)
	require.Equal(t, replacement, cfgs[0].Secret, "explicit secret on update must replace the stored one")
	require.NotEqual(t, original, cfgs[0].Secret)
}

// TestUpdateOneConfigNoSecretSentinelPersisted asserts the preservation path
// writes the existing column value back, not any sentinel string: after an
// omitted-secret update the stored value still verifies as a real HMAC key
// (base64 of exactly 24 bytes).
func TestUpdateOneConfigNoSecretSentinelPersisted(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()

	original := webhooks.NewSecret()
	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "https://example.com/sentinel",
		Secret:     original,
		EventTypes: []string{"A"},
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateOneConfig(ctx, cfg.ID, webhooks.ConfigUser{
		Endpoint:   "https://example.com/sentinel2",
		EventTypes: []string{"A"},
	}))

	var raw string
	err = db.NewSelect().
		Table("configs").Column("secret").Where("id = ?", cfg.ID).Scan(ctx, &raw)
	require.NoError(t, err)
	require.Equal(t, original, raw, "no sentinel may be persisted; the stored secret must survive verbatim")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	require.NoError(t, err)
	require.Len(t, decoded, 24, "preserved secret must remain a valid 24-byte key")
}

// pausedConfigUpdate holds the ordinary update immediately before its SQL
// write. A read-merge-write implementation would already have read a stale
// key at this point. Rotation commits before the update is released.
type pausedConfigUpdate struct {
	started chan struct{}
	release chan struct{}
}
type configUpdateContextKey struct{}

func (h pausedConfigUpdate) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if ctx.Value(configUpdateContextKey{}) == true && strings.HasPrefix(event.Query, "UPDATE ") {
		close(h.started)
		select {
		case <-h.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (pausedConfigUpdate) AfterQuery(context.Context, *bun.QueryEvent) {}

func TestConfigUpdateCannotOverwriteConcurrentRotation(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint: "https://sec103.invalid/original", Secret: webhooks.NewSecret(), EventTypes: []string{"A"},
	})
	require.NoError(t, err)
	hook := pausedConfigUpdate{started: make(chan struct{}), release: make(chan struct{})}
	db.AddQueryHook(hook)
	done := make(chan error, 1)
	go func() {
		done <- store.UpdateOneConfig(context.WithValue(ctx, configUpdateContextKey{}, true), cfg.ID,
			webhooks.ConfigUser{Endpoint: "https://sec103.invalid/updated", EventTypes: []string{"B"}})
	}()
	// Always release and join the writer, even when a failure aborts the test.
	var release sync.Once
	joined := false
	t.Cleanup(func() {
		release.Do(func() { close(hook.release) })
		if !joined {
			require.NoError(t, <-done)
		}
	})
	select {
	case <-hook.started:
	case <-ctx.Done():
		t.Fatal("ordinary update did not reach its write")
	}
	replacement := webhooks.NewSecret()
	_, err = store.UpdateOneConfigSecret(ctx, cfg.ID, replacement)
	require.NoError(t, err)
	release.Do(func() { close(hook.release) })
	err = <-done
	joined = true
	require.NoError(t, err)
	stored, err := store.FindManyConfigs(ctx, map[string]any{"id": cfg.ID})
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.True(t, stored[0].Secret == replacement, "ordinary update must retain the concurrently rotated key")
	require.Equal(t, "https://sec103.invalid/updated", stored[0].Endpoint)
}
