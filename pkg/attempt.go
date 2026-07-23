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

func terminalAttemptStatus(statusCode int) (string, bool) {
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return StatusAttemptSuccess, true
	}
	if isPermanentClientError(statusCode) {
		return StatusAttemptFailed, true
	}
	return "", false
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

	ID              string        `json:"id" bun:",pk"`
	WebhookID       string        `json:"webhookID" bun:"webhook_id"`
	CreatedAt       time.Time     `json:"createdAt" bun:"created_at,nullzero,notnull,default:current_timestamp"`
	UpdatedAt       time.Time     `json:"updatedAt" bun:"updated_at,nullzero,notnull,default:current_timestamp"`
	Config          Config        `json:"config" bun:"type:jsonb"`
	Payload         string        `json:"payload"`
	StatusCode      int           `json:"statusCode" bun:"status_code"`
	RetryAttempt    int           `json:"retryAttempt" bun:"retry_attempt"`
	Status          string        `json:"status"`
	NextRetryAfter  time.Time     `json:"nextRetryAfter,omitempty" bun:"next_retry_after,nullzero"`
	ResponseExcerpt string        `json:"-" bun:"-"`
	DeliveryError   string        `json:"-" bun:"-"`
	Duration        time.Duration `json:"-" bun:"-"`
}

type attemptOptions struct {
	firstAttemptAt time.Time
}

type AttemptOption func(*attemptOptions)

// WithFirstAttemptAt lets retry callers enforce abort-after against the real
// elapsed time since the first delivery attempt, including prior Retry-After
// delays.
func WithFirstAttemptAt(firstAttemptAt time.Time) AttemptOption {
	return func(opts *attemptOptions) {
		if !firstAttemptAt.IsZero() {
			opts.firstAttemptAt = firstAttemptAt.UTC()
		}
	}
}

func MakeAttempt(ctx context.Context, httpClient *http.Client, retryPolicy BackoffPolicy, id, webhookID string, attemptNb int, cfg Config, idempotencyKey string, payload []byte, isTest bool, opts ...AttemptOption) (Attempt, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return Attempt{}, errors.Wrap(err, "http.NewRequestWithContext")
	}

	options := attemptOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	requestTime := time.Now().UTC()
	if options.firstAttemptAt.IsZero() {
		options.firstAttemptAt = requestTime
	}

	timestamp := requestTime.Unix()
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
	decisionTime := time.Now().UTC()

	attempt, err := classifyResponse(ctx, resp, doErr, retryPolicy, attemptNb, decisionTime, options.firstAttemptAt, Attempt{
		ID:           id,
		WebhookID:    webhookID,
		Config:       cfg,
		Payload:      string(payload),
		RetryAttempt: attemptNb,
	})
	if err != nil {
		return Attempt{}, err
	}
	attempt.Duration = time.Since(start)
	if doErr != nil {
		attempt.DeliveryError = doErr.Error()
	}

	metrics.RecordDelivery(ctx, attempt.Status, attempt.StatusCode, attempt.Duration)
	return attempt, nil
}

// classifyResponse turns the result of the delivery HTTP call into a persisted
// Attempt with its final status. base carries the identity fields already set by
// the caller; doErr is the transport error (if any).
func classifyResponse(ctx context.Context, resp *http.Response, doErr error, retryPolicy BackoffPolicy, attemptNb int, now, firstAttemptAt time.Time, base Attempt) (Attempt, error) {
	attempt := base

	if doErr != nil {
		// Transport/timeout failure — return a retryable attempt so the record is
		// persisted and the retry loop picks it up instead of losing the event.
		attempt.StatusCode = 0
		attempt.DeliveryError = doErr.Error()
		return scheduleAttemptRetry(attempt, retryPolicy, attemptNb, now, firstAttemptAt, nil), nil
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			logging.FromContext(ctx).Error(
				errors.Wrap(err, "http.Response.Body.Close"))
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		attempt.StatusCode = resp.StatusCode
		attempt.ResponseExcerpt = string(body)
		attempt.DeliveryError = errors.Wrap(err, "io.ReadAll").Error()
		if status, terminal := terminalAttemptStatus(resp.StatusCode); terminal {
			attempt.Status = status
			return attempt, nil
		}
		delay, delayErr := resolveRetryDelay(retryPolicy, attemptNb, now, resp.Header.Get("Retry-After"))
		if delayErr != nil {
			attempt.Status = StatusAttemptFailed
			return attempt, nil
		}
		return scheduleAttemptRetry(attempt, retryPolicy, attemptNb, now, firstAttemptAt, &delay), nil
	}
	logging.FromContext(ctx).Debugf("webhooks.MakeAttempt: server response body: %s", string(body))
	attempt.ResponseExcerpt = string(body)

	attempt.StatusCode = resp.StatusCode

	// Successful responses and permanent client errors are terminal. Permanent
	// client errors will never succeed on retry, so fail them fast instead of
	// retrying for the whole abort-after window.
	if status, terminal := terminalAttemptStatus(resp.StatusCode); terminal {
		attempt.Status = status
		return attempt, nil
	}

	delay, err := resolveRetryDelay(retryPolicy, attemptNb, now, resp.Header.Get("Retry-After"))
	if err != nil {
		attempt.Status = StatusAttemptFailed
		return attempt, nil
	}
	return scheduleAttemptRetry(attempt, retryPolicy, attemptNb, now, firstAttemptAt, &delay), nil
}

func resolveRetryDelay(retryPolicy BackoffPolicy, attemptNb int, now time.Time, retryAfterHeader string) (time.Duration, error) {
	delay, err := retryPolicy.GetRetryDelay(attemptNb)
	if err != nil {
		return 0, err
	}

	// Respect an explicit Retry-After (429/503) when it asks us to wait longer
	// than our computed backoff, but never let endpoint-controlled delays bypass
	// the retry policy window.
	if retryAfter, ok := parseRetryAfter(retryAfterHeader, now); ok && retryAfter > delay {
		if limiter, ok := retryPolicy.(RetryDelayLimiter); ok {
			var limitErr error
			retryAfter, limitErr = limiter.LimitRetryDelay(attemptNb, retryAfter)
			if limitErr != nil {
				return 0, limitErr
			}
		}
		delay = retryAfter
	}
	return delay, nil
}

func scheduleAttemptRetry(attempt Attempt, retryPolicy BackoffPolicy, attemptNb int, now, firstAttemptAt time.Time, requestedDelay *time.Duration) Attempt {
	delay := time.Duration(0)
	if requestedDelay == nil {
		var err error
		delay, err = retryPolicy.GetRetryDelay(attemptNb)
		if err != nil {
			attempt.Status = StatusAttemptFailed
			return attempt
		}
	} else {
		delay = *requestedDelay
	}
	nextRetryAfter := now.Add(delay)
	if err := limitRetryWindow(retryPolicy, firstAttemptAt, nextRetryAfter); err != nil {
		attempt.Status = StatusAttemptFailed
		return attempt
	}
	attempt.Status = StatusAttemptToRetry
	attempt.NextRetryAfter = nextRetryAfter
	return attempt
}

func limitRetryWindow(retryPolicy BackoffPolicy, firstAttemptAt, nextRetryAfter time.Time) error {
	if firstAttemptAt.IsZero() {
		return nil
	}
	if limiter, ok := retryPolicy.(RetryWindowLimiter); ok {
		return limiter.LimitRetryWindow(nextRetryAfter.Sub(firstAttemptAt))
	}
	return nil
}
