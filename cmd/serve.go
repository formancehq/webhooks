package cmd

import (
	"context"
	"io"

	"github.com/formancehq/go-libs/v2/aws/iam"
	loggingv2 "github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/otlp"
	"github.com/formancehq/go-libs/v2/otlp/otlptraces"
	"github.com/formancehq/go-libs/v2/publish"

	"github.com/formancehq/go-libs/v2/auth"
	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/licence"
	"github.com/formancehq/go-libs/v2/service"
	loggingv5 "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/go-libs/v5/pkg/fx/messagingfx"
	"github.com/formancehq/webhooks/cmd/flag"
	"github.com/formancehq/webhooks/pkg/backoff"
	innerotlp "github.com/formancehq/webhooks/pkg/otlp"
	"github.com/formancehq/webhooks/pkg/server"
	"github.com/formancehq/webhooks/pkg/storage/postgres"
	"github.com/formancehq/webhooks/pkg/worker"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

// loggerV5Adapter wraps a v2 Logger to satisfy the v5 Logger interface.
type loggerV5Adapter struct {
	inner loggingv2.Logger
}

func (a *loggerV5Adapter) Debugf(fmt string, args ...any) { a.inner.Debugf(fmt, args...) }
func (a *loggerV5Adapter) Infof(fmt string, args ...any)  { a.inner.Infof(fmt, args...) }
func (a *loggerV5Adapter) Errorf(fmt string, args ...any) { a.inner.Errorf(fmt, args...) }
func (a *loggerV5Adapter) Debug(args ...any)              { a.inner.Debug(args...) }
func (a *loggerV5Adapter) Info(args ...any)               { a.inner.Info(args...) }
func (a *loggerV5Adapter) Error(args ...any)              { a.inner.Error(args...) }
func (a *loggerV5Adapter) WithFields(fields map[string]any) loggingv5.Logger {
	return &loggerV5Adapter{inner: a.inner.WithFields(fields)}
}
func (a *loggerV5Adapter) WithField(key string, value any) loggingv5.Logger {
	return &loggerV5Adapter{inner: a.inner.WithField(key, value)}
}
func (a *loggerV5Adapter) WithContext(ctx context.Context) loggingv5.Logger {
	return &loggerV5Adapter{inner: a.inner.WithContext(ctx)}
}
func (a *loggerV5Adapter) Writer() io.Writer       { return a.inner.Writer() }
func (a *loggerV5Adapter) Enabled(loggingv5.Level) bool { return true }

func newServeCommand() *cobra.Command {
	ret := &cobra.Command{
		Use:     "serve",
		Aliases: []string{"server"},
		Short:   "Run webhooks server",
		RunE:    serve,
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

func serve(cmd *cobra.Command, _ []string) error {
	connectionOptions, err := bunconnect.ConnectionOptionsFromFlags(cmd)
	if err != nil {
		return err
	}

	listen, _ := cmd.Flags().GetString(flag.Listen)
	options := []fx.Option{
		fx.Provide(func() server.ServiceInfo {
			return server.ServiceInfo{
				Version: Version,
			}
		}),
		fx.Provide(func(logger loggingv2.Logger) loggingv5.Logger {
			return &loggerV5Adapter{inner: logger}
		}),
		auth.FXModuleFromFlags(cmd),
		postgres.NewModule(*connectionOptions, service.IsDebug(cmd)),
		innerotlp.HttpClientModule(),
		messagingfx.PublishModuleFromFlags(cmd, service.IsDebug(cmd)),
		server.FXModuleFromFlags(cmd, listen, service.IsDebug(cmd)),
		licence.FXModuleFromFlags(cmd, ServiceName),
	}
	isWorker, _ := cmd.Flags().GetBool(flag.Worker)
	if isWorker {
		retryPeriod, _ := cmd.Flags().GetDuration(flag.RetryPeriod)
		retryBatchSize, _ := cmd.Flags().GetInt(flag.RetryBatchSize)
		minBackOffDelay, _ := cmd.Flags().GetDuration(flag.MinBackoffDelay)
		maxBackOffDelay, _ := cmd.Flags().GetDuration(flag.MaxBackoffDelay)
		abortAfter, _ := cmd.Flags().GetDuration(flag.AbortAfter)
		topics, _ := cmd.Flags().GetStringSlice(flag.KafkaTopics)

		options = append(options, worker.StartModule(
			retryPeriod,
			backoff.NewExponential(
				minBackOffDelay,
				maxBackOffDelay,
				abortAfter,
			),
			retryBatchSize,
			topics,
		))
	}

	return service.New(cmd.OutOrStdout(), options...).Run(cmd)
}
