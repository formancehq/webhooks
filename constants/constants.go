package constants

const (
	LogLevelFlag                      = "log-level"
	HttpBindAddressServerFlag         = "http-bind-address-server"
	HttpBindAddressWorkerMessagesFlag = "http-bind-address-worker-messages"
	HttpBindAddressWorkerRetriesFlag  = "http-bind-address-worker-retries"

	RetryScheduleFlag = "retry-schedule"

	StorageMongoConnStringFlag   = "storage-mongo-conn-string"
	StorageMongoDatabaseNameFlag = "storage-mongo-database-name"

	KafkaBrokersFlag       = "kafka-brokers"
	KafkaGroupIDFlag       = "kafka-consumer-group"
	KafkaTopicsFlag        = "kafka-topics"
	KafkaTLSEnabledFlag    = "kafka-tls-enabled"
	KafkaSASLEnabledFlag   = "kafka-sasl-enabled"
	KafkaSASLMechanismFlag = "kafka-sasl-mechanism"
	KafkaUsernameFlag      = "kafka-username"
	KafkaPasswordFlag      = "kafka-password"
)

const (
	DefaultBindAddressServer         = ":8080"
	DefaultBindAddressWorkerMessages = ":8081"
	DefaultBindAddressWorkerRetries  = ":8082"

	DefaultMongoConnString   = "mongodb://admin:admin@localhost:27017/"
	DefaultMongoDatabaseName = "webhooks"

	MongoCollectionConfigs  = "configs"
	MongoCollectionRequests = "requests"

	DefaultKafkaTopic   = "default"
	DefaultKafkaBroker  = "localhost:9092"
	DefaultKafkaGroupID = "webhooks"
)

var DefaultRetrySchedule = []string{"1m", "5m", "30m", "5h", "24h"}
