package storage

import (
	"github.com/numary/go-libs/sharedapi"
	"github.com/numary/webhooks-cloud/pkg/model"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Store interface {
	FindAllConfigs() (sharedapi.Cursor[model.ConfigInserted], error)
	FindConfigsByUserID(userId string) (sharedapi.Cursor[model.ConfigInserted], error)
	InsertOneConfig(config model.Config, userId string) (primitive.ObjectID, error)
	DropConfigsCollection() error
	Close() error
}
