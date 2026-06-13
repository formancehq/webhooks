package backoff

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExponential_Nominal(t *testing.T) {
	policy := NewExponential(time.Minute, time.Hour, 24*time.Hour, 0)
	wantDurations := []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		16 * time.Minute,
		32 * time.Minute,
		time.Hour,
		time.Hour,
		time.Hour,
	}
	for attemptNumber, wantDelay := range wantDurations {
		t.Run(fmt.Sprint("attempt #", attemptNumber), func(t *testing.T) {
			t.Parallel()
			gotDelay, err := policy.GetRetryDelay(attemptNumber)

			assert.NoError(t, err)
			assert.Equal(t, wantDelay, gotDelay)
		})
	}
}

func TestExponential_Limit(t *testing.T) {
	// Attempt:             0    1    2
	// Delay:               1m   2m   X
	// sinceFirstAttempt:   1m   3m   X
	policy := NewExponential(time.Minute, 5*time.Minute, 3*time.Minute, 0)

	delay, err := policy.GetRetryDelay(1)
	assert.NoError(t, err)
	assert.Equal(t, 2*time.Minute, delay)

	_, err = policy.GetRetryDelay(2)
	assert.ErrorIs(t, err, ErrMaxAttemptsReached)
}

func TestExponential_MaxAttemptsCap(t *testing.T) {
	// abort-after is huge, so only the attempt cap (3) can stop retries.
	policy := NewExponential(time.Minute, time.Hour, 100*24*time.Hour, 3)

	for attempt := range 2 {
		_, err := policy.GetRetryDelay(attempt)
		assert.NoError(t, err, "attempt %d should be allowed to schedule another retry", attempt)
	}

	_, err := policy.GetRetryDelay(2)
	assert.ErrorIs(t, err, ErrMaxAttemptsReached)
}

func TestExponential_CanRetryAttemptOnlyChecksAttemptCap(t *testing.T) {
	policy := NewExponential(time.Minute, time.Hour, 2*time.Minute, 3)
	limiter, ok := policy.(interface {
		CanRetryAttempt(int) error
	})
	assert.True(t, ok)

	assert.NoError(t, limiter.CanRetryAttempt(2))
	assert.ErrorIs(t, limiter.CanRetryAttempt(3), ErrMaxAttemptsReached)

	_, err := policy.GetRetryDelay(1)
	assert.ErrorIs(t, err, ErrMaxAttemptsReached, "abort-after still applies when scheduling a future retry")
}
