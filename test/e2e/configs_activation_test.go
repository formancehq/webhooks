//go:build it

package test_suite

import (
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"github.com/formancehq/webhooks/pkg/client/models/sdkerrors"
	"github.com/formancehq/webhooks/pkg/testserver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("Config activation", func() {

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
		secret     = webhooks.NewSecret()
		insertResp *components.ConfigResponse
		ctx        = logging.TestingContext()
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
		Expect(err).NotTo(HaveOccurred())

		insertResp = response.ConfigResponse
	})

	Context("deactivating the inserted one", func() {
		BeforeEach(func() {
			response, err := srv.GetValue().Client().Webhooks.V1.DeactivateConfig(
				ctx,
				operations.DeactivateConfigRequest{
					ID: insertResp.Data.ID,
				},
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(response.ConfigResponse.Data.Active).To(BeFalse())
		})

		Context("getting all configs", func() {
			It("should return 1 deactivated config", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{},
				)
				Expect(err).NotTo(HaveOccurred())

				Expect(response.ConfigsResponse.Cursor.Data).To(HaveLen(1))
				Expect(response.ConfigsResponse.Cursor.Data[0].Active).To(BeFalse())
			})
		})
	})

	Context("deactivating the inserted one, then reactivating it", func() {
		BeforeEach(func() {
			response, err := srv.GetValue().Client().Webhooks.V1.DeactivateConfig(
				ctx,
				operations.DeactivateConfigRequest{
					ID: insertResp.Data.ID,
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.ConfigResponse.Data.Active).To(BeFalse())

			activateConfigResponse, err := srv.GetValue().Client().Webhooks.V1.ActivateConfig(
				ctx,
				operations.ActivateConfigRequest{
					ID: insertResp.Data.ID,
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(activateConfigResponse.ConfigResponse.Data.Active).To(BeTrue())
		})

		Context("getting all configs", func() {
			It("should return 1 activated config", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{},
				)
				Expect(err).NotTo(HaveOccurred())

				Expect(response.ConfigsResponse.Cursor.Data).To(HaveLen(1))
				Expect(response.ConfigsResponse.Cursor.Data[0].Active).To(BeTrue())
			})
		})
	})

	Context("trying to deactivate an unknown ID", func() {
		It("should fail", func() {
			_, err := srv.GetValue().Client().Webhooks.V1.DeactivateConfig(
				ctx,
				operations.DeactivateConfigRequest{
					ID: "unknown",
				},
			)
			Expect(err).To(HaveOccurred())
			Expect(err.(*sdkerrors.ErrorResponse).ErrorCode).To(Equal(components.ErrorsEnumNotFound))
		})
	})
})
