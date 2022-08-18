package cmd

import (
	"github.com/numary/webhooks/pkg/server"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start webhooks server",
	Run:   server.Start,
}

func init() {
	rootCmd.AddCommand(serverCmd)
}
