package server

// SEC-103 correction-phase tests (phase 2).
//
// These tests pin the new contract next to the phase-1 regression files
// (sec103_secret_disclosure_test.go, which must stay byte-identical):
//   - ordinary responses (GET /configs, activate, deactivate) never carry a
//     secret field at all;
//   - POST /configs reveals the supplied or generated secret exactly once;
//   - PUT /configs/{id}/secret/change reveals only the new secret;
//   - PUT /configs/{id} with an omitted/empty secret preserves the stored
//     secret (verified through the store, and through HMAC signing with the
//     retained key), while an explicit valid secret replaces it.
//
// All fixtures are local fakes; no secret value is ever logged.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/security"
	"github.com/formancehq/webhooks/pkg/storage"
)

// recordingStore wraps stubStore to record UpdateOneConfig / InsertOneConfig
// / UpdateOneConfigSecret calls and to serve the create/rotate/update flows.
type recordingStore struct {
	*stubStore

	lastUpdate     *webhooks.ConfigUser
	inserted       *webhooks.ConfigUser
	changedSecrets []string
}

func newRecordingStore(cfgs ...webhooks.Config) *recordingStore {
	return &recordingStore{stubStore: newStubStore(cfgs...)}
}

func (s *recordingStore) InsertOneConfig(_ context.Context, cfg webhooks.ConfigUser) (webhooks.Config, error) {
	s.inserted = &cfg
	c := webhooks.NewConfig(cfg)
	s.configs = append(s.configs, c)
	return c, nil
}

func (s *recordingStore) UpdateOneConfig(_ context.Context, id string, cfg webhooks.ConfigUser) error {
	s.lastUpdate = &cfg
	for i, c := range s.configs {
		if c.ID == id && c.DeletedAt == nil {
			s.configs[i].Endpoint = cfg.Endpoint
			s.configs[i].EventTypes = cfg.EventTypes
			if cfg.Secret != "" {
				// explicit replacement, mirroring the SQL write
				s.configs[i].Secret = cfg.Secret
			}
			// empty secret => preserved (mirrors secret = configs.secret)
			return nil
		}
	}
	return storage.ErrConfigNotFound
}

func (s *recordingStore) UpdateOneConfigSecret(_ context.Context, id, secret string) (webhooks.Config, error) {
	s.changedSecrets = append(s.changedSecrets, secret)
	for i, c := range s.configs {
		if c.ID == id && c.DeletedAt == nil {
			if c.Secret == secret {
				return c, storage.ErrConfigNotModified
			}
			s.configs[i].Secret = secret
			c.Secret = secret
			return c, nil
		}
	}
	return webhooks.Config{}, storage.ErrConfigNotFound
}

func doSec103JSONRequest(t *testing.T, h http.Handler, method, path, token, body string) (int, string) {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestSec103CreateRevealsSecretOnce checks POST /configs returns the secret
// (supplied or generated) and that a following GET does not.
func TestSec103CreateRevealsSecretOnce(t *testing.T) {
	store := newRecordingStore()
	h := newSec103TestHandler(store, fakeScopeAuth{checkScopes: true, service: "webhooks"})

	supplied := webhooks.NewSecret()

	t.Run("supplied secret is revealed and stored decoded-valid", func(t *testing.T) {
		status, body := doSec103JSONRequest(t, h, http.MethodPost, PathConfigs, "webhooks:write",
			`{"endpoint":"https://sec103.invalid/create","secret":"`+supplied+`","eventTypes":["TYPE1"]}`)
		require.Equal(t, http.StatusOK, status, body)

		var resp struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &resp))
		assert.Equal(t, supplied, resp.Data["secret"], "create response must reveal the supplied secret")
		assert.Equal(t, "https://sec103.invalid/create", resp.Data["endpoint"])

		require.NotNil(t, store.inserted)
		assert.Equal(t, supplied, store.inserted.Secret, "supplied secret must be stored as given (decoded-valid)")

		status, body = doSec103JSONRequest(t, h, http.MethodGet, PathConfigs, "webhooks:read", "")
		require.Equal(t, http.StatusOK, status)
		assertNoSecretInJSON(t, body)
	})

	t.Run("generated secret is revealed once", func(t *testing.T) {
		status, body := doSec103JSONRequest(t, h, http.MethodPost, PathConfigs, "webhooks:write",
			`{"endpoint":"https://sec103.invalid/gen","eventTypes":["TYPE1"]}`)
		require.Equal(t, http.StatusOK, status, body)

		var resp struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &resp))
		generated, ok := resp.Data["secret"].(string)
		require.True(t, ok, "create response must carry a secret")
		assert.NotEmpty(t, generated)
		require.NotNil(t, store.inserted)
		assert.Equal(t, generated, store.inserted.Secret)

		status, body = doSec103JSONRequest(t, h, http.MethodGet, PathConfigs, "webhooks:read", "")
		require.Equal(t, http.StatusOK, status)
		assertNoSecretInJSON(t, body)
		assert.NotContains(t, body, generated)
	})

	t.Run("invalid supplied secret is rejected", func(t *testing.T) {
		status, body := doSec103JSONRequest(t, h, http.MethodPost, PathConfigs, "webhooks:write",
			`{"endpoint":"https://sec103.invalid/bad","secret":"invalid","eventTypes":["TYPE1"]}`)
		assert.NotEqual(t, http.StatusOK, status, "invalid secret must not be accepted: %s", body)
		var errResp struct {
			ErrorCode string `json:"errorCode"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &errResp))
		assert.Equal(t, "VALIDATION", errResp.ErrorCode)
	})
}

// TestSec103RotateRevealsOnlyNewSecret checks PUT /configs/{id}/secret/change
// reveals only the new secret and a following GET reveals nothing.
func TestSec103RotateRevealsOnlyNewSecret(t *testing.T) {
	cfg := seedConfig("cfg-sec103-rotate", "https://sec103.invalid/rotate", true)
	store := newRecordingStore(cfg)
	h := newSec103TestHandler(store, fakeScopeAuth{checkScopes: true, service: "webhooks"})

	newSecret := webhooks.NewSecret()
	status, body := doSec103JSONRequest(t, h, http.MethodPut,
		PathConfigs+"/"+cfg.ID+PathChangeSecret, "webhooks:write",
		`{"secret":"`+newSecret+`"}`)
	require.Equal(t, http.StatusOK, status, body)

	var resp struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, newSecret, resp.Data["secret"], "rotate response must reveal exactly the new secret")
	require.Len(t, store.changedSecrets, 1)
	assert.Equal(t, newSecret, store.changedSecrets[0])

	status, body = doSec103JSONRequest(t, h, http.MethodGet, PathConfigs, "webhooks:read", "")
	require.Equal(t, http.StatusOK, status)
	assertNoSecretInJSON(t, body)
	assert.NotContains(t, body, newSecret)
}

// TestSec103UpdatePreservesSecretWhenOmitted checks the update semantics:
// omitted, null and empty secret preserve the stored secret; only an explicit
// valid secret replaces it; an invalid one is rejected.
func TestSec103UpdatePreservesSecretWhenOmitted(t *testing.T) {
	cfg := seedConfig("cfg-sec103-update", "https://sec103.invalid/update", true)

	bodies := map[string]string{
		"omitted": `{"endpoint":"https://sec103.invalid/u1","eventTypes":["TYPE1"]}`,
		"null":    `{"endpoint":"https://sec103.invalid/u2","secret":null,"eventTypes":["TYPE1"]}`,
		"empty":   `{"endpoint":"https://sec103.invalid/u3","secret":"","eventTypes":["TYPE1"]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			store := newRecordingStore(cfg)
			h := newSec103TestHandler(store, fakeScopeAuth{checkScopes: true, service: "webhooks"})

			status, respBody := doSec103JSONRequest(t, h, http.MethodPut,
				PathConfigs+"/"+cfg.ID, "webhooks:write", body)
			require.Equal(t, http.StatusOK, status, respBody)

			require.NotNil(t, store.lastUpdate)
			assert.Empty(t, store.lastUpdate.Secret, "handler must not generate a secret on update (%s)", name)
			// stored secret untouched in the store (mirrors secret = configs.secret)
			require.Len(t, store.configs, 1)
			assert.Equal(t, fakeSecret, store.configs[0].Secret)
		})
	}

	t.Run("explicit valid secret replaces the stored one", func(t *testing.T) {
		store := newRecordingStore(cfg)
		h := newSec103TestHandler(store, fakeScopeAuth{checkScopes: true, service: "webhooks"})

		replacement := webhooks.NewSecret()
		status, _ := doSec103JSONRequest(t, h, http.MethodPut,
			PathConfigs+"/"+cfg.ID, "webhooks:write",
			`{"endpoint":"https://sec103.invalid/u4","secret":"`+replacement+`","eventTypes":["TYPE1"]}`)
		require.Equal(t, http.StatusOK, status)
		require.NotNil(t, store.lastUpdate)
		assert.Equal(t, replacement, store.lastUpdate.Secret)
		assert.Equal(t, replacement, store.configs[0].Secret)
	})

	t.Run("invalid secret is rejected and nothing is written", func(t *testing.T) {
		store := newRecordingStore(cfg)
		h := newSec103TestHandler(store, fakeScopeAuth{checkScopes: true, service: "webhooks"})

		status, body := doSec103JSONRequest(t, h, http.MethodPut,
			PathConfigs+"/"+cfg.ID, "webhooks:write",
			`{"endpoint":"https://sec103.invalid/u5","secret":"invalid","eventTypes":["TYPE1"]}`)
		assert.NotEqual(t, http.StatusOK, status, "invalid replacement secret must be rejected: %s", body)
		assert.Nil(t, store.lastUpdate)
	})
}

// TestSec103RetainedSecretStillSigns checks the signer path is unchanged: with
// the secret preserved by an unrelated update, deliveries are still HMAC-signed
// with the retained key (security.Sign/Verify round trip).
func TestSec103RetainedSecretStillSigns(t *testing.T) {
	cfg := seedConfig("cfg-sec103-sign", "https://sec103.invalid/sign", true)
	store := newRecordingStore(cfg)
	h := newSec103TestHandler(store, fakeScopeAuth{checkScopes: true, service: "webhooks"})

	// unrelated update without secret
	status, _ := doSec103JSONRequest(t, h, http.MethodPut, PathConfigs+"/"+cfg.ID, "webhooks:write",
		`{"endpoint":"https://sec103.invalid/sign2","eventTypes":["TYPE1"]}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, fakeSecret, store.configs[0].Secret)

	// the retained key still produces verifiable signatures
	payload := []byte(`{"data":"test"}`)
	timestamp := time.Now().UTC().Unix()
	signature, err := security.Sign("wh-sec103", timestamp, store.configs[0].Secret, payload)
	require.NoError(t, err)
	ok, err := security.Verify(signature, "wh-sec103", timestamp, store.configs[0].Secret, payload)
	require.NoError(t, err)
	assert.True(t, ok, "HMAC with the retained key must verify")

	// and a signature made with any other key must not verify
	other := webhooks.NewSecret()
	badSig, err := security.Sign("wh-sec103", timestamp, other, payload)
	require.NoError(t, err)
	ok, err = security.Verify(badSig, "wh-sec103", timestamp, store.configs[0].Secret, payload)
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestSec103ActivateDeactivateResponsesHaveNoSecretKey is a stronger form of
// the phase-1 assertion for the activation endpoints: the "secret" key must
// be absent entirely (not merely empty).
func TestSec103ActivateDeactivateResponsesHaveNoSecretKey(t *testing.T) {
	active := seedConfig("cfg-sec103-a1", "https://sec103.invalid/a1", true)
	inactive := seedConfig("cfg-sec103-a2", "https://sec103.invalid/a2", false)
	store := newRecordingStore(active, inactive)
	h := newSec103TestHandler(store, fakeScopeAuth{checkScopes: true, service: "webhooks"})

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPut, PathConfigs + "/" + active.ID + PathDeactivate},
		{http.MethodPut, PathConfigs + "/" + inactive.ID + PathActivate},
		{http.MethodPut, PathConfigs + "/" + active.ID + PathActivate}, // not-modified path
	} {
		status, body := doSec103JSONRequest(t, h, tc.method, tc.path, "webhooks:write", "")
		require.Equal(t, http.StatusOK, status, body)
		var v any
		require.NoError(t, json.Unmarshal([]byte(body), &v))
		assert.False(t, strings.Contains(body, `"secret"`), "response must not contain a secret key: %s", tc.path)
		walkNoSecret(t, v, "$")
	}
}

func TestSec103TestAttemptRetainsSignatureWithoutDisclosingKey(t *testing.T) {
	verified := make(chan bool, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		timestamp, err := strconv.ParseInt(r.Header.Get("formance-webhook-timestamp"), 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ok, err := security.Verify(r.Header.Get("formance-webhook-signature"), r.Header.Get("formance-webhook-id"), timestamp, fakeSecret, payload)
		verified <- ok && err == nil
	}))
	t.Cleanup(receiver.Close)
	cfg := seedConfig("cfg-sec103-test", receiver.URL, true)
	h := newSec103TestHandler(newRecordingStore(cfg), fakeScopeAuth{checkScopes: true, service: "webhooks"})
	status, body := doSec103JSONRequest(t, h, http.MethodGet, PathConfigs+"/"+cfg.ID+PathTest, "webhooks:read", "")
	require.Equal(t, http.StatusOK, status)
	assertNoSecretInJSON(t, body)
	var response struct {
		Data struct {
			Config map[string]any `json:"config"`
			Status string         `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &response))
	assert.Equal(t, "success", response.Data.Status)
	assert.Equal(t, cfg.Name, response.Data.Config["name"])
	_, disclosed := response.Data.Config["secret"]
	assert.False(t, disclosed)
	select {
	case ok := <-verified:
		assert.True(t, ok, "receiver must verify using the original key")
	default:
		t.Fatal("local receiver was not called")
	}
}
