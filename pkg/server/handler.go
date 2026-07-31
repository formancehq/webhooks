package server

import (
	"net/http"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/go-chi/chi/v5"

	"github.com/formancehq/go-libs/v2/service"
	"github.com/formancehq/go-libs/v5/pkg/audit/httpaudit"

	"github.com/formancehq/go-libs/v2/auth"
	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/webhooks/pkg/storage"
)

const (
	PathHealthCheck  = "/_healthcheck"
	PathInfo         = "/_info"
	PathConfigs      = "/configs"
	PathTest         = "/test"
	PathActivate     = "/activate"
	PathDeactivate   = "/deactivate"
	PathChangeSecret = "/secret/change"
	PathDeliveries   = "/deliveries"
	PathAttempts     = "/attempts"
	PathReplay       = "/replay"
	PathId           = "/{" + PathParamId + "}"
	PathParamId      = "id"
)

type serverHandler struct {
	*chi.Mux

	store      storage.Store
	httpClient *http.Client
}

func newServerHandler(
	store storage.Store,
	httpClient *http.Client,
	logger logging.Logger,
	info ServiceInfo,
	authenticator auth.Authenticator,
	publisher message.Publisher,
	debug bool,
	auditEnabled bool,
	deliveriesEnabled bool,
) http.Handler {
	h := &serverHandler{
		Mux:        chi.NewRouter(),
		store:      store,
		httpClient: httpClient,
	}

	if auditEnabled {
		// go-libs/v5 gates publication on its own enabled option, which
		// defaults to false. The middleware is only installed when audit is
		// requested, so opt it in explicitly here.
		h.Use(httpaudit.Middleware(publisher, "audit-events", "webhooks", nil,
			httpaudit.WithEnabled(true),
		))
	}
	h.Use(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			handler.ServeHTTP(w, r)
		})
	})
	h.Use(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(w, r.WithContext(logging.ContextWithLogger(r.Context(), logger)))
		})
	})
	h.Get(PathHealthCheck, h.HealthCheckHandle)
	h.Get(PathInfo, h.getInfo(info))

	h.Group(func(r chi.Router) {
		r.Use(auth.Middleware(authenticator))
		r.Use(service.OTLPMiddleware("webhooks", debug))

		r.Get(PathConfigs, h.getManyConfigsHandle)
		r.Post(PathConfigs, h.insertOneConfigHandle)
		r.Delete(PathConfigs+PathId, h.deleteOneConfigHandle)
		r.Put(PathConfigs+PathId, h.updateOneConfigHandle)
		r.Get(PathConfigs+PathId+PathTest, h.testOneConfigHandle)
		r.Put(PathConfigs+PathId+PathActivate, h.activateOneConfigHandle)
		r.Put(PathConfigs+PathId+PathDeactivate, h.deactivateOneConfigHandle)
		r.Put(PathConfigs+PathId+PathChangeSecret, h.changeSecretHandle)
		if deliveriesEnabled {
			r.Get(PathDeliveries, h.getDeliveriesHandle)
			r.Post(PathDeliveries+PathReplay, h.replayDeliveriesHandle)
			r.Get(PathDeliveries+PathId, h.getDeliveryHandle)
			r.Get(PathDeliveries+PathId+PathAttempts, h.getDeliveryAttemptsHandle)
			r.Post(PathDeliveries+PathId+PathReplay, h.replayDeliveryHandle)
		}
	})

	return h
}
