package model

import (
	"errors"
	"net/url"
	"time"
)

type Config struct {
	Active     bool     `json:"active" bson:"active"`
	EventTypes []string `json:"event_types,omitempty" bson:"event_types,omitempty"`
	Endpoints  []string `json:"endpoints,omitempty" bson:"endpoints,omitempty"`
}

type ConfigInserted struct {
	Config     `json:",inline" bson:"inline"`
	UserId     string    `json:"user_id" bson:"user_id"`
	InsertedAt time.Time `json:"inserted_at" bson:"inserted_at"`
}

func (c Config) Validate() error {
	if c.Active {
		if len(c.EventTypes) < 1 || len(c.Endpoints) < 1 {
			return errors.New(
				"the body should have at least one type of events and one endpoint")
		}

		for _, endpoint := range c.Endpoints {
			if _, err := url.Parse(endpoint); err != nil {
				return errors.New(
					"endpoints should be valid urls")
			}
		}
	} else {
		if len(c.EventTypes) > 0 || len(c.Endpoints) > 0 {
			return errors.New(
				"the body to set a webhook inactive shouldn't contain any types of events or endpoints")
		}
	}

	return nil
}
