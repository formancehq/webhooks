//go:build it

package test_suite

import (
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"github.com/formancehq/webhooks/pkg/client/models/sdkerrors"
	"github.com/formancehq/webhooks/pkg/testserver"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"

	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/security"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("Config tests", func() {
	var (
		db  = pgtesting.UsePostgresDatabase(pgServer)
		ctx = logging.TestingContext()
		srv = testserver.NewTestServer(func() testserver.Configuration {
			return testserver.Configuration{
				Postgres: db.GetValue().ConnectionOptions(),
				Topics:   []string{},
				Debug:    debug,
				Output:   GinkgoWriter,
				NatsURL:  natsServer.GetValue().URL,
			}
		})
	)
	When("testing configs", func() {
		Context("inserting a config with an endpoint to a success handler", func() {
			var (
				httpServer *httptest.Server
				insertResp *components.ConfigResponse
				secret     = webhooks.NewSecret()
			)

			BeforeEach(func() {
				httpServer = httptest.NewServer(http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						id := r.Header.Get("formance-webhook-id")
						ts := r.Header.Get("formance-webhook-timestamp")
						signatures := r.Header.Get("formance-webhook-signature")
						timeInt, err := strconv.ParseInt(ts, 10, 64)
						if err != nil {
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						}

						payload, err := io.ReadAll(r.Body)
						if err != nil {
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						}

						ok, err := security.Verify(signatures, id, timeInt, secret, payload)
						if err != nil {
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						}
						if !ok {
							http.Error(w, "WEBHOOKS SIGNATURE VERIFICATION NOK", http.StatusBadRequest)
							return
						}
					}))

				cfg := components.ConfigUser{
					Endpoint: httpServer.URL,
					Secret:   &secret,
					EventTypes: []string{
						"ledger.committed_transactions",
					},
				}
				response, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
					ctx,
					cfg,
				)
				Expect(err).ToNot(HaveOccurred())
				insertResp = response.ConfigResponse
				DeferCleanup(func() {
					httpServer.Close()
				})
			})

			Context("testing the inserted one", func() {
				It("should return a successful attempt", func() {
					response, err := srv.GetValue().Client().Webhooks.V1.TestConfig(
						ctx,
						operations.TestConfigRequest{
							ID: insertResp.Data.ID,
						},
					)
					Expect(err).ToNot(HaveOccurred())

					attemptResp := response.AttemptResponse
					Expect(attemptResp.Data.Config.ID).To(Equal(insertResp.Data.ID))
					Expect(attemptResp.Data.Payload).To(Equal(`{"data":"test"}`))
					Expect(int(attemptResp.Data.StatusCode)).To(Equal(http.StatusOK))
					Expect(attemptResp.Data.Status).To(Equal("success"))
				})
			})
		})

		Context("inserting a config with an endpoint to a fail handler", func() {
			var insertResp *components.ConfigResponse

			BeforeEach(func() {
				httpServer := httptest.NewServer(http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) {
						http.Error(w,
							"WEBHOOKS RECEIVED: MOCK ERROR RESPONSE", http.StatusNotFound)
					}))

				cfg := components.ConfigUser{
					Endpoint: httpServer.URL,
					EventTypes: []string{
						"ledger.committed_transactions",
					},
				}
				response, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
					ctx,
					cfg,
				)
				Expect(err).ToNot(HaveOccurred())
				insertResp = response.ConfigResponse
				DeferCleanup(func() {
					httpServer.Close()
				})
			})

			Context("testing the inserted one", func() {
				It("should return a failed attempt", func() {
					response, err := srv.GetValue().Client().Webhooks.V1.TestConfig(
						ctx,
						operations.TestConfigRequest{
							ID: insertResp.Data.ID,
						},
					)
					Expect(err).ToNot(HaveOccurred())

					attemptResp := response.AttemptResponse
					Expect(attemptResp.Data.Config.ID).To(Equal(insertResp.Data.ID))
					Expect(attemptResp.Data.Payload).To(Equal(`{"data":"test"}`))
					Expect(int(attemptResp.Data.StatusCode)).To(Equal(http.StatusNotFound))
					Expect(attemptResp.Data.Status).To(Equal("failed"))
				})
			})
		})

		Context("testing an unknown ID", func() {
			It("should fail", func() {
				_, err := srv.GetValue().Client().Webhooks.V1.TestConfig(
					ctx,
					operations.TestConfigRequest{
						ID: "unknown",
					},
				)
				Expect(err).To(HaveOccurred())
				Expect(err.(*sdkerrors.ErrorResponse).ErrorCode).To(Equal(components.ErrorsEnumNotFound))
			})
		})
	})
})
