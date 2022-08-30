package webhooks

import "time"

type Request struct {
	Config     ConfigInserted `json:"config" bson:"config"`
	Payload    string         `json:"payload" bson:"payload"`
	StatusCode int            `json:"statusCode" bson:"statusCode"`
	Attempt    int            `json:"attempt" bson:"attempt"`
	Date       time.Time      `json:"date" bson:"date"`
	Error      string         `json:"error" bson:"error"`
}
