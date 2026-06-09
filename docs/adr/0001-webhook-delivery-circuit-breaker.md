# ADR 0001: Reduce webhook retry amplification with delivery circuit breakers

## Status

Proposed

## Date

2026-06-09

## Context

The webhook worker currently consumes events from Kafka or NATS, finds matching active webhook configs, performs the HTTP delivery synchronously, and persists each delivery attempt in PostgreSQL. Failed attempts are stored with a `next_retry_after` timestamp. A background retrier then polls PostgreSQL, claims due attempts, performs the HTTP call again, inserts a new attempt, and updates the previously claimed attempts.

This works for isolated failures, but it becomes expensive when a recipient endpoint is unhealthy for an extended period.

For example, if a customer endpoint starts returning errors for every request while events continue to arrive, every event/config pair creates its own retry chain. PostgreSQL then becomes both the audit store and the retry scheduler for a large number of attempts that are very likely to fail for the same root cause. This is retry amplification: the system treats many correlated failures as independent work.

Moving retry delays to the broker helps with scheduling cost, but it does not solve retry amplification by itself. If an endpoint is known to be unhealthy, requeueing every failed delivery still creates a large amount of useless work. It also needs different implementations for NATS and Kafka:

- NATS JetStream supports delayed redelivery primitives such as delayed negative acknowledgement.
- Kafka does not currently provide a stable, portable "deliver this message after X" primitive in the broker. A Kafka-compatible implementation usually needs retry topics, a `not_before` timestamp, and a consumer that promotes due messages back to the delivery topic.

We need a design that reduces PostgreSQL load during endpoint outages while remaining compatible with both Kafka and NATS.

## Decision

Introduce a delivery circuit breaker per webhook config.

The circuit breaker is separate from the user-controlled `configs.active` flag:

- `configs.active` means the user wants this webhook subscription enabled.
- The circuit state means the platform currently considers delivery to this config healthy, unhealthy, or under recovery test.

The circuit has three states:

| State | Meaning | Behavior |
| --- | --- | --- |
| `closed` | Normal healthy state | Deliver events and retry failed deliveries normally. |
| `open` | Endpoint is considered unhealthy | Stop regular deliveries and regular retries for this config. Schedule only sparse recovery probes. |
| `half_open` | Recovery test in progress | Allow a small bounded number of probe deliveries. Close on success, reopen on failure. |

When the circuit is `closed`, delivery failures update health counters for the config. The circuit opens when configured thresholds are reached, for example:

- N consecutive retryable failures.
- A high retryable failure ratio over a rolling time window with a minimum request volume.
- Repeated timeout or network failures.

When the circuit is `open`:

- New events for this config are not delivered immediately.
- Due retries for this config are not delivered immediately.
- The system does not create one retry chain per event while the endpoint is known to be unhealthy.
- A recovery probe is scheduled with backoff, for example after 5 minutes, then 15 minutes, then 1 hour.

When the probe is due, the circuit moves to `half_open`. A successful probe closes the circuit and resets the failure counters. A failed probe reopens the circuit and increases the probe delay.

The circuit breaker should gate both initial deliveries and retries before any HTTP request is made. Existing active/inactive config checks still apply: inactive configs must not receive normal deliveries or probes.

## Delivery retry scheduling

Add a delivery retry scheduler abstraction for individual webhook delivery commands. This abstraction must schedule a specific webhook delivery, not the original source event. Retrying the original event would redeliver webhooks that may already have succeeded.

The scheduler interface should hide broker-specific behavior:

- NATS adapter: use JetStream delayed redelivery or delayed republish where available.
- Kafka adapter: use retry topics and a `not_before` timestamp. A retry topic consumer promotes messages back to the delivery topic only when due.
- Existing PostgreSQL retrier can remain as a transitional adapter during migration.

The circuit breaker is still required even if retry scheduling moves to the broker. Broker-delayed retry reduces scheduler load; the circuit breaker reduces useless retry work while an endpoint is unhealthy.

## Persistence model

Persist circuit state separately from webhook configuration. A dedicated table is preferable to overloading `configs`, for example:

```text
webhook_delivery_circuits
  config_id primary key
  state
  consecutive_failures
  window_started_at
  window_successes
  window_failures
  opened_until
  probe_attempt
  last_failure_at
  last_failure_status_code
  last_failure_reason
  updated_at
```

Delivery attempts remain the audit trail for real HTTP calls. The system should avoid inserting one full attempt row per suppressed event while a circuit is open unless product requirements explicitly require per-event audit. Prefer lightweight aggregate counters or metrics for suppressed deliveries, for example "suppressed 12,430 deliveries for config X while circuit was open".

## Consequences

Positive consequences:

- Caps retry work for a broken endpoint.
- Reduces PostgreSQL load during correlated endpoint outages.
- Makes endpoint health explicit and observable.
- Keeps user intent (`active`) separate from platform protection (`circuit_state`).
- Works with either the current PostgreSQL retrier or future NATS/Kafka retry adapters.

Negative consequences:

- Some deliveries may be skipped or delayed while the circuit is open, depending on the selected backlog policy.
- The system needs new state, thresholds, metrics, and operational tooling.
- False positives are possible if thresholds are too aggressive.
- Product behavior must be explicit when a circuit opens: suppress, buffer for replay, or use a hybrid policy.

## Open questions

1. What should happen to new events while the circuit is open?
   - Suppress/drop with lightweight audit.
   - Buffer for replay when the endpoint recovers.
   - Hybrid policy, such as buffer for a bounded time or bounded count, then suppress.

2. Should the circuit be keyed by webhook config ID, endpoint URL, customer, or a combination?

3. Which failures are retryable and circuit-breaking?
   - Timeouts and network errors should count.
   - 5xx and 429 should likely count.
   - 4xx responses may need separate classification because some are permanent configuration errors.

4. How should users be notified when a circuit opens or closes?

5. Should the API expose circuit status separately from config activation?

6. What are the default thresholds and probe backoff values?

## Rollout plan

1. Add circuit state persistence and observability while keeping the existing PostgreSQL retrier.
2. Gate initial deliveries and PostgreSQL retries on circuit state.
3. Add suppression or bounded backlog behavior for events received while the circuit is open.
4. Add NATS and Kafka delivery retry scheduler adapters behind a common interface.
5. Gradually move retry scheduling out of PostgreSQL once behavior and metrics are validated.

## Alternatives considered

### Move retries directly to the broker

This reduces PostgreSQL scheduler pressure, but it does not prevent retry amplification. A broken endpoint would still receive one retry chain per event unless a circuit breaker or equivalent health gate exists.

### Disable the webhook config automatically

Automatically setting `configs.active = false` would reduce work, but it conflates user intent with platform protection. A separate circuit state allows the webhook to remain user-enabled while the platform temporarily suppresses delivery attempts.

### Keep the current PostgreSQL retry loop and tune indexes

The current retry loop can be optimized, but it still uses PostgreSQL as a scheduler for many attempts that are correlated by the same unhealthy endpoint. This does not address the underlying amplification pattern.
