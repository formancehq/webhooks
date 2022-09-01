package test_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/constants"
	webhooks "github.com/numary/webhooks/pkg"
	"github.com/numary/webhooks/pkg/security"
	"github.com/numary/webhooks/pkg/server"
	"github.com/numary/webhooks/pkg/worker"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/fx/fxtest"
)

func TestWorker(t *testing.T) {
	secret := webhooks.NewSecret()
	httpHandler := http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("formance-webhook-id")
			ts := r.Header.Get("formance-webhook-timestamp")
			signatures := r.Header.Get("formance-webhook-signature")
			timeInt, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			timestamp := time.Unix(timeInt, 0)

			payload, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			ok, err := security.Verify(signatures, id, timestamp, []byte(secret), payload)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !ok {
				http.Error(w, "", http.StatusBadRequest)
				return
			}

			_, _ = fmt.Fprintf(w, "SIGNATURE VERIFIED\n")
			return
		})

	httptestServer := httptest.NewServer(httpHandler)
	defer httptestServer.Close()

	serverApp := fxtest.New(t,
		server.StartModule(
			viper.GetString(constants.HttpBindAddressServerFlag)))
	workerApp := fxtest.New(t,
		worker.StartModule(
			viper.GetString(constants.HttpBindAddressWorkerFlag), httptestServer.Client()))

	require.NoError(t, serverApp.Start(context.Background()))
	require.NoError(t, workerApp.Start(context.Background()))

	require.NoError(t, mongoClient.Database(
		viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.MongoCollectionConfigs).Drop(context.Background()))
	require.NoError(t, mongoClient.Database(
		viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.MongoCollectionRequests).Drop(context.Background()))

	var err error
	var conn *kafkago.Conn
	for conn == nil {
		conn, err = kafkago.DialLeader(context.Background(), "tcp",
			viper.GetStringSlice(constants.KafkaBrokersFlag)[0],
			viper.GetStringSlice(constants.KafkaTopicsFlag)[0], 0)
		if err != nil {
			sharedlogging.GetLogger(context.Background()).Debug("connecting to kafka: err: ", err)
			time.Sleep(3 * time.Second)
		}
	}
	defer func() {
		require.NoError(t, conn.Close())
	}()

	cfg := webhooks.Config{
		Endpoint:   httptestServer.URL,
		Secret:     secret,
		EventTypes: []string{"OTHER_TYPE", worker.EventTypeLedgerCommittedTransactions},
	}
	require.NoError(t, cfg.Validate())

	var insertedId string
	resBody := requestServer(t, http.MethodPost, server.PathConfigs, http.StatusOK, cfg)
	require.NoError(t, json.NewDecoder(resBody).Decode(&insertedId))
	require.NoError(t, resBody.Close())

	f, err := os.Open("./committed_transactions.json")
	require.NoError(t, err)
	by, err := ioutil.ReadAll(f)
	require.NoError(t, err)

	n := 3
	var messages []kafkago.Message
	for i := 0; i < n; i++ {
		messages = append(messages, kafkago.Message{
			Value: by,
		})
	}
	nbBytes, err := conn.WriteMessages(messages...)
	require.NoError(t, err)
	require.NotEqual(t, 0, nbBytes)

	t.Run("health check", func(t *testing.T) {
		requestWorker(t, http.MethodGet, server.PathHealthCheck, http.StatusOK)
	})

	t.Run("messages", func(t *testing.T) {
		msgs := 0
		for msgs != n {
			cur, err := mongoClient.Database(
				viper.GetString(constants.StorageMongoDatabaseNameFlag)).
				Collection(constants.MongoCollectionRequests).
				Find(context.Background(), bson.M{}, nil)
			require.NoError(t, err)
			var results []webhooks.Request
			require.NoError(t, cur.All(context.Background(), &results))
			msgs = len(results)
			time.Sleep(time.Second)
		}
		time.Sleep(time.Second)
		require.Equal(t, n, msgs)
	})

	require.NoError(t, serverApp.Stop(context.Background()))
	require.NoError(t, workerApp.Stop(context.Background()))
}
