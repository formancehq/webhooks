package cmd

import (
	"testing"

	cmdflag "github.com/formancehq/webhooks/cmd/flag"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestDeliveryPipelineFromFlags(t *testing.T) {
	for _, testCase := range []struct {
		value   string
		wantErr bool
	}{
		{value: "legacy"},
		{value: "deliveries"},
		{value: "invalid", wantErr: true},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			command := &cobra.Command{}
			cmdflag.Init(command.Flags())
			require.NoError(t, command.Flags().Set(cmdflag.DeliveryPipeline, testCase.value))

			pipeline, err := deliveryPipelineFromFlags(command)
			if testCase.wantErr {
				require.ErrorContains(t, err, "expected legacy or deliveries")
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.value, pipeline)
		})
	}
}
