package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iancoleman/strcase"
	ledger "github.com/numary/ledger/pkg/bus"
	payments "github.com/numary/payments/pkg"
	paymentIngestion "github.com/numary/payments/pkg/bridge/ingestion"
	webhooks "github.com/numary/webhooks/pkg"
)

const (
	PrefixLedger   = "ledger"
	PrefixPayments = "payments"
)

var ErrUnknownEventType = errors.New("unknown event type")

func (w *Worker) processMessage(ctx context.Context, msgValue []byte) error {
	var ev webhooks.EventMessage
	if err := json.Unmarshal(msgValue, &ev); err != nil {
		return fmt.Errorf("json.Unmarshal event message: %w", err)
	}
	eventType := strcase.ToSnake(ev.Type)

	switch ev.Type {
	case ledger.EventTypeCommittedTransactions:
		committedTxs := new(ledger.CommittedTransactions)
		if err := json.Unmarshal(ev.Payload, committedTxs); err != nil {
			return fmt.Errorf("json.Unmarshal event message payload: %w", err)
		}
		eventType = strings.Join([]string{PrefixLedger, eventType}, ".")
		fmt.Printf("\nEVENT FETCHED: %s\n%+v\n", eventType, committedTxs)
	case ledger.EventTypeSavedMetadata:
		metadata := new(ledger.SavedMetadata)
		if err := json.Unmarshal(ev.Payload, metadata); err != nil {
			return fmt.Errorf("json.Unmarshal event message payload: %w", err)
		}
		eventType = strings.Join([]string{PrefixLedger, eventType}, ".")
		fmt.Printf("\nEVENT FETCHED: %s\n%+v\n", eventType, metadata)
	case ledger.EventTypeUpdatedMapping:
		mapping := new(ledger.UpdatedMapping)
		if err := json.Unmarshal(ev.Payload, mapping); err != nil {
			return fmt.Errorf("json.Unmarshal event message payload: %w", err)
		}
		eventType = strings.Join([]string{PrefixLedger, eventType}, ".")
		fmt.Printf("\nEVENT FETCHED: %s\n%+v\n", eventType, mapping)
	case ledger.EventTypeRevertedTransaction:
		revertedTx := new(ledger.RevertedTransaction)
		if err := json.Unmarshal(ev.Payload, revertedTx); err != nil {
			return fmt.Errorf("json.Unmarshal event message payload: %w", err)
		}
		eventType = strings.Join([]string{PrefixLedger, eventType}, ".")
		fmt.Printf("\nEVENT FETCHED: %s\n%+v\n", eventType, revertedTx)
	case paymentIngestion.EventTypeSavedPayment:
		savedPayment := new(payments.SavedPayment)
		if err := json.Unmarshal(ev.Payload, savedPayment); err != nil {
			return fmt.Errorf("json.Unmarshal event message payload: %w", err)
		}
		eventType = strings.Join([]string{PrefixPayments, eventType}, ".")
		fmt.Printf("\nEVENT FETCHED: %s\n%+v\n", eventType, savedPayment)
	default:
		return fmt.Errorf("%w: %s", ErrUnknownEventType, ev.Type)
	}

	cur, err := w.Store.FindManyConfigs(ctx, map[string]any{webhooks.KeyEventTypes: ev.Type})
	if err != nil {
		return fmt.Errorf("storage.Store.FindManyConfigs: %w", err)
	}

	for _, cfg := range cur.Data {
		id := uuid.NewString()
		req, err := http.NewRequestWithContext(ctx,
			http.MethodPost, cfg.Endpoint, bytes.NewBuffer(msgValue))
		if err != nil {
			return fmt.Errorf("http.NewRequestWithContext: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Webhooks-ID", id)

		date := time.Now().UTC()
		resp, err := http.DefaultClient.Do(req)
		request := webhooks.Request{
			Config:     cfg,
			Payload:    string(msgValue),
			StatusCode: resp.StatusCode,
			Attempt:    0,
			Date:       date,
			Error:      err.Error(),
		}
		if _, err := w.Store.InsertOneRequest(ctx, request); err != nil {
			return fmt.Errorf("storage.Store.InsertOneRequest: %w", err)
		}

		if err := resp.Body.Close(); err != nil {
			return fmt.Errorf("http.Response.Body.Close: %w", err)
		}
	}

	return nil
}
