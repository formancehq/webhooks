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

var _ = Context("Config deletion", func() {
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

	Context("deleting the inserted one", func() {
		BeforeEach(func() {
			_, err := srv.GetValue().Client().Webhooks.V1.DeleteConfig(
				ctx,
				operations.DeleteConfigRequest{
					ID: insertResp.Data.ID,
				},
			)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("getting all configs", func() {
			It("should return 0 config", func() {
				response, err := srv.GetValue().Client().Webhooks.V1.GetManyConfigs(
					ctx,
					operations.GetManyConfigsRequest{},
				)
				Expect(err).NotTo(HaveOccurred())

				Expect(response.ConfigsResponse.Cursor.HasMore).To(BeFalse())
				Expect(response.ConfigsResponse.Cursor.Data).To(BeEmpty())
			})
		})

		AfterEach(func() {
			_, err := srv.GetValue().Client().Webhooks.V1.DeleteConfig(
				ctx,
				operations.DeleteConfigRequest{
					ID: insertResp.Data.ID,
				},
			)
			Expect(err).To(HaveOccurred())
			Expect(err.(*sdkerrors.ErrorResponse).ErrorCode).To(Equal(components.ErrorsEnumNotFound))
		})
	})

	Context("trying to delete an unknown ID", func() {
		It("should fail", func() {
			_, err := srv.GetValue().Client().Webhooks.V1.DeleteConfig(
				ctx,
				operations.DeleteConfigRequest{
					ID: "unknown",
				},
			)
			Expect(err).To(HaveOccurred())
			Expect(err.(*sdkerrors.ErrorResponse).ErrorCode).To(Equal(components.ErrorsEnumNotFound))
		})
	})
})
