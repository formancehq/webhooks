//go:build it

package test_suite

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"

	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"github.com/formancehq/webhooks/pkg/security"
	"github.com/formancehq/webhooks/pkg/testserver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("Config updates", func() {
	var (
		db  = pgtesting.UsePostgresDatabase(pgServer)
		srv = testserver.NewTestServer(func() testserver.Configuration {
			return testserver.Configuration{
				Postgres: db.GetValue().ConnectionOptions(),
				Topics:   []string{},
				Debug:    debug,
				Output:   GinkgoWriter,
				NatsURL:  natsServer.GetValue().URL,
			}
		})
		ctx        = logging.TestingContext()
		secret     = webhooks.NewSecret()
		insertResp *components.ConfigWithSecretResponse
	)

	BeforeEach(func() {
		cfg := components.ConfigUser{
			Endpoint: "https://example.com",
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

		insertResp = response.ConfigWithSecretResponse
	})

	Context("update the inserted one", func() {
		It("should be ok", func() {
			_, err := srv.GetValue().Client().Webhooks.V1.UpdateConfig(
				ctx,
				operations.UpdateConfigRequest{
					ID: insertResp.Data.ID,
					ConfigUser: components.ConfigUser{
						Endpoint: "https://example2.com",
						Secret:   &secret,
						EventTypes: []string{
							"ledger.committed_transactions",
						},
					},
				},
			)
			Expect(err).NotTo(HaveOccurred())

			response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
				ctx,
				operations.GetManyConfigsRequest{},
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(response.ConfigsResponse.Cursor.HasMore).To(BeFalse())
			Expect(response.ConfigsResponse.Cursor.Data).To(HaveLen(1))
			Expect(response.ConfigsResponse.Cursor.Data[0].Endpoint).To(Equal("https://example2.com"))
		})

		It("should preserve the stored secret when the update omits it", func() {
			// update with no secret in the body, then point the config at a
			// local endpoint that verifies the signature with the original
			// secret: if the secret had been rotated, verification fails
			httpServer := httptest.NewServer(http.HandlerFunc(
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
					if err != nil || !ok {
						http.Error(w, "signature verification failed", http.StatusBadRequest)
						return
					}
				}))
			defer httpServer.Close()

			_, err := srv.GetValue().Client().Webhooks.V1.UpdateConfig(
				ctx,
				operations.UpdateConfigRequest{
					ID: insertResp.Data.ID,
					ConfigUser: components.ConfigUser{
						Endpoint: httpServer.URL,
						EventTypes: []string{
							"ledger.committed_transactions",
						},
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())

			response, err := srv.GetValue().Client().Webhooks.V1.TestConfig(
				ctx,
				operations.TestConfigRequest{ID: insertResp.Data.ID},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.AttemptResponse.Data.Status).To(Equal("success"))
		})

		It("should reject an invalid replacement secret", func() {
			invalid := "invalid"
			_, err := srv.GetValue().Client().Webhooks.V1.UpdateConfig(
				ctx,
				operations.UpdateConfigRequest{
					ID: insertResp.Data.ID,
					ConfigUser: components.ConfigUser{
						Endpoint: "https://example2.com",
						Secret:   &invalid,
						EventTypes: []string{
							"ledger.committed_transactions",
						},
					},
				},
			)
			Expect(err).To(HaveOccurred())
		})
	})
})
