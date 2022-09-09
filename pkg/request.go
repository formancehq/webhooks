package webhooks

import "time"

const (
	StatusRequestSuccess = "success"
	StatusRequestToRetry = "to retry"
	StatusRequestFailed  = "failed"
)

type Request struct {
	RequestID    string    `json:"requestId" bson:"requestId"`
	Date         time.Time `json:"date" bson:"date"`
	Config       Config    `json:"config" bson:"config"`
	Payload      string    `json:"payload" bson:"payload"`
	StatusCode   int       `json:"statusCode" bson:"statusCode"`
	RetryAttempt int       `json:"retryAttempt,omitempty" bson:"retryAttempt,omitempty"`
	Status       string    `json:"status" bson:"status"`
	RetryAfter   time.Time `json:"retryAfter,omitempty" bson:"retryAfter,omitempty"`
}
