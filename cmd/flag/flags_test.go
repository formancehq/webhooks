package flag

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestAuditEnabledDefaultsToFalse(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	Init(flags)

	auditEnabled, err := flags.GetBool(AuditEnabled)
	require.NoError(t, err)
	require.False(t, auditEnabled)
}

func TestAuditEnabledCanBeEnabled(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	Init(flags)

	require.NoError(t, flags.Parse([]string{"--" + AuditEnabled}))

	auditEnabled, err := flags.GetBool(AuditEnabled)
	require.NoError(t, err)
	require.True(t, auditEnabled)
}
