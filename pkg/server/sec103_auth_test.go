package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/formancehq/go-libs/v2/auth"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

func TestSec103RealAuthenticatorScopes(t *testing.T) {
	// This issuer, its signing key and every token are isolated local fixtures.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	encode := base64.RawURLEncoding.EncodeToString
	var issuerURL string
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var document any
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			document = map[string]any{"issuer": issuerURL, "jwks_uri": issuerURL + "/keys"}
		case "/keys":
			document = map[string]any{"keys": []map[string]any{{
				"kty": "RSA", "kid": "local-fixture", "use": "sig", "alg": "RS256",
				"n": encode(key.N.Bytes()), "e": encode(big.NewInt(int64(key.E)).Bytes()),
			}}}
		default:
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(document); err != nil {
			t.Error(err)
		}
	}))
	issuerURL = issuer.URL
	t.Cleanup(issuer.Close)
	tokenForScopes := func(scopes string) string {
		t.Helper()
		header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": "local-fixture"})
		require.NoError(t, err)
		claims, err := json.Marshal(map[string]any{
			"iss": issuer.URL, "sub": "local-test", "scope": scopes,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		require.NoError(t, err)
		unsigned := encode(header) + "." + encode(claims)
		digest := sha256.Sum256([]byte(unsigned))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		require.NoError(t, err)
		return unsigned + "." + encode(signature)
	}
	cfg := seedConfig("cfg-local-auth", "https://sec103.invalid/auth", true)
	for _, checkScopes := range []bool{true, false} {
		var authenticator auth.Authenticator
		app := fx.New(fx.NopLogger,
			fx.Provide(func() logging.Logger { return logging.Testing() }),
			auth.Module(auth.ModuleConfig{Enabled: true, Issuers: []string{issuer.URL}, Service: "webhooks", CheckScopes: checkScopes}),
			fx.Populate(&authenticator))
		require.NoError(t, app.Err())
		handler := newSec103TestHandler(newStubStore(cfg), authenticator)
		for _, scopes := range []string{"webhooks:read", "webhooks:read payments:read", "payments:read"} {
			status, body := doSec103Request(t, handler, http.MethodGet, PathConfigs, tokenForScopes(scopes))
			want := http.StatusOK
			if checkScopes && scopes == "payments:read" {
				want = http.StatusUnauthorized
			}
			require.Equal(t, want, status, "scopes=%s, checking=%t", scopes, checkScopes)
			assertNoSecretInJSON(t, body)
			if status == http.StatusOK {
				assertLooksLikeConfigList(t, body, cfg.ID)
			}
		}
	}
}
