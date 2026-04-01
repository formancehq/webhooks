package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/go-libs/v2/publish"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessMessages_SynchronousProcessing verifies that processMessages
// blocks until the webhook HTTP call completes. The handler must not return
// before delivery finishes — otherwise the broker acks prematurely.
func TestProcessMessages_SynchronousProcessing(t *testing.T) {
	var callCount atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		started <- struct{}{}
		<-release // block until the test releases us
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := webhooks.ConfigUser{
		Endpoint:   server.URL,
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	}

	configStore := &mockStoreWithConfigs{
		mockStore: newMockStore(nil, nil),
		configs: []webhooks.Config{
			{
				ConfigUser: cfg,
				ID:         "cfg-1",
				Active:     true,
			},
		},
	}

	handler := processMessages(configStore, server.Client(), &noRetryPolicy{})

	ev := publish.EventMessage{
		Type: "test.event",
	}
	payload, err := json.Marshal(ev)
	require.NoError(t, err)

	msg := message.NewMessage("test-uuid", payload)

	// Run handler in a goroutine — it should block on the HTTP call
	done := make(chan error, 1)
	go func() {
		done <- handler(msg)
	}()

	// Wait for the HTTP request to start
	select {
	case <-started:
		// good — the handler reached the HTTP endpoint
	case err := <-done:
		t.Fatalf("handler returned before the webhook request started: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("handler never reached the webhook endpoint")
	}

	// The handler should still be blocked (waiting for the HTTP response)
	select {
	case err := <-done:
		t.Fatalf("handler returned before the webhook request completed: %v", err)
	default:
		// good — handler is still blocked, proving synchronous processing
	}

	// Release the HTTP handler
	close(release)

	// Now the handler should complete
	require.NoError(t, <-done)
	assert.Equal(t, int32(1), callCount.Load(),
		"exactly one webhook delivery should have been made")
}

// TestProcessMessages_ErrorReturnedOnFailure verifies that when the message
// handler encounters an unmarshal error, it returns an error (causing watermill
// to nack/retry) instead of silently dropping the message.
func TestProcessMessages_ErrorReturnedOnFailure(t *testing.T) {
	store := newMockStore(nil, nil)
	handler := processMessages(store, http.DefaultClient, &noRetryPolicy{})

	msg := message.NewMessage("test-uuid", []byte(`not-valid-at-all`))

	err := handler(msg)

	assert.Error(t, err, "handler should return error for invalid messages so watermill can nack/retry")
}

// TestProcessMessages_NackOnInsertFailureForRetry verifies that when the
// attempt needs retry but InsertOneAttempt fails, the handler returns an error
// (nack) instead of silently acking and losing the retry record.
func TestProcessMessages_NackOnInsertFailureForRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // trigger "to retry"
	}))
	defer server.Close()

	cfg := webhooks.ConfigUser{
		Endpoint:   server.URL,
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	}

	configStore := &failingInsertStore{
		mockStoreWithConfigs: &mockStoreWithConfigs{
			mockStore: newMockStore(nil, nil),
			configs: []webhooks.Config{
				{
					ConfigUser: cfg,
					ID:         "cfg-1",
					Active:     true,
				},
			},
		},
	}

	handler := processMessages(configStore, server.Client(), &noRetryPolicy{})

	ev := publish.EventMessage{
		Type: "test.event",
	}
	payload, err := json.Marshal(ev)
	require.NoError(t, err)

	msg := message.NewMessage("test-uuid", payload)

	err = handler(msg)
	assert.Error(t, err, "handler should return error when insert fails for a non-success attempt")
	assert.Contains(t, err.Error(), "insert attempt")
}

// mockStoreWithConfigs extends mockStore to return pre-configured configs
type mockStoreWithConfigs struct {
	*mockStore
	configs []webhooks.Config
}

func (m *mockStoreWithConfigs) FindManyConfigs(_ context.Context, _ map[string]any) ([]webhooks.Config, error) {
	return m.configs, nil
}

// failingInsertStore always fails on InsertOneAttempt
type failingInsertStore struct {
	*mockStoreWithConfigs
}

func (m *failingInsertStore) InsertOneAttempt(_ context.Context, _ webhooks.Attempt) error {
	return assert.AnError
}

type noRetryPolicy struct{}

func (n *noRetryPolicy) GetRetryDelay(_ int) (time.Duration, error) {
	return 0, nil
}
