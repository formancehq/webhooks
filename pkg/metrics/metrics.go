// Package metrics provides the OpenTelemetry instruments for webhook delivery.
//
// Instruments are created lazily from the global meter provider so they bind to
// the real provider installed by the otlpmetrics fx module at startup (and to a
// no-op provider, harmlessly, when metrics are disabled — e.g. in unit tests).
package metrics

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "webhooks"

var (
	once             sync.Once
	deliveryCounter  metric.Int64Counter
	deliveryDuration metric.Float64Histogram
)

func instruments() (metric.Int64Counter, metric.Float64Histogram) {
	once.Do(func() {
		meter := otel.GetMeterProvider().Meter(meterName)
		deliveryCounter, _ = meter.Int64Counter(
			"webhooks_delivery_attempts_total",
			metric.WithDescription("Total webhook delivery attempts, by outcome status and HTTP status class"),
		)
		deliveryDuration, _ = meter.Float64Histogram(
			"webhooks_delivery_duration_seconds",
			metric.WithDescription("Duration of the outbound webhook HTTP call"),
			metric.WithUnit("s"),
		)
	})
	return deliveryCounter, deliveryDuration
}

// RecordDelivery records the outcome of a single delivery attempt.
//
// Attributes are deliberately low-cardinality (outcome status + HTTP status
// class). Per-endpoint breakdown is available from the otelhttp delivery spans;
// we keep it out of metrics to avoid a label-cardinality explosion across the
// full set of customer endpoints.
func RecordDelivery(ctx context.Context, status string, statusCode int, elapsed time.Duration) {
	counter, histogram := instruments()
	attrs := metric.WithAttributes(
		attribute.String("status", status),
		attribute.String("status_class", statusClass(statusCode)),
	)
	counter.Add(ctx, 1, attrs)
	histogram.Record(ctx, elapsed.Seconds(), attrs)
}

// statusClass buckets an HTTP status code into a low-cardinality class. A code
// of 0 means the request never got a response (transport/timeout failure).
func statusClass(statusCode int) string {
	switch {
	case statusCode == 0:
		return "error"
	case statusCode >= 200 && statusCode < 300:
		return "2xx"
	case statusCode >= 300 && statusCode < 400:
		return "3xx"
	case statusCode >= 400 && statusCode < 500:
		return "4xx"
	case statusCode >= 500:
		return "5xx"
	default:
		return "unknown"
	}
}
