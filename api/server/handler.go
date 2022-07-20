package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/numary/go-libs/sharedapi"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks-cloud/internal/storage"
	"github.com/numary/webhooks-cloud/pkg/model"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	HealthCheckPath = "/_healthcheck"
	ConfigsPath     = "/configs"
	idPath          = "/:" + userIdPathParam
	userIdPathParam = "userId"
)

type webhooksHandler struct {
	*httprouter.Router

	store storage.Store
}

func NewWebhooksHandler(store storage.Store) http.Handler {
	h := &webhooksHandler{
		Router: httprouter.New(),
		store:  store,
	}

	h.Router.GET(HealthCheckPath, h.healthCheckHandle)
	h.Router.GET(ConfigsPath, h.getAllConfigsHandle)
	h.Router.GET(ConfigsPath+idPath, h.getConfigsByUserIDHandle)
	h.Router.POST(ConfigsPath+idPath, h.insertConfigByUserIDHandle)
	h.Router.DELETE(ConfigsPath, h.deleteAllConfigsHandle)

	return h
}

func (h *webhooksHandler) getAllConfigsHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	cursor, err := h.store.FindAllConfigs()
	if err != nil {
		sharedlogging.Errorf("mongodb.Store.FindAllConfigs: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	resp := sharedapi.BaseResponse[model.ConfigInserted]{
		Cursor: &cursor,
	}
	var data []byte
	if data, err = json.Marshal(resp); err != nil {
		sharedlogging.Errorf("json.Marshal: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(data); err != nil {
		sharedlogging.Errorf("http.ResponseWriter.Write: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	sharedlogging.Infof("GET /configs: %d results", len(cursor.Data))
}

func (h *webhooksHandler) getConfigsByUserIDHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	if err := validateParams(p); err != nil {
		var errIP *errInvalidParams
		if errors.As(err, &errIP) {
			http.Error(w, errIP.Error(), errIP.status)
		} else {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		sharedlogging.Errorf("validateParams: %s", err)
		return
	}

	cursor, err := h.store.FindConfigsByUserID(p.ByName(userIdPathParam))
	if err != nil {
		sharedlogging.Errorf("mongodb.Store.FindConfigsByUserID: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	resp := sharedapi.BaseResponse[model.ConfigInserted]{
		Cursor: &cursor,
	}
	var data []byte
	if data, err = json.Marshal(resp); err != nil {
		sharedlogging.Errorf("json.Marshal: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(data); err != nil {
		sharedlogging.Errorf("http.ResponseWriter.Write: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	sharedlogging.Infof("GET /configs/%s: %d results", p.ByName(userIdPathParam), len(cursor.Data))
}

func (h *webhooksHandler) insertConfigByUserIDHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	if err := validateParams(p); err != nil {
		var errIP *errInvalidParams
		if errors.As(err, &errIP) {
			http.Error(w, errIP.Error(), errIP.status)
		} else {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		sharedlogging.Errorf("validateParams: %s", err)
		return
	}

	config := model.Config{}
	if err := decodeJSONBody(r, &config); err != nil {
		var errIB *errInvalidBody
		if errors.As(err, &errIB) {
			http.Error(w, errIB.Error(), errIB.status)
		} else {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		sharedlogging.Errorf("decodeJSONBody: %s", err)
		return
	}

	if err := config.Validate(); err != nil {
		sharedlogging.Errorf("invalid config: %s", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var err error
	var id primitive.ObjectID
	if id, err = h.store.InsertOneConfig(config, p.ByName(userIdPathParam)); err != nil {
		sharedlogging.Errorf("mongodb.Store.InsertOneConfig: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	sharedlogging.Infof("POST /configs/%s: inserted %s", p.ByName(userIdPathParam), id)
}

func (h *webhooksHandler) deleteAllConfigsHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	if err := h.store.DropConfigsCollection(); err != nil {
		sharedlogging.Errorf("mongodb.Store.DropConfigsCollection: %s", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	sharedlogging.Infof("deleted all configs")
}

func (h *webhooksHandler) healthCheckHandle(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	sharedlogging.Infof("health check OK")
}
