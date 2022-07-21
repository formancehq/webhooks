package test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/numary/go-libs/sharedapi"
	"github.com/numary/webhooks-cloud/api/server"
	"github.com/numary/webhooks-cloud/internal/storage/mongodb"
	"github.com/numary/webhooks-cloud/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobal(t *testing.T) {
	store, err := mongodb.NewStore()
	require.NoError(t, err)
	defer func(store mongodb.Store) {
		require.NoError(t, store.Close())
	}(store)
	h := server.NewWebhooksHandler(store)

	t.Run("no configs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, server.ConfigsPath,
			nil)
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusOK, resp.Result().StatusCode)

		req = httptest.NewRequest(http.MethodGet, server.ConfigsPath,
			nil)
		resp = recordResponse(h, req)
		assert.Equal(t, http.StatusOK, resp.Result().StatusCode)
		cur := decodeCursorResponse[model.ConfigInserted](t, resp.Body)
		assert.Equal(t, 0, len(cur.Data))
	})

	req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
		buffer(t, model.Config{
			Active:     true,
			EventTypes: []string{"TYPE1", "TYPE2"},
			Endpoints:  []string{"https://www.site1.com", "https://www.site2.com"},
		}))
	req.Header.Set("Content-Type", "application/json")
	resp := recordResponse(h, req)
	assert.Equal(t, http.StatusOK, resp.Result().StatusCode)

	req = httptest.NewRequest(http.MethodPost, server.ConfigsPath,
		buffer(t, model.Config{
			Active:     true,
			EventTypes: []string{"TYPE3"},
			Endpoints:  []string{"https://www.site3.com"},
		}))
	req.Header.Set("Content-Type", "application/json")
	resp = recordResponse(h, req)
	assert.Equal(t, http.StatusOK, resp.Result().StatusCode)

	req = httptest.NewRequest(http.MethodPost, server.ConfigsPath,
		buffer(t, model.Config{
			Active:     false,
			EventTypes: []string{},
			Endpoints:  []string{},
		}))
	req.Header.Set("Content-Type", "application/json")
	resp = recordResponse(h, req)
	assert.Equal(t, http.StatusOK, resp.Result().StatusCode)

	t.Run("get all configs", func(t *testing.T) {
		req = httptest.NewRequest(http.MethodGet, server.ConfigsPath, nil)
		resp = recordResponse(h, req)
		assert.Equal(t, http.StatusOK, resp.Result().StatusCode)
		cur := decodeCursorResponse[model.ConfigInserted](t, resp.Body)
		assert.Equal(t, 3, len(cur.Data))
		assert.Equal(t, false, cur.Data[0].Active)
	})

	t.Run("delete all configs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, server.ConfigsPath, nil)
		resp = recordResponse(h, req)
		assert.Equal(t, http.StatusOK, resp.Result().StatusCode)

		req = httptest.NewRequest(http.MethodGet, server.ConfigsPath, nil)
		resp = recordResponse(h, req)
		assert.Equal(t, http.StatusOK, resp.Result().StatusCode)
		cur := decodeCursorResponse[model.ConfigInserted](t, resp.Body)
		assert.Equal(t, 0, len(cur.Data))
	})
}

func TestInsertConfigErrors(t *testing.T) {
	store, err := mongodb.NewStore()
	require.NoError(t, err)
	defer func(store mongodb.Store) {
		require.NoError(t, store.Close())
	}(store)
	h := server.NewWebhooksHandler(store)

	t.Run("invalid config", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			buffer(t, model.Config{
				Active:    false,
				Endpoints: []string{"https://www.site1.com"},
			}))
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)

		req = httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			buffer(t, model.Config{
				Active:     false,
				EventTypes: []string{"TYPE"},
			}))
		resp = recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
	})

	t.Run("invalid content type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			buffer(t, model.Config{}))
		req.Header.Set("Content-Type", "invalid")
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusUnsupportedMediaType, resp.Result().StatusCode)
	})

	t.Run("nil body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			nil)
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
	})

	t.Run("invalid body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			bytes.NewBuffer([]byte("{")))
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
	})

	t.Run("invalid body double json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			bytes.NewBuffer([]byte("{\"active\":false}{\"active\":false}")))
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
	})

	t.Run("invalid body unknown field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			bytes.NewBuffer([]byte("{\"field\":false}")))
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
	})

	t.Run("invalid body invalid value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			bytes.NewBuffer([]byte("{\"active\":1}")))
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
	})

	t.Run("invalid body syntax", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, server.ConfigsPath,
			bytes.NewBuffer([]byte("{\"active\":true,}")))
		resp := recordResponse(h, req)
		assert.Equal(t, http.StatusBadRequest, resp.Result().StatusCode)
	})
}

func TestHealthCheck(t *testing.T) {
	store, err := mongodb.NewStore()
	require.NoError(t, err)
	h := server.NewWebhooksHandler(store)

	req := httptest.NewRequest(
		http.MethodGet, server.HealthCheckPath, nil)
	resp := recordResponse(h, req)
	assert.Equal(t, http.StatusOK, resp.Result().StatusCode)
}

func recordResponse(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func buffer(t *testing.T, v any) *bytes.Buffer {
	data, err := json.Marshal(v)
	assert.NoError(t, err)
	return bytes.NewBuffer(data)
}

func decodeCursorResponse[T any](t *testing.T, reader io.Reader) *sharedapi.Cursor[T] {
	res := sharedapi.BaseResponse[T]{}
	err := json.NewDecoder(reader).Decode(&res)
	assert.NoError(t, err)
	return res.Cursor
}
