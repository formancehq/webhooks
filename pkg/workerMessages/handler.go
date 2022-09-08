package workerMessages

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

const (
	PathHealthCheck = "/_healthcheck"
)

func newWorkerMessagesHandler() http.Handler {
	h := httprouter.New()
	h.GET(PathHealthCheck, healthCheckHandle)

	return h
}
