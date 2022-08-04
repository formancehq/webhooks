package cmd

import (
	"github.com/numary/webhooks-cloud/pkg/worker"
	"github.com/spf13/cobra"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Launch worker",
	RunE:  worker.Start,
}

func init() {
	rootCmd.AddCommand(workerCmd)
}
