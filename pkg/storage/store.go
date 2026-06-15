package storage

import (
	"context"
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/pkg/errors"
)

var (
	ErrConfigNotFound      = errors.New("config not found")
	ErrConfigNotModified   = errors.New("config not modified")
	ErrWebhookIDNotFound   = errors.New("webhook ID not found")
	ErrAttemptsNotModified = errors.New("attempt not modified")
)

type Store interface {
	FindManyConfigs(ctx context.Context, filter map[string]any) ([]webhooks.Config, error)
	InsertOneConfig(ctx context.Context, cfg webhooks.ConfigUser) (webhooks.Config, error)
	DeleteOneConfig(ctx context.Context, id string) error
	UpdateOneConfigActivation(ctx context.Context, id string, active bool) (webhooks.Config, error)
	UpdateOneConfigSecret(ctx context.Context, id, secret string) (webhooks.Config, error)
	FindAttemptsToRetryByWebhookID(ctx context.Context, webhookID string) ([]webhooks.Attempt, error)
	FindFirstAttemptCreatedAtByWebhookID(ctx context.Context, webhookID string) (time.Time, error)
	FindWebhookIDsToRetry(ctx context.Context, limit int) (webhookIDs []string, err error)
	RecoverStaleRetryingAttempts(ctx context.Context, staleDuration time.Duration) error
	UpdateAttemptsStatus(ctx context.Context, webhookID string, status string) ([]webhooks.Attempt, error)
	InsertOneAttempt(ctx context.Context, att webhooks.Attempt) error
	InsertOneAttemptAndUpdateAttemptsStatus(ctx context.Context, att webhooks.Attempt, webhookID string, status string) error
	Close(ctx context.Context) error
	UpdateOneConfig(ctx context.Context, id string, cfg webhooks.ConfigUser) error

	// PurgeFinishedAttempts deletes terminal attempts older than the given
	// retentions (success vs failed), up to batchSize rows per status and run.
	// A retention <= 0 disables purging for that status.
	PurgeFinishedAttempts(ctx context.Context, successOlderThan, failedOlderThan time.Duration, batchSize int) (int64, error)
	// FailUnclaimableAttempts marks pending attempts whose config no longer exists
	// or is inactive as 'failed'. 'to retry' rows are failed immediately; 'retrying'
	// rows are failed only after retryingStaleDuration so in-flight workers are not
	// raced by retention. At most batchSize rows are updated per run.
	FailUnclaimableAttempts(ctx context.Context, batchSize int, retryingStaleDuration time.Duration) (int64, error)
	// CountAttemptsToRetry returns the capped number of attempts currently queued
	// for retry ('to retry'). Used as the retry-queue-depth gauge — the leading
	// indicator of a retry storm.
	CountAttemptsToRetry(ctx context.Context) (int64, error)
}
