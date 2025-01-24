package postgres_test

import (
	"context"
	"testing"

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
	require.NoError(t, db.ResetModel(context.TODO(), (*webhooks.Attempt)(nil)))

	store, err := postgres.NewStore(db)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close(context.Background())
	})

	cfgs, err := store.FindManyConfigs(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Equal(t, 0, len(cfgs))

	ids, err := store.FindWebhookIDsToRetry(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, len(ids))

	atts, err := store.FindAttemptsToRetryByWebhookID(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, 0, len(atts))
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
