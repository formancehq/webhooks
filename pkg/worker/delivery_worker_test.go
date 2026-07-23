package worker

import (
	"context"
	"encoding/json"
	"errors"
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
	"go.uber.org/fx"
)

type deliveryMockStore struct {
	mu             sync.Mutex
	configs        []webhooks.Config
	deliveries     []webhooks.Delivery
	claimed        []webhooks.Delivery
	completed      []webhooks.Delivery
	attempts       []webhooks.DeliveryAttempt
	cancelled      []string
	failedClaims   []string
	failureReasons []string
	findError      error
	insertError    error
	enqueueStarted chan struct{}
	enqueueRelease chan struct{}
	claimStarted   chan struct{}
	claimCancelled chan struct{}
	claimRelease   chan struct{}
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

func (m *deliveryMockStore) ClaimDeliveries(ctx context.Context, limit int) ([]webhooks.Delivery, error) {
	if m.claimStarted != nil {
		close(m.claimStarted)
		<-ctx.Done()
		close(m.claimCancelled)
		<-m.claimRelease
		return nil, ctx.Err()
	}
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

func (m *deliveryMockStore) FailClaimedDelivery(_ context.Context, id string, _ time.Time, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedClaims = append(m.failedClaims, id)
	m.failureReasons = append(m.failureReasons, reason)
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

func TestNewDeliveryDispatcherAppliesDefaultHTTPTimeout(t *testing.T) {
	client := &http.Client{}
	dispatcher := NewDeliveryDispatcher(&deliveryMockStore{}, client, time.Second, &noRetryPolicy{}, 1)
	require.Equal(t, defaultDeliveryHTTPTimeout, dispatcher.httpClient.Timeout)
	require.Zero(t, client.Timeout, "the injected client must not be mutated")

	configured := &http.Client{Timeout: 5 * time.Second}
	dispatcher = NewDeliveryDispatcher(&deliveryMockStore{}, configured, time.Second, &noRetryPolicy{}, 1)
	require.Same(t, configured, dispatcher.httpClient)
	require.Equal(t, 5*time.Second, dispatcher.httpClient.Timeout)
}

type lifecycleRecorder struct {
	hook fx.Hook
}

func (l *lifecycleRecorder) Append(hook fx.Hook) {
	l.hook = hook
}

func TestRunDeliveryDispatcherWaitsForRunToFinishOnStop(t *testing.T) {
	claimStarted := make(chan struct{})
	claimCancelled := make(chan struct{})
	claimRelease := make(chan struct{})
	store := &deliveryMockStore{
		claimStarted: claimStarted, claimCancelled: claimCancelled, claimRelease: claimRelease,
	}
	dispatcher := NewDeliveryDispatcher(store, http.DefaultClient, time.Hour, &noRetryPolicy{}, 1)
	lifecycle := &lifecycleRecorder{}
	runDeliveryDispatcher(lifecycle, dispatcher)
	require.NoError(t, lifecycle.hook.OnStart(context.Background()))
	select {
	case <-claimStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher did not start claiming")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- lifecycle.hook.OnStop(stopCtx) }()
	select {
	case <-claimCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher claim did not observe shutdown cancellation")
	}
	select {
	case <-stopped:
		t.Fatal("shutdown returned before the dispatcher finished")
	default:
	}
	close(claimRelease)
	require.NoError(t, <-stopped)
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

type expiredWindowPolicy struct{}

func (expiredWindowPolicy) GetRetryDelay(int) (time.Duration, error) { return time.Second, nil }
func (expiredWindowPolicy) LimitRetryWindow(time.Duration) error {
	return errors.New("retry window elapsed")
}

func TestDeliveryDispatcherFailsExpiredDeliveryBeforeCallingEndpoint(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}))
	defer server.Close()
	now := time.Now().UTC()
	cycleStartedAt := now.Add(-2 * time.Hour)
	store := &deliveryMockStore{
		configs: []webhooks.Config{{
			ConfigUser: webhooks.ConfigUser{Endpoint: server.URL, Secret: webhooks.NewSecret()},
			ID:         "config-1", Active: true,
		}},
		claimed: []webhooks.Delivery{{
			ID: "delivery-expired", ConfigID: "config-1", Status: webhooks.StatusDeliveryDelivering,
			ClaimedAt: &now, CycleStartedAt: &cycleStartedAt,
		}},
	}
	NewDeliveryDispatcher(store, server.Client(), time.Second, expiredWindowPolicy{}, 1).dispatch(context.Background())

	require.Zero(t, hits)
	require.Equal(t, []string{"delivery-expired"}, store.failedClaims)
	require.Equal(t, []string{"retry window elapsed"}, store.failureReasons)
	require.Empty(t, store.completed, "no synthetic HTTP attempt should be persisted")
	require.Empty(t, store.attempts)
}

type cappedRetryPolicy struct{}

func (cappedRetryPolicy) GetRetryDelay(int) (time.Duration, error) { return time.Second, nil }
func (cappedRetryPolicy) CanRetryAttempt(int) error                { return errors.New("attempt cap reached") }

func TestDeliveryDispatcherEnforcesAttemptCapBeforeCallingEndpoint(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}))
	defer server.Close()
	now := time.Now().UTC()
	store := &deliveryMockStore{
		configs: []webhooks.Config{{
			ConfigUser: webhooks.ConfigUser{Endpoint: server.URL, Secret: webhooks.NewSecret()},
			ID:         "config-1", Active: true,
		}},
		claimed: []webhooks.Delivery{{
			ID: "delivery-attempt-capped", ConfigID: "config-1", Status: webhooks.StatusDeliveryDelivering,
			ClaimedAt: &now, CycleStartedAt: &now, AttemptCount: 15,
		}},
	}
	NewDeliveryDispatcher(store, server.Client(), time.Second, cappedRetryPolicy{}, 1).dispatch(context.Background())

	require.Zero(t, hits)
	require.Equal(t, []string{"delivery-attempt-capped"}, store.failedClaims)
	require.Equal(t, []string{"attempt cap reached"}, store.failureReasons)
	require.Empty(t, store.completed)
	require.Empty(t, store.attempts)
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
