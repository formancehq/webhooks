package webhooks

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

const (
	CircuitStateClosed   = "closed"
	CircuitStateOpen     = "open"
	CircuitStateHalfOpen = "half_open"

	CircuitDecisionReasonClosed        = "closed"
	CircuitDecisionReasonOpenUntil     = "open_until"
	CircuitDecisionReasonHalfOpenProbe = "half_open_probe"
	CircuitDecisionReasonProbeBusy     = "half_open_probe_busy"
)

const (
	DefaultCircuitFailureThreshold = 10
	DefaultCircuitOpenDelay        = 5 * time.Minute
	DefaultCircuitMaxOpenDelay     = time.Hour
)

type DeliveryCircuit struct {
	bun.BaseModel `bun:"table:webhook_delivery_circuits"`

	ConfigID              string    `json:"configID" bun:"config_id,pk"`
	State                 string    `json:"state"`
	ConsecutiveFailures   int       `json:"consecutiveFailures" bun:"consecutive_failures"`
	OpenedUntil           time.Time `json:"openedUntil,omitempty" bun:"opened_until,nullzero"`
	ProbeAttempt          int       `json:"probeAttempt" bun:"probe_attempt"`
	LastFailureAt         time.Time `json:"lastFailureAt,omitempty" bun:"last_failure_at,nullzero"`
	LastFailureStatusCode int       `json:"lastFailureStatusCode,omitempty" bun:"last_failure_status_code,nullzero"`
	LastFailureReason     string    `json:"lastFailureReason,omitempty" bun:"last_failure_reason,nullzero"`
	UpdatedAt             time.Time `json:"updatedAt" bun:"updated_at,nullzero,notnull,default:current_timestamp"`
}

type CircuitDecision struct {
	Allowed     bool
	Reason      string
	OpenedUntil time.Time
}

type DeliveryFailure struct {
	StatusCode int
	Reason     string
}

type CircuitBreaker interface {
	BeforeDelivery(ctx context.Context, configID string) (CircuitDecision, error)
	RecordSuccess(ctx context.Context, configID string) error
	RecordFailure(ctx context.Context, configID string, failure DeliveryFailure) error
}

func NewDeliveryCircuit(configID string) DeliveryCircuit {
	return DeliveryCircuit{
		ConfigID:            configID,
		State:               CircuitStateClosed,
		ConsecutiveFailures: 0,
		ProbeAttempt:        0,
		UpdatedAt:           time.Now().UTC(),
	}
}

func CircuitProbeDelay(probeAttempt int) time.Duration {
	delay := DefaultCircuitOpenDelay
	for i := 0; i < probeAttempt; i++ {
		delay *= 2
		if delay >= DefaultCircuitMaxOpenDelay {
			return DefaultCircuitMaxOpenDelay
		}
	}
	return delay
}
