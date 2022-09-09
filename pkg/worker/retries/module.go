package retries

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
	return fx.Module("webhooks worker retries",
		fx.Provide(
			func() (string, *http.Client) { return addr, httpClient },
			httpserver.NewMuxServer,
			mongo.NewStore,
			retry.BuildSchedule,
			NewWorkerRetries,
			newWorkerRetriesHandler,
		),
		fx.Invoke(httpserver.RegisterHandler),
		fx.Invoke(httpserver.Run),
		fx.Invoke(run),
	)
}

func run(lc fx.Lifecycle, w *WorkerRetries) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("starting worker retries...")
			go func() {
				if err := w.Run(ctx); err != nil {
					sharedlogging.GetLogger(ctx).Errorf("kafka.WorkerRetries.Run: %s", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("stopping worker retries...")
			w.Stop(ctx)
			if err := w.store.Close(ctx); err != nil {
				return fmt.Errorf("storage.Store.Close: %w", err)
			}
			return nil
		},
	})
}
