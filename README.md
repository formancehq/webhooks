# Formance Webhooks

Webhooks delivers Formance platform events to user-configured HTTP endpoints with HMAC signing, durable persistence, bounded retries, delivery visibility, and replay.

## Commands

| Command | Role |
|---------|------|
| `serve` | Runs the authenticated HTTP API. Pass `--worker` to embed the worker. |
| `worker` | Consumes broker events and dispatches webhook deliveries. |
| `migrate` | Applies PostgreSQL schema migrations. |
| `backfill-deliveries` | One-shot upgrade command that imports outstanding data from the pre-deliveries `attempts` table. |

## Architecture

```text
Kafka / NATS
     │
     ▼
Consumer ── transactionally inserts pending deliveries ──▶ PostgreSQL
     │                                                        │
     └──────────── ACK after commit                           ▼
                                                        Dispatcher
                                                             │
                                                             ▼
                                                     Customer endpoints
```

The worker has one processing model:

1. The broker consumer normalizes an event and inserts one `pending` delivery for every matching active config.
2. `(event_id, config_id)` is unique, so broker redelivery does not duplicate persisted work.
3. The broker message is acknowledged only after the PostgreSQL transaction commits.
4. The dispatcher claims due deliveries with `FOR UPDATE SKIP LOCKED`.
5. The HTTP result and the delivery transition are committed atomically with an append-only attempt record.

See [docs/architecture.md](docs/architecture.md), [docs/message-processing.md](docs/message-processing.md), and [docs/retry-mechanism.md](docs/retry-mechanism.md) for details.

## Data model

- `configs` stores webhook subscriptions, endpoints, event filters, activation state, and signing secrets.
- `deliveries` stores one current-state row per event and config.
- `delivery_attempts` stores the append-only history of outbound HTTP calls without copying signing secrets.
- `replay_requests` stores short-lived idempotency records for replay commands.

Delivery states are `pending`, `delivering`, `succeeded`, `failed`, and `cancelled`.

## Delivery API

- `GET /deliveries` lists delivery metadata with cursor pagination and omits payloads.
- `GET /deliveries/{id}` returns one delivery including its payload.
- `GET /deliveries/{id}/attempts` returns its attempt history.
- `POST /deliveries/{id}/replay` requeues one failed or pending delivery.
- `POST /deliveries/replay` requeues a bounded page of deliveries.

Replay commands require `Idempotency-Key`. Failed deliveries receive a fresh retry generation; pending deliveries are only expedited.

## Upgrade from the attempts model

The runtime never reads or writes the old `attempts` queue. Upgrades from versions that used it must be coordinated by the Operator:

1. Stop all old workers and wait for their termination.
2. Apply the new schema migrations.
3. Run `webhooks backfill-deliveries` until it completes.
4. Deploy the new Webhooks version.
5. Recreate the workers.

The backfill is resumable and idempotent. It remains in the binary only as an upgrade adapter; there is no runtime pipeline selector and no supported mixed-worker state.

## Retry policy

- Network errors, timeouts, `408`, `429`, and `5xx` are retryable.
- Other `4xx` responses are terminal.
- Backoff is exponential from `--min-backoff-delay` to `--max-backoff-delay`.
- `--max-attempts` and `--abort-after` bound each retry generation.
- `Retry-After` is honored without bypassing the configured bounds.

## Development

```shell
nix develop --impure --command just pre-commit
nix develop --impure --command just tests
```

The E2E suite runs against PostgreSQL and NATS and exercises the generated Go SDK in [pkg/client](pkg/client).
