package server

// Local fixtures for the configuration secret-disclosure regressions.
//
// Local-only fakes: an in-memory storage.Store and a scope-checking
// auth.Authenticator. The HTTP router, the real go-libs auth.Middleware and
// the real handler code under test are NOT faked.
//
// fakeSecret is a throwaway local value (base64 of 24 bytes) that satisfies
// webhooks.ConfigUser.Validate. It is never a real credential.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/formancehq/go-libs/v2/auth"
	"github.com/formancehq/go-libs/v2/logging"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/storage"
)

const fakeSecret = "U0VDMTAzRkFLRVNFQ1JFVDAxMjM0NTY3" // base64("SEC103FAKESECRET01234567"), 24 bytes decoded

// stubStore is an in-memory storage.Store. Only the methods reachable from
// the config read/activation handlers are implemented; the rest panic so a
// future change that routes a new call through these tests fails loudly.
type stubStore struct {
	configs []webhooks.Config
}

var _ storage.Store = (*stubStore)(nil)

func newStubStore(cfgs ...webhooks.Config) *stubStore {
	return &stubStore{configs: cfgs}
}

func seedConfig(id, endpoint string, active bool) webhooks.Config {
	now := time.Now().UTC()
	return webhooks.Config{
		ConfigUser: webhooks.ConfigUser{
			Endpoint:   endpoint,
			Secret:     fakeSecret,
			EventTypes: []string{"ledger.committed_transactions"},
		},
		ID:        id,
		Active:    active,
		Name:      "sec103-" + id,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (s *stubStore) FindManyConfigs(_ context.Context, filter map[string]any) ([]webhooks.Config, error) {
	res := []webhooks.Config{}
	for _, c := range s.configs {
		if c.DeletedAt != nil {
			continue
		}
		if id, ok := filter["id"]; ok && c.ID != id {
			continue
		}
		if ep, ok := filter["endpoint"]; ok && c.Endpoint != ep {
			continue
		}
		res = append(res, c)
	}
	return res, nil
}

func (s *stubStore) UpdateOneConfigActivation(_ context.Context, id string, active bool) (webhooks.Config, error) {
	for i, c := range s.configs {
		if c.ID == id && c.DeletedAt == nil {
			if c.Active == active {
				return c, storage.ErrConfigNotModified
			}
			s.configs[i].Active = active
			s.configs[i].UpdatedAt = time.Now().UTC()
			c.Active = active
			return c, nil
		}
	}
	return webhooks.Config{}, storage.ErrConfigNotFound
}

func (s *stubStore) Close(context.Context) error { return nil }

func (s *stubStore) InsertOneConfig(context.Context, webhooks.ConfigUser) (webhooks.Config, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) DeleteOneConfig(context.Context, string) error {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) UpdateOneConfigSecret(context.Context, string, string) (webhooks.Config, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) UpdateOneConfig(context.Context, string, webhooks.ConfigUser) error {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) EnqueueEvent(context.Context, string, string, string, string, time.Time) error {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) ClaimDeliveries(context.Context, int) ([]webhooks.Delivery, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) CompleteDelivery(context.Context, webhooks.Delivery, webhooks.DeliveryAttempt) (string, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) FailClaimedDelivery(context.Context, string, time.Time, string) error {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) CancelDelivery(context.Context, string) error {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) RecoverStaleDeliveries(context.Context, time.Duration) (int64, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) CountPendingDeliveries(context.Context) (int64, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) FindDeliveries(context.Context, webhooks.DeliveryFilter) (webhooks.DeliveryPage, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) GetDelivery(context.Context, string) (webhooks.Delivery, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) FindDeliveryAttempts(context.Context, string, *webhooks.DeliveryCursor, int) ([]webhooks.DeliveryAttempt, *webhooks.DeliveryCursor, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) ReplayDelivery(context.Context, string, string) (webhooks.Delivery, bool, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) ReplayDeliveries(context.Context, webhooks.ReplayDeliveriesRequest, string) (webhooks.ReplayDeliveriesResult, bool, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) PurgeFinishedDeliveries(context.Context, time.Duration, time.Duration, int) (int64, error) {
	panic("not used by SEC-103 tests")
}
func (s *stubStore) BackfillDeliveries(context.Context, time.Duration, time.Duration, int) (int64, error) {
	panic("not used by SEC-103 tests")
}

// fakeScopeAuth is a local auth.Authenticator whose "token" is the literal
// space-separated scope list in the Authorization header (e.g.
// "Bearer webhooks:read payments:read"). It reproduces the scope rule of
// go-libs v2.2.5 auth/auth.go (checkScopes branch): read methods need
// <service>:read OR <service>:write, other methods need <service>:write.
// It is plugged into the REAL auth.Middleware used by the server router.
type fakeScopeAuth struct {
	checkScopes bool
	service     string
}

var _ auth.Authenticator = fakeScopeAuth{}

func (a fakeScopeAuth) Authenticate(_ http.ResponseWriter, r *http.Request) (bool, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false, fmt.Errorf("no authorization header")
	}
	scopes := strings.Fields(strings.TrimPrefix(header, "Bearer "))
	if !a.checkScopes {
		return true, nil
	}
	has := func(want string) bool {
		for _, s := range scopes {
			if s == want {
				return true
			}
		}
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		if has(a.service+":read") || has(a.service+":write") {
			return true, nil
		}
		return false, fmt.Errorf("missing access, found scopes: '%s' need %s:read|write", strings.Join(scopes, ", "), a.service)
	default:
		if has(a.service+":write") {
			return true, nil
		}
		return false, fmt.Errorf("missing access, found scopes: '%s' need %s:write", strings.Join(scopes, ", "), a.service)
	}
}

// newSec103TestHandler builds the real server handler (real chi router, real
// auth.Middleware, real OTLP middleware) over the given store/authenticator.
func newSec103TestHandler(store storage.Store, authenticator auth.Authenticator) http.Handler {
	return newServerHandler(
		store,
		&http.Client{},
		logging.Testing(),
		ServiceInfo{Version: "sec103-repro"},
		authenticator,
		nil, // publisher: audit middleware is disabled
		false,
		false,
	)
}
