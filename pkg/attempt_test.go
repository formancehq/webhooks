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

func TestMakeAttempt_StatusClassification(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		wantStatus string
	}{
		{"2xx success", http.StatusOK, webhooks.StatusAttemptSuccess},
		{"201 success", http.StatusCreated, webhooks.StatusAttemptSuccess},
		{"400 permanent", http.StatusBadRequest, webhooks.StatusAttemptFailed},
		{"401 permanent", http.StatusUnauthorized, webhooks.StatusAttemptFailed},
		{"403 permanent", http.StatusForbidden, webhooks.StatusAttemptFailed},
		{"404 permanent", http.StatusNotFound, webhooks.StatusAttemptFailed},
		{"405 permanent", http.StatusMethodNotAllowed, webhooks.StatusAttemptFailed},
		{"408 retryable", http.StatusRequestTimeout, webhooks.StatusAttemptToRetry},
		{"429 retryable", http.StatusTooManyRequests, webhooks.StatusAttemptToRetry},
		{"500 retryable", http.StatusInternalServerError, webhooks.StatusAttemptToRetry},
		{"503 retryable", http.StatusServiceUnavailable, webhooks.StatusAttemptToRetry},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			cfg := webhooks.Config{
				ConfigUser: webhooks.ConfigUser{
					Endpoint:   server.URL,
					Secret:     webhooks.NewSecret(),
					EventTypes: []string{"test.event"},
				},
				ID:     "cfg-status",
				Active: true,
			}

			policy := &fixedBackoff{delay: 15 * time.Second}
			attempt, err := webhooks.MakeAttempt(
				context.Background(), server.Client(), policy,
				"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false,
			)

			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, attempt.Status)
			assert.Equal(t, tc.statusCode, attempt.StatusCode)
			if tc.wantStatus == webhooks.StatusAttemptToRetry {
				assert.False(t, attempt.NextRetryAfter.IsZero(), "retryable attempt should schedule a retry")
			}
		})
	}
}

func TestMakeAttempt_HonorsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-retry-after",
		Active: true,
	}

	// Backoff suggests 15s; Retry-After asks for 120s and must win.
	policy := &fixedBackoff{delay: 15 * time.Second}
	before := time.Now().UTC()
	attempt, err := webhooks.MakeAttempt(
		context.Background(), server.Client(), policy,
		"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false,
	)

	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptToRetry, attempt.Status)
	assert.GreaterOrEqual(t, attempt.NextRetryAfter.Sub(before), 110*time.Second,
		"Retry-After (120s) should override the shorter backoff")
}

func TestMakeAttempt_ClampsAbsurdRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "99999999") // ~3 years
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-retry-after-clamp",
		Active: true,
	}

	policy := &fixedBackoff{delay: 15 * time.Second}
	before := time.Now().UTC()
	attempt, err := webhooks.MakeAttempt(
		context.Background(), server.Client(), policy,
		"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false,
	)

	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptToRetry, attempt.Status)
	assert.LessOrEqual(t, attempt.NextRetryAfter.Sub(before), 7*time.Hour,
		"an endpoint-controlled Retry-After must not park the delivery years in the future")
}
