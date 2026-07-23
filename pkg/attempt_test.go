package webhooks_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/backoff"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type failingResponseBody struct{}

func (failingResponseBody) Read([]byte) (int, error) { return 0, errors.New("body read failed") }
func (failingResponseBody) Close() error             { return nil }

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

func TestMakeAttempt_ResponseBodyReadErrorConsumesRetryBudget(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(failingResponseBody{}),
		}, nil
	})}
	cfg := webhooks.Config{ConfigUser: webhooks.ConfigUser{
		Endpoint: "https://example.com", Secret: webhooks.NewSecret(), EventTypes: []string{"test.event"},
	}, ID: "cfg-body-error", Active: true}

	attempt, err := webhooks.MakeAttempt(context.Background(), client, &fixedBackoff{delay: time.Second},
		"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false)
	require.NoError(t, err)
	require.Equal(t, webhooks.StatusAttemptToRetry, attempt.Status)
	require.Equal(t, http.StatusInternalServerError, attempt.StatusCode)
	require.Contains(t, attempt.DeliveryError, "body read failed")
	require.False(t, attempt.NextRetryAfter.IsZero())
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

func TestMakeAttempt_MaxAttemptsOneDoesNotScheduleRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-max-attempts-one",
		Active: true,
	}

	attempt, err := webhooks.MakeAttempt(
		context.Background(), server.Client(),
		backoff.NewExponential(time.Second, time.Minute, time.Hour, 1),
		"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false,
	)

	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptFailed, attempt.Status)
	assert.True(t, attempt.NextRetryAfter.IsZero(), "max-attempts=1 must not schedule a retry")
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

func TestMakeAttempt_RetryAfterIsScheduledFromResponseTime(t *testing.T) {
	const retryAfter = time.Second
	const responseDelay = 300 * time.Millisecond

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(responseDelay)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-retry-after-response-time",
		Active: true,
	}

	policy := &fixedBackoff{delay: 100 * time.Millisecond}
	before := time.Now().UTC()
	attempt, err := webhooks.MakeAttempt(
		context.Background(), server.Client(), policy,
		"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false,
	)

	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptToRetry, attempt.Status)
	assert.GreaterOrEqual(t, attempt.NextRetryAfter.Sub(before), retryAfter+responseDelay/2,
		"Retry-After delay must be anchored after the endpoint response, not before the request")
}

func TestMakeAttempt_RetryAfterCannotExceedAbortAfterWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "21600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-retry-after-abort-after",
		Active: true,
	}

	attempt, err := webhooks.MakeAttempt(
		context.Background(), server.Client(),
		backoff.NewExponential(time.Second, time.Hour, 3*time.Second, 0),
		"attempt-id", "webhook-id", 0, cfg, "", []byte(`{"type":"test.event"}`), false,
	)

	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptFailed, attempt.Status)
	assert.True(t, attempt.NextRetryAfter.IsZero(), "Retry-After beyond abort-after must not schedule a retry")
}

func TestMakeAttempt_RetryAfterCannotExtendPastFirstAttemptWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7200")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "cfg-retry-after-first-attempt-window",
		Active: true,
	}

	attempt, err := webhooks.MakeAttempt(
		context.Background(), server.Client(),
		backoff.NewExponential(time.Second, time.Hour, 72*time.Hour, 15),
		"attempt-id", "webhook-id", 12, cfg, "", []byte(`{"type":"test.event"}`), false,
		webhooks.WithFirstAttemptAt(time.Now().UTC().Add(-71*time.Hour)),
	)

	require.NoError(t, err)
	assert.Equal(t, webhooks.StatusAttemptFailed, attempt.Status)
	assert.True(t, attempt.NextRetryAfter.IsZero(), "Retry-After must not extend the real retry window past abort-after")
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
