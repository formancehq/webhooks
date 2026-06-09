package postgres_test

import (
	"context"
	"testing"
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func TestDeliveryCircuitCreatedClosedByDefault(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()

	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)

	decision, err := store.BeforeDelivery(ctx, cfg.ID)
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, webhooks.CircuitDecisionReasonClosed, decision.Reason)

	circuit := selectDeliveryCircuit(t, db, cfg.ID)
	require.Equal(t, webhooks.CircuitStateClosed, circuit.State)
	require.Equal(t, 0, circuit.ConsecutiveFailures)
	require.Equal(t, 0, circuit.ProbeAttempt)
}

func TestDeliveryCircuitOpensAfterFailureThreshold(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()

	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)

	for range webhooks.DefaultCircuitFailureThreshold - 1 {
		require.NoError(t, store.RecordFailure(ctx, cfg.ID, webhooks.DeliveryFailure{StatusCode: 500}))
	}

	circuit := selectDeliveryCircuit(t, db, cfg.ID)
	require.Equal(t, webhooks.CircuitStateClosed, circuit.State)
	require.Equal(t, webhooks.DefaultCircuitFailureThreshold-1, circuit.ConsecutiveFailures)

	beforeOpen := time.Now().UTC()
	require.NoError(t, store.RecordFailure(ctx, cfg.ID, webhooks.DeliveryFailure{StatusCode: 500}))

	circuit = selectDeliveryCircuit(t, db, cfg.ID)
	require.Equal(t, webhooks.CircuitStateOpen, circuit.State)
	require.Equal(t, webhooks.DefaultCircuitFailureThreshold, circuit.ConsecutiveFailures)
	require.Equal(t, 0, circuit.ProbeAttempt)
	require.WithinDuration(t, beforeOpen.Add(webhooks.DefaultCircuitOpenDelay), circuit.OpenedUntil, 2*time.Second)

	decision, err := store.BeforeDelivery(ctx, cfg.ID)
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, webhooks.CircuitDecisionReasonOpenUntil, decision.Reason)
}

func TestDeliveryCircuitAllowsSingleHalfOpenProbe(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()

	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)
	openDeliveryCircuit(t, store, db, cfg.ID)

	_, err = db.NewUpdate().
		Model((*webhooks.DeliveryCircuit)(nil)).
		Where("config_id = ?", cfg.ID).
		Set("opened_until = ?", time.Now().UTC().Add(-time.Minute)).
		Exec(ctx)
	require.NoError(t, err)

	decision, err := store.BeforeDelivery(ctx, cfg.ID)
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, webhooks.CircuitDecisionReasonHalfOpenProbe, decision.Reason)

	decision, err = store.BeforeDelivery(ctx, cfg.ID)
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, webhooks.CircuitDecisionReasonProbeBusy, decision.Reason)

	circuit := selectDeliveryCircuit(t, db, cfg.ID)
	require.Equal(t, webhooks.CircuitStateHalfOpen, circuit.State)
}

func TestDeliveryCircuitReopensAfterFailedProbe(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()

	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)
	openDeliveryCircuit(t, store, db, cfg.ID)

	_, err = db.NewUpdate().
		Model((*webhooks.DeliveryCircuit)(nil)).
		Where("config_id = ?", cfg.ID).
		Set("opened_until = ?", time.Now().UTC().Add(-time.Minute)).
		Exec(ctx)
	require.NoError(t, err)

	decision, err := store.BeforeDelivery(ctx, cfg.ID)
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	beforeFailure := time.Now().UTC()
	require.NoError(t, store.RecordFailure(ctx, cfg.ID, webhooks.DeliveryFailure{StatusCode: 500}))

	circuit := selectDeliveryCircuit(t, db, cfg.ID)
	require.Equal(t, webhooks.CircuitStateOpen, circuit.State)
	require.Equal(t, 1, circuit.ProbeAttempt)
	require.WithinDuration(t, beforeFailure.Add(webhooks.CircuitProbeDelay(1)), circuit.OpenedUntil, 2*time.Second)
}

func TestDeliveryCircuitClosesAfterSuccessfulProbe(t *testing.T) {
	store, db := newTestStoreWithDB(t)
	ctx := context.Background()

	cfg, err := store.InsertOneConfig(ctx, webhooks.ConfigUser{
		Endpoint:   "http://localhost:8080",
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	})
	require.NoError(t, err)
	openDeliveryCircuit(t, store, db, cfg.ID)

	_, err = db.NewUpdate().
		Model((*webhooks.DeliveryCircuit)(nil)).
		Where("config_id = ?", cfg.ID).
		Set("state = ?", webhooks.CircuitStateHalfOpen).
		Set("probe_attempt = 2").
		Exec(ctx)
	require.NoError(t, err)

	require.NoError(t, store.RecordSuccess(ctx, cfg.ID))

	circuit := selectDeliveryCircuit(t, db, cfg.ID)
	require.Equal(t, webhooks.CircuitStateClosed, circuit.State)
	require.Equal(t, 0, circuit.ConsecutiveFailures)
	require.Equal(t, 0, circuit.ProbeAttempt)
	require.True(t, circuit.OpenedUntil.IsZero())
	require.True(t, circuit.LastFailureAt.IsZero())
	require.Equal(t, "", circuit.LastFailureReason)
}

func openDeliveryCircuit(t *testing.T, store interface {
	RecordFailure(context.Context, string, webhooks.DeliveryFailure) error
}, db interface {
	NewSelect() *bun.SelectQuery
}, configID string) {
	t.Helper()

	ctx := context.Background()
	for range webhooks.DefaultCircuitFailureThreshold {
		require.NoError(t, store.RecordFailure(ctx, configID, webhooks.DeliveryFailure{StatusCode: 500}))
	}

	circuit := selectDeliveryCircuit(t, db, configID)
	require.Equal(t, webhooks.CircuitStateOpen, circuit.State)
}

func selectDeliveryCircuit(t *testing.T, db interface {
	NewSelect() *bun.SelectQuery
}, configID string) webhooks.DeliveryCircuit {
	t.Helper()

	circuit := webhooks.DeliveryCircuit{}
	require.NoError(t, db.NewSelect().
		Model(&circuit).
		Where("config_id = ?", configID).
		Scan(context.Background()))
	return circuit
}
