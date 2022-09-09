package messages

import (
	"context"
	"fmt"
	"net/http"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/pkg/httpserver"
	"github.com/numary/webhooks/pkg/retry"
	"github.com/numary/webhooks/pkg/storage/mongo"
	"go.uber.org/fx"
)

func StartModule(addr string, httpClient *http.Client) fx.Option {
	return fx.Module("webhooks worker messages",
		fx.Provide(
			func() (string, *http.Client) { return addr, httpClient },
			httpserver.NewMuxServer,
			mongo.NewStore,
			retry.BuildSchedule,
			NewWorkerMessages,
			newWorkerMessagesHandler,
		),
		fx.Invoke(httpserver.RegisterHandler),
		fx.Invoke(httpserver.Run),
		fx.Invoke(run),
	)
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
