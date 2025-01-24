package server

import (
	"github.com/formancehq/go-libs/logging"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/server/apierrors"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/go-chi/chi/v5"
	"github.com/pkg/errors"
	"net/http"
)

func (h *serverHandler) updateOneConfigHandle(w http.ResponseWriter, r *http.Request) {

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

	id := chi.URLParam(r, PathParamId)

	err := h.store.UpdateOneConfig(r.Context(), id, cfg)
	if err == nil {
		logging.FromContext(r.Context()).Debugf("PUT %s/%s", PathConfigs, id)
	} else if errors.Is(err, storage.ErrConfigNotFound) {
		logging.FromContext(r.Context()).Debugf("PUT %s/%s: %s", PathConfigs, id, storage.ErrConfigNotFound)
		apierrors.ResponseError(w, r, apierrors.NewNotFoundError(storage.ErrConfigNotFound.Error()))
	} else {
		logging.FromContext(r.Context()).Errorf("PUT %s/%s: %s", PathConfigs, id, err)
		apierrors.ResponseError(w, r, err)
	}
}
