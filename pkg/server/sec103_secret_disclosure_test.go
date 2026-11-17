package server

// Regression tests for stored webhook signing secret disclosure.
//
// Broken invariant: an authenticated caller allowed to read webhook
// configurations must never receive the stored HMAC signing secret in a
// configuration read response (GET /configs) or in the activate/deactivate
// responses. Base aa6803b violates this: ConfigUser.Secret is serialized
// with json:"secret" on every read path (pkg/config.go, pkg/server/get.go,
// pkg/server/activation.go).
//
// These tests intentionally decode responses as raw JSON (map[string]any)
// instead of the generated/SDK response models, so the correction worker can
// change the response types without touching this regression (digest
// stability). No secret value is ever logged: failure messages report only
// field presence and lengths.
//
// All fixtures are local fakes (stubStore, fakeScopeAuth, fakeSecret); the
// router, auth.Middleware and handlers under test are the real ones.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/formancehq/go-libs/v2/auth"
)

func doSec103Request(t *testing.T, h http.Handler, method, path, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// assertNoSecretInJSON fails the test if any object in the decoded JSON tree
// carries a non-empty "secret" field, or if the raw body contains the stored
// fake secret value. It never prints secret values.
func assertNoSecretInJSON(t *testing.T, body string) {
	t.Helper()
	if strings.TrimSpace(body) == "" {
		// Empty body (e.g. 401 from the auth middleware): nothing can leak.
		if strings.Contains(body, fakeSecret) {
			t.Errorf("raw response body contains the stored signing secret value")
		}
		return
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	walkNoSecret(t, v, "$")
	if strings.Contains(body, fakeSecret) {
		t.Errorf("raw response body contains the stored signing secret value")
	}
}

func walkNoSecret(t *testing.T, v any, path string) {
	t.Helper()
	switch node := v.(type) {
	case map[string]any:
		for k, val := range node {
			if strings.EqualFold(k, "secret") {
				if s, ok := val.(string); ok && s != "" {
					t.Errorf("JSON field %q at %s carries a non-empty secret (len=%d)", k, path, len(s))
				} else if val != nil {
					t.Errorf("JSON field %q at %s is present and non-empty (%T)", k, path, val)
				}
			}
			walkNoSecret(t, val, path+"."+k)
		}
	case []any:
		for i, item := range node {
			walkNoSecret(t, item, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

// assertLooksLikeConfigList proves the response is a genuine, otherwise
// intact configuration list, so a red result can only be caused by the
// secret disclosure assertion and not by a broken handler.
func assertLooksLikeConfigList(t *testing.T, body string, wantIDs ...string) {
	t.Helper()
	var resp struct {
		Cursor struct {
			Data []map[string]any `json:"data"`
		} `json:"cursor"`
		Data *map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("cannot decode response envelope: %v", err)
	}
	items := resp.Cursor.Data
	if items == nil && resp.Data != nil {
		items = []map[string]any{*resp.Data}
	}
	if len(items) != len(wantIDs) {
		t.Fatalf("expected %d config(s) in response, got %d", len(wantIDs), len(items))
	}
	for i, id := range wantIDs {
		if got := items[i]["id"]; got != id {
			t.Fatalf("config[%d].id = %v, want %q", i, got, id)
		}
		if items[i]["endpoint"] == nil || items[i]["endpoint"] == "" {
			t.Errorf("config[%d] has no endpoint: response is not a genuine config payload", i)
		}
	}
}

// TestSec103GetConfigsMustNotDiscloseSecret is the core regression: ordinary
// authenticated reads of GET /configs must not carry the stored secret.
// Red on base aa6803b (secret serialized verbatim), green once redacted.
func TestSec103GetConfigsMustNotDiscloseSecret(t *testing.T) {
	cfg := seedConfig("cfg-sec103-1", "https://sec103.invalid/hook", true)

	cases := []struct {
		name         string
		checkScopes  bool
		token        string
		wantStatus   int
		wantConfigID string // empty => expect denial, no config payload
	}{
		{name: "check-scopes on, webhooks:read alone", checkScopes: true, token: "webhooks:read", wantStatus: http.StatusOK, wantConfigID: cfg.ID},
		{name: "check-scopes on, webhooks:read + payments:read (pentest token)", checkScopes: true, token: "payments:read webhooks:read", wantStatus: http.StatusOK, wantConfigID: cfg.ID},
		{name: "check-scopes on, webhooks:write", checkScopes: true, token: "webhooks:write", wantStatus: http.StatusOK, wantConfigID: cfg.ID},
		{name: "check-scopes off (default), any valid token incl. payments:read only", checkScopes: false, token: "payments:read", wantStatus: http.StatusOK, wantConfigID: cfg.ID},
		{name: "check-scopes on, payments:read only is denied", checkScopes: true, token: "payments:read", wantStatus: http.StatusUnauthorized},
		{name: "check-scopes on, no token is denied", checkScopes: true, token: "", wantStatus: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var authenticator auth.Authenticator = fakeScopeAuth{checkScopes: tc.checkScopes, service: "webhooks"}
			if tc.token == "" {
				authenticator = fakeScopeAuth{checkScopes: tc.checkScopes, service: "webhooks"} // header absent -> denied
			}
			h := newSec103TestHandler(newStubStore(cfg), authenticator)

			status, body := doSec103Request(t, h, http.MethodGet, PathConfigs, tc.token)
			if status != tc.wantStatus {
				t.Fatalf("GET /configs status = %d, want %d (body: %.200s)", status, tc.wantStatus, body)
			}
			if tc.wantConfigID != "" {
				assertLooksLikeConfigList(t, body, tc.wantConfigID)
			}
			// The security assertion applies to every response, allowed or denied.
			assertNoSecretInJSON(t, body)
		})
	}
}

// TestSec103ActivateDeactivateMustNotDiscloseSecret covers the sibling read
// paths PUT /configs/{id}/activate and PUT /configs/{id}/deactivate, whose
// responses also serialize the full stored config on base.
func TestSec103ActivateDeactivateMustNotDiscloseSecret(t *testing.T) {
	active := seedConfig("cfg-sec103-active", "https://sec103.invalid/a", true)
	inactive := seedConfig("cfg-sec103-inactive", "https://sec103.invalid/b", false)

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantID     string
	}{
		{name: "deactivate active config", method: http.MethodPut, path: PathConfigs + "/" + active.ID + PathDeactivate, wantStatus: http.StatusOK, wantID: active.ID},
		{name: "activate inactive config", method: http.MethodPut, path: PathConfigs + "/" + inactive.ID + PathActivate, wantStatus: http.StatusOK, wantID: inactive.ID},
		{name: "activate already-active config (not-modified path still encodes body)", method: http.MethodPut, path: PathConfigs + "/" + active.ID + PathActivate, wantStatus: http.StatusOK, wantID: active.ID},
		{name: "deactivate with webhooks:read only is denied", method: http.MethodPut, path: PathConfigs + "/" + active.ID + PathDeactivate, wantStatus: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := "webhooks:write"
			if strings.Contains(tc.name, "read only") {
				token = "webhooks:read"
			}
			h := newSec103TestHandler(newStubStore(active, inactive), fakeScopeAuth{checkScopes: true, service: "webhooks"})

			status, body := doSec103Request(t, h, tc.method, tc.path, token)
			if status != tc.wantStatus {
				t.Fatalf("%s %s status = %d, want %d (body: %.200s)", tc.method, tc.path, status, tc.wantStatus, body)
			}
			if tc.wantID != "" {
				assertLooksLikeConfigList(t, body, tc.wantID)
			}
			assertNoSecretInJSON(t, body)
		})
	}
}

// TestSec103GetConfigsFilterByIDMustNotDiscloseSecret covers the same read
// with the ?id= filter (the postgres FindManyConfigs path Jira cites).
func TestSec103GetConfigsFilterByIDMustNotDiscloseSecret(t *testing.T) {
	cfg := seedConfig("cfg-sec103-2", "https://sec103.invalid/hook2", true)
	h := newSec103TestHandler(newStubStore(cfg), fakeScopeAuth{checkScopes: true, service: "webhooks"})

	status, body := doSec103Request(t, h, http.MethodGet, PathConfigs+"?id="+cfg.ID, "webhooks:read")
	if status != http.StatusOK {
		t.Fatalf("GET /configs?id= status = %d, want 200 (body: %.200s)", status, body)
	}
	assertLooksLikeConfigList(t, body, cfg.ID)
	assertNoSecretInJSON(t, body)
}
