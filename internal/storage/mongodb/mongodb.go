package mongodb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/numary/go-libs/sharedapi"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks-cloud/pkg/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

type Store struct {
	uri        string
	client     *mongo.Client
	collection *mongo.Collection
}

func NewStore() (Store, error) {
	mongoDBUri := os.Getenv("MONGODB_CONN_STRING")
	if mongoDBUri == "" {
		return Store{}, errors.New("env var MONGODB_CONN_STRING not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoDBUri))
	if err != nil {
		return Store{}, err
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return Store{}, err
	}

	return Store{
		uri:        mongoDBUri,
		client:     client,
		collection: client.Database("webhooks").Collection("configs"),
	}, nil
}

func (s Store) FindAllConfigs() (sharedapi.Cursor[model.ConfigInserted], error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: -1}})
	cur, err := s.collection.Find(ctx, bson.D{}, opts)
	if err != nil {
		return sharedapi.Cursor[model.ConfigInserted]{}, fmt.Errorf("mongo.Collection.Find: %w", err)
	}
	defer func(cur *mongo.Cursor, ctx context.Context) {
		if err := cur.Close(ctx); err != nil {
			sharedlogging.Errorf("mongo.Cursor.Close: %s", err)
		}
	}(cur, ctx)

	results := []model.ConfigInserted{}
	if err := cur.All(ctx, &results); err != nil {
		return sharedapi.Cursor[model.ConfigInserted]{}, fmt.Errorf("mongo.Cursor.All: %w", err)
	}

	return sharedapi.Cursor[model.ConfigInserted]{
		Data: results,
	}, nil
}

func (s Store) FindConfigsByUserID(userId string) (sharedapi.Cursor[model.ConfigInserted], error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: -1}})
	cur, err := s.collection.Find(ctx, bson.D{{Key: "user_id", Value: userId}}, opts)
	if err != nil {
		return sharedapi.Cursor[model.ConfigInserted]{}, fmt.Errorf("mongo.Collection.Find: %w", err)
	}
	defer func(cur *mongo.Cursor, ctx context.Context) {
		if err := cur.Close(ctx); err != nil {
			sharedlogging.Errorf("mongo.Cursor.Close: %s", err)
		}
	}(cur, ctx)

	results := []model.ConfigInserted{}
	if err := cur.All(ctx, &results); err != nil {
		return sharedapi.Cursor[model.ConfigInserted]{}, fmt.Errorf("mongo.Cursor.All: %w", err)
	}

	return sharedapi.Cursor[model.ConfigInserted]{
		Data: results,
	}, nil
}

func (s Store) InsertOneConfig(config model.Config, userId string) (primitive.ObjectID, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	configInserted := model.ConfigInserted{
		Config:     config,
		UserId:     userId,
		InsertedAt: time.Now().UTC(),
	}
	res, err := s.collection.InsertOne(ctx, configInserted)
	if err != nil {
		return primitive.ObjectID{}, err
	}

	return res.InsertedID.(primitive.ObjectID), nil
}

func (s Store) DropConfigsCollection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.collection.Drop(ctx); err != nil {
		return err
	}

	return nil
}

func (s Store) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if s.client == nil {
		return nil
	}

	return s.client.Disconnect(ctx)
}
