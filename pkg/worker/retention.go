package worker

import (
	"context"
	"time"

	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/webhooks/pkg/storage"
)

const (
	defaultRetentionBatchSize = 5000
	defaultRetentionPeriod    = time.Hour
)

// RetentionConfig configures the periodic cleanup of the attempts table.
// Without it, the table grows without bound: success rows are kept forever and
// attempts of deleted configs loop in the retry queue. In production this drove
// the table to 100+ GB and dominated cross-AZ RDS replication cost.
type RetentionConfig struct {
	// Period between cleanup runs.
	Period time.Duration
	// SuccessDelay retains 'success' attempts for this long (<=0 disables).
	SuccessDelay time.Duration
	// FailedDelay retains 'failed' attempts for this long (<=0 disables).
	FailedDelay time.Duration
	// BatchSize bounds the number of rows deleted per statement.
	BatchSize int
}

// Enabled reports whether any retention action is configured.
func (c RetentionConfig) Enabled() bool {
	return c.SuccessDelay > 0 || c.FailedDelay > 0
}

// Retention periodically purges old terminal attempts and reclaims attempts
// whose config has been deleted.
type Retention struct {
	store  storage.Store
	cfg    RetentionConfig
	doneCh chan struct{}
}

func NewRetention(store storage.Store, cfg RetentionConfig) *Retention {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultRetentionBatchSize
	}
	// Guard against a zero/negative period: time.NewTicker panics on <= 0.
	if cfg.Period <= 0 {
		cfg.Period = defaultRetentionPeriod
	}
	return &Retention{
		store:  store,
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
	if n, err := r.store.FailOrphanedAttempts(ctx, r.cfg.BatchSize); err != nil {
		logging.FromContext(ctx).Errorf("retention: failing orphaned attempts: %s", err)
	} else if n > 0 {
		logging.FromContext(ctx).Infof("retention: marked %d orphaned attempts as failed", n)
	}

	if n, err := r.store.PurgeFinishedAttempts(ctx, r.cfg.SuccessDelay, r.cfg.FailedDelay, r.cfg.BatchSize); err != nil {
		logging.FromContext(ctx).Errorf("retention: purging finished attempts: %s", err)
	} else if n > 0 {
		logging.FromContext(ctx).Infof("retention: purged %d finished attempts", n)
	}
}
