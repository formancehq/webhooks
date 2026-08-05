# Retry Mechanism

## Dispatcher

First attempts and retries use the same durable dispatcher:

1. Claim due `pending` deliveries and atomically move them to `delivering` with `FOR UPDATE SKIP LOCKED`.
2. Execute outbound HTTP calls with bounded concurrency and a 30-second client timeout.
3. Atomically append a `delivery_attempts` row and transition the delivery.
4. Return retryable results to `pending` with `next_attempt_at`.
5. Move terminal results to `succeeded` or `failed`.

Multiple workers can dispatch concurrently because locked rows are skipped rather than shared.

## States

| State | Meaning |
|-------|---------|
| `pending` | Ready for a first attempt or a retry at `next_attempt_at`. |
| `delivering` | Claimed by one dispatcher. |
| `succeeded` | The endpoint returned a successful response. |
| `failed` | The response is terminal or the retry budget is exhausted. |
| `cancelled` | The associated config was deleted or deactivated. |

```text
pending ── claim ──▶ delivering ── success ──▶ succeeded
   ▲                     │
   │                     ├─ retryable failure
   └─────────────────────┘
                         └─ terminal/budget exhausted ──▶ failed
```

## Retry classification

- Network errors and timeouts are retryable.
- `408`, `429`, and `5xx` responses are retryable.
- Other `4xx` responses are permanent failures.
- `Retry-After` is preserved when present.

Backoff is exponential and bounded by both `--max-attempts` and `--abort-after`. The limits are checked before each outbound call, including after downtime or a delayed `Retry-After`.

## Crash recovery

A worker crash can leave a delivery in `delivering`. Every dispatcher periodically returns claims older than five minutes to `pending`. The recovery window is longer than the outbound HTTP timeout so active requests are not reclaimed during normal operation.

Delivery identity and the event idempotency key remain stable across retries. Receivers should use them to deduplicate the possible at-least-once resend after an uncertain HTTP outcome.

## Replay

Manual replay preserves delivery identity:

- replaying a failed delivery increments `replay_generation` and grants a fresh retry budget;
- replaying a pending delivery only moves `next_attempt_at` forward;
- succeeded, delivering, cancelled, or inactive-config deliveries are not replayable by default.

Replay commands are idempotent through `Idempotency-Key`.

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--retry-period` | `3s` | Dispatcher polling interval. |
| `--retry-batch-size` | `50` | Maximum deliveries claimed per tick. |
| `--min-backoff-delay` | `1m` | Initial retry delay. |
| `--max-backoff-delay` | `1h` | Maximum retry delay. |
| `--abort-after` | `10h` | Maximum elapsed time per retry generation. |
| `--max-attempts` | `15` | Maximum HTTP attempts per retry generation. |

## PostgreSQL indexes

```sql
CREATE UNIQUE INDEX idx_deliveries_event_config
    ON deliveries (event_id, config_id);

CREATE INDEX idx_deliveries_pending_due
    ON deliveries (next_attempt_at, id)
    WHERE status = 'pending';

CREATE INDEX idx_deliveries_delivering_recovery
    ON deliveries (claimed_at, id)
    WHERE status = 'delivering';
```
