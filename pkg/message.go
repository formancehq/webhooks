package webhooks

import (
	"encoding/json"
	"time"
)

type EventMessage struct {
	Date    time.Time       `json:"date" bson:"date"`
	App     string          `json:"app" bson:"app"`
	Version string          `json:"version" bson:"version"`
	Type    string          `json:"type" bson:"type"`
	Payload json.RawMessage `json:"payload" bson:"payload"`
}
