package worker

import (
	"context"
	"time"

	"github.com/formancehq/go-libs/v2/logging"
)

const (
	defaultRetentionBatchSize = 5000
	defaultRetentionPeriod    = time.Hour
)

// RetentionConfig configures the periodic cleanup of the attempts table.
// Without it, the table grows without bound: success rows are kept forever and
// attempts of deleted or inactive configs stay in the retry queue. In production
// this drove the table to 100+ GB and dominated cross-AZ RDS replication cost.
type RetentionConfig struct {
	// Period between cleanup runs.
	Period time.Duration
	// SuccessDelay retains 'success' attempts for this long (<=0 disables).
	SuccessDelay time.Duration
	// FailedDelay retains 'failed' attempts for this long (<=0 disables).
	FailedDelay time.Duration
	// BatchSize bounds the number of rows touched per cleanup statement and run.
	BatchSize int
}

// Enabled reports whether any retention action is configured. The runner must
// also stay enabled when terminal purging is disabled, because it reclaims
// retrying/to-retry attempts whose config was deleted or deactivated.
func (c RetentionConfig) Enabled() bool {
	return c.Period > 0 || c.SuccessDelay > 0 || c.FailedDelay > 0
}

// Retention periodically purges old terminal attempts and reclaims attempts
// whose config has been deleted or deactivated.
type Retention struct {
	legacyStore   retentionStore
	deliveryStore deliveryRetentionStore
	cfg           RetentionConfig
	doneCh        chan struct{}
}

type retentionStore interface {
	FailUnclaimableAttempts(ctx context.Context, batchSize int, retryingStaleDuration time.Duration) (int64, error)
	PurgeFinishedAttempts(ctx context.Context, successOlderThan, failedOlderThan time.Duration, batchSize int) (int64, error)
}

type deliveryRetentionStore interface {
	PurgeFinishedDeliveries(ctx context.Context, successOlderThan, failedOlderThan time.Duration, batchSize int) (int64, error)
}

func NewRetention(store retentionStore, cfg RetentionConfig) *Retention {
	retention := newRetention(cfg)
	retention.legacyStore = store
	retention.deliveryStore, _ = store.(deliveryRetentionStore)
	return retention
}

func NewDeliveryRetention(store deliveryRetentionStore, cfg RetentionConfig) *Retention {
	retention := newRetention(cfg)
	retention.deliveryStore = store
	return retention
}

func newRetention(cfg RetentionConfig) *Retention {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultRetentionBatchSize
	}
	// Guard against a zero/negative period: time.NewTicker panics on <= 0.
	if cfg.Period <= 0 {
		cfg.Period = defaultRetentionPeriod
	}
	return &Retention{
		cfg:    cfg,
		doneCh: make(chan struct{}),
	}
}

func (r *Retention) Run(ctx context.Context) {
	defer close(r.doneCh)

	ticker := time.NewTicker(r.cfg.Period)
	defer ticker.Stop()

	// Run once on startup so a freshly deployed worker reclaims any standing
	// backlog without waiting a full period.
	r.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Retention) runOnce(ctx context.Context) {
	if r.legacyStore != nil {
		if n, err := r.legacyStore.FailUnclaimableAttempts(ctx, r.cfg.BatchSize, staleRetryingAttemptAge); err != nil {
			logging.FromContext(ctx).Errorf("retention: failing unclaimable attempts: %s", err)
		} else if n > 0 {
			logging.FromContext(ctx).Infof("retention: marked %d unclaimable attempts as failed", n)
		}

		if n, err := r.legacyStore.PurgeFinishedAttempts(ctx, r.cfg.SuccessDelay, r.cfg.FailedDelay, r.cfg.BatchSize); err != nil {
			logging.FromContext(ctx).Errorf("retention: purging finished attempts: %s", err)
		} else if n > 0 {
			logging.FromContext(ctx).Infof("retention: purged %d finished attempts", n)
		}
	}
	if r.deliveryStore != nil {
		if n, err := r.deliveryStore.PurgeFinishedDeliveries(ctx, r.cfg.SuccessDelay, r.cfg.FailedDelay, r.cfg.BatchSize); err != nil {
			logging.FromContext(ctx).Errorf("retention: purging finished deliveries: %s", err)
		} else if n > 0 {
			logging.FromContext(ctx).Infof("retention: purged %d finished deliveries", n)
		}
	}
}
