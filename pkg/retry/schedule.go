package retry

import (
	"time"

	"github.com/numary/webhooks/constants"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

func BuildSchedule() []time.Duration {
	stringSchedule := viper.GetStringSlice(constants.RetryScheduleFlag)
	durationSchedule := make([]time.Duration, len(stringSchedule))
	for i, s := range stringSchedule {
		d, err := time.ParseDuration(s)
		if err != nil {
			panic(errors.Wrap(err, "parsing schedule duration"))
		}
		durationSchedule[i] = d
	}
	return durationSchedule
}
