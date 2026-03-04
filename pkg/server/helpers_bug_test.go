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

// TestBug1_ContentTypeWithCharset verifies that charset parameter is accepted.
func TestBug1_ContentTypeWithCharset(t *testing.T) {
	type testPayload struct {
		Name string `json:"name"`
	}

	body := []byte(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	var dst testPayload
	err := decodeJSONBody(req, &dst, false)

	require.NoError(t, err)
	assert.Equal(t, "test", dst.Name)
}

// TestBug1_AllowEmptyWithoutContentType verifies that allowEmpty=true
// does not require Content-Type header when body is empty.
func TestBug1_AllowEmptyWithoutContentType(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	// No Content-Type header set

	var dst struct{ Name string }
	err := decodeJSONBody(req, &dst, true)

	assert.NoError(t, err, "allowEmpty=true should not require Content-Type")
}

// TestBug1_RequireContentTypeWhenNotAllowEmpty verifies that allowEmpty=false
// requires Content-Type header.
func TestBug1_RequireContentTypeWhenNotAllowEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	// No Content-Type header set

	var dst struct{ Name string }
	err := decodeJSONBody(req, &dst, false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Content-Type")
}
