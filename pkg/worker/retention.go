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

// RetentionConfig configures periodic cleanup of terminal deliveries.
type RetentionConfig struct {
	// Period between cleanup runs.
	Period time.Duration
	// SuccessDelay retains succeeded deliveries for this long (<=0 disables).
	SuccessDelay time.Duration
	// FailedDelay retains failed deliveries for this long (<=0 disables).
	FailedDelay time.Duration
	// BatchSize bounds the number of rows touched per cleanup statement and run.
	BatchSize int
}

// Enabled reports whether any retention action is configured.
func (c RetentionConfig) Enabled() bool {
	return c.Period > 0 || c.SuccessDelay > 0 || c.FailedDelay > 0
}

// Retention periodically purges old terminal deliveries.
type Retention struct {
	store  retentionStore
	cfg    RetentionConfig
	doneCh chan struct{}
}

type retentionStore interface {
	PurgeFinishedDeliveries(ctx context.Context, successOlderThan, failedOlderThan time.Duration, batchSize int) (int64, error)
}

func NewRetention(store retentionStore, cfg RetentionConfig) *Retention {
	retention := newRetention(cfg)
	retention.store = store
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
	if n, err := r.store.PurgeFinishedDeliveries(ctx, r.cfg.SuccessDelay, r.cfg.FailedDelay, r.cfg.BatchSize); err != nil {
		logging.FromContext(ctx).Errorf("retention: purging finished deliveries: %s", err)
	} else if n > 0 {
		logging.FromContext(ctx).Infof("retention: purged %d finished deliveries", n)
	}
}
