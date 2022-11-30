package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/formancehq/go-libs/sharedapi"
	"github.com/formancehq/go-libs/sharedlogging"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *serverHandler) testOneConfigHandle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, PathParamId)
	cur, err := findConfig(r.Context(), h.store, id)
	if err == nil {
		sharedlogging.GetLogger(r.Context()).Infof("GET %s/%s%s", PathConfigs, id, PathTest)
		attempt, err := webhooks.MakeAttempt(r.Context(), h.httpClient, nil,
			uuid.NewString(), 0, cur.Data[0], []byte(`{"data":"test"}`))
		if err != nil {
			sharedlogging.GetLogger(r.Context()).Errorf("GET %s/%s%s: %s", PathConfigs, id, PathTest, err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		} else {
			sharedlogging.GetLogger(r.Context()).Infof("GET %s/%s%s", PathConfigs, id, PathTest)
			resp := sharedapi.BaseResponse[webhooks.Attempt]{
				Data: &attempt,
			}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				sharedlogging.GetLogger(r.Context()).Errorf("json.Encoder.Encode: %s", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
		}
	} else if errors.Is(err, ErrConfigNotFound) {
		sharedlogging.GetLogger(r.Context()).Infof("GET %s/%s%s: %s", PathConfigs, id, PathTest, ErrConfigNotFound)
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
	} else {
		sharedlogging.GetLogger(r.Context()).Errorf("GET %s/%s%s: %s", PathConfigs, id, PathTest, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
