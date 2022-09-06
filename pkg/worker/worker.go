package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/pkg/kafka"
	"github.com/numary/webhooks/pkg/storage"
	"github.com/twmb/franz-go/pkg/kgo"
)

type Worker struct {
	httpClient *http.Client
	store      storage.Store

	kafkaClient kafka.Client
	kafkaTopics []string

	stopChan chan chan struct{}
}

func NewWorker(store storage.Store, httpClient *http.Client) (*Worker, error) {
	kafkaClient, kafkaTopics, err := kafka.NewClient()
	if err != nil {
		return nil, fmt.Errorf("kafka.NewClient: %w", err)
	}

	return &Worker{
		httpClient:  httpClient,
		store:       store,
		kafkaClient: kafkaClient,
		kafkaTopics: kafkaTopics,
		stopChan:    make(chan chan struct{}),
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	msgChan := make(chan *kgo.Record)
	errChan := make(chan error)
	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	go fetchMessages(ctxWithCancel, w.kafkaClient, msgChan, errChan)

	for {
		select {
		case ch := <-w.stopChan:
			sharedlogging.GetLogger(ctx).Debug("worker: received from stopChan")
			close(ch)
			return nil
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("worker: context done: %s", ctx.Err())
			return nil
		case err := <-errChan:
			return fmt.Errorf("kafka.Worker.fetchMessages: %w", err)
		case msg := <-msgChan:
			ctx = sharedlogging.ContextWithLogger(ctx,
				sharedlogging.GetLogger(ctx).WithFields(map[string]any{
					"offset": msg.Offset,
				}))
			sharedlogging.GetLogger(ctx).WithFields(map[string]any{
				"time":      msg.Timestamp.UTC().Format(time.RFC3339),
				"partition": msg.Partition,
				"headers":   msg.Headers,
			}).Debug("worker: new kafka message fetched")

			// w.kafkaClient.PauseFetchTopics(w.kafkaTopics...)

			if err := w.processMessage(ctx, msg.Value); err != nil {
				return fmt.Errorf("worker.Worker.processMessage: %w", err)
			}

			// w.kafkaClient.ResumeFetchTopics(w.kafkaTopics...)
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
					if ctx.Err() == nil {
						select {
						case errChan <- fmt.Errorf(
							"kafka.Client.PollFetches: topic: %s: partition: %d: %w",
							err.Topic, err.Partition, err.Err):
						case <-ctx.Done():
							return
						}
					}
				}
			}

			iter := fetches.RecordIter()
			for !iter.Done() {
				record := iter.Next()
				select {
				case msgChan <- record:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (w *Worker) Stop(ctx context.Context) {
	ch := make(chan struct{})
	select {
	case <-ctx.Done():
		sharedlogging.GetLogger(ctx).Debugf("worker stopped: context done: %s", ctx.Err())
		return
	case w.stopChan <- ch:
		select {
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("worker stopped via stopChan: context done: %s", ctx.Err())
			return
		case <-ch:
			sharedlogging.GetLogger(ctx).Debug("worker stopped via stopChan")
		}
	}
}
