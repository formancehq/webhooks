package cmd

import (
	"testing"

	"github.com/formancehq/go-libs/v2/logging"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

func TestWorkerServiceOptionsProvideAllDependencies(t *testing.T) {
	cmd := newWorkerCommand()
	require.NoError(t, cmd.Flags().Set("postgres-uri", "postgresql://localhost/webhooks"))
	options, err := workerServiceOptions(cmd)
	require.NoError(t, err)

	options = append(options,
		fx.Supply(fx.Annotate(logging.Testing(), fx.As(new(logging.Logger)))),
	)

	require.NoError(t, fx.ValidateApp(options...))
}
