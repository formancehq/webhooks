package server

import (
	"github.com/numary/webhooks/cmd/flag"
	"github.com/numary/webhooks/pkg/httpserver"
	"github.com/numary/webhooks/pkg/storage/mongo"
	"github.com/numary/webhooks/pkg/telemetry"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func StartModule(addr string) fx.Option {
	var options []fx.Option

	if viper.GetBool(flag.OtelTraces) {
		options = append(options, telemetry.Module())
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
