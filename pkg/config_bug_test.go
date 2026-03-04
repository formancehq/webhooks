package webhooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBug6_InvalidEndpointsRejected verifies that endpoints without
// http/https scheme or without a host are rejected.
func TestBug6_InvalidEndpointsRejected(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"plain text", "foobar"},
		{"relative path", "/just/a/path"},
		{"no scheme", "example.com/webhook"},
		{"javascript scheme", "javascript:alert(1)"},
		{"ftp scheme", "ftp://example.com/webhook"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ConfigUser{
				Endpoint:   tt.endpoint,
				EventTypes: []string{"test.event"},
			}
			err := cfg.Validate()
			assert.ErrorIs(t, err, ErrInvalidEndpoint,
				"endpoint '%s' should be rejected", tt.endpoint)
		})
	}
}

// TestBug6_ValidEndpointsAccepted verifies that valid http/https endpoints pass.
func TestBug6_ValidEndpointsAccepted(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"http", "http://example.com/webhook"},
		{"https", "https://example.com/webhook"},
		{"https with port", "https://example.com:8443/webhook"},
		{"localhost", "http://localhost:8080/hook"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ConfigUser{
				Endpoint:   tt.endpoint,
				EventTypes: []string{"test.event"},
			}
			err := cfg.Validate()
			assert.NoError(t, err, "endpoint '%s' should be accepted", tt.endpoint)
		})
	}
}
