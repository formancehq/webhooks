package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

func TestWorkerHTTPServerModuleProvidesHandlerDependencies(t *testing.T) {
	cmd := newWorkerCommand()

	app := fx.New(
		workerHTTPServerModule(cmd, "127.0.0.1:0"),
		fx.NopLogger,
	)

	require.NoError(t, app.Err())
}
