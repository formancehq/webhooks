package server

import (
	"context"
	"net/http"
	"syscall"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/internal/storage/mongo"
	"github.com/numary/webhooks/internal/svix"
	"github.com/numary/webhooks/pkg/mux"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func Start(cmd *cobra.Command, args []string) {
	app := fx.New(StartModule(cmd.Context(), http.DefaultClient))
	app.Run()
}

func StartModule(ctx context.Context, httpClient *http.Client) fx.Option {
	sharedlogging.GetLogger(ctx).Debugf(
		"webhooks server module started: env variables: %+v viper keys: %+v",
		syscall.Environ(), viper.AllKeys())

	return fx.Module("webhooks server module",
		fx.Provide(
			func() *http.Client { return httpClient },
			mongo.NewConfigStore,
			svix.New,
			newServerHandler,
			mux.NewServer,
		),
		fx.Invoke(register),
	)
}

func register(mux *http.ServeMux, h http.Handler) {
	mux.Handle("/", h)
}
