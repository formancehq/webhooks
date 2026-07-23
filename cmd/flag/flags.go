package flag

import (
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

const (
	LogLevel = "log-level"
	Listen   = "listen"
	Worker   = "worker"

	AuditEnabled = "audit-enabled"

	RetryPeriod      = "retry-period"
	RetryBatchSize   = "retry-batch-size"
	AbortAfter       = "abort-after"
	MaxAttempts      = "max-attempts"
	MinBackoffDelay  = "min-backoff-delay"
	MaxBackoffDelay  = "max-backoff-delay"
	DeliveryPipeline = "delivery-pipeline"

	RetentionPeriod       = "retention-period"
	RetentionSuccessDelay = "retention-success-delay"
	RetentionFailedDelay  = "retention-failed-delay"

	KafkaTopics = "kafka-topics"
	AutoMigrate = "auto-migrate"
)

const (
	DefaultBindAddressServer = ":8080"

	DefaultPostgresConnString = "postgresql://webhooks:webhooks@127.0.0.1/webhooks?sslmode=disable"

	DefaultKafkaTopic = "default"
)

var (
	DefaultRetryPeriod    = 3 * time.Second
	DefaultRetryBatchSize = 50
	DefaultAbortAfter     = 10 * time.Hour
	DefaultMaxAttempts    = 15

	DefaultRetentionPeriod       = time.Hour
	DefaultRetentionSuccessDelay = 30 * 24 * time.Hour
	DefaultRetentionFailedDelay  = 90 * 24 * time.Hour
)

func Init(flagSet *pflag.FlagSet) {
	flagSet.String(LogLevel, logrus.InfoLevel.String(), "Log level")

	flagSet.String(Listen, DefaultBindAddressServer, "server HTTP bind address")
	flagSet.Duration(RetryPeriod, DefaultRetryPeriod, "worker retry period")
	flagSet.Int(RetryBatchSize, DefaultRetryBatchSize, "number of webhook IDs to claim per retry tick")
	flagSet.Bool(Worker, false, "Enable worker on server")
	flagSet.Bool(AuditEnabled, false, "Enable HTTP audit events publishing")

	flagSet.StringSlice(KafkaTopics, []string{DefaultKafkaTopic}, "Kafka topics")

	flagSet.Duration(AbortAfter, DefaultAbortAfter, "consider a webhook as failed after retrying it for this duration.")
	flagSet.Int(MaxAttempts, DefaultMaxAttempts, "hard cap on delivery attempts per webhook (0 disables the cap, leaving abort-after as the only bound)")
	flagSet.Duration(MinBackoffDelay, time.Minute, "minimum backoff delay")
	flagSet.Duration(MaxBackoffDelay, time.Hour, "maximum backoff delay")
	flagSet.String(DeliveryPipeline, "legacy", "delivery pipeline to run: legacy or deliveries")

	flagSet.Duration(RetentionPeriod, DefaultRetentionPeriod, "interval between attempts-table cleanup runs")
	flagSet.Duration(RetentionSuccessDelay, DefaultRetentionSuccessDelay, "retain 'success' attempts for this long before purging (0 disables)")
	flagSet.Duration(RetentionFailedDelay, DefaultRetentionFailedDelay, "retain 'failed' attempts for this long before purging (0 disables)")

	flagSet.Bool(AutoMigrate, false, "auto migrate database")
}
