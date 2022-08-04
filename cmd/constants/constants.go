package constants

const (
	StorageMongoConnStringFlag = "storage.mongo.conn_string"
	ServerHttpBindAddressFlag  = "server.http.bind_address"

	DefaultMongoConnString = "mongodb://admin:admin@localhost:27017/"
	DefaultBindAddress     = ":8080"

	KafkaBrokerFlag                = "kafka-broker"
	KafkaGroupIDFlag               = "kafka-consumer-group"
	KafkaTopicFlag                 = "kafka-topic"
	KafkaTLSEnabledFlag            = "kafka-tls-enabled"
	KafkaTLSInsecureSkipVerifyFlag = "kafka-tls-insecure-skip-verify"
	KafkaSASLEnabledFlag           = "kafka-sasl-enabled"
	KafkaSASLMechanismFlag         = "kafka-sasl-mechanism"
	KafkaUsernameFlag              = "kafka-username"
	KafkaPasswordFlag              = "kafka-password"

	DefaultKafkaTopic   = "defaultTopic"
	DefaultKafkaBroker  = "localhost:9092"
	DefaultKafkaGroupID = "1"

	SvixTokenFlag = "svix.token"
	SvixAppIdFlag = "svix.appId"

	DefaultSvixToken = "testsk_CSkhagouqu-JXgZznr35dG2TYTmsCPnb"
	DefaultSvixAppId = "app_2CqvO4XINy9ucWhqGPFXj4EkNS7"
)
