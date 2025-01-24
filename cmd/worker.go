package cmd

import (
	"net/http"

	"github.com/formancehq/go-libs/v2/otlp"

	"github.com/formancehq/go-libs/v2/auth"
	"github.com/formancehq/go-libs/v2/aws/iam"
	"github.com/formancehq/go-libs/v2/publish"

	"github.com/formancehq/webhooks/pkg/storage/postgres"

	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/licence"

	"github.com/formancehq/go-libs/v2/otlp/otlptraces"

	"github.com/formancehq/go-libs/v2/httpserver"
	"github.com/formancehq/go-libs/v2/service"
	"github.com/formancehq/webhooks/cmd/flag"
	"github.com/formancehq/webhooks/pkg/backoff"
	innerotlp "github.com/formancehq/webhooks/pkg/otlp"
	"github.com/formancehq/webhooks/pkg/worker"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func newWorkerCommand() *cobra.Command {
	ret := &cobra.Command{
		Use:     "worker",
		Short:   "Run webhooks worker",
		RunE:    runWorker,
		PreRunE: handleAutoMigrate,
	}
	otlp.AddFlags(ret.Flags())
	otlptraces.AddFlags(ret.Flags())
	publish.AddFlags(ServiceName, ret.Flags())
	auth.AddFlags(ret.Flags())
	flag.Init(ret.Flags())
	bunconnect.AddFlags(ret.Flags())
	iam.AddFlags(ret.Flags())
	service.AddFlags(ret.Flags())
	licence.AddFlags(ret.Flags())

	return ret
}

func runWorker(cmd *cobra.Command, _ []string) error {
	connectionOptions, err := bunconnect.ConnectionOptionsFromFlags(cmd)
	if err != nil {
		return err
	}

	retryPeriod, _ := cmd.Flags().GetDuration(flag.RetryPeriod)
	minBackOffDelay, _ := cmd.Flags().GetDuration(flag.MinBackoffDelay)
	maxBackOffDelay, _ := cmd.Flags().GetDuration(flag.MaxBackoffDelay)
	abortAfter, _ := cmd.Flags().GetDuration(flag.AbortAfter)
	topics, _ := cmd.Flags().GetStringSlice(flag.KafkaTopics)
	listen, _ := cmd.Flags().GetString(flag.Listen)

	return service.New(
		cmd.OutOrStdout(),
		innerotlp.HttpClientModule(),
		licence.FXModuleFromFlags(cmd, ServiceName),
		postgres.NewModule(*connectionOptions, service.IsDebug(cmd)),
		fx.Provide(worker.NewWorkerHandler),
		fx.Invoke(func(lc fx.Lifecycle, h http.Handler) {
			lc.Append(httpserver.NewHook(h, httpserver.WithAddress(listen)))
		}),
		otlp.FXModuleFromFlags(cmd),
		otlptraces.FXModuleFromFlags(cmd),
		worker.StartModule(
			cmd,
			retryPeriod,
			backoff.NewExponential(
				minBackOffDelay,
				maxBackOffDelay,
				abortAfter,
			),
			service.IsDebug(cmd),
			topics,
		),
	).Run(cmd)
}
