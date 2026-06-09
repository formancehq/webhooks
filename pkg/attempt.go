package webhooks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/go-libs/v2/logging"
	"github.com/formancehq/webhooks/pkg/metrics"
	"github.com/formancehq/webhooks/pkg/security"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// maxResponseBody caps how much of an endpoint's response we read. The body is
// only used for debug logging / diagnostics, so there is no reason to let a
// malicious or buggy endpoint stream an unbounded payload into worker memory.
const maxResponseBody = 64 << 10 // 64 KiB

// isPermanentClientError reports whether an HTTP status code is a client error
// that will never succeed on retry. Retrying these (wrong credentials, gone,
// method not allowed, ...) only amplifies load — the root cause of the observed
// retry storms. 408 (Request Timeout) and 429 (Too Many Requests) are explicitly
// excluded: they are transient and worth retrying.
func isPermanentClientError(statusCode int) bool {
	if statusCode < http.StatusBadRequest || statusCode >= http.StatusInternalServerError {
		return false
	}
	switch statusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	default:
		return true
	}
}

// maxRetryAfterDelay clamps how far a Retry-After header can push the next
// retry: the endpoint controls this value, so an absurd one must not park a
// delivery years in the future.
const maxRetryAfterDelay = 6 * time.Hour

// parseRetryAfter interprets a Retry-After header (delay-seconds or HTTP-date)
// relative to now, clamped to maxRetryAfterDelay. It returns false when the
// header is absent or unparseable.
func parseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return min(time.Duration(secs)*time.Second, maxRetryAfterDelay), true
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := t.Sub(now); d > 0 {
			return min(d, maxRetryAfterDelay), true
		}
	}
	return 0, false
}

const (
	StatusAttemptSuccess  = "success"
	StatusAttemptToRetry  = "to retry"
	StatusAttemptRetrying = "retrying"
	StatusAttemptFailed   = "failed"
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

	start := time.Now()
	resp, doErr := httpClient.Do(req)

	attempt, err := classifyResponse(ctx, resp, doErr, retryPolicy, attemptNb, ts, Attempt{
		ID:           id,
		WebhookID:    webhookID,
		Config:       cfg,
		Payload:      string(payload),
		RetryAttempt: attemptNb,
	})
	if err != nil {
		return Attempt{}, err
	}

	metrics.RecordDelivery(ctx, attempt.Status, attempt.StatusCode, time.Since(start))
	return attempt, nil
}

// classifyResponse turns the result of the delivery HTTP call into a persisted
// Attempt with its final status. base carries the identity fields already set by
// the caller; doErr is the transport error (if any).
func classifyResponse(ctx context.Context, resp *http.Response, doErr error, retryPolicy BackoffPolicy, attemptNb int, ts time.Time, base Attempt) (Attempt, error) {
	attempt := base

	if doErr != nil {
		// Transport/timeout failure — return a retryable attempt so the record is
		// persisted and the retry loop picks it up instead of losing the event.
		attempt.StatusCode = 0
		delay, delayErr := retryPolicy.GetRetryDelay(attemptNb)
		if delayErr != nil {
			attempt.Status = StatusAttemptFailed
			return attempt, nil
		}
		attempt.Status = StatusAttemptToRetry
		attempt.NextRetryAfter = ts.Add(delay)
		return attempt, nil
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			logging.FromContext(ctx).Error(
				errors.Wrap(err, "http.Response.Body.Close"))
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return Attempt{}, errors.Wrap(err, "io.ReadAll")
	}
	logging.FromContext(ctx).Debugf("webhooks.MakeAttempt: server response body: %s", string(body))

	attempt.StatusCode = resp.StatusCode

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		attempt.Status = StatusAttemptSuccess
		return attempt, nil
	}

	// Permanent client errors will never succeed on retry — fail fast instead of
	// retrying for the whole abort-after window.
	if isPermanentClientError(resp.StatusCode) {
		attempt.Status = StatusAttemptFailed
		return attempt, nil
	}

	delay, err := retryPolicy.GetRetryDelay(attemptNb)
	if err != nil {
		attempt.Status = StatusAttemptFailed
		return attempt, nil
	}

	// Respect an explicit Retry-After (429/503) when it asks us to wait longer
	// than our computed backoff.
	if retryAfter, ok := parseRetryAfter(resp.Header.Get("Retry-After"), ts); ok && retryAfter > delay {
		delay = retryAfter
	}

	attempt.Status = StatusAttemptToRetry
	attempt.NextRetryAfter = ts.Add(delay)
	return attempt, nil
}
