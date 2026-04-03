package postgres_test

import (
	"context"
	"testing"

	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/bun/bundebug"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestFindManyConfigs_PanicOnUnknownFilter proves that FindManyConfigs panics
// when called with an unrecognized filter key, instead of returning an error.
//
// This is a P1 bug: in production, a single malformed API request with an unknown
// query parameter would crash the entire process via an unrecovered panic.
func TestFindManyConfigs_PanicOnUnknownFilter(t *testing.T) {
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

	require.NoError(t, storage.Migrate(context.Background(), db))

	store, err := postgres.NewStore(db)
	require.NoError(t, err)
	defer func() {
		_ = store.Close(context.Background())
	}()

	// Known filters should work fine
	_, err = store.FindManyConfigs(context.Background(), map[string]any{
		"id": "some-id",
	})
	require.NoError(t, err)

	_, err = store.FindManyConfigs(context.Background(), map[string]any{
		"endpoint": "https://example.com",
	})
	require.NoError(t, err)

	_, err = store.FindManyConfigs(context.Background(), map[string]any{
		"active": true,
	})
	require.NoError(t, err)

	// Unknown filter should return an error, NOT panic.
	// Before the fix, this would panic. After the fix, it returns an error.
	_, err = store.FindManyConfigs(context.Background(), map[string]any{
		"unknown_filter": "value",
	})
	require.Error(t, err, "FindManyConfigs should return an error for unknown filter keys")
	require.Contains(t, err.Error(), "unsupported filter key")
}
