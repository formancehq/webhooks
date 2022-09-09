package webhooks

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/webhooks/pkg/security"
	"github.com/pkg/errors"
)

func MakeAttempt(ctx context.Context, httpClient *http.Client, schedule []time.Duration, attempt int, cfg Config, data []byte) (Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewBuffer(data))
	if err != nil {
		return Request{}, errors.Wrap(err, "http.NewRequestWithContext")
	}

	id := uuid.NewString()
	date := time.Now().UTC()
	signature, err := security.Sign(id, date, cfg.Secret, data)
	if err != nil {
		return Request{}, errors.Wrap(err, "security.Sign")
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "formance-webhooks/v1")
	req.Header.Set("formance-webhook-id", id)
	req.Header.Set("formance-webhook-timestamp", fmt.Sprintf("%d", date.Unix()))
	req.Header.Set("formance-webhook-signature", signature)

	resp, err := httpClient.Do(req)
	if err != nil {
		return Request{}, errors.Wrap(err, "http.Client.Do")
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			sharedlogging.GetLogger(ctx).Error(
				errors.Wrap(err, "http.Response.Body.Close"))
		}
	}()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("RESP SERVER BODY: %s\n", body)

	request := Request{
		RequestID:    id,
		Date:         date,
		Config:       cfg,
		Payload:      string(data),
		StatusCode:   resp.StatusCode,
		RetryAttempt: attempt,
	}

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		request.Status = StatusRequestSuccess
		return request, nil
	}

	if attempt == len(schedule)-1 {
		request.Status = StatusRequestFailed
		return request, nil
	}

	request.Status = StatusRequestToRetry
	request.RetryAfter = date.Add(schedule[attempt])
	return request, nil
}
