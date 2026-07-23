package webhooks

import (
	"time"

	"github.com/uptrace/bun"
)

const (
	StatusDeliveryPending    = "pending"
	StatusDeliveryDelivering = "delivering"
	StatusDeliverySucceeded  = "succeeded"
	StatusDeliveryFailed     = "failed"
	StatusDeliveryCancelled  = "cancelled"

	OutcomeDeliverySucceeded        = "succeeded"
	OutcomeDeliveryRetryableFailure = "retryable_failure"
	OutcomeDeliveryPermanentFailure = "permanent_failure"
)

// Delivery is the durable state of one event sent to one webhook config.
// DeliveryAttempt contains the append-only history of the HTTP calls.
type Delivery struct {
	bun.BaseModel `bun:"table:deliveries"`

	ID               string     `json:"id" bun:",pk"`
	EventID          string     `json:"eventID" bun:"event_id,notnull"`
	IdempotencyKey   string     `json:"idempotencyKey,omitempty" bun:"idempotency_key"`
	ConfigID         string     `json:"configID" bun:"config_id,notnull"`
	EventType        string     `json:"eventType" bun:"event_type,notnull"`
	Payload          string     `json:"payload,omitempty" bun:"payload,notnull"`
	Status           string     `json:"status" bun:"status,notnull"`
	AttemptCount     int        `json:"attemptCount" bun:"attempt_count,notnull"`
	ReplayGeneration int        `json:"replayGeneration" bun:"replay_generation,notnull"`
	CycleStartedAt   *time.Time `json:"cycleStartedAt,omitempty" bun:"cycle_started_at"`
	NextAttemptAt    *time.Time `json:"nextAttemptAt,omitempty" bun:"next_attempt_at"`
	ClaimedAt        *time.Time `json:"claimedAt,omitempty" bun:"claimed_at"`
	LastAttemptAt    *time.Time `json:"lastAttemptAt,omitempty" bun:"last_attempt_at"`
	LastStatusCode   *int       `json:"lastStatusCode,omitempty" bun:"last_status_code"`
	LastError        string     `json:"lastError,omitempty" bun:"last_error"`
	CreatedAt        time.Time  `json:"createdAt" bun:"created_at,nullzero,notnull,default:current_timestamp"`
	UpdatedAt        time.Time  `json:"updatedAt" bun:"updated_at,nullzero,notnull,default:current_timestamp"`
}

type DeliveryAttempt struct {
	bun.BaseModel `bun:"table:delivery_attempts"`

	ID               string    `json:"id" bun:",pk"`
	DeliveryID       string    `json:"deliveryID" bun:"delivery_id,notnull"`
	AttemptNumber    int       `json:"attemptNumber" bun:"attempt_number,notnull"`
	ReplayGeneration int       `json:"replayGeneration" bun:"replay_generation,notnull"`
	Endpoint         string    `json:"endpoint" bun:"endpoint,notnull"`
	Outcome          string    `json:"outcome" bun:"outcome,notnull"`
	StatusCode       int       `json:"statusCode" bun:"status_code,notnull"`
	Error            string    `json:"error,omitempty" bun:"error"`
	DurationMillis   *int64    `json:"durationMillis,omitempty" bun:"duration_millis"`
	ResponseExcerpt  string    `json:"responseExcerpt,omitempty" bun:"response_excerpt"`
	CreatedAt        time.Time `json:"createdAt" bun:"created_at,nullzero,notnull,default:current_timestamp"`
}

type DeliveryFilter struct {
	ConfigID      string
	Status        string
	CreatedAfter  time.Time
	CreatedBefore time.Time
	After         *DeliveryCursor
	PageSize      int
}

type DeliveryCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

type ReplayDeliveryCursor struct {
	Position      DeliveryCursor `json:"position"`
	CreatedAtFrom time.Time      `json:"createdAtFrom"`
	CreatedAtTo   time.Time      `json:"createdAtTo"`
	Statuses      []string       `json:"statuses"`
	ConfigIDs     []string       `json:"configIds,omitempty"`
}

type DeliveryPage struct {
	Data       []Delivery
	NextCursor *DeliveryCursor
	HasMore    bool
}

type ReplayDeliveriesRequest struct {
	CreatedAtFrom time.Time       `json:"createdAtFrom"`
	CreatedAtTo   time.Time       `json:"createdAtTo,omitempty"`
	Statuses      []string        `json:"statuses,omitempty"`
	ConfigIDs     []string        `json:"configIds,omitempty"`
	Cursor        *DeliveryCursor `json:"-"`
	CursorToken   string          `json:"cursor,omitempty"`
	PageSize      int             `json:"pageSize,omitempty"`
}

type ReplayDeliveriesResult struct {
	Replayed        int             `json:"replayed"`
	Expedited       int             `json:"expedited"`
	Skipped         int             `json:"skipped"`
	HasMore         bool            `json:"hasMore"`
	NextCursor      *DeliveryCursor `json:"-"`
	NextCursorToken string          `json:"nextCursor,omitempty"`
	CreatedAtTo     time.Time       `json:"createdAtTo"`
}

type ReplayRequestRecord struct {
	bun.BaseModel `bun:"table:replay_requests"`

	Key         string    `bun:"key,pk"`
	RequestHash string    `bun:"request_hash,notnull"`
	Response    string    `bun:"response,notnull"`
	CreatedAt   time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp"`
}
