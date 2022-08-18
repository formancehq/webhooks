package cmd

import (
	"github.com/numary/webhooks/pkg/worker"
	"github.com/spf13/cobra"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Start webhooks worker",
	Run:   worker.Start,
}

func init() {
	rootCmd.AddCommand(workerCmd)
}
