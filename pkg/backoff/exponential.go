package backoff

import (
	"errors"
	"time"

	webhooks "github.com/formancehq/webhooks/pkg"
)

var ErrMaxAttemptsReached = errors.New("max attempts reached")

// NewExponential builds an exponential backoff policy. maxAttempts is a hard cap
// on the number of attempts: once reached, GetRetryDelay returns
// ErrMaxAttemptsReached regardless of the abort-after window. A value <= 0
// disables the cap (abort-after remains the only bound). The cap exists because
// abort-after alone produced ~724 attempts per dead endpoint in production.
func NewExponential(minRetryDelay, maxRetryDelay, abortAfterDelay time.Duration, maxAttempts int) webhooks.BackoffPolicy {
	return &exponential{
		minRetryDelay,
		maxRetryDelay,
		abortAfterDelay,
		maxAttempts,
	}
}

type exponential struct {
	minRetryDelay   time.Duration
	maxRetryDelay   time.Duration
	abortAfterDelay time.Duration
	maxAttempts     int
}

func (e *exponential) GetRetryDelay(attemptNumber int) (time.Duration, error) {
	if e.maxAttempts > 0 && attemptNumber+1 >= e.maxAttempts {
		return 0, ErrMaxAttemptsReached
	}

	delay := e.retryDelayForAttempt(attemptNumber)
	if e.elapsedBeforeAttempt(attemptNumber)+delay > e.abortAfterDelay {
		return 0, ErrMaxAttemptsReached
	}
	return delay, nil
}

func (e *exponential) CanRetryAttempt(attemptNumber int) error {
	if e.maxAttempts > 0 && attemptNumber >= e.maxAttempts {
		return ErrMaxAttemptsReached
	}
	return nil
}

func (e *exponential) LimitRetryDelay(attemptNumber int, delay time.Duration) (time.Duration, error) {
	if e.elapsedBeforeAttempt(attemptNumber)+delay > e.abortAfterDelay {
		return 0, ErrMaxAttemptsReached
	}
	return delay, nil
}

func (e *exponential) retryDelayForAttempt(attemptNumber int) time.Duration {
	delay := e.minRetryDelay
	for i := 0; i < attemptNumber; i++ {
		delay <<= 1
		if delay > e.maxRetryDelay {
			delay = e.maxRetryDelay
		}
	}
	return delay
}

func (e *exponential) elapsedBeforeAttempt(attemptNumber int) time.Duration {
	var elapsed time.Duration
	for i := 0; i < attemptNumber; i++ {
		elapsed += e.retryDelayForAttempt(i)
	}
	return elapsed
}
