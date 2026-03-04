package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/publish"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

type Retrier struct {
	httpClient *http.Client
	store      storage.Store

	retriesCron time.Duration
	retryPolicy webhooks.BackoffPolicy

	stopChan chan chan struct{}
}

func NewRetrier(store storage.Store, httpClient *http.Client, retriesCron time.Duration, retryPolicy webhooks.BackoffPolicy) (*Retrier, error) {
	return &Retrier{
		httpClient:  httpClient,
		store:       store,
		retriesCron: retriesCron,
		retryPolicy: retryPolicy,
		stopChan:    make(chan chan struct{}),
	}, nil
}

func (w *Retrier) Run(ctx context.Context) error {
	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	go w.attemptRetries(ctxWithCancel)

	for {
		select {
		case ch := <-w.stopChan:
			logging.FromContext(ctx).Debug("worker: received from stopChan")
			close(ch)
			return nil
		case <-ctx.Done():
			logging.FromContext(ctx).Debugf("worker: context done: %s", ctx.Err())
			return nil
		}
	}
}

func (w *Retrier) Stop(ctx context.Context) {
	ch := make(chan struct{})
	select {
	case <-ctx.Done():
		logging.FromContext(ctx).Debugf("worker stopped: context done: %s", ctx.Err())
		return
	case w.stopChan <- ch:
		select {
		case <-ctx.Done():
			logging.FromContext(ctx).Debugf("worker stopped via stopChan: context done: %s", ctx.Err())
			return
		case <-ch:
			logging.FromContext(ctx).Debug("worker stopped via stopChan")
		}
	default:
		logging.FromContext(ctx).Debug("trying to stop worker: no communication")
	}
}

var ErrNoAttemptsFound = errors.New("attemptRetries: no attempts found")

func (w *Retrier) attemptRetries(ctx context.Context) {
	ticker := time.NewTicker(w.retriesCron)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processRetries(ctx)
		}
	}
}

func (w *Retrier) processRetries(ctx context.Context) {
	webhookIDs, err := w.store.FindWebhookIDsToRetry(ctx)
	if err != nil {
		logging.FromContext(ctx).Errorf("storage.Store.FindWebhookIDsToRetry: %s", err)
		return
	}

	logging.FromContext(ctx).Debugf(
		"found %d distinct webhookIDs to retry: %+v", len(webhookIDs), webhookIDs)

	for _, webhookID := range webhookIDs {
		atts, err := w.store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
		if err != nil {
			logging.FromContext(ctx).Errorf("storage.Store.FindAttemptsToRetryByWebhookID: %s", err)
			continue
		}
		if len(atts) == 0 {
			logging.FromContext(ctx).Errorf("%s for webhookID: %s", ErrNoAttemptsFound, webhookID)
			continue
		}

		var ev publish.EventMessage
		err = json.Unmarshal([]byte(atts[0].Payload), &ev)
		if err != nil {
			logging.FromContext(ctx).Errorf("json.Unmarshal: %s", err)
			continue
		}

		newAttemptNb := atts[0].RetryAttempt + 1
		attempt, err := webhooks.MakeAttempt(ctx, w.httpClient, w.retryPolicy, uuid.NewString(),
			webhookID, newAttemptNb, atts[0].Config, ev.IdempotencyKey, []byte(atts[0].Payload), false)
		if err != nil {
			logging.FromContext(ctx).Errorf("webhooks.MakeAttempt: %s", err)
			continue
		}

		if err := w.store.InsertOneAttempt(ctx, attempt); err != nil {
			logging.FromContext(ctx).Errorf("storage.Store.InsertOneAttempt retried: %s", err)
			continue
		}

		if _, err := w.store.UpdateAttemptsStatus(ctx, webhookID, attempt.Status); err != nil {
			if errors.Is(err, storage.ErrAttemptsNotModified) {
				continue
			}
			logging.FromContext(ctx).Errorf("storage.Store.UpdateAttemptsStatus: %s", err)
			continue
		}
	}
}
