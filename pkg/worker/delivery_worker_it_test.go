//go:build it

package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/publish"
	"github.com/formancehq/go-libs/v2/testing/docker"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func newWorkerIntegrationStore(t *testing.T) (storage.Store, *bun.DB) {
	t.Helper()
	server := pgtesting.CreatePostgresServer(t, docker.NewPool(t, logging.Testing()))
	database := server.NewDatabase(t)
	db, err := bunconnect.OpenSQLDB(logging.TestingContext(), database.ConnectionOptions())
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	require.NoError(t, storage.Migrate(context.Background(), db))
	store, err := postgres.NewStore(db)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	return store, db
}

func deliveryEventMessage(t *testing.T, id string) *message.Message {
	t.Helper()
	payload, err := json.Marshal(publish.EventMessage{Type: "test.event", IdempotencyKey: id + "-key"})
	require.NoError(t, err)
	return message.NewMessage(id, payload)
}

func TestDeliveryRouterAcknowledgesAfterPostgresCommitAndNacksOnRollback(t *testing.T) {
	ctx := context.Background()
	store, db := newWorkerIntegrationStore(t)
	_, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint: "https://example.com/webhooks", Secret: webhooks.NewSecret(), EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)
	subscriber := runDeliveryRouter(t, store)

	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = blocker.Rollback() })
	_, err = blocker.ExecContext(ctx, "LOCK TABLE deliveries IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)
	committedMessage := deliveryEventMessage(t, "committed-event")
	subscriber.messages <- committedMessage
	require.Eventually(t, func() bool {
		var waiting int
		err := db.NewRaw(`
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND wait_event_type = 'Lock'
			  AND query ILIKE '%insert%deliveries%'
		`).Scan(ctx, &waiting)
		return err == nil && waiting > 0
	}, 5*time.Second, 20*time.Millisecond, "delivery insert never reached the database lock")
	select {
	case <-committedMessage.Acked():
		t.Fatal("message was acknowledged before the PostgreSQL transaction could commit")
	case <-committedMessage.Nacked():
		t.Fatal("message was nacked while the PostgreSQL transaction was waiting to commit")
	default:
	}
	require.NoError(t, blocker.Commit())
	select {
	case <-committedMessage.Acked():
	case <-time.After(5 * time.Second):
		t.Fatal("message was not acknowledged after the PostgreSQL transaction committed")
	}
	deliveries, err := db.NewSelect().Model((*webhooks.Delivery)(nil)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, deliveries)

	_, err = db.ExecContext(ctx, `
		CREATE FUNCTION reject_delivery_insert() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'forced delivery insert rollback';
		END;
		$$ LANGUAGE plpgsql
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		CREATE TRIGGER reject_delivery_insert
		BEFORE INSERT ON deliveries
		FOR EACH ROW EXECUTE FUNCTION reject_delivery_insert()
	`)
	require.NoError(t, err)
	rolledBackMessage := deliveryEventMessage(t, "rolled-back-event")
	subscriber.messages <- rolledBackMessage
	select {
	case <-rolledBackMessage.Nacked():
	case <-time.After(5 * time.Second):
		t.Fatal("message was not nacked after the PostgreSQL transaction rolled back")
	}
	deliveries, err = db.NewSelect().Model((*webhooks.Delivery)(nil)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, deliveries, "the rolled-back delivery must not be persisted")
}
