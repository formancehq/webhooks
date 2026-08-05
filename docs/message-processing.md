# Message Processing

## Overview

The worker consumes events from Kafka or NATS through Watermill and durably enqueues webhook deliveries before acknowledging each broker message.

## Flow

```text
Broker
  │
  ▼
Watermill handler
  ├─ unmarshal the event
  ├─ normalize the event type
  ├─ find matching active configs
  ├─ INSERT pending deliveries in one transaction
  └─ return after commit
        │
        ├─ success → broker ACK
        └─ error   → broker NACK
```

The handler never performs outbound HTTP. Endpoint latency therefore does not block broker ingestion.

`deliveries` has a unique `(event_id, config_id)` index. If the broker redelivers a message after an uncertain acknowledgement, `ON CONFLICT DO NOTHING` makes the enqueue operation idempotent.

## Event format

Events are decoded as `publish.EventMessage`:

- `type` is the event type matched against config filters;
- `app`, when present, is prepended to the type;
- `idempotency_key` is propagated to outbound webhook headers.

The normalized type is lowercase and formatted as `<app>.<type>` when `app` is present.

## Transaction and acknowledgement contract

All matching deliveries are inserted in a single PostgreSQL transaction. The consumer returns success only after that transaction commits.

| Failure | Result |
|---------|--------|
| Invalid broker payload | NACK |
| Config lookup failure | NACK |
| Delivery insert or commit failure | NACK |
| No matching active config | ACK with no delivery |
| Existing `(event_id, config_id)` | ACK after idempotent no-op |

Once persisted, first attempts and retries are both handled by the dispatcher described in [retry-mechanism.md](retry-mechanism.md).
