package backoff

import (
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
)

func NewNoRetry() webhooks.BackoffPolicy {
	return new(NoRetry)
}

type NoRetry struct{}

func (n *NoRetry) GetRetryDelay(attemptNumber int) (time.Duration, error) {
	return 0, ErrMaxAttemptsReached
}

func (n *NoRetry) CanRetryAttempt(attemptNumber int) error {
	return ErrMaxAttemptsReached
}

func (n *NoRetry) LimitRetryDelay(attemptNumber int, delay time.Duration) (time.Duration, error) {
	return 0, ErrMaxAttemptsReached
}
