package test_test

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/formancehq/webhooks/pkg/security"
)

func webhooksSuccessHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("formance-webhook-id")
	ts := r.Header.Get("formance-webhook-timestamp")
	signatures := r.Header.Get("formance-webhook-signature")
	timeInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	timestamp := time.Unix(timeInt, 0)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ok, err := security.Verify(signatures, id, timestamp, secret, payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	_, _ = fmt.Fprintf(w, "WEBHOOK RECEIVED: MOCK OK RESPONSE\n")
	return
}

func webhooksFailHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "WEBHOOKS RECEIVED: MOCK ERROR RESPONSE", http.StatusNotFound)
	return
}
