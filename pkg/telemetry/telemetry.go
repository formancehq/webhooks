package telemetry

import (
	"github.com/numary/go-libs/sharedotlp/pkg/sharedotlptraces"
	"github.com/numary/webhooks/cmd/flag"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func Module() fx.Option {
	return sharedotlptraces.TracesModule(sharedotlptraces.ModuleConfig{
		Batch:    viper.GetBool(flag.OtelTracesBatch),
		Exporter: viper.GetString(flag.OtelTracesExporter),
		JaegerConfig: func() *sharedotlptraces.JaegerConfig {
			if viper.GetString(flag.OtelTracesExporter) != sharedotlptraces.JaegerExporter {
				return nil
			}
			return &sharedotlptraces.JaegerConfig{
				Endpoint: viper.GetString(flag.OtelTracesExporterJaegerEndpoint),
				User:     viper.GetString(flag.OtelTracesExporterJaegerUser),
				Password: viper.GetString(flag.OtelTracesExporterJaegerPassword),
			}
		}(),
		OTLPConfig: func() *sharedotlptraces.OTLPConfig {
			if viper.GetString(flag.OtelTracesExporter) != sharedotlptraces.OTLPExporter {
				return nil
			}
			return &sharedotlptraces.OTLPConfig{
				Mode:     viper.GetString(flag.OtelTracesExporterOTLPMode),
				Endpoint: viper.GetString(flag.OtelTracesExporterOTLPEndpoint),
				Insecure: viper.GetBool(flag.OtelTracesExporterOTLPInsecure),
			}
		}(),
	})
}
