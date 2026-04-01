# Retry Mechanism

## Overview

The retry mechanism processes failed webhook delivery attempts. A background worker (the `Retrier`) polls the database at regular intervals, claims a batch of pending retries, executes the HTTP calls, and updates their status.

## Architecture

### Statuses

| Status | Description |
|--------|-------------|
| `success` | Webhook delivered successfully (HTTP 2xx) |
| `to retry` | Delivery failed, scheduled for retry with a `next_retry_after` timestamp |
| `retrying` | Claimed by a worker, currently being processed |
| `failed` | Max retry duration exceeded, permanently failed |

### Lifecycle

```
[new message] --> MakeAttempt --> HTTP call
                                    |
                            success? --> "success"
                            failure? --> "to retry" + next_retry_after
                                              |
                                    [retrier tick] --> claim ("retrying")
                                              |
                                        HTTP call (30s timeout)
                                              |
                                    success? --> "success"
                                    failure? --> "to retry" (retry again later)
                                    max delay? --> "failed"
```

## Worker Behavior

### Tick Loop

The `Retrier` runs in a single goroutine with the following loop:

1. **Recover stale claims** (once per minute) -- Reset attempts stuck in `retrying` for more than 5 minutes (from crashed workers) back to `to retry`. This runs at most once per minute to avoid unnecessary database load.
2. **Claim a batch** -- Atomically set up to `--retry-batch-size` (default: 50) distinct webhook IDs from `to retry` to `retrying` using a single CTE query. Oldest retries are claimed first (`ORDER BY next_retry_after ASC`).
3. **Process batch in parallel** -- All claimed webhook IDs are processed concurrently via a bounded worker pool (`pond`), capped at `--retry-batch-size` concurrent goroutines. For each webhook:
   - Fetch the `retrying` attempts
   - Unmarshal the payload from the most recent attempt
   - Execute the HTTP call (`MakeAttempt`) with a 30-second timeout
   - Insert a new attempt record with the result
   - Update only the claimed (`retrying`) attempts to the final status
   - The worker waits for all goroutines to complete before sleeping
4. **Sleep** -- Wait `--retry-period` (default: 3 seconds), then repeat.

### Error Handling

Per-webhook errors are **logged and skipped**, not fatal. A single failing webhook endpoint does not stop the worker from processing the rest of the batch. Only context cancellation stops the worker.

Errors that are handled gracefully:
- HTTP call failures (timeout, DNS, connection refused)
- Malformed payloads
- Database errors on individual attempts

### Claim Query

The claim is atomic and safe for concurrent workers:

```sql
WITH to_claim AS (
    SELECT DISTINCT ON (webhook_id) webhook_id
    FROM attempts
    JOIN configs c ON c.id = attempts.config->>'id'
    WHERE attempts.status = 'to retry'
      AND attempts.next_retry_after < NOW()
    ORDER BY webhook_id, attempts.next_retry_after ASC
    LIMIT $batch_size
),
claimed AS (
    UPDATE attempts
    SET status = 'retrying', updated_at = NOW()
    WHERE webhook_id IN (SELECT webhook_id FROM to_claim)
      AND status = 'to retry'
    RETURNING webhook_id
)
SELECT DISTINCT webhook_id FROM claimed
```

When two workers execute this concurrently:
- Worker A's UPDATE locks the rows and sets them to `retrying`.
- Worker B's UPDATE finds those rows no longer match `status = 'to retry'` and skips them.
- No duplicate processing occurs.

Oldest retries are prioritized via `ORDER BY next_retry_after ASC`, preventing starvation of long-pending webhooks.

### Status Scoping

`UpdateAttemptsStatus` only modifies attempts in `retrying` status. Historical attempts (`success`, `failed`) are never overwritten. This ensures:
- Accurate audit trail per attempt
- No accidental overwrites from concurrent Kafka messages creating new attempts for the same webhook

## Multi-Worker Scaling

| Workers | Estimated Throughput |
|---------|---------------------|
| 1 | ~1,000 webhooks/min |
| 2 | ~2,000 webhooks/min |
| 4 | ~4,000 webhooks/min |
| N | ~N x 1,000 webhooks/min |

Throughput scales linearly because each worker claims its own exclusive batch.

### Crash Recovery

If a worker crashes mid-processing, its claimed attempts remain in `retrying` status. Every worker runs a recovery step at the start of each tick:

```sql
UPDATE attempts
SET status = 'to retry', updated_at = NOW()
WHERE status = 'retrying'
  AND updated_at < NOW() - INTERVAL '5 minutes'
```

This ensures no attempt is permanently stuck. The 5-minute window is chosen to be well above the 30-second HTTP timeout, avoiding false recoveries on slow-but-active requests.

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--retry-period` | `3s` | Interval between retry ticks |
| `--retry-batch-size` | `50` | Number of distinct webhook IDs claimed per tick |
| `--min-backoff-delay` | `1m` | Minimum delay before retrying a failed attempt |
| `--max-backoff-delay` | `1h` | Maximum delay between retries (exponential backoff) |
| `--abort-after` | `30d` | Stop retrying after this duration and mark as `failed` |

## Database Indexes

Two partial indexes optimize the retry queries:

```sql
-- Speeds up the claim query (finding eligible retries)
CREATE INDEX idx_attempts_retry_pending
ON attempts (next_retry_after)
WHERE status = 'to retry';

-- Speeds up fetching claimed attempts by webhook ID
CREATE INDEX idx_attempts_retrying
ON attempts (webhook_id)
WHERE status = 'retrying';

-- Speeds up the stale recovery query
CREATE INDEX idx_attempts_retrying_recovery
ON attempts (updated_at)
WHERE status = 'retrying';
```

## HTTP Client

The HTTP client used for webhook delivery has a **30-second timeout** (`pkg/otlp/module.go`). This prevents a single slow endpoint from blocking the worker indefinitely and ensures the stale recovery window (5 minutes) is never reached during normal operation.

## Backoff Strategy

Uses exponential backoff with jitter (see `pkg/backoff/exponential.go`):

- Each retry attempt increases the delay exponentially, starting from `--min-backoff-delay`.
- The delay is capped at `--max-backoff-delay`.
- After `--abort-after` total elapsed time since the first attempt, the webhook is marked as `failed` permanently.
