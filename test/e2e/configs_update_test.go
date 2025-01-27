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
		insertResp *components.ConfigResponse
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

		insertResp = response.ConfigResponse
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
	})
})
