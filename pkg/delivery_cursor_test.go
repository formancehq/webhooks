package webhooks_test

import (
	"testing"
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/stretchr/testify/require"
)

func TestReplayDeliveryCursorPreservesFrozenSelection(t *testing.T) {
	from := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	to := from.Add(30 * time.Minute)
	want := webhooks.ReplayDeliveryCursor{
		Position:      webhooks.DeliveryCursor{CreatedAt: from.Add(time.Minute), ID: "delivery-id"},
		CreatedAtFrom: from, CreatedAtTo: to,
		Statuses:  []string{webhooks.StatusDeliveryFailed, webhooks.StatusDeliveryPending},
		ConfigIDs: []string{"config-1", "config-2"},
	}
	token, err := webhooks.EncodeReplayDeliveryCursor(want)
	require.NoError(t, err)
	got, err := webhooks.DecodeReplayDeliveryCursor(token)
	require.NoError(t, err)
	require.Equal(t, want, *got)
}

func TestReplayDeliveryCursorRejectsListCursor(t *testing.T) {
	token, err := webhooks.EncodeDeliveryCursor(&webhooks.DeliveryCursor{CreatedAt: time.Now().UTC(), ID: "delivery-id"})
	require.NoError(t, err)
	_, err = webhooks.DecodeReplayDeliveryCursor(token)
	require.Error(t, err)
}
