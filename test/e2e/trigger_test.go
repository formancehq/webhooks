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
		It("persists retries and eventually marks the deliveries as failed", func() {
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

			Eventually(getNumDeliveriesToRetry).WithArguments(db).
				WithTimeout(5 * time.Second).
				Should(BeNumerically(">", 0))

			Eventually(getNumDeliveryAttempts).WithArguments(db).
				WithTimeout(12 * time.Second).
				Should(BeNumerically(">=", 3))

			Eventually(getNumPendingDeliveriesForEndpoint).WithArguments(db, httpServer.URL).
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

			Eventually(getNumEndpointAttemptsByOutcome).WithArguments(db, httpServer.URL, webhooks.OutcomeDeliveryRetryableFailure).
				WithTimeout(5 * time.Second).
				Should(BeNumerically(">", 0))

			recovered.Store(true)

			Eventually(getNumEndpointAttemptsByOutcome).WithArguments(db, httpServer.URL, webhooks.OutcomeDeliverySucceeded).
				WithTimeout(10 * time.Second).
				Should(BeNumerically(">", 0))

			Eventually(getNumPendingDeliveriesForEndpoint).WithArguments(db, httpServer.URL).
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

			Eventually(getNumEndpointAttemptsByOutcome).WithArguments(db, httpServer.URL, webhooks.OutcomeDeliveryPermanentFailure).
				WithTimeout(5 * time.Second).
				Should(BeNumerically(">=", 1))

			// A permanent 4xx must never enter the retry queue
			Consistently(getNumPendingDeliveriesForEndpoint).WithArguments(db, httpServer.URL).
				WithTimeout(3 * time.Second).
				Should(Equal(0))
		})
	})
})

func getNumDeliveriesToRetry(db *bun.DB) (int, error) {
	return db.NewSelect().Model((*webhooks.Delivery)(nil)).
		Where("status = ?", webhooks.StatusDeliveryPending).
		Where("attempt_count > 0").
		Count(logging.TestingContext())
}

func getNumDeliveryAttempts(db *bun.DB) (int, error) {
	return db.NewSelect().Model((*webhooks.DeliveryAttempt)(nil)).Count(logging.TestingContext())
}

func getNumEndpointAttemptsByOutcome(db *bun.DB, endpoint, outcome string) (int, error) {
	count, err := db.NewSelect().Model((*webhooks.DeliveryAttempt)(nil)).
		Where("endpoint = ?", endpoint).
		Where("outcome = ?", outcome).
		Count(logging.TestingContext())
	return count, err
}

func getNumPendingDeliveriesForEndpoint(db *bun.DB, endpoint string) (int, error) {
	count, err := db.NewSelect().Model((*webhooks.Delivery)(nil)).
		Join("JOIN configs AS config ON config.id = delivery.config_id").
		Where("config.endpoint = ?", endpoint).
		Where("delivery.status IN (?)", bun.List([]string{
			webhooks.StatusDeliveryPending,
			webhooks.StatusDeliveryDelivering,
		})).
		Count(logging.TestingContext())
	return count, err
}
