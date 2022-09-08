package workerMessages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/numary/go-libs/sharedlogging"
	webhooks "github.com/numary/webhooks/pkg"
	"github.com/numary/webhooks/pkg/kafka"
	"github.com/numary/webhooks/pkg/security"
	"github.com/numary/webhooks/pkg/storage"
	"github.com/pkg/errors"
	"github.com/twmb/franz-go/pkg/kgo"
)

type WorkerMessages struct {
	httpClient *http.Client
	store      storage.Store

	kafkaClient kafka.Client
	kafkaTopics []string

	stopChan chan chan struct{}
}

func NewWorkerMessages(store storage.Store, httpClient *http.Client) (*WorkerMessages, error) {
	kafkaClient, kafkaTopics, err := kafka.NewClient()
	if err != nil {
		return nil, errors.Wrap(err, "kafka.NewClient")
	}

	return &WorkerMessages{
		httpClient:  httpClient,
		store:       store,
		kafkaClient: kafkaClient,
		kafkaTopics: kafkaTopics,
		stopChan:    make(chan chan struct{}),
	}, nil
}

func (w *WorkerMessages) Run(ctx context.Context) error {
	msgChan := make(chan *kgo.Record)
	errChan := make(chan error)
	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	go fetchMessages(ctxWithCancel, w.kafkaClient, msgChan, errChan)

	for {
		select {
		case ch := <-w.stopChan:
			sharedlogging.GetLogger(ctx).Debug("workerMessages: received from stopChan")
			close(ch)
			return nil
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("workerMessages: context done: %s", ctx.Err())
			return nil
		case err := <-errChan:
			return errors.Wrap(err, "kafka.WorkerMessages.fetchMessages")
		case msg := <-msgChan:
			ctx = sharedlogging.ContextWithLogger(ctx,
				sharedlogging.GetLogger(ctx).WithFields(map[string]any{
					"offset": msg.Offset,
				}))
			sharedlogging.GetLogger(ctx).WithFields(map[string]any{
				"time":      msg.Timestamp.UTC().Format(time.RFC3339),
				"partition": msg.Partition,
				"headers":   msg.Headers,
			}).Debug("workerMessages: new kafka message fetched")

			w.kafkaClient.PauseFetchTopics(w.kafkaTopics...)

			if err := w.processMessage(ctx, msg.Value); err != nil {
				return errors.Wrap(err, "worker.WorkerMessages.processMessage")
			}

			w.kafkaClient.ResumeFetchTopics(w.kafkaTopics...)
		}
	}
}

func (w *WorkerMessages) Stop(ctx context.Context) {
	ch := make(chan struct{})
	select {
	case <-ctx.Done():
		sharedlogging.GetLogger(ctx).Debugf("workerMessages stopped: context done: %s", ctx.Err())
		return
	case w.stopChan <- ch:
		select {
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("workerMessages stopped via stopChan: context done: %s", ctx.Err())
			return
		case <-ch:
			sharedlogging.GetLogger(ctx).Debug("workerMessages stopped via stopChan")
		}
	}
}

func fetchMessages(ctx context.Context, kafkaClient kafka.Client, msgChan chan *kgo.Record, errChan chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			fetches := kafkaClient.PollFetches(ctx)
			if errs := fetches.Errors(); len(errs) > 0 {
				for _, err := range errs {
					select {
					case <-ctx.Done():
						return
					default:
						errChan <- fmt.Errorf(
							"kafka.Client.PollRecords: topic: %s: partition: %d: %w",
							err.Topic, err.Partition, err.Err)
					}
				}
			}

			var records []*kgo.Record
			fetches.EachRecord(func(record *kgo.Record) {
				msgChan <- record
				records = append(records, record)
			})
		}
	}
}

func (w *WorkerMessages) processMessage(ctx context.Context, msgValue []byte) error {
	var ev webhooks.EventMessage
	if err := json.Unmarshal(msgValue, &ev); err != nil {
		return errors.Wrap(err, "json.Unmarshal event message")
	}

	eventApp := strings.ToLower(ev.App)
	eventType := strings.ToLower(ev.Type)
	ev.Type = strings.Join([]string{eventApp, eventType}, ".")

	cur, err := w.store.FindManyConfigs(ctx, map[string]any{webhooks.KeyEventTypes: ev.Type})
	if err != nil {
		return errors.Wrap(err, "storage.store.FindManyConfigs")
	}

	for _, cfg := range cur.Data {
		if err := w.sendWebhook(ctx, cfg, ev); err != nil {
			return errors.Wrap(err, "sending webhook")
		}
	}

	return nil
}

func (w *WorkerMessages) sendWebhook(ctx context.Context, cfg webhooks.Config, ev webhooks.EventMessage) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return errors.Wrap(err, "json.Marshal event message")
	}

	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, cfg.Endpoint, bytes.NewBuffer(data))
	if err != nil {
		return errors.Wrap(err, "http.NewRequestWithContext")
	}

	id := uuid.NewString()
	date := time.Now().UTC()
	signature, err := security.Sign(id, date, cfg.Secret, data)
	if err != nil {
		return errors.Wrap(err, "security.Sign")
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "formance-webhooks/v1")
	req.Header.Set("formance-webhook-id", id)
	req.Header.Set("formance-webhook-timestamp", fmt.Sprintf("%d", date.Unix()))
	req.Header.Set("formance-webhook-signature", signature)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "http.Client.Do")
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			sharedlogging.GetLogger(ctx).Error(
				errors.Wrap(err, "http.Response.Body.Close"))
		}
	}()

	requestInserted := webhooks.Request{
		Date:       date,
		ID:         id,
		Config:     cfg,
		Payload:    string(data),
		StatusCode: resp.StatusCode,
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode > http.StatusMultipleChoices {
		requestInserted.RetryAfter = date.Add(5 * time.Second)
	} else {
		requestInserted.Success = true
		sharedlogging.GetLogger(ctx).Infof(
			"webhook sent with ID %s to %s of type %s", id, cfg.Endpoint, ev.Type)
	}

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("RESP SERVER BODY: %s\n", body)

	if _, err := w.store.InsertOneRequest(ctx, requestInserted); err != nil {
		return errors.Wrap(err, "storage.store.InsertOneRequest")
	}

	return nil
}
