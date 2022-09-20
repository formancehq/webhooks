package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/formancehq/webhooks/cmd/flag"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/go-libs/sharedlogging/sharedlogginglogrus"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	rootCmd = &cobra.Command{
		Use: "webhooks",
	}
	retriesSchedule []time.Duration
)

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		sharedlogging.Errorf("cobra.Command.Execute: %s", err)
		os.Exit(1)
	}
}

var ErrScheduleInvalid = errors.New("the retries schedule should only contain durations of at least 1 second")

func init() {
	cobra.CheckErr(flag.Init(rootCmd.PersistentFlags()))

	var err error
	logger := logrus.New()
	lvl, err := logrus.ParseLevel(viper.GetString(flag.LogLevel))
	if err != nil {
		cobra.CheckErr(fmt.Errorf("logrus.ParseLevel: %w", err))
	}
	logger.SetLevel(lvl)
	if logger.GetLevel() < logrus.DebugLevel {
		logger.SetFormatter(&logrus.JSONFormatter{})
	}

	sharedlogging.SetFactory(
		sharedlogging.StaticLoggerFactory(
			sharedlogginglogrus.New(logger)))

	retriesSchedule, err = rootCmd.PersistentFlags().GetDurationSlice(flag.RetriesSchedule)
	if err != nil {
		cobra.CheckErr(errors.Wrap(err, "flagSet.GetDurationSlice"))
	}

	// Check that the schedule is valid
	for _, s := range retriesSchedule {
		if s < time.Second {
			cobra.CheckErr(ErrScheduleInvalid)
		}
	}
}
