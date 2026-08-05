# Architecture

## Overview

Webhooks has two commands backed by one PostgreSQL delivery model:

- `serve` exposes the authenticated configuration, delivery, and replay endpoints;
- `worker` consumes broker events and dispatches persisted deliveries.

```text
Kafka / NATS ──▶ Consumer ──▶ PostgreSQL ──▶ Dispatcher ──▶ User endpoints
                                    ▲
                                    │
API clients ───────▶ Server ────────┘
```

`serve --worker` embeds both roles in one process. A dedicated worker exposes only its health endpoint.

## Server

| Method | Path | Description |
|--------|------|-------------|
| GET | `/configs` | List webhook configs. |
| POST | `/configs` | Create a config. |
| PUT | `/configs/{id}` | Update a config. |
| DELETE | `/configs/{id}` | Soft-delete a config and cancel pending deliveries. |
| PUT | `/configs/{id}/activate` | Activate a config. |
| PUT | `/configs/{id}/deactivate` | Deactivate a config and cancel pending deliveries. |
| PUT | `/configs/{id}/secret/change` | Rotate the signing secret. |
| GET | `/configs/{id}/test` | Send a test webhook. |
| GET | `/deliveries` | List deliveries. |
| GET | `/deliveries/{id}` | Inspect one delivery and its payload. |
| GET | `/deliveries/{id}/attempts` | Inspect its attempt history. |
| POST | `/deliveries/{id}/replay` | Replay one delivery. |
| POST | `/deliveries/replay` | Replay a bounded page. |
| GET | `/_healthcheck` | Health check. |
| GET | `/_info` | Version information. |

OAuth2 client credentials protect the application endpoints. Audit middleware can publish API calls to `audit-events`.

## Worker

The worker has two responsibilities:

1. **Event ingestion** — normalize each broker event, insert one `pending` delivery per matching config in a transaction, and acknowledge only after commit.
2. **Dispatch** — claim due rows with `FOR UPDATE SKIP LOCKED`, perform bounded concurrent HTTP calls, and atomically persist the attempt and next delivery state.

The consumer never performs outbound HTTP. Slow endpoints therefore affect dispatcher capacity without blocking broker persistence.

## Data model

**Config** represents a webhook subscription: endpoint, event filters, signing secret, activation state, and timestamps. Deletion is soft so retained deliveries keep referential integrity.

**Delivery** is the current state of one event/config pair:

- stable delivery and event IDs;
- unique `(event_id, config_id)` identity;
- event type and payload;
- `pending`, `delivering`, `succeeded`, `failed`, or `cancelled` state;
- attempt counters, replay generation, lease timestamps, and next-attempt time.

**DeliveryAttempt** is the append-only result of an outbound call. It stores endpoint, outcome, status code, sanitized transport error, duration, and a bounded response excerpt. It never stores the signing secret.

**ReplayRequestRecord** retains individual and bulk replay idempotency decisions for 24 hours.

## Upgrade adapter

`backfill-deliveries` exists only for upgrades from releases that queued retries in `attempts`. The Operator must stop all old workers before running it, then deploy and start the new workers only after it succeeds. The runtime has no selector and cannot start the old worker implementation.

The backfill is resumable, idempotent, retention-bounded for terminal history, and always imports outstanding retries. Backfilled attempt rows may omit duration or transport error when the source data cannot reconstruct them.

## Webhook request

Every outbound delivery is an HTTP POST with:

| Header | Description |
|--------|-------------|
| `content-type` | `application/json` |
| `user-agent` | `formance-webhooks/v0` |
| `formance-webhook-id` | Stable delivery ID. |
| `formance-webhook-timestamp` | Unix delivery timestamp. |
| `formance-webhook-signature` | `v1,<base64>` HMAC-SHA256 signature. |
| `formance-webhook-test` | Whether this is a test delivery. |
| `formance-webhook-idempotency-key` | Event idempotency key when present. |

The signed value is `{webhook_id}.{timestamp}.{body}`. Receivers should compare the computed signature in constant time and use the stable IDs to deduplicate possible at-least-once sends.

## Response handling

- `2xx` succeeds;
- network errors, timeouts, `408`, `429`, and `5xx` schedule a bounded retry;
- other `4xx` responses fail permanently;
- retry budget exhaustion marks the delivery failed.
