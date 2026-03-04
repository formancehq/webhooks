package worker

import (
	"context"
	"testing"
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStore implements storage.Store for testing purposes.
type mockStore struct {
	findWebhookIDsToRetryFn        func(ctx context.Context) ([]string, error)
	findAttemptsToRetryByWebhookFn func(ctx context.Context, webhookID string) ([]webhooks.Attempt, error)
}

func (m *mockStore) FindManyConfigs(ctx context.Context, filter map[string]any) ([]webhooks.Config, error) {
	return nil, nil
}
func (m *mockStore) InsertOneConfig(ctx context.Context, cfg webhooks.ConfigUser) (webhooks.Config, error) {
	return webhooks.Config{}, nil
}
func (m *mockStore) DeleteOneConfig(ctx context.Context, id string) error { return nil }
func (m *mockStore) UpdateOneConfigActivation(ctx context.Context, id string, active bool) (webhooks.Config, error) {
	return webhooks.Config{}, nil
}
func (m *mockStore) UpdateOneConfigSecret(ctx context.Context, id, secret string) (webhooks.Config, error) {
	return webhooks.Config{}, nil
}
func (m *mockStore) FindAttemptsToRetryByWebhookID(ctx context.Context, webhookID string) ([]webhooks.Attempt, error) {
	if m.findAttemptsToRetryByWebhookFn != nil {
		return m.findAttemptsToRetryByWebhookFn(ctx, webhookID)
	}
	return nil, nil
}
func (m *mockStore) FindWebhookIDsToRetry(ctx context.Context) ([]string, error) {
	if m.findWebhookIDsToRetryFn != nil {
		return m.findWebhookIDsToRetryFn(ctx)
	}
	return nil, nil
}
func (m *mockStore) UpdateAttemptsStatus(ctx context.Context, webhookID string, status string) ([]webhooks.Attempt, error) {
	return nil, nil
}
func (m *mockStore) InsertOneAttempt(ctx context.Context, att webhooks.Attempt) error { return nil }
func (m *mockStore) Close(ctx context.Context) error                                  { return nil }
func (m *mockStore) UpdateOneConfig(ctx context.Context, id string, cfg webhooks.ConfigUser) error {
	return nil
}

// TestBug12_WorkerSurvivesTransientErrors verifies that the worker
// continues running after transient errors instead of terminating.
func TestBug12_WorkerSurvivesTransientErrors(t *testing.T) {
	callCount := 0
	store := &mockStore{
		findWebhookIDsToRetryFn: func(ctx context.Context) ([]string, error) {
			callCount++
			if callCount == 1 {
				return nil, errors.New("temporary connection error")
			}
			return nil, nil
		},
	}

	retrier, err := NewRetrier(store, nil, 10*time.Millisecond, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run the retrier — it should survive transient errors and exit via context timeout
	runErr := retrier.Run(ctx)

	// Worker should NOT return an error — it should survive and exit via context cancellation
	assert.NoError(t, runErr, "worker should survive transient errors")
	assert.Greater(t, callCount, 1,
		"worker should have called FindWebhookIDsToRetry more than once (survived the error)")
}
