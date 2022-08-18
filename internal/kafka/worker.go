package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/internal/storage"
	svixgo "github.com/svix/svix-webhooks/go"
)

type Event struct {
	Date    time.Time      `json:"date"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

type Worker struct {
	reader     Reader
	store      storage.Store
	svixClient *svixgo.Svix
	svixAppId  string

	stopChan chan chan struct{}
}

func NewWorker(reader Reader, store storage.Store, svixClient *svixgo.Svix, svixAppId string) *Worker {
	return &Worker{
		reader:     reader,
		store:      store,
		svixClient: svixClient,
		svixAppId:  svixAppId,
		stopChan:   make(chan chan struct{}),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	sharedlogging.GetLogger(ctx).Debugf("worker: ctx.Deadline(): %s %v", time.Until(deadline), ok)

	for {
		select {
		case ch := <-w.stopChan:
			sharedlogging.GetLogger(ctx).Debug("worker: received message from stopChan")
			close(ch)
			return nil
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("worker: context done: %s", ctx.Err())
			return nil
		default:
			sharedlogging.GetLogger(ctx).Debugf("worker: ctx.Err: %s", ctx.Err())
		}

		m, err := w.reader.FetchMessage(ctx)
		if err != nil {
			if !errors.Is(err, io.EOF) &&
				!errors.Is(err, context.Canceled) &&
				!errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("kafka.Reader.FetchMessage: %w", err)
			}
			continue
		}

		ctx := sharedlogging.ContextWithLogger(ctx,
			sharedlogging.GetLogger(ctx).WithFields(map[string]any{
				"offset": m.Offset,
			}))
		sharedlogging.GetLogger(ctx).WithFields(map[string]any{
			"time":      m.Time.UTC().Format(time.RFC3339),
			"partition": m.Partition,
			"data":      string(m.Value),
			"headers":   m.Headers,
		}).Debug("new kafka message fetched")

		ev := Event{}
		if err := json.Unmarshal(m.Value, &ev); err != nil {
			return fmt.Errorf("json.Unmarshal: %w", err)
		}

		toSend, err := w.store.FindEventType(ctx, ev.Type)
		if err != nil {
			return fmt.Errorf("store.FindEventType: %w", err)
		}

		if toSend {
			id := uuid.New().String()
			messageIn := &svixgo.MessageIn{
				EventType: ev.Type,
				EventId:   *svixgo.NullableString(id),
				Payload:   ev.Payload,
			}
			options := &svixgo.PostOptions{IdempotencyKey: &id}
			dumpIn := spew.Sdump(
				"svix appId: ", w.svixAppId,
				"svix.MessageIn: ", messageIn,
				"svix.PostOptions: ", options)

			if out, err := w.svixClient.Message.CreateWithOptions(
				w.svixAppId, messageIn, options); err != nil {
				return fmt.Errorf("svix.Svix.Message.CreateWithOptions: %s: dumpIn: %s",
					err, dumpIn)
			} else {
				sharedlogging.GetLogger(ctx).Debug("new webhook sent: ",
					"dumpIn: ", dumpIn, "dumpOut: ", spew.Sdump(out))
			}
		} else {
			sharedlogging.GetLogger(ctx).Debugf("message ignored of type: %s", ev.Type)
		}

		if err := w.reader.CommitMessages(ctx, m); err != nil {
			return fmt.Errorf("kafka.Reader.CommitMessages: %w", err)
		}
	}
}

func (w *Worker) Stop(ctx context.Context) {
	ch := make(chan struct{})
	w.stopChan <- ch
	<-ch
	sharedlogging.GetLogger(ctx).Debug("worker stopped")
}
