package retries

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
	"go.formance.com/webhooks/pkg/healthcheck"
)

const (
	PathHealthCheck = "/_healthcheck"
)

func newWorkerRetriesHandler() http.Handler {
	h := httprouter.New()
	h.GET(PathHealthCheck, healthcheck.Handle)

	return h
}
