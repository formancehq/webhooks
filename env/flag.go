package env

import (
	"github.com/davecgh/go-spew/spew"
	"github.com/numary/webhooks-cloud/cmd/constants"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func Flags(flagSet *pflag.FlagSet) error {
	defineFlags(flagSet)
	if err := viper.BindPFlags(flagSet); err != nil {
		return err
	}
	BindEnv(viper.GetViper())
	spew.Dump("FLAGS VIPER ALL SETTINGS", viper.AllSettings())
	return nil
}

func defineFlags(flagSet *pflag.FlagSet) {
	flagSet.String(constants.ServerHttpBindAddressFlag, constants.DefaultBindAddress, "API bind address")
	flagSet.String(constants.StorageMongoConnStringFlag, constants.DefaultMongoConnString, "Mongo connection string")

	flagSet.StringSlice(constants.KafkaBrokersFlag, []string{constants.DefaultKafkaBroker}, "Kafka brokers")
	flagSet.String(constants.KafkaGroupIDFlag, constants.DefaultKafkaGroupID, "Kafka consumer group")
	flagSet.String(constants.KafkaTopicFlag, constants.DefaultKafkaTopic, "Kafka topic")
	flagSet.Bool(constants.KafkaTLSEnabledFlag, false, "Kafka TLS enabled")
	flagSet.Bool(constants.KafkaTLSInsecureSkipVerifyFlag, false, "Kafka TLS insecure skip verify")
	flagSet.Bool(constants.KafkaSASLEnabledFlag, false, "Kafka SASL enabled")
	flagSet.String(constants.KafkaSASLMechanismFlag, "", "Kafka SASL mechanism")
	flagSet.String(constants.KafkaUsernameFlag, "", "Kafka username")
	flagSet.String(constants.KafkaPasswordFlag, "", "Kafka password")

	flagSet.String(constants.SvixTokenFlag, "", "Svix auth token")
	flagSet.String(constants.SvixAppIdFlag, "", "Svix app ID")
}
