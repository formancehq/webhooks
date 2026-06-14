//go:build it

package test_suite

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/testserver"

	webhooks "github.com/formancehq/webhooks/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/uptrace/bun"
)

var _ = Context("Retries", func() {
	var (
		db  = pgtesting.UsePostgresDatabase(pgServer)
		ctx = logging.TestingContext()
		srv = testserver.NewTestServer(func() testserver.Configuration {
			return testserver.Configuration{
				Postgres: db.GetValue().ConnectionOptions(),
				Topics: []string{
					"foo",
				},
				Debug:           debug,
				Output:          GinkgoWriter,
				NatsURL:         natsServer.GetValue().URL,
				RetryPeriod:     time.Second,
				MinBackoffDelay: time.Second,
				AbortAfter:      3 * time.Second,
			}
		})
	)
	Context("the endpoint only returning transient errors", func() {
		var httpServer *httptest.Server
		BeforeEach(func() {
			httpServer = httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "error", http.StatusInternalServerError)
				}))
			DeferCleanup(httpServer.Close)

			cfg := components.ConfigUser{
				Endpoint: httpServer.URL,
				EventTypes: []string{
					"foo",
				},
			}
			_, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
				ctx,
				cfg,
			)
			Expect(err).To(BeNil())
		})
		It("with an exponential backoff, 3 attempts have to be made and all should have a failed status", func() {
			_, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
				ctx,
				components.ConfigUser{
					Endpoint: httpServer.URL,
					EventTypes: []string{
						"foo",
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())

			db, err := bunconnect.OpenSQLDB(logging.TestingContext(), db.GetValue().ConnectionOptions())
			Expect(err).ToNot(HaveOccurred())

			err = natsServer.
				GetValue().
				Client(GinkgoT()).
				Publish("foo", []byte(`{"type":"foo"}`))
			Expect(err).To(BeNil())

			Eventually(getNumAttemptsToRetry).WithArguments(db).
				WithTimeout(5 * time.Second).
				Should(BeNumerically(">", 0))

			Eventually(getNumFailedAttempts).WithArguments(db).
				WithTimeout(12 * time.Second).
				Should(BeNumerically(">=", 3))

			Eventually(getNumPendingRetryAttempts).WithArguments(db).
				WithTimeout(10 * time.Second).
				Should(Equal(0))
		})
	})
	Context("the endpoint recovering after a transient error", func() {
		var (
			httpServer *httptest.Server
			recovered  atomic.Bool
			calls      atomic.Int32
		)
		BeforeEach(func() {
			recovered.Store(false)
			calls.Store(0)
			httpServer = httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					if !recovered.Load() {
						http.Error(w, "temporary error", http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusOK)
				}))
			DeferCleanup(httpServer.Close)

			cfg := components.ConfigUser{
				Endpoint: httpServer.URL,
				EventTypes: []string{
					"foo",
				},
			}
			_, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
				ctx,
				cfg,
			)
			Expect(err).To(BeNil())
		})
		It("should deliver queued messages once the endpoint returns 200", func() {
			db, err := bunconnect.OpenSQLDB(logging.TestingContext(), db.GetValue().ConnectionOptions())
			Expect(err).ToNot(HaveOccurred())

			err = natsServer.
				GetValue().
				Client(GinkgoT()).
				Publish("foo", []byte(`{"type":"foo"}`))
			Expect(err).To(BeNil())

			Eventually(getNumEndpointAttemptsByStatus).WithArguments(db, httpServer.URL, webhooks.StatusAttemptToRetry).
				WithTimeout(5 * time.Second).
				Should(BeNumerically(">", 0))

			recovered.Store(true)

			Eventually(getNumEndpointAttemptsByStatus).WithArguments(db, httpServer.URL, webhooks.StatusAttemptSuccess).
				WithTimeout(10 * time.Second).
				Should(BeNumerically(">", 0))

			Eventually(getNumPendingRetryAttemptsForEndpoint).WithArguments(db, httpServer.URL).
				WithTimeout(5 * time.Second).
				Should(Equal(0))

			Eventually(func() int32 {
				return calls.Load()
			}).WithTimeout(5 * time.Second).Should(BeNumerically(">=", 2))
		})
	})
	Context("the endpoint returning a permanent client error", func() {
		var httpServer *httptest.Server
		BeforeEach(func() {
			httpServer = httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "error", http.StatusNotFound)
				}))
			DeferCleanup(httpServer.Close)

			cfg := components.ConfigUser{
				Endpoint: httpServer.URL,
				EventTypes: []string{
					"foo",
				},
			}
			_, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
				ctx,
				cfg,
			)
			Expect(err).To(BeNil())
		})
		It("should fail immediately without scheduling any retry", func() {
			db, err := bunconnect.OpenSQLDB(logging.TestingContext(), db.GetValue().ConnectionOptions())
			Expect(err).ToNot(HaveOccurred())

			err = natsServer.
				GetValue().
				Client(GinkgoT()).
				Publish("foo", []byte(`{"type":"foo"}`))
			Expect(err).To(BeNil())

			Eventually(getNumFailedAttempts).WithArguments(db).
				WithTimeout(5 * time.Second).
				Should(BeNumerically(">=", 1))

			// A permanent 4xx must never enter the retry queue
			Consistently(getNumPendingRetryAttempts).WithArguments(db).
				WithTimeout(3 * time.Second).
				Should(Equal(0))
		})
	})
})

func getNumAttemptsToRetry(db *bun.DB) (int, error) {
	var results []webhooks.Attempt
	err := db.NewSelect().Model(&results).
		Where("status = ?", "to retry").
		Scan(logging.TestingContext())
	if err != nil {
		return 0, err
	}
	return len(results), nil
}

func getNumFailedAttempts(db *bun.DB) (int, error) {
	var results []webhooks.Attempt
	err := db.NewSelect().Model(&results).
		Where("status = ?", "failed").
		Scan(logging.TestingContext())
	if err != nil {
		return 0, err
	}

	return len(results), nil
}

func getNumEndpointAttemptsByStatus(db *bun.DB, endpoint, status string) (int, error) {
	count, err := db.NewSelect().Model((*webhooks.Attempt)(nil)).
		Where("config->>'endpoint' = ?", endpoint).
		Where("status = ?", status).
		Count(logging.TestingContext())
	return count, err
}

func getNumPendingRetryAttempts(db *bun.DB) (int, error) {
	var results []webhooks.Attempt
	err := db.NewSelect().Model(&results).
		Where("status IN (?)", bun.List([]string{
			webhooks.StatusAttemptToRetry,
			webhooks.StatusAttemptRetrying,
		})).
		Scan(logging.TestingContext())
	if err != nil {
		return 0, err
	}

	return len(results), nil
}

func getNumPendingRetryAttemptsForEndpoint(db *bun.DB, endpoint string) (int, error) {
	count, err := db.NewSelect().Model((*webhooks.Attempt)(nil)).
		Where("config->>'endpoint' = ?", endpoint).
		Where("status IN (?)", bun.List([]string{
			webhooks.StatusAttemptToRetry,
			webhooks.StatusAttemptRetrying,
		})).
		Count(logging.TestingContext())
	return count, err
}
