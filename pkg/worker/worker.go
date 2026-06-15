package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/alitto/pond"
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

	retriesCron    time.Duration
	retryPolicy    webhooks.BackoffPolicy
	retryBatchSize int
	retryPool      *pond.WorkerPool

	stopChan chan chan struct{}
}

func NewRetrier(store storage.Store, httpClient *http.Client, retriesCron time.Duration, retryPolicy webhooks.BackoffPolicy, retryBatchSize int) (*Retrier, error) {
	return &Retrier{
		httpClient:     httpClient,
		store:          store,
		retriesCron:    retriesCron,
		retryPolicy:    retryPolicy,
		retryBatchSize: retryBatchSize,
		retryPool:      pond.New(retryBatchSize, retryBatchSize),
		stopChan:       make(chan chan struct{}),
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

var errNoAttemptsFound = errors.New("attemptRetries: no attempts found")

const (
	staleRecoveryInterval   = time.Minute
	staleRetryingAttemptAge = 5 * time.Minute
)

func (w *Retrier) attemptRetries(ctx context.Context) {
	lastRecovery := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Recover attempts stuck in "retrying" state from crashed workers (at most once per minute)
			if time.Since(lastRecovery) >= staleRecoveryInterval {
				if err := w.store.RecoverStaleRetryingAttempts(ctx, staleRetryingAttemptAge); err != nil {
					logging.FromContext(ctx).Errorf("recovering stale retrying attempts: %s", err)
				}
				lastRecovery = time.Now()
			}

			// Atomically claim a batch of webhookIDs to retry
			webhookIDs, err := w.store.FindWebhookIDsToRetry(ctx, w.retryBatchSize)
			if err != nil {
				logging.FromContext(ctx).Errorf("claiming webhook IDs to retry: %s", err)
				time.Sleep(w.retriesCron)
				continue
			}

			logging.FromContext(ctx).Debugf(
				"claimed %d distinct webhookIDs to retry: %+v", len(webhookIDs), webhookIDs)

			group := w.retryPool.Group()
			for _, webhookID := range webhookIDs {
				id := webhookID
				group.Submit(func() {
					w.processWebhookRetry(ctx, id)
				})
			}
			group.Wait()
		}

		time.Sleep(w.retriesCron)
	}
}

func (w *Retrier) processWebhookRetry(ctx context.Context, webhookID string) {
	atts, err := w.store.FindAttemptsToRetryByWebhookID(ctx, webhookID)
	if err != nil {
		logging.FromContext(ctx).Errorf("finding attempts for webhook %s: %s", webhookID, err)
		return
	}
	if len(atts) == 0 {
		logging.FromContext(ctx).Errorf("%s for webhookID: %s", errNoAttemptsFound, webhookID)
		return
	}

	firstAttemptAt, err := w.store.FindFirstAttemptCreatedAtByWebhookID(ctx, webhookID)
	if err != nil {
		logging.FromContext(ctx).Errorf("finding first attempt for webhook %s: %s", webhookID, err)
		return
	}

	if err := limitRetryWindowBeforeAttempt(w.retryPolicy, firstAttemptAt); err != nil {
		logging.FromContext(ctx).Debugf("retry window elapsed for webhook %s: %s", webhookID, err)
		if _, updateErr := w.store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptFailed); updateErr != nil {
			logging.FromContext(ctx).Errorf("marking retry window elapsed attempts failed for webhook %s: %s",
				webhookID, updateErr)
		}
		return
	}

	var ev publish.EventMessage
	if err := json.Unmarshal([]byte(atts[0].Payload), &ev); err != nil {
		logging.FromContext(ctx).Errorf("unmarshalling payload for webhook %s: %s", webhookID, err)
		return
	}

	newAttemptNb := atts[0].RetryAttempt + 1
	if limiter, ok := w.retryPolicy.(webhooks.RetryAttemptLimiter); ok {
		if err := limiter.CanRetryAttempt(newAttemptNb); err != nil {
			logging.FromContext(ctx).Debugf("retry cap reached for webhook %s at attempt %d: %s",
				webhookID, newAttemptNb, err)
			if _, updateErr := w.store.UpdateAttemptsStatus(ctx, webhookID, webhooks.StatusAttemptFailed); updateErr != nil {
				logging.FromContext(ctx).Errorf("marking retry capped attempts failed for webhook %s: %s",
					webhookID, updateErr)
			}
			return
		}
	}

	attempt, err := webhooks.MakeAttempt(ctx, w.httpClient, w.retryPolicy, uuid.NewString(),
		webhookID, newAttemptNb, atts[0].Config, ev.IdempotencyKey, []byte(atts[0].Payload), false,
		webhooks.WithFirstAttemptAt(firstAttemptAt))
	if err != nil {
		logging.FromContext(ctx).Errorf("making attempt for webhook %s: %s", webhookID, err)
		return
	}

	claimedStatus := attempt.Status
	if claimedStatus == webhooks.StatusAttemptToRetry {
		claimedStatus = webhooks.StatusAttemptFailed
	}
	if err := w.store.InsertOneAttemptAndUpdateAttemptsStatus(ctx, attempt, webhookID, claimedStatus); err != nil {
		if errors.Is(err, storage.ErrAttemptsNotModified) {
			return
		}
		logging.FromContext(ctx).Errorf("inserting attempt and updating attempts status for webhook %s: %s", webhookID, err)
	}
}

func limitRetryWindowBeforeAttempt(retryPolicy webhooks.BackoffPolicy, firstAttemptAt time.Time) error {
	if firstAttemptAt.IsZero() {
		return nil
	}
	if limiter, ok := retryPolicy.(webhooks.RetryWindowLimiter); ok {
		return limiter.LimitRetryWindow(time.Since(firstAttemptAt))
	}
	return nil
}
