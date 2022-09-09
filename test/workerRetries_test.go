package test_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/numary/webhooks/constants"
	webhooks "github.com/numary/webhooks/pkg"
	"github.com/numary/webhooks/pkg/server"
	"github.com/numary/webhooks/pkg/worker/retries"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/fx/fxtest"
)

func TestWorkerRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoClient, err := mongo.Connect(ctx,
		options.Client().ApplyURI(
			viper.GetString(constants.StorageMongoConnStringFlag)))
	require.NoError(t, err)

	require.NoError(t, mongoClient.Database(
		viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.MongoCollectionRequests).Drop(context.Background()))

	// New test server with success handler
	httpServerSuccess := httptest.NewServer(http.HandlerFunc(webhooksSuccessHandler))
	defer func() {
		httpServerSuccess.CloseClientConnections()
		httpServerSuccess.Close()
	}()

	failedRequest := webhooks.Request{
		Date:      time.Now(),
		RequestID: uuid.NewString(),
		Config: webhooks.Config{
			ConfigUser: webhooks.ConfigUser{
				Endpoint:   httpServerSuccess.URL,
				Secret:     secret,
				EventTypes: []string{type1},
			},
			ID:        uuid.NewString(),
			Active:    true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Payload:      fmt.Sprintf("{\"type\":\"%s\"}", type1),
		StatusCode:   http.StatusNotFound,
		Status:       webhooks.StatusRequestToRetry,
		RetryAttempt: 0,
		RetryAfter:   time.Now(),
	}

	_, err = mongoClient.Database(
		viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.MongoCollectionRequests).InsertOne(context.Background(), failedRequest)
	require.NoError(t, err)

	workerRetriesApp := fxtest.New(t,
		retries.StartModule(
			viper.GetString(constants.HttpBindAddressWorkerRetriesFlag), httpServerSuccess.Client()))
	require.NoError(t, workerRetriesApp.Start(context.Background()))

	t.Run("health check", func(t *testing.T) {
		requestWorkerRetries(t, http.MethodGet, server.PathHealthCheck, http.StatusOK)
	})

	expectedSentRequests := 2

	t.Run("failed request should be retried successfully", func(t *testing.T) {
		sentRequests := 0
		for sentRequests != expectedSentRequests {
			opts := options.Find().SetSort(bson.M{webhooks.KeyID: -1})
			cur, err := mongoClient.Database(
				viper.GetString(constants.StorageMongoDatabaseNameFlag)).
				Collection(constants.MongoCollectionRequests).
				Find(context.Background(), bson.M{}, opts)
			require.NoError(t, err)
			var results []webhooks.Request
			require.NoError(t, cur.All(context.Background(), &results))
			sentRequests = len(results)
			if sentRequests != expectedSentRequests {
				time.Sleep(time.Second)
			} else {
				// First request should be successful
				require.Equal(t, webhooks.StatusRequestSuccess, results[0].Status)
				require.Equal(t, expectedSentRequests-1, results[0].RetryAttempt)
			}
		}
		time.Sleep(time.Second)
		require.Equal(t, expectedSentRequests, sentRequests)
	})

	require.NoError(t, workerRetriesApp.Stop(context.Background()))
}
