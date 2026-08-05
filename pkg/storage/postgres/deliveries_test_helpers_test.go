package postgres_test

import (
	"context"
	"testing"

	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/bun/bundebug"
	"github.com/formancehq/go-libs/v2/logging"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type testStore struct {
	storage.Store
	db *bun.DB
}

func (s testStore) InsertDeliveries(ctx context.Context, deliveries []webhooks.Delivery) error {
	if len(deliveries) == 0 {
		return nil
	}
	_, err := s.db.NewInsert().Model(&deliveries).
		On("CONFLICT (event_id, config_id) DO NOTHING").Exec(ctx)
	return err
}

func newTestStore(t *testing.T) testStore {
	t.Helper()
	store, _ := newTestStoreWithDB(t)
	return store
}

func newTestStoreWithDB(t *testing.T) (testStore, *bun.DB) {
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

	productionStore, err := postgres.NewStore(db)
	require.NoError(t, err)
	store := testStore{Store: productionStore, db: db}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	return store, db
}
