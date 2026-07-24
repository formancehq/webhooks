# Architecture

## Overview

The webhooks service delivers event notifications to user-configured HTTP endpoints. It consists of two main components that can run independently or together:

- **Server** — REST API for managing webhook configurations (CRUD, activation, secret management)
- **Worker** — Background service that consumes events from a message broker (Kafka/NATS) and delivers webhooks, with automatic retry on failure

## Components

```
┌─────────────────┐      ┌──────────────────┐      ┌──────────────────┐
│  Kafka / NATS   │─────▶│     Worker       │─────▶│  User Endpoints  │
│  (event source) │      │  (consumer +     │      │  (webhook targets)│
└─────────────────┘      │   retrier)       │      └──────────────────┘
                         └────────┬─────────┘
                                  │
                         ┌────────▼─────────┐
┌─────────────────┐      │                  │
│   API Clients   │─────▶│    PostgreSQL    │
│                 │◀─────│    (configs +    │
└─────────────────┘      │ deliveries +    │
                         │     attempts)   │
         ▲               └──────────────────┘
         │
┌────────┴─────────┐
│     Server       │
│  (REST API)      │
└──────────────────┘
```

### Server

The server exposes a REST API (see `openapi.yaml`) with these endpoints:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/configs` | List webhook configs (filterable by id, endpoint) |
| POST | `/configs` | Create a new webhook config |
| PUT | `/configs/{id}` | Update a webhook config |
| DELETE | `/configs/{id}` | Delete a webhook config |
| PUT | `/configs/{id}/activate` | Activate a config |
| PUT | `/configs/{id}/deactivate` | Deactivate a config |
| PUT | `/configs/{id}/secret/change` | Rotate the signing secret |
| GET | `/configs/{id}/test` | Send a test webhook |
| GET | `/deliveries` | List durable deliveries |
| GET | `/deliveries/{id}` | Inspect a delivery and payload |
| GET | `/deliveries/{id}/attempts` | Inspect delivery attempts |
| POST | `/deliveries/{id}/replay` | Replay one delivery |
| POST | `/deliveries/replay` | Replay a bounded page of deliveries |
| GET | `/_healthcheck` | Health check |
| GET | `/_info` | Service version info |

Authentication is handled via OAuth2 client credentials (configurable via `--auth-*` flags).

### Worker

The worker has two responsibilities:

1. **Event consumption** — In durable mode, subscribes through Watermill, inserts one pending delivery per matching config, and acknowledges only after the transaction commits. Endpoint latency never blocks broker ingestion.

2. **Dispatcher** — Claims pending first attempts and retries with an atomic `FOR UPDATE SKIP LOCKED` query, performs bounded concurrent HTTP calls, and records each result atomically with the delivery transition. See [retry-mechanism.md](retry-mechanism.md).

`--delivery-pipeline=legacy` remains the default for the coordinated migration release. Run `webhooks backfill-deliveries`, stop legacy workers for the final active-queue pass, then restart server and workers with `--delivery-pipeline=deliveries`.

### Data Model

**Config** — A webhook subscription:
- `id` (UUID) — unique identifier
- `endpoint` (URL) — where webhooks are sent
- `event_types` (string array) — which event types trigger this webhook
- `secret` (base64) — HMAC-SHA256 signing key (24 random bytes, base64-encoded)
- `active` (boolean) — whether the config receives webhooks
- `name` (string, optional) — human-readable label

**Attempt** — A single webhook delivery attempt:
- `id` (UUID) — unique identifier
- `webhook_id` (UUID) — groups all attempts for one event/config pair
- `config` (JSONB) — snapshot of the config at delivery time
- `payload` (string) — the event payload sent
- `status_code` (int) — HTTP response status
- `status` — one of: `success`, `to retry`, `retrying`, `failed`
- `retry_attempt` (int) — attempt number (0 = first delivery)
- `next_retry_after` (timestamp) — when this attempt can be retried

**Delivery** — Durable current state for one event/config pair:
- stable delivery and event IDs, config ID, event type and raw payload
- `pending|delivering|succeeded|failed|cancelled`
- retry-generation counters, lease timestamps and the next-attempt timestamp

**DeliveryAttempt** — Append-only result of one outbound call. It stores endpoint, outcome, status code, sanitized transport error, optional duration and a bounded response excerpt, never the signing secret. Backfilled attempts leave duration/error absent when legacy data cannot reconstruct them.

## Webhook Delivery

### Request Format

Each webhook is an HTTP POST with these headers:

| Header | Description |
|--------|-------------|
| `content-type` | `application/json` |
| `user-agent` | `formance-webhooks/v0` |
| `formance-webhook-id` | Unique webhook ID |
| `formance-webhook-timestamp` | Unix timestamp of the delivery |
| `formance-webhook-signature` | HMAC-SHA256 signature (`v1,<base64>`) |
| `formance-webhook-test` | `true` if this is a test webhook |
| `formance-webhook-idempotency-key` | Idempotency key (if present in the event) |

### Signature Verification

Signatures use HMAC-SHA256. The signed payload is:

```
{webhook_id}.{timestamp}.{body}
```

The signature header format is `v1,<base64-encoded-hmac>`. Recipients should:
1. Extract the timestamp and signature from the headers
2. Reconstruct the signed payload: `{formance-webhook-id}.{formance-webhook-timestamp}.{raw-body}`
3. Compute HMAC-SHA256 with the shared secret
4. Compare signatures using constant-time comparison

### Response Handling

- **2xx** → `success`, no retry
- **Non-2xx** → `to retry`, scheduled with exponential backoff
- **Max duration exceeded** → `failed`, no more retries

## Technology Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.25+ |
| HTTP framework | [chi](https://github.com/go-chi/chi) |
| Database | PostgreSQL via [bun](https://bun.uptrace.dev/) ORM |
| Message broker | Kafka or NATS via [Watermill](https://watermill.io/) |
| Dependency injection | [uber/fx](https://github.com/uber-go/fx) |
| CLI | [cobra](https://github.com/spf13/cobra) |
| Observability | OpenTelemetry (traces) |
| SDK | Auto-generated Go client via [Speakeasy](https://speakeasyapi.dev/) |
