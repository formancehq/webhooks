package worker

import (
	"context"
	"fmt"
	"net/http"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/pkg/httpserver"
	"github.com/numary/webhooks/pkg/storage/mongo"
	"go.uber.org/fx"
)

func StartModule(addr string, httpClient *http.Client) fx.Option {
	return fx.Module("webhooks worker",
		fx.Provide(
			func() (string, *http.Client) { return addr, httpClient },
			httpserver.NewMuxServer,
			mongo.NewStore,
			NewWorker,
			newWorkerHandler,
		),
		fx.Invoke(httpserver.RegisterHandler),
		fx.Invoke(httpserver.Run),
		fx.Invoke(run),
	)
}

func run(lc fx.Lifecycle, w *Worker) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("starting worker...")
			go func() {
				if err := w.Run(ctx); err != nil {
					sharedlogging.GetLogger(ctx).Errorf("kafka.Worker.Run: %s", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("stopping worker...")
			w.Stop(ctx)
			err1 := w.store.Close(ctx)
			err2 := w.reader.Close()
			if err1 != nil || err2 != nil {
				return fmt.Errorf("[closing store: %s] [closing reader: %w]", err1, err2)
			}
			return nil
		},
	})
}
