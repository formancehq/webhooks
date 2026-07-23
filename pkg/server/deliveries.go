package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/formancehq/go-libs/v2/api"
	"github.com/formancehq/go-libs/v2/bun/bunpaginate"
	"github.com/formancehq/go-libs/v2/logging"
	webhooks "github.com/formancehq/webhooks/pkg"
	"github.com/formancehq/webhooks/pkg/metrics"
	"github.com/formancehq/webhooks/pkg/server/apierrors"
	"github.com/formancehq/webhooks/pkg/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

const (
	defaultDeliveryPageSize = 100
	maxDeliveryPageSize     = 1000
	maxReplayWindow         = 90 * 24 * time.Hour
)

func parsePageSize(value string, defaultValue int) (int, error) {
	if value == "" {
		return defaultValue, nil
	}
	pageSize, err := strconv.Atoi(value)
	if err != nil || pageSize <= 0 || pageSize > maxDeliveryPageSize {
		return 0, fmt.Errorf("pageSize must be between 1 and %d", maxDeliveryPageSize)
	}
	return pageSize, nil
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp must use RFC3339: %w", err)
	}
	return parsed.UTC(), nil
}

func validDeliveryStatus(status string) bool {
	switch status {
	case "", webhooks.StatusDeliveryPending, webhooks.StatusDeliveryDelivering,
		webhooks.StatusDeliverySucceeded, webhooks.StatusDeliveryFailed, webhooks.StatusDeliveryCancelled:
		return true
	default:
		return false
	}
}

func (h *serverHandler) getDeliveriesHandle(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if len(values) != 1 {
			apierrors.ResponseError(w, r, apierrors.NewValidationError("query parameters must have one value"))
			return
		}
		switch key {
		case "configId", "status", "createdAtFrom", "createdAtTo", "cursor", "pageSize":
		default:
			apierrors.ResponseError(w, r, apierrors.NewValidationError("unsupported query parameter: "+key))
			return
		}
	}
	filter := webhooks.DeliveryFilter{ConfigID: query.Get("configId"), Status: query.Get("status")}
	if filter.ConfigID != "" {
		if _, err := uuid.Parse(filter.ConfigID); err != nil {
			apierrors.ResponseError(w, r, apierrors.NewValidationError("configId must be a UUID"))
			return
		}
	}
	if !validDeliveryStatus(filter.Status) {
		apierrors.ResponseError(w, r, apierrors.NewValidationError("invalid delivery status"))
		return
	}
	var err error
	filter.CreatedAfter, err = parseOptionalTime(query.Get("createdAtFrom"))
	if err == nil {
		filter.CreatedBefore, err = parseOptionalTime(query.Get("createdAtTo"))
	}
	if err == nil {
		filter.After, err = webhooks.DecodeDeliveryCursor(query.Get("cursor"))
	}
	if err == nil {
		filter.PageSize, err = parsePageSize(query.Get("pageSize"), defaultDeliveryPageSize)
	}
	if err != nil {
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}
	page, err := h.store.FindDeliveries(r.Context(), filter)
	if err != nil {
		apierrors.ResponseError(w, r, err)
		return
	}
	for index := range page.Data {
		page.Data[index].Payload = ""
	}
	next, err := webhooks.EncodeDeliveryCursor(page.NextCursor)
	if err != nil {
		apierrors.ResponseError(w, r, err)
		return
	}
	response := api.BaseResponse[webhooks.Delivery]{Cursor: &bunpaginate.Cursor[webhooks.Delivery]{
		Data: page.Data, PageSize: filter.PageSize, HasMore: page.HasMore, Next: next,
	}}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		apierrors.ResponseError(w, r, err)
	}
}

func (h *serverHandler) getDeliveryHandle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, PathParamId)
	delivery, err := h.store.GetDelivery(r.Context(), id)
	if errors.Is(err, storage.ErrDeliveryNotFound) {
		apierrors.ResponseError(w, r, apierrors.NewNotFoundError(err.Error()))
		return
	}
	if err != nil {
		apierrors.ResponseError(w, r, err)
		return
	}
	if err := json.NewEncoder(w).Encode(api.BaseResponse[webhooks.Delivery]{Data: &delivery}); err != nil {
		apierrors.ResponseError(w, r, err)
	}
}

func (h *serverHandler) getDeliveryAttemptsHandle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, PathParamId)
	if _, err := h.store.GetDelivery(r.Context(), id); errors.Is(err, storage.ErrDeliveryNotFound) {
		apierrors.ResponseError(w, r, apierrors.NewNotFoundError(err.Error()))
		return
	} else if err != nil {
		apierrors.ResponseError(w, r, err)
		return
	}
	cursor, err := webhooks.DecodeDeliveryCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}
	pageSize, err := parsePageSize(r.URL.Query().Get("pageSize"), defaultDeliveryPageSize)
	if err != nil {
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}
	attempts, nextCursor, err := h.store.FindDeliveryAttempts(r.Context(), id, cursor, pageSize)
	if err != nil {
		apierrors.ResponseError(w, r, err)
		return
	}
	next, err := webhooks.EncodeDeliveryCursor(nextCursor)
	if err != nil {
		apierrors.ResponseError(w, r, err)
		return
	}
	response := api.BaseResponse[webhooks.DeliveryAttempt]{Cursor: &bunpaginate.Cursor[webhooks.DeliveryAttempt]{
		Data: attempts, PageSize: pageSize, HasMore: next != "", Next: next,
	}}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		apierrors.ResponseError(w, r, err)
	}
}

func requireIdempotencyKey(r *http.Request) (string, error) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return "", fmt.Errorf("Idempotency-Key header is required")
	}
	if len(key) > 255 {
		return "", fmt.Errorf("Idempotency-Key must not exceed 255 characters")
	}
	return key, nil
}

func replayError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrDeliveryNotFound):
		apierrors.ResponseError(w, r, apierrors.NewNotFoundError(err.Error()))
	case errors.Is(err, storage.ErrDeliveryNotReplayable), errors.Is(err, storage.ErrIdempotencyConflict):
		apierrors.ResponseError(w, r, apierrors.NewConflictError(err.Error()))
	default:
		apierrors.ResponseError(w, r, err)
	}
}

func (h *serverHandler) replayDeliveryHandle(w http.ResponseWriter, r *http.Request) {
	key, err := requireIdempotencyKey(r)
	if err != nil {
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}
	delivery, applied, err := h.store.ReplayDelivery(r.Context(), chi.URLParam(r, PathParamId), key)
	if err != nil {
		replayError(w, r, err)
		return
	}
	logging.FromContext(r.Context()).Infof("replayed delivery %s", delivery.ID)
	if applied {
		metrics.RecordReplay(r.Context(), "individual", "replayed", 1)
		metrics.RecordDeliveryTransition(r.Context(), webhooks.StatusDeliveryPending, "replay", 1)
	}
	if err := json.NewEncoder(w).Encode(api.BaseResponse[webhooks.Delivery]{Data: &delivery}); err != nil {
		apierrors.ResponseError(w, r, err)
	}
}

func (h *serverHandler) replayDeliveriesHandle(w http.ResponseWriter, r *http.Request) {
	key, err := requireIdempotencyKey(r)
	if err != nil {
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}
	request := webhooks.ReplayDeliveriesRequest{}
	if err := decodeJSONBody(r, &request, false); err != nil {
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}
	if request.CreatedAtFrom.IsZero() {
		apierrors.ResponseError(w, r, apierrors.NewValidationError("createdAtFrom is required"))
		return
	}
	if request.PageSize == 0 {
		request.PageSize = maxDeliveryPageSize
	}
	if request.PageSize < 1 || request.PageSize > maxDeliveryPageSize {
		apierrors.ResponseError(w, r, apierrors.NewValidationError("pageSize must be between 1 and 1000"))
		return
	}
	if len(request.Statuses) == 0 {
		request.Statuses = []string{webhooks.StatusDeliveryFailed, webhooks.StatusDeliveryPending}
	}
	for _, status := range request.Statuses {
		if status != webhooks.StatusDeliveryFailed && status != webhooks.StatusDeliveryPending {
			apierrors.ResponseError(w, r, apierrors.NewValidationError("bulk replay only accepts failed and pending statuses"))
			return
		}
	}
	sort.Strings(request.Statuses)
	for _, id := range request.ConfigIDs {
		if _, err := uuid.Parse(id); err != nil {
			apierrors.ResponseError(w, r, apierrors.NewValidationError("configIds must contain UUIDs"))
			return
		}
	}
	sort.Strings(request.ConfigIDs)
	replayCursor, err := webhooks.DecodeReplayDeliveryCursor(request.CursorToken)
	if err != nil {
		apierrors.ResponseError(w, r, apierrors.NewValidationError(err.Error()))
		return
	}
	if replayCursor != nil {
		if request.CreatedAtTo.IsZero() {
			request.CreatedAtTo = replayCursor.CreatedAtTo
		}
		if !request.CreatedAtFrom.Equal(replayCursor.CreatedAtFrom) ||
			!request.CreatedAtTo.Equal(replayCursor.CreatedAtTo) ||
			!slices.Equal(request.Statuses, replayCursor.Statuses) ||
			!slices.Equal(request.ConfigIDs, replayCursor.ConfigIDs) {
			apierrors.ResponseError(w, r, apierrors.NewValidationError("replay cursor does not match request filters"))
			return
		}
		request.Cursor = &replayCursor.Position
	}
	effectiveTo := request.CreatedAtTo
	if effectiveTo.IsZero() {
		effectiveTo = time.Now().UTC()
	}
	if effectiveTo.Before(request.CreatedAtFrom) || effectiveTo.Sub(request.CreatedAtFrom) > maxReplayWindow {
		apierrors.ResponseError(w, r, apierrors.NewValidationError("replay window must be positive and at most 90 days"))
		return
	}
	result, applied, err := h.store.ReplayDeliveries(r.Context(), request, key)
	if err != nil {
		replayError(w, r, err)
		return
	}
	if result.NextCursorToken == "" {
		if result.NextCursor != nil {
			result.NextCursorToken, err = webhooks.EncodeReplayDeliveryCursor(webhooks.ReplayDeliveryCursor{
				Position: *result.NextCursor, CreatedAtFrom: request.CreatedAtFrom, CreatedAtTo: result.CreatedAtTo,
				Statuses: request.Statuses, ConfigIDs: request.ConfigIDs,
			})
		}
		if err != nil {
			apierrors.ResponseError(w, r, err)
			return
		}
	}
	logging.FromContext(r.Context()).Infof("bulk replay: replayed=%d expedited=%d skipped=%d", result.Replayed, result.Expedited, result.Skipped)
	if applied {
		metrics.RecordReplay(r.Context(), "bulk", "replayed", result.Replayed)
		metrics.RecordReplay(r.Context(), "bulk", "expedited", result.Expedited)
		metrics.RecordDeliveryTransition(r.Context(), webhooks.StatusDeliveryPending, "replay", result.Replayed+result.Expedited)
	}
	if err := json.NewEncoder(w).Encode(api.BaseResponse[webhooks.ReplayDeliveriesResult]{Data: &result}); err != nil {
		apierrors.ResponseError(w, r, err)
	}
}
