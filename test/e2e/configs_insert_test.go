//go:build it

package test_suite

import (
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/sdkerrors"
	"github.com/formancehq/webhooks/pkg/testserver"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("Config insertion", func() {
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
	It("inserting a valid config", func() {
		cfg := components.ConfigUser{
			Endpoint: "https://example.com",
			EventTypes: []string{
				"ledger.committed_transactions",
			},
		}
		response, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
			ctx,
			cfg,
		)
		Expect(err).ToNot(HaveOccurred())

		insertResp := response.ConfigResponse
		Expect(insertResp.Data.Endpoint).To(Equal(cfg.Endpoint))
		Expect(insertResp.Data.EventTypes).To(Equal(cfg.EventTypes))
		Expect(insertResp.Data.Active).To(BeTrue())
		Expect(insertResp.Data.CreatedAt).NotTo(Equal(time.Time{}))
		Expect(insertResp.Data.UpdatedAt).NotTo(Equal(time.Time{}))
		_, err = uuid.Parse(insertResp.Data.ID)
		Expect(err).NotTo(HaveOccurred())
	})

	It("inserting an invalid config without event types", func() {
		cfg := components.ConfigUser{
			Endpoint:   "https://example.com",
			EventTypes: []string{},
		}
		_, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
			ctx,
			cfg,
		)
		Expect(err).To(HaveOccurred())
	})

	It("inserting an invalid config without endpoint", func() {
		cfg := components.ConfigUser{
			Endpoint: "",
			EventTypes: []string{
				"ledger.committed_transactions",
			},
		}
		_, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
			ctx,
			cfg,
		)
		Expect(err).To(HaveOccurred())
		Expect(err.(*sdkerrors.ErrorResponse).ErrorCode).To(Equal(components.ErrorsEnumValidation))
	})

	It("inserting an invalid config with invalid secret", func() {
		secret := "invalid"
		cfg := components.ConfigUser{
			Endpoint: "https://example.com",
			Secret:   &secret,
			EventTypes: []string{
				"ledger.committed_transactions",
			},
		}
		_, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(
			ctx,
			cfg,
		)
		Expect(err).To(HaveOccurred())
	})
})
