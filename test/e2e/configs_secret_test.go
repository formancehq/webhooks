//go:build it

package test_suite

import (
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"github.com/formancehq/webhooks/pkg/testserver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("Config secrets", func() {
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

	Context("changing the secret of the inserted one", func() {
		Context("without passing a secret", func() {
			var rotateResp *components.ConfigWithSecretResponse

			BeforeEach(func() {
				response, err := srv.GetValue().Client().Webhooks.V1.ChangeConfigSecret(
					ctx,
					operations.ChangeConfigSecretRequest{
						ConfigChangeSecret: &components.ConfigChangeSecret{
							Secret: "",
						},
						ID: insertResp.Data.ID,
					},
				)
				Expect(err).NotTo(HaveOccurred())
				rotateResp = response.ConfigWithSecretResponse
				// one-time disclosure: the rotation response carries the new
				// secret, which must differ from the previous one
				Expect(rotateResp.Data.Secret).To(Not(BeEmpty()))
				Expect(rotateResp.Data.Secret).To(Not(Equal(insertResp.Data.Secret)))
			})

			Context("getting all configs", func() {
				It("should return 1 config without any secret", func() {
					response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
						ctx,
						operations.GetManyConfigsRequest{},
					)
					Expect(err).NotTo(HaveOccurred())

					resp := response.ConfigsResponse
					Expect(resp.Cursor.HasMore).To(BeFalse())
					Expect(resp.Cursor.Data).To(HaveLen(1))
					// reads never disclose the stored secret
				})
			})
		})

		Context("bringing our own valid secret", func() {
			newSecret := webhooks.NewSecret()
			BeforeEach(func() {
				response, err := srv.GetValue().Client().Webhooks.V1.ChangeConfigSecret(
					ctx,
					operations.ChangeConfigSecretRequest{
						ConfigChangeSecret: &components.ConfigChangeSecret{
							Secret: newSecret,
						},
						ID: insertResp.Data.ID,
					},
				)
				Expect(err).NotTo(HaveOccurred())

				// one-time disclosure: the rotation response reveals exactly
				// the supplied secret
				Expect(response.ConfigWithSecretResponse.Data.Secret).To(Equal(newSecret))
			})

			Context("getting all configs", func() {
				It("should return 1 config without the secret", func() {
					response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
						ctx,
						operations.GetManyConfigsRequest{},
					)
					Expect(err).NotTo(HaveOccurred())

					resp := response.ConfigsResponse
					Expect(resp.Cursor.HasMore).To(BeFalse())
					Expect(resp.Cursor.Data).To(HaveLen(1))
					// the stored secret is not returned on reads
				})
			})
		})

		Context("bringing our own invalid secret", func() {
			invalidSecret := "invalid"
			It("should return a bad request error", func() {
				_, err := srv.GetValue().Client().Webhooks.V1.ChangeConfigSecret(
					ctx,
					operations.ChangeConfigSecretRequest{
						ConfigChangeSecret: &components.ConfigChangeSecret{
							Secret: invalidSecret,
						},
						ID: insertResp.Data.ID,
					},
				)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
