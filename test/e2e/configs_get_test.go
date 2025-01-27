//go:build it

package test_suite

import (
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/pointer"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"github.com/formancehq/webhooks/pkg/testserver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("Config get", func() {
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
	It("should return 0 config", func() {
		response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
			ctx,
			operations.GetManyConfigsRequest{},
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(response.ConfigsResponse.Cursor.HasMore).To(BeFalse())
		Expect(response.ConfigsResponse.Cursor.Data).To(BeEmpty())
	})

	When("inserting 2 configs", func() {
		var (
			insertResp1 *components.ConfigResponse
			insertResp2 *components.ConfigResponse
		)

		BeforeEach(func() {
			var (
				err  error
				cfg1 = components.ConfigUser{
					Endpoint: "https://example1.com",
					EventTypes: []string{
						"ledger.committed_transactions",
					},
				}
				cfg2 = components.ConfigUser{
					Endpoint: "https://example2.com",
					EventTypes: []string{
						"ledger.saved_metadata",
					},
				}
			)

			response, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
				ctx,
				cfg1,
			)
			Expect(err).ToNot(HaveOccurred())
			insertResp1 = response.ConfigResponse

			response, err = srv.GetValue().Client().Webhooks.V1.InsertConfig(
				ctx,
				cfg2,
			)
			Expect(err).ToNot(HaveOccurred())
			insertResp2 = response.ConfigResponse
		})

		Context("getting all configs without filters", func() {
			It("should return 2 configs", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{},
				)
				Expect(err).NotTo(HaveOccurred())

				resp := response.ConfigsResponse
				Expect(resp.Cursor.HasMore).To(BeFalse())
				Expect(resp.Cursor.Data).To(HaveLen(2))
				Expect(resp.Cursor.Data[0].Endpoint).To(Equal(insertResp2.Data.Endpoint))
				Expect(resp.Cursor.Data[1].Endpoint).To(Equal(insertResp1.Data.Endpoint))
			})
		})

		Context("getting all configs with known endpoint filter", func() {
			It("should return 1 config with the same endpoint", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{
						Endpoint: pointer.For(insertResp1.Data.Endpoint),
					},
				)
				Expect(err).NotTo(HaveOccurred())

				resp := response.ConfigsResponse
				Expect(resp.Cursor.HasMore).To(BeFalse())
				Expect(resp.Cursor.Data).To(HaveLen(1))
				Expect(resp.Cursor.Data[0].Endpoint).To(Equal(insertResp1.Data.Endpoint))
			})
		})

		Context("getting all configs with unknown endpoint filter", func() {
			It("should return 0 config", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{
						Endpoint: pointer.For("https://unknown.com"),
					},
				)
				Expect(err).NotTo(HaveOccurred())

				resp := response.ConfigsResponse
				Expect(resp.Cursor.HasMore).To(BeFalse())
				Expect(resp.Cursor.Data).To(BeEmpty())
			})
		})

		Context("getting all configs with known ID filter", func() {
			It("should return 1 config with the same ID", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{
						ID: pointer.For(insertResp1.Data.ID),
					},
				)
				Expect(err).NotTo(HaveOccurred())

				resp := response.ConfigsResponse
				Expect(resp.Cursor.HasMore).To(BeFalse())
				Expect(resp.Cursor.Data).To(HaveLen(1))
				Expect(resp.Cursor.Data[0].ID).To(Equal(insertResp1.Data.ID))
			})
		})

		Context("getting all configs with unknown ID filter", func() {
			It("should return 0 config", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{
						ID: pointer.For("unknown"),
					},
				)
				Expect(err).NotTo(HaveOccurred())

				resp := response.ConfigsResponse
				Expect(resp.Cursor.HasMore).To(BeFalse())
				Expect(resp.Cursor.Data).To(BeEmpty())
			})
		})
	})
})
