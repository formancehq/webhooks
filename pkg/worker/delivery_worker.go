package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/alitto/pond"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/publish"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/metrics"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type DeliveryDispatcher struct {
	store       deliveryDispatchStore
	httpClient  *http.Client
	period      time.Duration
	retryPolicy webhooks.BackoffPolicy
	batchSize   int
	pool        *pond.WorkerPool
}

type deliveryEnqueuer interface {
	EnqueueEvent(ctx context.Context, eventID, idempotencyKey, eventType, payload string, createdAt time.Time) error
}

type deliveryDispatchStore interface {
	FindManyConfigs(ctx context.Context, filter map[string]any) ([]webhooks.Config, error)
	ClaimDeliveries(ctx context.Context, limit int) ([]webhooks.Delivery, error)
	CompleteDelivery(ctx context.Context, delivery webhooks.Delivery, attempt webhooks.DeliveryAttempt) (string, error)
	FailClaimedDelivery(ctx context.Context, id string, claimedAt time.Time, reason string) error
	CancelDelivery(ctx context.Context, id string) error
	RecoverStaleDeliveries(ctx context.Context, staleDuration time.Duration) (int64, error)
}

func NewDeliveryDispatcher(store deliveryDispatchStore, httpClient *http.Client, period time.Duration, retryPolicy webhooks.BackoffPolicy, batchSize int) *DeliveryDispatcher {
	if batchSize <= 0 {
		batchSize = 50
	}
	return &DeliveryDispatcher{
		store: store, httpClient: httpClient, period: period, retryPolicy: retryPolicy,
		batchSize: batchSize, pool: pond.New(batchSize, batchSize),
	}
}

func (d *DeliveryDispatcher) Run(ctx context.Context) {
	if d.period <= 0 {
		d.period = 3 * time.Second
	}
	ticker := time.NewTicker(d.period)
	recoveryTicker := time.NewTicker(staleRecoveryInterval)
	defer ticker.Stop()
	defer recoveryTicker.Stop()

	d.dispatch(ctx)
	for {
		select {
		case <-ctx.Done():
			d.pool.StopAndWait()
			return
		case <-recoveryTicker.C:
			if recovered, err := d.store.RecoverStaleDeliveries(ctx, staleRetryingAttemptAge); err != nil {
				logging.FromContext(ctx).Errorf("recovering stale deliveries: %s", err)
			} else if recovered > 0 {
				metrics.RecordRecoveredClaims(ctx, recovered)
			}
		case <-ticker.C:
			d.dispatch(ctx)
		}
	}
}

func (d *DeliveryDispatcher) dispatch(ctx context.Context) {
	deliveries, err := d.store.ClaimDeliveries(ctx, d.batchSize)
	if err != nil {
		logging.FromContext(ctx).Errorf("claiming deliveries: %s", err)
		return
	}
	group := d.pool.Group()
	for i := range deliveries {
		delivery := deliveries[i]
		group.Submit(func() { d.dispatchOne(ctx, delivery) })
	}
	group.Wait()
}

func (d *DeliveryDispatcher) dispatchOne(ctx context.Context, delivery webhooks.Delivery) {
	ctx, span := Tracer.Start(ctx, "DispatchDelivery", trace.WithAttributes(
		attribute.String("event_id", delivery.EventID),
		attribute.String("delivery_id", delivery.ID),
		attribute.Int("replay_generation", delivery.ReplayGeneration),
	))
	defer span.End()
	configs, err := d.store.FindManyConfigs(ctx, map[string]any{"id": delivery.ConfigID, "active": true})
	if err != nil {
		logging.FromContext(ctx).Errorf("finding config for delivery %s: %s", delivery.ID, err)
		span.RecordError(err)
		return
	}
	if len(configs) == 0 {
		if cancelErr := d.store.CancelDelivery(ctx, delivery.ID); cancelErr != nil {
			logging.FromContext(ctx).Errorf("cancelling delivery %s: %s", delivery.ID, cancelErr)
		}
		return
	}

	now := time.Now().UTC()
	var preflightErr error
	if delivery.AttemptCount > 0 {
		if limiter, ok := d.retryPolicy.(webhooks.RetryAttemptLimiter); ok {
			preflightErr = limiter.CanRetryAttempt(delivery.AttemptCount)
		}
	}
	if preflightErr == nil && delivery.CycleStartedAt != nil {
		preflightErr = limitRetryWindowBeforeAttempt(d.retryPolicy, *delivery.CycleStartedAt)
	}
	if preflightErr != nil {
		if delivery.ClaimedAt == nil {
			logging.FromContext(ctx).Errorf("failing delivery %s without claim timestamp", delivery.ID)
			return
		}
		if err := d.store.FailClaimedDelivery(ctx, delivery.ID, *delivery.ClaimedAt, preflightErr.Error()); err != nil {
			logging.FromContext(ctx).Errorf("failing delivery %s before attempt: %s", delivery.ID, err)
			span.RecordError(err)
			return
		}
		metrics.RecordDeliveryTransition(ctx, webhooks.StatusDeliveryFailed, "normal", 1)
		return
	}
	if delivery.CycleStartedAt == nil {
		delivery.CycleStartedAt = &now
	}
	attemptResult, err := webhooks.MakeAttempt(ctx, d.httpClient, d.retryPolicy, uuid.NewString(),
		delivery.ID, delivery.AttemptCount, configs[0], delivery.IdempotencyKey,
		[]byte(delivery.Payload), false, webhooks.WithFirstAttemptAt(*delivery.CycleStartedAt))
	if err != nil {
		logging.FromContext(ctx).Errorf("sending delivery %s: %s", delivery.ID, err)
		span.RecordError(err)
		return
	}

	completedAt := time.Now().UTC()
	delivery.AttemptCount++
	delivery.LastAttemptAt = &completedAt
	delivery.LastError = attemptResult.DeliveryError
	if attemptResult.StatusCode == 0 {
		delivery.LastStatusCode = nil
	} else {
		statusCode := attemptResult.StatusCode
		delivery.LastStatusCode = &statusCode
	}
	delivery.NextAttemptAt = nil
	outcome := webhooks.OutcomeDeliveryPermanentFailure
	switch attemptResult.Status {
	case webhooks.StatusAttemptSuccess:
		delivery.Status = webhooks.StatusDeliverySucceeded
		outcome = webhooks.OutcomeDeliverySucceeded
	case webhooks.StatusAttemptToRetry:
		delivery.Status = webhooks.StatusDeliveryPending
		delivery.NextAttemptAt = &attemptResult.NextRetryAfter
		outcome = webhooks.OutcomeDeliveryRetryableFailure
	default:
		delivery.Status = webhooks.StatusDeliveryFailed
	}

	attempt := webhooks.DeliveryAttempt{
		ID: uuid.NewString(), DeliveryID: delivery.ID, AttemptNumber: delivery.AttemptCount,
		ReplayGeneration: delivery.ReplayGeneration, Endpoint: configs[0].Endpoint,
		Outcome: outcome, StatusCode: attemptResult.StatusCode, Error: attemptResult.DeliveryError,
		ResponseExcerpt: attemptResult.ResponseExcerpt,
		CreatedAt:       completedAt,
	}
	durationMillis := attemptResult.Duration.Milliseconds()
	attempt.DurationMillis = &durationMillis
	finalStatus, err := d.store.CompleteDelivery(ctx, delivery, attempt)
	if err != nil {
		logging.FromContext(ctx).Errorf("completing delivery %s: %s", delivery.ID, err)
		span.RecordError(err)
		return
	}
	metrics.RecordDeliveryTransition(ctx, finalStatus, "normal", 1)
}

func processDeliveryMessages(store deliveryEnqueuer) func(msg *message.Message) error {
	return func(msg *message.Message) error {
		sourceSpan, event, err := publish.UnmarshalMessage(msg)
		if err != nil {
			return fmt.Errorf("unmarshal message: %w", err)
		}
		ctx, span := Tracer.Start(msg.Context(), "EnqueueDeliveries",
			trace.WithLinks(trace.Link{SpanContext: sourceSpan.SpanContext()}),
			trace.WithAttributes(attribute.String("event_id", msg.UUID)),
		)
		defer span.End()
		ctx = context.WithoutCancel(ctx)
		eventApp := strings.ToLower(event.App)
		eventType := strings.ToLower(event.Type)
		if eventApp == "" {
			event.Type = eventType
		} else {
			event.Type = strings.Join([]string{eventApp, eventType}, ".")
		}
		span.SetAttributes(attribute.String("event_type", event.Type))
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		if err := store.EnqueueEvent(ctx, msg.UUID, event.IdempotencyKey, event.Type, string(payload), time.Now().UTC()); err != nil {
			wrapped := fmt.Errorf("enqueue deliveries for event %s: %w", event.Type, err)
			span.RecordError(wrapped)
			return wrapped
		}
		return nil
	}
}
