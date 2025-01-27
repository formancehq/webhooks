//go:build it

package test_suite

import (
	"github.com/davecgh/go-spew/spew"
	"github.com/formancehq/go-libs/v2/bun/bunconnect"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/testserver"
	"net/http"
	"net/http/httptest"
	"time"

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
	Context("the endpoint only returning errors", func() {
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
				WithTimeout(5 * time.Second).
				Should(BeNumerically(">=", 3))

			<-time.After(2 * time.Second)
			toRetry, err := getNumAttemptsToRetry(db)
			Expect(err).ToNot(HaveOccurred())
			Expect(toRetry).To(Equal(0))
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
	spew.Dump(results)
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
