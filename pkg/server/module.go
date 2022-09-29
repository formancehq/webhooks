package server

import (
	"github.com/numary/go-libs/sharedotlp/pkg/sharedotlptraces"
	"github.com/spf13/viper"
	"go.formance.com/webhooks/pkg/httpserver"
	"go.formance.com/webhooks/pkg/storage/mongo"
	"go.uber.org/fx"
)

func StartModule(addr string) fx.Option {
	var options []fx.Option

	if mod := sharedotlptraces.CLITracesModule(viper.GetViper()); mod != nil {
		options = append(options, mod)
	}

	options = append(options, fx.Provide(
		func() string { return addr },
		httpserver.NewMuxServer,
		mongo.NewStore,
		newServerHandler,
	))
	options = append(options, fx.Invoke(httpserver.RegisterHandler))
	options = append(options, fx.Invoke(httpserver.Run))

	return fx.Module("webhooks server", options...)
}
