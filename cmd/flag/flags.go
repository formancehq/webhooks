package flag

import (
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	LogLevel                      = "log-level"
	HttpBindAddressServer         = "http-bind-address-server"
	HttpBindAddressWorkerMessages = "http-bind-address-worker-messages"
	HttpBindAddressWorkerRetries  = "http-bind-address-worker-retries"

	RetriesSchedule = "retries-schedule"
	RetriesCron     = "retries-cron"

	StorageMongoConnString   = "storage-mongo-conn-string"
	StorageMongoDatabaseName = "storage-mongo-database-name"

	KafkaBrokers       = "kafka-brokers"
	KafkaGroupID       = "kafka-consumer-group"
	KafkaTopics        = "kafka-topics"
	KafkaTLSEnabled    = "kafka-tls-enabled"
	KafkaSASLEnabled   = "kafka-sasl-enabled"
	KafkaSASLMechanism = "kafka-sasl-mechanism"
	KafkaUsername      = "kafka-username"
	KafkaPassword      = "kafka-password"
)

const (
	DefaultBindAddressServer         = ":8080"
	DefaultBindAddressWorkerMessages = ":8081"
	DefaultBindAddressWorkerRetries  = ":8082"

	DefaultMongoConnString   = "mongodb://admin:admin@localhost:27017/"
	DefaultMongoDatabaseName = "webhooks"

	DefaultKafkaTopic   = "default"
	DefaultKafkaBroker  = "localhost:9092"
	DefaultKafkaGroupID = "webhooks"
)

var (
	DefaultRetriesSchedule = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 5 * time.Hour, 24 * time.Hour}
	DefaultRetriesCron     = time.Minute
)

func Init(flagSet *pflag.FlagSet) error {
	flagSet.String(LogLevel, logrus.InfoLevel.String(), "Log level")

	flagSet.String(HttpBindAddressServer, DefaultBindAddressServer, "server HTTP bind address")
	flagSet.String(HttpBindAddressWorkerMessages, DefaultBindAddressWorkerMessages, "worker messages HTTP bind address")
	flagSet.String(HttpBindAddressWorkerRetries, DefaultBindAddressWorkerRetries, "worker retries HTTP bind address")
	flagSet.DurationSlice(RetriesSchedule, DefaultRetriesSchedule, "worker retries schedule")
	flagSet.Duration(RetriesCron, DefaultRetriesCron, "worker retries cron")
	flagSet.String(StorageMongoConnString, DefaultMongoConnString, "Mongo connection string")
	flagSet.String(StorageMongoDatabaseName, DefaultMongoDatabaseName, "Mongo database name")

	flagSet.StringSlice(KafkaBrokers, []string{DefaultKafkaBroker}, "Kafka brokers")
	flagSet.String(KafkaGroupID, DefaultKafkaGroupID, "Kafka consumer group")
	flagSet.StringSlice(KafkaTopics, []string{DefaultKafkaTopic}, "Kafka topics")
	flagSet.Bool(KafkaTLSEnabled, false, "Kafka TLS enabled")
	flagSet.Bool(KafkaSASLEnabled, false, "Kafka SASL enabled")
	flagSet.String(KafkaSASLMechanism, "", "Kafka SASL mechanism")
	flagSet.String(KafkaUsername, "", "Kafka username")
	flagSet.String(KafkaPassword, "", "Kafka password")

	if err := viper.BindPFlags(flagSet); err != nil {
		return fmt.Errorf("viper.BinPFlags: %w", err)
	}

	viper.GetViper().SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.GetViper().AutomaticEnv()

	return nil
}
