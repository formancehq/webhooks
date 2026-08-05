package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type deliveryOnlyRetentionStore struct {
	deliveryCalls int
}

func (s *deliveryOnlyRetentionStore) PurgeFinishedDeliveries(context.Context, time.Duration, time.Duration, int) (int64, error) {
	s.deliveryCalls++
	return 0, nil
}

func TestRetentionConfigEnabled(t *testing.T) {
	require.False(t, RetentionConfig{}.Enabled())

	require.True(t, RetentionConfig{Period: time.Hour}.Enabled())
	require.True(t, RetentionConfig{SuccessDelay: time.Hour}.Enabled())
	require.True(t, RetentionConfig{FailedDelay: time.Hour}.Enabled())
}

func TestRetentionPurgesDeliveries(t *testing.T) {
	store := &deliveryOnlyRetentionStore{}
	retention := NewRetention(store, RetentionConfig{Period: time.Hour})
	retention.runOnce(context.Background())
	require.Equal(t, 1, store.deliveryCalls)
}
