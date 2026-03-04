package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBug1_ContentTypeCheckBeforeParsing verifies that decodeJSONBody
// rejects wrong Content-Type BEFORE parsing the body.
func TestBug1_ContentTypeCheckBeforeParsing(t *testing.T) {
	type testPayload struct {
		Name string `json:"name"`
	}

	body := []byte(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")

	var dst testPayload
	err := decodeJSONBody(req, &dst, false)

	require.Error(t, err, "should reject wrong Content-Type")
	assert.Contains(t, err.Error(), "Content-Type")

	// dst must remain zero-value since Content-Type is checked before parsing
	assert.Equal(t, "", dst.Name,
		"body should NOT be parsed when Content-Type is wrong")
}

// TestBug1_ValidContentTypeAllowsParsing verifies normal operation.
func TestBug1_ValidContentTypeAllowsParsing(t *testing.T) {
	type testPayload struct {
		Name string `json:"name"`
	}

	body := []byte(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	var dst testPayload
	err := decodeJSONBody(req, &dst, false)

	require.NoError(t, err)
	assert.Equal(t, "test", dst.Name)
}
