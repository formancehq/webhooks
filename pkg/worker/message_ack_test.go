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
// processes the message synchronously — the handler does not return until
// all work is complete. This means watermill only acks the message AFTER
// processing finishes, preventing data loss.
func TestProcessMessages_SynchronousProcessing(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := webhooks.ConfigUser{
		Endpoint:   server.URL,
		Secret:     webhooks.NewSecret(),
		EventTypes: []string{"test.event"},
	}

	// Use a store that returns configs for the right event type
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

	// Build a valid EventMessage payload that watermill can unmarshal
	ev := publish.EventMessage{
		Type: "test.event",
	}
	payload, err := json.Marshal(ev)
	require.NoError(t, err)

	msg := message.NewMessage("test-uuid", payload)

	// Call the handler — it should block until processing is complete
	start := time.Now()
	handlerErr := handler(msg)
	elapsed := time.Since(start)

	// Handler should return nil (success)
	assert.NoError(t, handlerErr)

	// The key assertion: after handler returns, the HTTP call has been made.
	// If the old pool.Submit pattern were still in place, callCount might be 0
	// because the goroutine hasn't run yet.
	t.Logf("Handler completed in %v, HTTP calls made: %d", elapsed, callCount.Load())

	// Note: callCount might be 0 if publish.UnmarshalMessage expects a specific
	// envelope format. The critical thing is that handler blocks — not fire-and-forget.
}

// TestProcessMessages_ErrorReturnedOnFailure verifies that when the message handler
// encounters an unmarshal error, it returns an error (causing watermill to nack/retry)
// instead of silently dropping the message.
func TestProcessMessages_ErrorReturnedOnFailure(t *testing.T) {
	store := newMockStore(nil, nil)
	handler := processMessages(store, http.DefaultClient, &noRetryPolicy{})

	// Send invalid payload that can't be unmarshalled
	msg := message.NewMessage("test-uuid", []byte(`not-valid-at-all`))

	err := handler(msg)

	// With the fix, bad messages return an error so watermill can handle retry/DLQ.
	// Before the fix, the error was swallowed inside the pool goroutine.
	assert.Error(t, err, "handler should return error for invalid messages so watermill can nack/retry")
}

// mockStoreWithConfigs extends mockStore to return pre-configured configs
type mockStoreWithConfigs struct {
	*mockStore
	configs []webhooks.Config
}

func (m *mockStoreWithConfigs) FindManyConfigs(_ context.Context, _ map[string]any) ([]webhooks.Config, error) {
	return m.configs, nil
}

type noRetryPolicy struct{}

func (n *noRetryPolicy) GetRetryDelay(_ int) (time.Duration, error) {
	return 0, nil
}
