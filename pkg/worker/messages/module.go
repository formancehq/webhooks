package messages

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/cmd/flag"
	"github.com/numary/webhooks/pkg/httpserver"
	"github.com/numary/webhooks/pkg/storage/mongo"
	"github.com/numary/webhooks/pkg/telemetry"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func StartModule(addr string, httpClient *http.Client, retriesSchedule []time.Duration) fx.Option {
	var options []fx.Option

	if viper.GetBool(flag.OtelTraces) {
		options = append(options, telemetry.Module())
	}

	options = append(options, fx.Provide(
		func() (string, *http.Client, []time.Duration) {
			return addr, httpClient, retriesSchedule
		},
		httpserver.NewMuxServer,
		mongo.NewStore,
		NewWorkerMessages,
		newWorkerMessagesHandler,
	))
	options = append(options, fx.Invoke(httpserver.RegisterHandler))
	options = append(options, fx.Invoke(httpserver.Run))
	options = append(options, fx.Invoke(run))

	return fx.Module("webhooks worker messages", options...)
}

func run(lc fx.Lifecycle, w *WorkerMessages) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("starting worker messages...")
			go func() {
				if err := w.Run(ctx); err != nil {
					sharedlogging.GetLogger(ctx).Errorf("kafka.WorkerMessages.Run: %s", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("stopping worker messages...")
			w.Stop(ctx)
			w.kafkaClient.Close()
			if err := w.store.Close(ctx); err != nil {
				return fmt.Errorf("storage.Store.Close: %w", err)
			}
			return nil
		},
	})
}
