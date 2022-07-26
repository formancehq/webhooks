package server

import (
	"context"
	"errors"
	"net/http"
	"syscall"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks-cloud/internal/storage/mongodb"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

var (
	Version  = "develop"
	BindAddr = ":8080"
)

func FxOptions() fx.Option {
	return fx.Options(
		fx.Provide(
			mongodb.NewStore,
			NewWebhooksHandler,
			NewMux,
		),
		fx.Invoke(Register),
	)
}

func Start(*cobra.Command, []string) error {
	sharedlogging.Infof("env: %+v", syscall.Environ())
	sharedlogging.Infof("app started with version: %s", Version)

	app := fx.New(FxOptions())
	app.Run()

	return nil
}

func NewMux(lc fx.Lifecycle) *http.ServeMux {
	sharedlogging.Infof("Executing NewMux.")
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    BindAddr,
		Handler: mux,
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			sharedlogging.Infof("Starting HTTP server.")
			go func() {
				if err := server.ListenAndServe(); err != nil &&
					!errors.Is(err, http.ErrServerClosed) {
					sharedlogging.Errorf("ListenAndServe: %s", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			sharedlogging.Infof("Stopping HTTP server.")
			return server.Shutdown(ctx)
		},
	})

	return mux
}

func Register(mux *http.ServeMux, h http.Handler) {
	mux.Handle("/", h)
}
