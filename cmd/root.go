package cmd

import (
	"fmt"
	"os"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/go-libs/sharedlogging/sharedlogginglogrus"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	debugFlag = "debug"
)

var rootCmd = &cobra.Command{
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {

		if err := bindFlagsToViper(cmd); err != nil {
			return err
		}

		logrusLogger := logrus.New()
		if viper.GetBool(debugFlag) {
			logrusLogger.SetLevel(logrus.DebugLevel)
			logrusLogger.Infof("Debug mode enabled.")
		}
		logger := sharedlogginglogrus.New(logrusLogger)
		sharedlogging.SetFactory(sharedlogging.StaticLoggerFactory(logger))

		return nil
	},
}

func exitWithCode(code int, v ...any) {
	fmt.Fprintln(os.Stdout, v...)
	os.Exit(code)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		exitWithCode(1, err)
	}
}

func init() {
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	rootCmd.PersistentFlags().BoolP(debugFlag, "d", false, "Debug mode")
}

//
//func Execute() {
//	logger := logrus.New()
//	loggerFactory := sharedlogging.StaticLoggerFactory(sharedlogginglogrus.New(logger))
//	sharedlogging.SetFactory(loggerFactory)
//
//	viper.SetDefault("version", Version)
//
//	////rootCmd := &cobra.Command{
//	////	Use:  "webhooks",
//	////	RunE: server.Start,
//	////}
//	//
//	//if err := env.Flags(rootCmd.PersistentFlags()); err != nil {
//	//	sharedlogging.Errorf("env.Flags: %s", err)
//	//	os.Exit(1)
//	//}
//	//
//	//if err := rootCmd.Execute(); err != nil {
//	//	sharedlogging.Errorf("cobra.Command.Execute: %s", err)
//	//	os.Exit(1)
//	//}
//}
