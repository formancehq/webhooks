package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iancoleman/strcase"
	webhooks "github.com/numary/webhooks/pkg"
	"github.com/numary/webhooks/pkg/security"
)

const (
	PrefixLedger   = "ledger"
	PrefixPayments = "payments"
)

const (
	EventTypeLedgerCommittedTransactions = "COMMITTED_TRANSACTIONS"
	EventTypeLedgerSavedMetadata         = "SAVED_METADATA"
	EventTypeLedgerUpdatedMapping        = "UPDATED_MAPPING"
	EventTypeLedgerRevertedTransaction   = "REVERTED_TRANSACTION"
	EventTypePaymentsSavedPayment        = "SAVED_PAYMENT"
)

var ErrUnknownEventType = errors.New("unknown event type")

func (w *Worker) processMessage(ctx context.Context, msgValue []byte) error {
	fmt.Printf("MSG:%s\n", string(msgValue))
	var ev webhooks.EventMessage
	if err := json.Unmarshal(msgValue, &ev); err != nil {
		return fmt.Errorf("json.Unmarshal event message: %w", err)
	}

	prefix := ""
	switch ev.Type {
	case EventTypeLedgerCommittedTransactions,
		EventTypeLedgerSavedMetadata,
		EventTypeLedgerUpdatedMapping,
		EventTypeLedgerRevertedTransaction:
		prefix = PrefixLedger
	case EventTypePaymentsSavedPayment:
		prefix = PrefixPayments
	default:
		return fmt.Errorf("%w: %s", ErrUnknownEventType, ev.Type)
	}

	eventType := strcase.ToSnake(ev.Type)
	eventType = strings.Join([]string{prefix, eventType}, ".")
	fmt.Printf("\nEVENT FETCHED: %s\n", eventType)

	cur, err := w.store.FindManyConfigs(ctx, map[string]any{webhooks.KeyEventTypes: ev.Type})
	if err != nil {
		return fmt.Errorf("storage.store.FindManyConfigs: %w", err)
	}

	for _, cfg := range cur.Data {
		if err := w.sendWebhook(ctx, cfg, msgValue); err != nil {
			return err
		}
	}

	return nil
}

func (w *Worker) sendWebhook(ctx context.Context, cfg webhooks.Config, msgValue []byte) error {
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, cfg.Endpoint, bytes.NewBuffer(msgValue))
	if err != nil {
		return fmt.Errorf("http.NewRequestWithContext: %w", err)
	}

	id := uuid.NewString()
	date := time.Now().UTC()
	signature, err := security.Sign(id, date, []byte(cfg.Secret), msgValue)
	if err != nil {
		return fmt.Errorf("security.Sign: %w", err)
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "formance-webhooks/v0")
	req.Header.Set("formance-webhook-id", id)
	req.Header.Set("formance-webhook-timestamp", fmt.Sprintf("%d", date.Unix()))
	req.Header.Set("formance-webhook-signature", signature)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http.Client.Do: %w", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			panic(fmt.Errorf("http.Response.Body.Close: %w", err))
		}
	}()

	requestInserted := webhooks.Request{
		Date:       date,
		ID:         id,
		Config:     cfg,
		Payload:    string(msgValue),
		StatusCode: resp.StatusCode,
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode > http.StatusMultipleChoices {
		requestInserted.RetryAfter = date.Add(5 * time.Second)
	} else {
		requestInserted.Success = true
	}

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("RESP SERVER BODY: %s\n", body)

	if _, err := w.store.InsertOneRequest(ctx, requestInserted); err != nil {
		return fmt.Errorf("storage.store.InsertOneRequest: %w", err)
	}

	return nil
}
