package webhooks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/webhooks/pkg/security"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

const (
	StatusAttemptSuccess = "success"
	StatusAttemptToRetry = "to retry"
	StatusAttemptFailed  = "failed"
)

type Attempt struct {
	bun.BaseModel `bun:"table:attempts"`

	ID             string    `json:"id" bun:",pk"`
	WebhookID      string    `json:"webhookID" bun:"webhook_id"`
	CreatedAt      time.Time `json:"createdAt" bun:"created_at,nullzero,notnull,default:current_timestamp"`
	UpdatedAt      time.Time `json:"updatedAt" bun:"updated_at,nullzero,notnull,default:current_timestamp"`
	Config         Config    `json:"config" bun:"type:jsonb"`
	Payload        string    `json:"payload"`
	StatusCode     int       `json:"statusCode" bun:"status_code"`
	RetryAttempt   int       `json:"retryAttempt" bun:"retry_attempt"`
	Status         string    `json:"status"`
	NextRetryAfter time.Time `json:"nextRetryAfter,omitempty" bun:"next_retry_after,nullzero"`
}

func MakeAttempt(ctx context.Context, httpClient *http.Client, retryPolicy BackoffPolicy, id, webhookID string, attemptNb int, cfg Config, idempotencyKey string, payload []byte, isTest bool) (Attempt, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return Attempt{}, errors.Wrap(err, "http.NewRequestWithContext")
	}

	ts := time.Now().UTC()
	timestamp := ts.Unix()
	signature, err := security.Sign(webhookID, timestamp, cfg.Secret, payload)
	if err != nil {
		return Attempt{}, errors.Wrap(err, "security.Sign")
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "formance-webhooks/v0")
	req.Header.Set("formance-webhook-id", webhookID)
	req.Header.Set("formance-webhook-timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("formance-webhook-signature", signature)
	req.Header.Set("formance-webhook-test", fmt.Sprintf("%v", isTest))
	if idempotencyKey != "" {
		req.Header.Set("formance-webhook-idempotency-key", idempotencyKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return Attempt{}, errors.Wrap(err, "http.Client.Do")
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			logging.FromContext(ctx).Error(
				errors.Wrap(err, "http.Response.Body.Close"))
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Attempt{}, errors.Wrap(err, "io.ReadAll")
	}
	logging.FromContext(ctx).Debugf("webhooks.MakeAttempt: server response body: %s", string(body))

	attempt := Attempt{
		ID:           id,
		WebhookID:    webhookID,
		Config:       cfg,
		Payload:      string(payload),
		StatusCode:   resp.StatusCode,
		RetryAttempt: attemptNb,
	}

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		attempt.Status = StatusAttemptSuccess
		return attempt, nil
	}

	delay, err := retryPolicy.GetRetryDelay(attemptNb)
	if err != nil {
		attempt.Status = StatusAttemptFailed
		return attempt, nil
	}

	attempt.Status = StatusAttemptToRetry
	attempt.NextRetryAfter = ts.Add(delay)
	return attempt, nil
}
