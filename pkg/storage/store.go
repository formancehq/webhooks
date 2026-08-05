package storage

import (
	"context"
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/pkg/errors"
)

var (
	ErrConfigNotFound        = errors.New("config not found")
	ErrConfigNotModified     = errors.New("config not modified")
	ErrDeliveryNotFound      = errors.New("delivery not found")
	ErrDeliveryNotReplayable = errors.New("delivery cannot be replayed")
	ErrIdempotencyConflict   = errors.New("idempotency key already used with another request")
)

type Store interface {
	FindManyConfigs(ctx context.Context, filter map[string]any) ([]webhooks.Config, error)
	InsertOneConfig(ctx context.Context, cfg webhooks.ConfigUser) (webhooks.Config, error)
	DeleteOneConfig(ctx context.Context, id string) error
	UpdateOneConfigActivation(ctx context.Context, id string, active bool) (webhooks.Config, error)
	UpdateOneConfigSecret(ctx context.Context, id, secret string) (webhooks.Config, error)
	Close(ctx context.Context) error
	UpdateOneConfig(ctx context.Context, id string, cfg webhooks.ConfigUser) error

	EnqueueEvent(ctx context.Context, eventID, idempotencyKey, eventType, payload string, createdAt time.Time) error
	ClaimDeliveries(ctx context.Context, limit int) ([]webhooks.Delivery, error)
	CompleteDelivery(ctx context.Context, delivery webhooks.Delivery, attempt webhooks.DeliveryAttempt) (string, error)
	FailClaimedDelivery(ctx context.Context, id string, claimedAt time.Time, reason string) error
	CancelDelivery(ctx context.Context, id string) error
	RecoverStaleDeliveries(ctx context.Context, staleDuration time.Duration) (int64, error)
	CountPendingDeliveries(ctx context.Context) (int64, error)
	FindDeliveries(ctx context.Context, filter webhooks.DeliveryFilter) (webhooks.DeliveryPage, error)
	GetDelivery(ctx context.Context, id string) (webhooks.Delivery, error)
	FindDeliveryAttempts(ctx context.Context, deliveryID string, after *webhooks.DeliveryCursor, pageSize int) ([]webhooks.DeliveryAttempt, *webhooks.DeliveryCursor, error)
	ReplayDelivery(ctx context.Context, id, idempotencyKey string) (webhooks.Delivery, bool, error)
	ReplayDeliveries(ctx context.Context, request webhooks.ReplayDeliveriesRequest, idempotencyKey string) (webhooks.ReplayDeliveriesResult, bool, error)
	PurgeFinishedDeliveries(ctx context.Context, successOlderThan, failedOlderThan time.Duration, batchSize int) (int64, error)
	BackfillDeliveries(ctx context.Context, successSince, failedSince time.Duration, batchSize int) (int64, error)
}
