package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetentionConfigEnabled(t *testing.T) {
	require.False(t, RetentionConfig{}.Enabled())

	require.True(t, RetentionConfig{Period: time.Hour}.Enabled(),
		"the runner must start to reclaim deleted/inactive config attempts even when terminal purging is disabled")
	require.True(t, RetentionConfig{SuccessDelay: time.Hour}.Enabled())
	require.True(t, RetentionConfig{FailedDelay: time.Hour}.Enabled())
}
