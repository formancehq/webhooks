package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/backoff"
	"github.com/stretchr/testify/require"
)

// mockStore implements storage.Store with only the methods needed for retry tests.
type mockStore struct {
	mu         sync.Mutex
	webhookIDs []string
	attempts   map[string][]webhooks.Attempt
	inserted   []webhooks.Attempt
	updated    map[string]string // webhookID -> final status
}

func newMockStore(webhookIDs []string, attempts map[string][]webhooks.Attempt) *mockStore {
	return &mockStore{
		webhookIDs: webhookIDs,
		attempts:   attempts,
		updated:    make(map[string]string),
	}
}

func (m *mockStore) FindWebhookIDsToRetry(_ context.Context, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.webhookIDs) == 0 {
		return nil, nil
	}
	end := min(limit, len(m.webhookIDs))
	claimed := m.webhookIDs[:end]
	m.webhookIDs = m.webhookIDs[end:]
	return claimed, nil
}

func (m *mockStore) FindAttemptsToRetryByWebhookID(_ context.Context, webhookID string) ([]webhooks.Attempt, error) {
	return m.attempts[webhookID], nil
}

func (m *mockStore) InsertOneAttempt(_ context.Context, att webhooks.Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inserted = append(m.inserted, att)
	return nil
}

func (m *mockStore) UpdateAttemptsStatus(_ context.Context, webhookID string, status string) ([]webhooks.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updated[webhookID] = status
	return nil, nil
}

func (m *mockStore) RecoverStaleRetryingAttempts(_ context.Context, _ time.Duration) error {
	return nil
}

// Unused Store interface methods
func (m *mockStore) FindManyConfigs(_ context.Context, _ map[string]any) ([]webhooks.Config, error) {
	return nil, nil
}
func (m *mockStore) InsertOneConfig(_ context.Context, _ webhooks.ConfigUser) (webhooks.Config, error) {
	return webhooks.Config{}, nil
}
func (m *mockStore) DeleteOneConfig(_ context.Context, _ string) error { return nil }
func (m *mockStore) UpdateOneConfigActivation(_ context.Context, _ string, _ bool) (webhooks.Config, error) {
	return webhooks.Config{}, nil
}
func (m *mockStore) UpdateOneConfigSecret(_ context.Context, _, _ string) (webhooks.Config, error) {
	return webhooks.Config{}, nil
}
func (m *mockStore) UpdateOneConfig(_ context.Context, _ string, _ webhooks.ConfigUser) error {
	return nil
}
func (m *mockStore) Close(_ context.Context) error { return nil }

func TestProcessWebhookRetrySuccess(t *testing.T) {
	// HTTP server that returns 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	webhookID := "webhook-1"
	payload, _ := json.Marshal(map[string]string{"type": "test.event"})

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "config-1",
		Active: true,
	}

	store := newMockStore(nil, map[string][]webhooks.Attempt{
		webhookID: {
			{
				ID:           "att-1",
				WebhookID:    webhookID,
				Config:       cfg,
				Payload:      string(payload),
				StatusCode:   500,
				RetryAttempt: 1,
				Status:       webhooks.StatusAttemptRetrying,
			},
		},
	})

	retrier, err := NewRetrier(store, server.Client(), time.Second, backoff.NewExponential(time.Second, time.Minute, time.Hour), 10)
	require.NoError(t, err)

	retrier.processWebhookRetry(context.Background(), webhookID)

	// A new attempt should have been inserted
	require.Len(t, store.inserted, 1)
	require.Equal(t, webhooks.StatusAttemptSuccess, store.inserted[0].Status)

	// Status should have been updated to success
	require.Equal(t, webhooks.StatusAttemptSuccess, store.updated[webhookID])
}

func TestProcessWebhookRetryFailure(t *testing.T) {
	// HTTP server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	webhookID := "webhook-1"
	payload, _ := json.Marshal(map[string]string{"type": "test.event"})

	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "config-1",
		Active: true,
	}

	store := newMockStore(nil, map[string][]webhooks.Attempt{
		webhookID: {
			{
				ID:           "att-1",
				WebhookID:    webhookID,
				Config:       cfg,
				Payload:      string(payload),
				StatusCode:   500,
				RetryAttempt: 1,
				Status:       webhooks.StatusAttemptRetrying,
			},
		},
	})

	retrier, err := NewRetrier(store, server.Client(), time.Second, backoff.NewExponential(time.Second, time.Minute, time.Hour), 10)
	require.NoError(t, err)

	retrier.processWebhookRetry(context.Background(), webhookID)

	// A new attempt should have been inserted with "to retry" status
	require.Len(t, store.inserted, 1)
	require.Equal(t, webhooks.StatusAttemptToRetry, store.inserted[0].Status)
}

func TestPoolProcessesBatchInParallel(t *testing.T) {
	var concurrentCount atomic.Int32
	var maxConcurrent atomic.Int32

	// HTTP server that tracks concurrent requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := concurrentCount.Add(1)
		// Track max concurrency
		for {
			old := maxConcurrent.Load()
			if current <= old || maxConcurrent.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // simulate latency
		concurrentCount.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	payload, _ := json.Marshal(map[string]string{"type": "test.event"})
	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "config-1",
		Active: true,
	}

	// Create 10 webhooks
	webhookIDs := make([]string, 10)
	attempts := make(map[string][]webhooks.Attempt)
	for i := range 10 {
		id := "webhook-" + string(rune('A'+i))
		webhookIDs[i] = id
		attempts[id] = []webhooks.Attempt{
			{
				ID:           "att-" + id,
				WebhookID:    id,
				Config:       cfg,
				Payload:      string(payload),
				StatusCode:   500,
				RetryAttempt: 1,
				Status:       webhooks.StatusAttemptRetrying,
			},
		}
	}

	store := newMockStore(webhookIDs, attempts)

	retrier, err := NewRetrier(store, server.Client(), time.Second, backoff.NewExponential(time.Second, time.Minute, time.Hour), 10)
	require.NoError(t, err)

	// Manually claim and process (simulating one tick)
	claimed, err := store.FindWebhookIDsToRetry(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 10)

	group := retrier.retryPool.Group()
	for _, webhookID := range claimed {
		id := webhookID
		group.Submit(func() {
			retrier.processWebhookRetry(context.Background(), id)
		})
	}
	group.Wait()

	// All 10 should have been processed
	require.Len(t, store.inserted, 10)

	// Should have achieved some parallelism (more than 1 concurrent)
	require.Greater(t, maxConcurrent.Load(), int32(1), "pool should process webhooks in parallel")
}

func TestProcessWebhookRetryNoAttempts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("HTTP server should not be called when there are no attempts")
	}))
	defer server.Close()

	store := newMockStore(nil, map[string][]webhooks.Attempt{})

	retrier, err := NewRetrier(store, server.Client(), time.Second, backoff.NewExponential(time.Second, time.Minute, time.Hour), 10)
	require.NoError(t, err)

	// Should not panic or make HTTP calls
	retrier.processWebhookRetry(context.Background(), "nonexistent")

	require.Len(t, store.inserted, 0)
}

func TestProcessWebhookRetryBadPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("HTTP server should not be called with bad payload")
	}))
	defer server.Close()

	webhookID := "webhook-1"
	cfg := webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   server.URL,
			Secret:     webhooks.NewSecret(),
			EventTypes: []string{"test.event"},
		},
		ID:     "config-1",
		Active: true,
	}

	store := newMockStore(nil, map[string][]webhooks.Attempt{
		webhookID: {
			{
				ID:           "att-1",
				WebhookID:    webhookID,
				Config:       cfg,
				Payload:      "not-valid-json",
				StatusCode:   500,
				RetryAttempt: 1,
				Status:       webhooks.StatusAttemptRetrying,
			},
		},
	})

	retrier, err := NewRetrier(store, server.Client(), time.Second, backoff.NewExponential(time.Second, time.Minute, time.Hour), 10)
	require.NoError(t, err)

	// Should not panic, should log error and return
	retrier.processWebhookRetry(context.Background(), webhookID)

	require.Len(t, store.inserted, 0)
}
