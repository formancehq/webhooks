package retries

import (
	"context"
	"net/http"
	"time"

	"github.com/numary/go-libs/sharedlogging"
	webhooks "github.com/numary/webhooks/pkg"
	"github.com/numary/webhooks/pkg/storage"
	"github.com/pkg/errors"
)

type WorkerRetries struct {
	httpClient *http.Client
	store      storage.Store

	retrySchedule []time.Duration

	stopChan chan chan struct{}
}

func NewWorkerRetries(store storage.Store, httpClient *http.Client, schedule []time.Duration) (*WorkerRetries, error) {
	return &WorkerRetries{
		httpClient:    httpClient,
		store:         store,
		retrySchedule: schedule,
		stopChan:      make(chan chan struct{}),
	}, nil
}

func (w *WorkerRetries) Run(ctx context.Context) error {
	errChan := make(chan error)
	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	go w.retryRequests(ctxWithCancel, errChan)

	for {
		select {
		case ch := <-w.stopChan:
			sharedlogging.GetLogger(ctx).Debug("workerRetries: received from stopChan")
			close(ch)
			return nil
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("workerRetries: context done: %s", ctx.Err())
			return nil
		case err := <-errChan:
			return errors.Wrap(err, "kafka.WorkerRetries.retryRequests")
		}
	}
}

func (w *WorkerRetries) Stop(ctx context.Context) {
	ch := make(chan struct{})
	select {
	case <-ctx.Done():
		sharedlogging.GetLogger(ctx).Debugf("workerRetries stopped: context done: %s", ctx.Err())
		return
	case w.stopChan <- ch:
		select {
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("workerRetries stopped via stopChan: context done: %s", ctx.Err())
			return
		case <-ch:
			sharedlogging.GetLogger(ctx).Debug("workerRetries stopped via stopChan")
		}
	}
}

func (w *WorkerRetries) retryRequests(ctx context.Context, errChan chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			filter := map[string]any{
				"status":     webhooks.StatusRequestToRetry,
				"retryAfter": map[string]any{"$lt": time.Now().UTC()},
			}
			cur, err := w.store.FindManyRequests(ctx, filter)
			if err != nil {
				errChan <- errors.Wrap(err, "storage.Store.FindManyRequests to retry")
				continue
			}
			sharedlogging.GetLogger(ctx).Debugf("found %d requests to retry", len(cur.Data))

			for _, toRetry := range cur.Data {
				request, err := webhooks.MakeAttempt(ctx, w.httpClient, w.retrySchedule,
					toRetry.RetryAttempt+1, toRetry.Config, []byte(toRetry.Payload))
				if err != nil {
					errChan <- errors.Wrap(err, "webhooks.MakeAttempt")
					continue
				}
				if _, err := w.store.InsertOneRequest(ctx, request); err != nil {
					errChan <- errors.Wrap(err, "storage.Store.InsertOneRequest retried")
					continue
				}
			}

		}
		time.Sleep(time.Minute)
	}
}
