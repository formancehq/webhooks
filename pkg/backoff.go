package webhooks

import "time"

type BackoffPolicy interface {
	GetRetryDelay(attemptNumber int) (time.Duration, error)
}

// RetryAttemptLimiter is implemented by retry policies that can reject a retry
// attempt before the outbound HTTP call is made.
type RetryAttemptLimiter interface {
	CanRetryAttempt(attemptNumber int) error
}
