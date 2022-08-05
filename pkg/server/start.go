package server

import (
	"net/http"
	"syscall"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks-cloud/internal/storage/mongo"
	"github.com/numary/webhooks-cloud/internal/svix"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func Start(*cobra.Command, []string) error {
	sharedlogging.Infof("env: %+v", syscall.Environ())

	app := fx.New(StartModule())
	app.Run()

	return nil
}

func StartModule() fx.Option {
	return fx.Module("webhooks server module",
		fx.Provide(
			mongo.NewConfigStore,
			svix.New,
			newConfigHandler,
			newHttpServeMux,
		),
		fx.Invoke(register),
	)
}

func register(mux *http.ServeMux, h http.Handler) {
	mux.Handle("/", h)
}
