package cmd

import (
	"os"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/go-libs/sharedlogging/sharedlogginglogrus"
	"github.com/numary/webhooks-cloud/api/server"
	"github.com/numary/webhooks-cloud/cmd/constants"
	"github.com/numary/webhooks-cloud/cmd/internal"
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

	rootCmd.PersistentFlags().String(constants.ServerHttpBindAddressFlag,
		constants.DefaultBindAddress, "API bind address")
	rootCmd.PersistentFlags().String(constants.StorageMongoConnStringFlag,
		constants.DefaultMongoConnString, "Mongo connection string")

	rootCmd.PersistentFlags().StringSlice(constants.KafkaBrokerFlag, []string{""}, "Kafka broker")
	rootCmd.PersistentFlags().String(constants.KafkaGroupIDFlag, "organization-manager", "Kafka consumer group")
	rootCmd.PersistentFlags().String(constants.KafkaTopicFlag, "auth", "Kafka topic")
	rootCmd.PersistentFlags().Bool(constants.KafkaTLSEnabledFlag, false, "")
	rootCmd.PersistentFlags().Bool(constants.KafkaTLSInsecureSkipVerifyFlag, false, "")
	rootCmd.PersistentFlags().Bool(constants.KafkaSASLEnabledFlag, false, "")
	rootCmd.PersistentFlags().String(constants.KafkaSASLMechanismFlag, "", "")
	rootCmd.PersistentFlags().String(constants.KafkaUsernameFlag, "", "")
	rootCmd.PersistentFlags().String(constants.KafkaPasswordFlag, "", "")

	if err := viper.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		sharedlogging.Errorf("viper.BindFlags: %s", err)
		os.Exit(1)
	}

	internal.BindEnv(viper.GetViper())

	if err := rootCmd.Execute(); err != nil {
		sharedlogging.Errorf("cobra.Command.Execute: %s", err)
		os.Exit(1)
	}
}
