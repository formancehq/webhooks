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

// RetryDelayLimiter is implemented by retry policies that can reject a custom
// retry delay before it is persisted.
type RetryDelayLimiter interface {
	LimitRetryDelay(attemptNumber int, delay time.Duration) (time.Duration, error)
}

// RetryWindowLimiter is implemented by retry policies that can reject a retry
// schedule based on the real elapsed time since the first delivery attempt.
type RetryWindowLimiter interface {
	LimitRetryWindow(elapsed time.Duration) error
}
