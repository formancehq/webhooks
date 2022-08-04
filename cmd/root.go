package cmd

import (
	"os"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/go-libs/sharedlogging/sharedlogginglogrus"
	"github.com/numary/webhooks-cloud/api/server"
	"github.com/numary/webhooks-cloud/env"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var Version = "develop"

func Execute() {
	logger := logrus.New()
	loggerFactory := sharedlogging.StaticLoggerFactory(sharedlogginglogrus.New(logger))
	sharedlogging.SetFactory(loggerFactory)

	viper.SetDefault("version", Version)

	rootCmd := &cobra.Command{
		Use:  "webhooks",
		RunE: server.Start,
	}

	if err := env.Flags(rootCmd.PersistentFlags()); err != nil {
		sharedlogging.Errorf("env.Flags: %s", err)
		os.Exit(1)
	}

	if err := rootCmd.Execute(); err != nil {
		sharedlogging.Errorf("cobra.Command.Execute: %s", err)
		os.Exit(1)
	}
}
