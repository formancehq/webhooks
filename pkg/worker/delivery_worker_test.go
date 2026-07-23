package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/v2/publish"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/stretchr/testify/require"
)

type deliveryMockStore struct {
	mu             sync.Mutex
	configs        []webhooks.Config
	deliveries     []webhooks.Delivery
	claimed        []webhooks.Delivery
	completed      []webhooks.Delivery
	attempts       []webhooks.DeliveryAttempt
	cancelled      []string
	findError      error
	insertError    error
	enqueueStarted chan struct{}
	enqueueRelease chan struct{}
}

func (m *deliveryMockStore) FindManyConfigs(context.Context, map[string]any) ([]webhooks.Config, error) {
	if m.findError != nil {
		return nil, m.findError
	}
	return m.configs, nil
}

func (m *deliveryMockStore) EnqueueEvent(_ context.Context, eventID, idempotencyKey, eventType, payload string, createdAt time.Time) error {
	if m.enqueueStarted != nil {
		close(m.enqueueStarted)
		<-m.enqueueRelease
	}
	if m.insertError != nil {
		return m.insertError
	}
	for _, config := range m.configs {
		nextAttemptAt := createdAt
		m.deliveries = append(m.deliveries, webhooks.Delivery{
			EventID: eventID, IdempotencyKey: idempotencyKey, ConfigID: config.ID,
			EventType: eventType, Payload: payload, Status: webhooks.StatusDeliveryPending,
			NextAttemptAt: &nextAttemptAt, CreatedAt: createdAt,
		})
	}
	return nil
}

type singleMessageSubscriber struct {
	messages chan *message.Message
	close    sync.Once
}

func newSingleMessageSubscriber() *singleMessageSubscriber {
	return &singleMessageSubscriber{messages: make(chan *message.Message, 1)}
}

func (s *singleMessageSubscriber) Subscribe(context.Context, string) (<-chan *message.Message, error) {
	return s.messages, nil
}

func (s *singleMessageSubscriber) Close() error {
	s.close.Do(func() { close(s.messages) })
	return nil
}

func runDeliveryRouter(t *testing.T, store deliveryEnqueuer) *singleMessageSubscriber {
	t.Helper()
	logger := watermill.NopLogger{}
	router := message.NewDefaultRouter(logger)
	subscriber := newSingleMessageSubscriber()
	router.AddConsumerHandler("deliveries-test", "events", subscriber, processDeliveryMessages(store))
	go func() { _ = router.Run(context.Background()) }()
	select {
	case <-router.Running():
	case <-time.After(2 * time.Second):
		t.Fatal("router did not start")
	}
	t.Cleanup(func() {
		_ = router.Close()
	})
	return subscriber
}

func (m *deliveryMockStore) ClaimDeliveries(_ context.Context, limit int) ([]webhooks.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.claimed) > limit {
		result := m.claimed[:limit]
		m.claimed = m.claimed[limit:]
		return result, nil
	}
	result := m.claimed
	m.claimed = nil
	return result, nil
}

func (m *deliveryMockStore) CompleteDelivery(_ context.Context, delivery webhooks.Delivery, attempt webhooks.DeliveryAttempt) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed = append(m.completed, delivery)
	m.attempts = append(m.attempts, attempt)
	return delivery.Status, nil
}

func (m *deliveryMockStore) CancelDelivery(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelled = append(m.cancelled, id)
	return nil
}
func (m *deliveryMockStore) RecoverStaleDeliveries(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func TestProcessDeliveryMessagesPersistsBeforeAcknowledgement(t *testing.T) {
	store := &deliveryMockStore{configs: []webhooks.Config{{
		ConfigUser: webhooks.ConfigUser{Endpoint: "https://example.com", Secret: webhooks.NewSecret(), EventTypes: []string{"ledger.transaction.created"}},
		ID:         "config-1", Active: true,
	}}}
	event := publish.EventMessage{App: "Ledger", Type: "Transaction.Created", IdempotencyKey: "event-key"}
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	handler := processDeliveryMessages(store)
	require.NoError(t, handler(message.NewMessage("message-id", payload)))

	require.Len(t, store.deliveries, 1)
	delivery := store.deliveries[0]
	require.Equal(t, "message-id", delivery.EventID)
	require.Equal(t, "event-key", delivery.IdempotencyKey)
	require.Equal(t, "ledger.transaction.created", delivery.EventType)
	require.Equal(t, webhooks.StatusDeliveryPending, delivery.Status)
	require.Equal(t, "config-1", delivery.ConfigID)
}

func TestDeliveryRouterAcknowledgesOnlyAfterDurableEnqueueReturns(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	store := &deliveryMockStore{
		configs:        []webhooks.Config{{ID: "config-1", Active: true}},
		enqueueStarted: started, enqueueRelease: release,
	}
	subscriber := runDeliveryRouter(t, store)
	payload, err := json.Marshal(publish.EventMessage{Type: "test.event"})
	require.NoError(t, err)
	msg := message.NewMessage("ack-message", payload)
	subscriber.messages <- msg

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue was not called")
	}
	select {
	case <-msg.Acked():
		t.Fatal("message was acknowledged before durable enqueue returned")
	case <-msg.Nacked():
		t.Fatal("message was nacked before durable enqueue returned")
	default:
	}
	close(release)
	select {
	case <-msg.Acked():
	case <-time.After(2 * time.Second):
		t.Fatal("message was not acknowledged after durable enqueue")
	}
}

func TestDeliveryRouterNacksWhenDurableEnqueueFails(t *testing.T) {
	store := &deliveryMockStore{insertError: context.DeadlineExceeded}
	subscriber := runDeliveryRouter(t, store)
	payload, err := json.Marshal(publish.EventMessage{Type: "test.event"})
	require.NoError(t, err)
	msg := message.NewMessage("nack-message", payload)
	subscriber.messages <- msg
	select {
	case <-msg.Nacked():
	case <-time.After(2 * time.Second):
		t.Fatal("message was not nacked after durable enqueue failure")
	}
}

func TestProcessDeliveryMessagesReturnsPersistenceFailureForBrokerNack(t *testing.T) {
	store := &deliveryMockStore{
		configs:     []webhooks.Config{{ID: "config-1", Active: true}},
		insertError: context.DeadlineExceeded,
	}
	payload, err := json.Marshal(publish.EventMessage{Type: "test.event"})
	require.NoError(t, err)
	err = processDeliveryMessages(store)(message.NewMessage("message-id", payload))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDeliveryDispatcherPersistsRetryableFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("temporary"))
	}))
	defer server.Close()
	now := time.Now().UTC()
	store := &deliveryMockStore{
		configs: []webhooks.Config{{ConfigUser: webhooks.ConfigUser{Endpoint: server.URL, Secret: webhooks.NewSecret()}, ID: "config-1", Active: true}},
		claimed: []webhooks.Delivery{{
			ID: "delivery-1", ConfigID: "config-1", Payload: `{"type":"test.event"}`,
			Status: webhooks.StatusDeliveryDelivering, ClaimedAt: &now,
		}},
	}
	dispatcher := NewDeliveryDispatcher(store, server.Client(), time.Second, &noRetryPolicy{}, 1)
	dispatcher.dispatch(context.Background())

	require.Len(t, store.completed, 1)
	require.Equal(t, webhooks.StatusDeliveryPending, store.completed[0].Status)
	require.Equal(t, 1, store.completed[0].AttemptCount)
	require.NotNil(t, store.completed[0].NextAttemptAt)
	require.Len(t, store.attempts, 1)
	require.Equal(t, webhooks.OutcomeDeliveryRetryableFailure, store.attempts[0].Outcome)
	require.Equal(t, 500, store.attempts[0].StatusCode)
	require.Equal(t, "temporary", store.attempts[0].ResponseExcerpt)
}

func TestDeliveryDispatcherLeavesClaimRecoverableOnConfigLookupError(t *testing.T) {
	now := time.Now().UTC()
	store := &deliveryMockStore{
		findError: context.DeadlineExceeded,
		claimed: []webhooks.Delivery{{
			ID: "delivery-lookup-error", ConfigID: "config-1", Status: webhooks.StatusDeliveryDelivering,
			ClaimedAt: &now,
		}},
	}
	NewDeliveryDispatcher(store, http.DefaultClient, time.Second, &noRetryPolicy{}, 1).dispatch(context.Background())

	require.Empty(t, store.completed)
	require.Empty(t, store.cancelled, "transient lookup errors must leave the claim for stale recovery")
}

func TestDeliveryDispatcherFailsPermanent404WithoutRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	now := time.Now().UTC()
	store := &deliveryMockStore{
		configs: []webhooks.Config{{ConfigUser: webhooks.ConfigUser{Endpoint: server.URL, Secret: webhooks.NewSecret()}, ID: "config-1", Active: true}},
		claimed: []webhooks.Delivery{{
			ID: "delivery-404", ConfigID: "config-1", Payload: `{"type":"test.event"}`,
			Status: webhooks.StatusDeliveryDelivering, ClaimedAt: &now,
		}},
	}
	NewDeliveryDispatcher(store, server.Client(), time.Second, &noRetryPolicy{}, 1).dispatch(context.Background())

	require.Len(t, store.completed, 1)
	require.Equal(t, webhooks.StatusDeliveryFailed, store.completed[0].Status)
	require.Nil(t, store.completed[0].NextAttemptAt)
	require.Equal(t, webhooks.OutcomeDeliveryPermanentFailure, store.attempts[0].Outcome)
}
