package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/otlp/otlpmetrics"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"go.uber.org/fx"
)

var Tracer = otel.Tracer("listener")

func StartModule(cmd *cobra.Command, retriesCron time.Duration, retryPolicy webhooks.BackoffPolicy, retryBatchSize int, topics []string, retention RetentionConfig) fx.Option {
	var options []fx.Option

	options = append(options, fx.Invoke(func(r *message.Router, subscriber message.Subscriber, store storage.Store) {
		configureMessageRouter(r, subscriber, topics, store)
	}))
	options = append(options,
		fx.Provide(func(store storage.Store, httpClient *http.Client) *DeliveryDispatcher {
			return NewDeliveryDispatcher(store, httpClient, retriesCron, retryPolicy, retryBatchSize)
		}),
		fx.Invoke(runDeliveryDispatcher),
	)

	// Only register the DB-backed queue-depth gauge when metrics are actually
	// exported: the otlpmetrics module installs a periodic reader even with the
	// no-op exporter, which would otherwise run the COUNT query every collect
	// interval for a value nobody reads.
	exporter, _ := cmd.Flags().GetString(otlpmetrics.OtelMetricsExporterFlag)
	keepInMemory, _ := cmd.Flags().GetBool(otlpmetrics.OtelMetricsKeepInMemoryFlag)
	if exporter != "" || keepInMemory {
		options = append(options, fx.Invoke(func(store storage.Store) error {
			return registerQueueDepthMetric(store)
		}))
	}

	if retention.Enabled() {
		options = append(options,
			fx.Provide(func(store storage.Store) *Retention {
				return NewRetention(store, retention)
			}),
			fx.Invoke(runRetention),
		)
	}

	return fx.Options(options...)
}

// registerQueueDepthMetric registers the retry-queue-depth observable gauge. It
// binds to the global meter provider (a no-op when metrics are disabled), so the
// callback only queries the store when a real collector is scraping.
func registerQueueDepthMetric(store storage.Store) error {
	meter := otel.GetMeterProvider().Meter("webhooks")
	_, err := meter.Int64ObservableGauge(
		"webhooks_retry_queue_depth",
		metric.WithDescription("Number of webhook deliveries currently queued, capped at 1000000"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			n, err := store.CountPendingDeliveries(ctx)
			if err != nil {
				return err
			}
			o.Observe(n)
			return nil
		}),
	)
	return err
}

func runDeliveryDispatcher(lc fx.Lifecycle, dispatcher *DeliveryDispatcher) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				dispatcher.Run(ctx)
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
			case <-stopCtx.Done():
			}
			return nil
		},
	})
}

func runRetention(lc fx.Lifecycle, r *Retention) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(startCtx context.Context) error {
			logging.FromContext(startCtx).Debugf("starting retention...")
			go r.Run(ctx)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			logging.FromContext(stopCtx).Debugf("stopping retention...")
			cancel()
			select {
			case <-r.doneCh:
			case <-stopCtx.Done():
			}
			return nil
		},
	})
}

func configureMessageRouter(r *message.Router, subscriber message.Subscriber, topics []string, store storage.Store) {
	for _, topic := range topics {
		r.AddConsumerHandler(fmt.Sprintf("messages-%s", topic), topic, subscriber, processDeliveryMessages(store))
	}
}
