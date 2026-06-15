package backoff

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoRetry(t *testing.T) {
	policy := NewNoRetry()
	_, err := policy.GetRetryDelay(0)
	assert.ErrorIs(t, err, ErrMaxAttemptsReached)

	limiter, ok := policy.(interface {
		CanRetryAttempt(int) error
	})
	require.True(t, ok)
	assert.ErrorIs(t, limiter.CanRetryAttempt(0), ErrMaxAttemptsReached)

	delayLimiter, ok := policy.(interface {
		LimitRetryDelay(int, time.Duration) (time.Duration, error)
	})
	require.True(t, ok)
	_, err = delayLimiter.LimitRetryDelay(0, time.Second)
	assert.ErrorIs(t, err, ErrMaxAttemptsReached)
}
