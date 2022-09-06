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
	"sync"
	"testing"
	"time"

	"github.com/numary/webhooks/constants"
	webhooks "github.com/numary/webhooks/pkg"
	"github.com/numary/webhooks/pkg/kafka"
	"github.com/numary/webhooks/pkg/security"
	"github.com/numary/webhooks/pkg/server"
	"github.com/numary/webhooks/pkg/worker"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
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
	defer func() {
		httptestServer.CloseClientConnections()
		httptestServer.Close()
	}()

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

	cfg := webhooks.ConfigUser{
		Endpoint:   httptestServer.URL,
		Secret:     secret,
		EventTypes: []string{"OTHER_TYPE", worker.EventTypeLedgerCommittedTransactions},
	}
	require.NoError(t, cfg.Validate())

	var insertedId string
	resBody := requestServer(t, http.MethodPost, server.PathConfigs, http.StatusOK, cfg)
	require.NoError(t, json.NewDecoder(resBody).Decode(&insertedId))
	require.NoError(t, resBody.Close())

	f1, err := os.Open("./committed_transactions_1.json")
	require.NoError(t, err)
	by1, err := ioutil.ReadAll(f1)
	require.NoError(t, err)

	f2, err := os.Open("./committed_transactions_2.json")
	require.NoError(t, err)
	by2, err := ioutil.ReadAll(f2)
	require.NoError(t, err)

	f3, err := os.Open("./committed_transactions_3.json")
	require.NoError(t, err)
	by3, err := ioutil.ReadAll(f3)
	require.NoError(t, err)

	kafkaClient, topics, err := kafka.NewClient()
	require.NoError(t, err)
	defer kafkaClient.Close()

	fmt.Printf("POLL FETCHES\n")
	fetches := kafkaClient.PollFetches(nil) //nolint: staticcheck
	if errs := fetches.Errors(); len(errs) > 0 {
		fmt.Printf("kgo.Client.PollFetches: %+v\n", errs)
		return
	}

	fmt.Printf("FETCH 1\n")
	iter := fetches.RecordIter()
	fmt.Printf("FETCH 2\n")
	for !iter.Done() {
		fmt.Printf("FETCH 3\n")
		record := iter.Next()
		fmt.Printf("Message consumed from the topic: %s\n", string(record.Value))
	}
	fmt.Printf("FETCH FINISHED\n")

	records := []*kgo.Record{
		{Topic: topics[0], Value: by1},
		{Topic: topics[0], Value: by2},
		{Topic: topics[0], Value: by3},
	}
	n := len(records)

	var wg sync.WaitGroup
	wg.Add(n)
	for _, record := range records {
		kafkaClient.Produce(context.Background(), record, func(_ *kgo.Record, err error) {
			defer wg.Done()
			if err != nil {
				fmt.Printf("record had a produce error: %v\n", err)
			} else {
				fmt.Printf("record produced\n")
			}
		})
	}
	wg.Wait()

	fmt.Printf("PRODUCE FINISHED\n")

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
