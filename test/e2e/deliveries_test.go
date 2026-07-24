//go:build it

package test_suite

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/go-libs/v2/testing/platform/pgtesting"
	"github.com/formancehq/webhooks/pkg/client/models/components"
	"github.com/formancehq/webhooks/pkg/client/models/operations"
	"github.com/formancehq/webhooks/pkg/testserver"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Context("Durable deliveries", func() {
	var (
		db  = pgtesting.UsePostgresDatabase(pgServer)
		ctx = logging.TestingContext()
		srv = testserver.NewTestServer(func() testserver.Configuration {
			return testserver.Configuration{
				Postgres: db.GetValue().ConnectionOptions(), Topics: []string{"durable"},
				Debug: debug, Output: GinkgoWriter, NatsURL: natsServer.GetValue().URL,
				RetryPeriod: 100 * time.Millisecond, MinBackoffDelay: 100 * time.Millisecond,
				AbortAfter: 3 * time.Second, DeliveryPipeline: true,
			}
		})
	)

	It("records a terminal 404 and successfully replays it after the endpoint recovers", func() {
		var recovered atomic.Bool
		var calls atomic.Int32
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			if !recovered.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		DeferCleanup(endpoint.Close)

		inserted, err := srv.GetValue().Client().Webhooks.V1.InsertConfig(ctx, components.ConfigUser{
			Endpoint: endpoint.URL, EventTypes: []string{"durable"},
		})
		Expect(err).ToNot(HaveOccurred())
		configID := inserted.ConfigResponse.Data.ID
		Expect(natsServer.GetValue().Client(GinkgoT()).Publish("durable", []byte(`{"type":"durable","idempotency_key":"durable-event"}`))).To(Succeed())

		var delivery components.Delivery
		Eventually(func(g Gomega) {
			response, err := srv.GetValue().Client().Webhooks.V1.GetDeliveries(ctx, operations.GetDeliveriesRequest{
				ConfigID: &configID, Status: components.DeliveryStatusFailed.ToPointer(),
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.DeliveriesResponse.Cursor.Data).To(HaveLen(1))
			delivery = response.DeliveriesResponse.Cursor.Data[0]
			g.Expect(delivery.Payload).To(BeNil(), "list responses must not expose payloads")
		}).WithTimeout(5 * time.Second).Should(Succeed())

		detail, err := srv.GetValue().Client().Webhooks.V1.GetDelivery(ctx, operations.GetDeliveryRequest{ID: delivery.ID})
		Expect(err).ToNot(HaveOccurred())
		Expect(detail.DeliveryResponse.Data.Payload).ToNot(BeNil())
		Expect(*detail.DeliveryResponse.Data.Payload).To(ContainSubstring("durable-event"))

		recovered.Store(true)
		_, err = srv.GetValue().Client().Webhooks.V1.ReplayDelivery(ctx, operations.ReplayDeliveryRequest{
			ID: delivery.ID, IdempotencyKey: uuid.NewString(),
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			response, err := srv.GetValue().Client().Webhooks.V1.GetDelivery(ctx, operations.GetDeliveryRequest{ID: delivery.ID})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.DeliveryResponse.Data.Status).To(Equal(components.DeliveryStatusSucceeded))
			g.Expect(response.DeliveryResponse.Data.ReplayGeneration).To(Equal(int64(1)))
			g.Expect(response.DeliveryResponse.Data.AttemptCount).To(Equal(int64(1)))
		}).WithTimeout(5 * time.Second).Should(Succeed())
		Expect(calls.Load()).To(Equal(int32(2)))
	})
})
