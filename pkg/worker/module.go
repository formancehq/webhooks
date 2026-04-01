package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/publish"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/google/uuid"
	"go.uber.org/fx"
)

var Tracer = otel.Tracer("listener")

func StartModule(cmd *cobra.Command, retriesCron time.Duration, retryPolicy webhooks.BackoffPolicy, retryBatchSize int, debug bool, topics []string) fx.Option {
	var options []fx.Option

	options = append(options, fx.Invoke(func(r *message.Router, subscriber message.Subscriber, store storage.Store, httpClient *http.Client) {
		configureMessageRouter(r, subscriber, topics, store, httpClient, retryPolicy)
	}))
	options = append(options, publish.FXModuleFromFlags(cmd, debug))
	options = append(options, fx.Provide(
		func() (time.Duration, webhooks.BackoffPolicy, int) {
			return retriesCron, retryPolicy, retryBatchSize
		},
		NewRetrier,
	))
	options = append(options, fx.Invoke(run))

	return fx.Options(options...)
}

func run(lc fx.Lifecycle, w *Retrier) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logging.FromContext(ctx).Debugf("starting worker...")
			go func() {
				if err := w.Run(context.Background()); err != nil {
					logging.FromContext(ctx).Errorf("kafka.Retrier.Run: %s", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logging.FromContext(ctx).Debugf("stopping worker...")
			w.Stop(ctx)

			if err := w.store.Close(ctx); err != nil {
				return fmt.Errorf("storage.Store.Close: %w", err)
			}
			return nil
		},
	})
}

func configureMessageRouter(r *message.Router, subscriber message.Subscriber, topics []string,
	store storage.Store, httpClient *http.Client, retryPolicy webhooks.BackoffPolicy,
) {
	for _, topic := range topics {
		r.AddNoPublisherHandler(fmt.Sprintf("messages-%s", topic), topic, subscriber, processMessages(store, httpClient, retryPolicy))
	}
}

func processMessages(store storage.Store, httpClient *http.Client, retryPolicy webhooks.BackoffPolicy) func(msg *message.Message) error {
	return func(msg *message.Message) error {
		var ev *publish.EventMessage
		span, ev, err := publish.UnmarshalMessage(msg)
		if err != nil {
			return fmt.Errorf("unmarshal message: %w", err)
		}

		ctx, span := Tracer.Start(msg.Context(), "HandleEvent",
			trace.WithLinks(trace.Link{
				SpanContext: span.SpanContext(),
			}),
			trace.WithAttributes(
				attribute.String("event-id", msg.UUID),
				attribute.Bool("duplicate", false),
				attribute.String("event-type", ev.Type),
			),
		)
		defer span.End()
		defer func() {
			if err != nil {
				span.RecordError(err)
			}
		}()
		ctx = context.WithoutCancel(ctx)

		eventApp := strings.ToLower(ev.App)
		eventType := strings.ToLower(ev.Type)

		if eventApp == "" {
			ev.Type = eventType
		} else {
			ev.Type = strings.Join([]string{eventApp, eventType}, ".")
		}

		filter := map[string]any{
			"event_types": ev.Type,
			"active":      true,
		}
		logging.FromContext(ctx).Debugf("searching configs with event types: %s", ev.Type)
		cfgs, err := store.FindManyConfigs(ctx, filter)
		if err != nil {
			return fmt.Errorf("find configs for event %s: %w", ev.Type, err)
		}

		data, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}

		for _, cfg := range cfgs {
			logging.FromContext(ctx).Debugf("dispatching webhook to config %s at %s", cfg.ID, cfg.Endpoint)

			attempt, err := webhooks.MakeAttempt(ctx, httpClient, retryPolicy, uuid.NewString(),
				uuid.NewString(), 0, cfg, ev.IdempotencyKey, data, false)
			if err != nil {
				logging.FromContext(ctx).Errorf("make attempt for config %s: %s", cfg.ID, err)
				continue
			}

			if attempt.Status == webhooks.StatusAttemptSuccess {
				logging.FromContext(ctx).Debugf(
					"webhook sent with ID %s to %s of type %s",
					attempt.WebhookID, cfg.Endpoint, ev.Type)
			}

			if err := store.InsertOneAttempt(ctx, attempt); err != nil {
				logging.FromContext(ctx).Errorf("insert attempt for config %s: %s", cfg.ID, err)
				if attempt.Status != webhooks.StatusAttemptSuccess {
					// Can't persist the retry record — nack so the broker redelivers
					return fmt.Errorf("insert attempt for config %s: %w", cfg.ID, err)
				}
				continue
			}
		}

		return nil
	}
}
