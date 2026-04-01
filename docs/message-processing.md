# Message Processing

## Overview

The worker consumes events from a message broker (Kafka or NATS) via [Watermill](https://watermill.io/) and delivers webhooks to matching configured endpoints.

## Processing Model

Messages are processed **synchronously** within the Watermill handler. The handler only returns after all webhook deliveries for that message are complete. This ensures:

- **No data loss** — The message is acknowledged by the broker only after processing finishes. If the worker crashes mid-processing, the broker redelivers the message.
- **Backpressure** — If delivery is slow, the consumer naturally slows down. No unbounded queue builds up in memory.
- **Error propagation** — If the message cannot be parsed, the handler returns an error, allowing Watermill to nack/retry/dead-letter as configured.

### Why not a worker pool?

A previous implementation dispatched messages to a `pond` worker pool and returned `nil` immediately. This created a data loss window: the message was acknowledged before processing finished. If the worker crashed between ack and completion, messages were silently lost.

The current design relies on Watermill's built-in concurrency model. Watermill's router supports concurrent message handling natively — configure it via `RouterConfig.Handler.MaxConcurrentMessages` if higher throughput is needed.

## Flow

```
Broker (Kafka/NATS)
       │
       ▼
  Watermill Router
       │
       ▼
  processMessages handler (synchronous)
       │
       ├─ Unmarshal event
       ├─ Normalize event type (lowercase, app prefix)
       ├─ Query matching active configs
       ├─ For each config:
       │    ├─ MakeAttempt (HTTP POST with signature)
       │    ├─ Insert attempt record
       │    └─ Log result
       └─ Return nil (ack) or error (nack)
```

## Event Format

Events are expected as `publish.EventMessage` JSON objects with at minimum:
- `type` — The event type (matched against config `event_types`)
- `app` — Optional app prefix (combined as `app.type`)

The full event payload is forwarded as the webhook body.

## Error Handling

| Error | Behavior |
|-------|----------|
| Unmarshal failure | Return error → broker nack/retry |
| No matching configs | Return nil → message acknowledged (no work needed) |
| HTTP call failure on one config | Log error, continue to next config |
| Database insert failure | Log error, continue to next config |

Individual config failures do not block other configs from receiving the same event. Only message-level errors (bad payload, config query failure) cause a nack.
