package server

import (
	"net/http"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/v2/otlp"

	"github.com/spf13/cobra"

	"github.com/formancehq/go-libs/v2/auth"
	"github.com/formancehq/webhooks/cmd/flag"
	"github.com/formancehq/webhooks/pkg/storage"

	"github.com/formancehq/go-libs/v2/httpserver"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/otlp/otlptraces"
	"go.uber.org/fx"
)

func FXModuleFromFlags(cmd *cobra.Command, addr string, debug bool, auditEnabled bool) fx.Option {
	var options []fx.Option
	pipeline, _ := cmd.Flags().GetString(flag.DeliveryPipeline)
	deliveriesEnabled := pipeline == "deliveries"

	options = append(options,
		otlp.FXModuleFromFlags(cmd),
		otlptraces.FXModuleFromFlags(cmd),
	)

	options = append(options, fx.Provide(
		func(
			store storage.Store,
			httpClient *http.Client,
			logger logging.Logger,
			info ServiceInfo,
			authenticator auth.Authenticator,
			publisher message.Publisher,
		) http.Handler {
			return newServerHandler(store, httpClient, logger, info, authenticator, publisher, debug, auditEnabled, deliveriesEnabled)
		},
	), fx.Invoke(func(lc fx.Lifecycle, handler http.Handler) {
		lc.Append(httpserver.NewHook(handler, httpserver.WithAddress(addr)))
	}))

	return fx.Module("webhooks server", options...)
}
