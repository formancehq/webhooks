package server

import (
	"encoding/json"
	"net/http"

	"github.com/formancehq/go-libs/v2/api"
	"github.com/formancehq/go-libs/v2/logging"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/server/apierrors"
	"github.com/pkg/errors"
)

func (h *serverHandler) insertOneConfigHandle(w http.ResponseWriter, r *http.Request) {
	cfg := webhooks.ConfigUser{}
	if err := decodeJSONBody(r, &cfg, false); err != nil {
		logging.FromContext(r.Context()).Errorf("decodeJSONBody: %s", err)
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}

	if err := cfg.Validate(); err != nil {
		err := errors.Wrap(err, "invalid config")
		logging.FromContext(r.Context()).Errorf(err.Error())
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}

	c, err := h.store.InsertOneConfig(r.Context(), cfg)
	if err == nil {
		logging.FromContext(r.Context()).Debugf("POST %s: inserted id %s", PathConfigs, c.ID)
		resp := api.BaseResponse[webhooks.Config]{
			Data: &c,
		}

		if err := json.NewEncoder(w).Encode(resp); err != nil {
			logging.FromContext(r.Context()).Errorf("json.Encoder.Encode: %s", err)
			apierrors.ResponseError(w, r, err)
			return
		}
	} else {
		logging.FromContext(r.Context()).Errorf("POST %s: %s", PathConfigs, err)
		apierrors.ResponseError(w, r, err)
	}
}
