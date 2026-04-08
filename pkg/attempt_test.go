package webhooks_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	webhooks "github.com/formancehq/webhooks/pkg"
)

type fixedBackoff struct {
	delay time.Duration
}

func (f *fixedBackoff) GetRetryDelay(int) (time.Duration, error) {
	return f.delay, nil
}

type noRetryPolicy struct{}

func (n *noRetryPolicy) GetRetryDelay(int) (time.Duration, error) {
	return 0, fmt.Errorf("max retries exceeded")
}

func TestMakeAttempt_TransportError_ReturnsRetryableAttempt(t *testing.T) {
	// Use a server that is immediately closed to force a transport error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Close() // close immediately so httpClient.Do fails

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-transport",
		Active: true,
	}

	policy := &fixedBackoff{delay: 15 * time.Second}

	attempt, err := webhooks.MakeAttempt(
		context.Background(), server.Client(), policy,
		"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false,
	)

	// No bare error — the attempt is returned with retry status
	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptToRetry, attempt.Status)
	assert.Equal(t, 0, attempt.StatusCode)
	assert.False(t, attempt.NextRetryAfter.IsZero(), "NextRetryAfter should be set")
}

func TestMakeAttempt_TransportError_MaxRetriesExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-transport-max",
		Active: true,
	}

	// Policy that always returns error = max retries exceeded
	policy := &noRetryPolicy{}

	attempt, err := webhooks.MakeAttempt(
		context.Background(), http.DefaultClient, policy,
		"attempt-id", "webhook-id", 999, cfg, "", []byte(`{"type":"test.event"}`), false,
	)

	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptFailed, attempt.Status)
}
