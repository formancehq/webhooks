package cmd

import (
	"os"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/go-libs/sharedlogging/sharedlogginglogrus"
	"github.com/numary/webhooks-cloud/api/server"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func Execute() {
	logger := logrus.New()
	loggerFactory := sharedlogging.StaticLoggerFactory(sharedlogginglogrus.New(logger))
	sharedlogging.SetFactory(loggerFactory)

	rootCmd := &cobra.Command{
		Use:  "webhooks",
		RunE: server.Start,
	}

	if err := rootCmd.Execute(); err != nil {
		sharedlogging.Errorf("cobra.Command.Execute: %s", err)
		os.Exit(1)
	}
}
