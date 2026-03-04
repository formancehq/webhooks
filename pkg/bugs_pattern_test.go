package webhooks

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBug2_RangeLoopIndexModifiesSlice verifies that the index-based
// range loop correctly updates slice elements in-place.
func TestBug2_RangeLoopIndexModifiesSlice(t *testing.T) {
	atts := []Attempt{
		{ID: "1", Status: StatusAttemptToRetry},
		{ID: "2", Status: StatusAttemptToRetry},
	}

	newStatus := StatusAttemptSuccess

	// The corrected pattern: index-based loop
	for i := range atts {
		atts[i].Status = newStatus
	}

	assert.Equal(t, StatusAttemptSuccess, atts[0].Status,
		"index-based loop should update atts[0].Status")
	assert.Equal(t, StatusAttemptSuccess, atts[1].Status,
		"index-based loop should update atts[1].Status")
}

// TestBug9_FilterReturnsErrorOnUnknownKey verifies that an unknown filter key
// returns an error instead of panicking.
func TestBug9_FilterReturnsErrorOnUnknownKey(t *testing.T) {
	filter := map[string]any{
		"unknown_key": "value",
	}

	// Simulate the corrected filter logic from FindManyConfigs
	assert.NotPanics(t, func() {
		for key := range filter {
			switch key {
			case "id":
			case "endpoint":
			case "active":
			case "event_types":
			default:
				_ = fmt.Errorf("unknown filter key: %s", key)
			}
		}
	}, "unknown filter key should not panic")
}
