# Formance Webhooks

Webhooks is the Formance service responsible for delivering platform events (ledger, payments, …) to user-configured HTTP endpoints, with HMAC signing and automatic retries.

This document covers three things:

1. [Architecture deep-dive](#1-architecture-deep-dive) — how the service actually works today
2. [Known problems & limitations](#2-known-problems--limitations) — a detailed inventory of the issues we are facing
3. [Improvement plan](#3-improvement-plan) — a phased, prioritized roadmap to fix them

---

## 1. Architecture deep-dive

### 1.1 Components

The binary has two run modes (a third `retries` mode mentioned in older docs no longer exists):

| Command | Role |
|---------|------|
| `serve` | REST API for managing webhook configs (CRUD, activate/deactivate, secret rotation, test delivery). Can optionally embed the worker with `--worker`. |
| `worker` | Background service: consumes events from Kafka/NATS via [Watermill](https://watermill.io/), delivers webhooks, and runs the retry loop (`Retrier`). Exposes only a `/_healthcheck` endpoint. |

Both are wired with `uber/fx` dependency injection, share a PostgreSQL database (via `bun`), and are configured through `cobra`/`pflag` flags (see [cmd/flag/flags.go](cmd/flag/flags.go)).

```text
┌─────────────────┐      ┌───────────────────────────┐      ┌──────────────────┐
│  Kafka / NATS   │─────▶│          Worker           │─────▶│  User Endpoints  │
│ (event source)  │      │ ┌───────────┐ ┌─────────┐ │      │ (webhook targets)│
└─────────────────┘      │ │ Consumer  │ │ Retrier │ │      └──────────────────┘
                         │ │ (inline   │ │ (DB     │ │
                         │ │  send)    │ │  poll)  │ │
                         │ └─────┬─────┘ └────┬────┘ │
                         └───────┼────────────┼──────┘
                                 ▼            ▼
┌─────────────────┐      ┌───────────────────────────┐
│   API Clients   │─────▶│        PostgreSQL         │
│  (fctl, SDKs)   │◀─────│   configs + attempts      │
└─────────────────┘      └───────────────────────────┘
         ▲
         │
┌────────┴─────────┐
│      Server      │  REST API + OAuth2 auth + audit middleware
└──────────────────┘
```

### 1.2 Data model

Two tables only ([pkg/config.go](pkg/config.go), [pkg/attempt.go](pkg/attempt.go)):

**`configs`** — a webhook subscription:
- `id` (UUID, PK), `endpoint` (URL), `event_types` (text array), `secret` (base64, 24 random bytes), `active` (bool), `name`, `created_at`, `updated_at`

**`attempts`** — one row **per delivery attempt** (not per delivery):
- `id` (UUID, PK), `webhook_id` (groups all attempts of one event×config pair)
- `config` (**JSONB snapshot of the full config at send time, including the secret**)
- `payload` (full event JSON as text), `status_code`, `retry_attempt`, `status`, `next_retry_after`

Statuses: `success`, `to retry`, `retrying` (claimed by a worker), `failed` (retry window exhausted).

Key design consequence: **the current state of a delivery is smeared across multiple rows**. Each retry inserts a *new* attempt row, and the previous `retrying` rows are bulk-updated to the new status (`UpdateAttemptsStatus`). There is no `deliveries` aggregate.

### 1.3 Event consumption path (hot path)

[pkg/worker/module.go](pkg/worker/module.go) — `processMessages`:

1. Watermill delivers a message; payload is unmarshalled into `publish.EventMessage`.
2. Event type is normalized to `<app>.<type>` lower-case.
3. `FindManyConfigs({event_types, active: true})` finds matching configs.
4. **For each config, sequentially and synchronously**, `MakeAttempt` POSTs the payload to the endpoint (30s HTTP timeout, [pkg/otlp/module.go](pkg/otlp/module.go)).
5. The resulting attempt row (`success` or `to retry`) is inserted.
6. The message is acked when the handler returns `nil`; if persisting a *retryable* attempt fails, the handler returns an error → nack → broker redelivery.
7. `context.WithoutCancel` detaches the handler from shutdown cancellation so in-flight sends complete.

### 1.4 Retry path

[pkg/worker/worker.go](pkg/worker/worker.go) — the `Retrier` loop, every `--retry-period` (default 3s):

1. **Stale recovery** (≤ once/min): attempts stuck in `retrying` > 5 min (crashed worker) are reset to `to retry`.
2. **Atomic claim** ([pkg/storage/postgres/postgres.go](pkg/storage/postgres/postgres.go) `FindWebhookIDsToRetry`): a CTE selects up to `--retry-batch-size` (default 50) distinct `webhook_id`s whose oldest due attempt is ready, joins `configs` on `attempts.config->>'id'` to keep only active configs, locks rows with `FOR UPDATE SKIP LOCKED` (multi-worker safe), and flips them to `retrying`.
3. **Parallel processing** via a bounded `pond` pool: for each webhook, re-read the `retrying` attempts, re-send with `MakeAttempt` at `retry_attempt + 1`, insert the new attempt, then terminalize the claimed rows. If the new attempt is retryable, only the newly inserted row stays in `to retry`.
4. Sleep, repeat.

Backoff is exponential without jitter ([pkg/backoff/exponential.go](pkg/backoff/exponential.go)): `min-backoff-delay` (1m) doubling up to `max-backoff-delay` (1h), aborting to `failed` once cumulative elapsed time exceeds `--abort-after` (default 72h) or `--max-attempts` is reached (default 15 attempts).

### 1.5 Security model

[pkg/security/security.go](pkg/security/security.go) — Svix-style signing:

- Signed string: `{webhook_id}.{unix_timestamp}.{body}`, HMAC-SHA256 with the config secret, sent as `formance-webhook-signature: v1,<base64>`.
- Headers: `formance-webhook-id`, `-timestamp`, `-test`, `-idempotency-key` (propagated from the event).
- API auth: OAuth2 via `go-libs/auth` middleware; audit middleware publishes API calls to an `audit-events` topic.

### 1.6 Testing & tooling

- E2E ginkgo suite ([test/e2e](test/e2e)) running against a real Postgres + NATS through the generated Speakeasy SDK ([pkg/client](pkg/client), vendored with a `replace` directive).
- Earthly for lint/tests, Nix flake for the dev shell, GoReleaser for releases.

---

## 2. Known problems & limitations

Ordered by severity. References point at the code that produced the incidents; the Phase 1 fixes in this PR mitigate the retry-storm items called out below. The items marked **(prod)** have caused real production incidents — see [§2.0](#20-the-core-problem-retry-storm--backlog-amplification-prod).

### 2.0 The core problem: retry-storm → backlog amplification (prod)

This is the recurring, money-burning failure mode behind every webhooks incident we have had. The chain:

> A dead or misconfigured customer endpoint (wrong credentials, decommissioned URL, expired tunnel) → **every error is retried as if transient** → attempts pile up in `to retry` → the retry loop re-sends + the `attempts` table explodes → PostgreSQL/RDS load, **cross-AZ replication traffic, and AWS cost** blow up, drowning observability in noise.

**Root cause 1 — no retryable/non-retryable distinction.**
Mitigated in this PR by terminal 4xx classification.
Previously, `MakeAttempt` treated *any* non-2xx response as `to retry` ([pkg/attempt.go:106-118](pkg/attempt.go:106)). A `403` (bad credentials), `404` (gone), or `405` (wrong method) is **permanent** but was retried for the full `--abort-after` window anyway. There was no client-error short-circuit, no `Retry-After` honoring for `429`.

**Root cause 2 — no circuit breaker, no max-attempt cap; `abort-after`=30d is the *only* bound.**
Partially mitigated in this PR by `--max-attempts` (default 15) and `--abort-after=72h`; the per-endpoint circuit breaker is still future work.
With the previous `min-backoff=1m` doubling to a `max-backoff=1h` cap, the delay reached 1h after ~7 retries (~2h cumulative), then fired **once per hour for 30 days ≈ 718 more** → **~724 attempts per dead endpoint**. Production incidents confirmed that guardrails did not bound dead endpoints beyond the 30-day timer.

**Root cause 3 — orphaned attempts loop and are never reclaimed or purged.**
Mitigated in this PR by retention cleanup for deleted and inactive configs.
Deleting/deactivating a config used to leave `to retry` rows that the active-config join skipped but nothing ever transitioned to `failed` ([§2.1](#21-reliability--delivery-semantics)). Combined with zero retention ([§2.3](#23-scalability--data-growth)), the table only grew.

**Sanitized production evidence:** retry storms were dominated by permanent endpoint failures, orphaned attempts stayed indefinitely in `to retry`, large `attempts` tables made the retry-claim query expensive, and manual cleanup materially reduced cross-AZ database replication traffic. Detailed incident evidence lives in internal runbooks.

**Immediate priorities** (detailed in the plan): (a) treat permanent 4xx as terminal, (b) add a per-endpoint circuit breaker + max-attempt cap, (c) implement retention/compaction of `attempts`, (d) split observability by `service.name`/route/external dependency so outbound delivery latency is not misdiagnosed as internal service latency.

### 2.1 Reliability & delivery semantics

**P0 — Permanent errors retried as transient. (prod)**
See [§2.0](#20-the-core-problem-retry-storm--backlog-amplification-prod) root cause 1. The fix is small and high-impact: classify `4xx` (except `408`/`429`) as `failed` immediately in `MakeAttempt`.

**P0 — No circuit breaker / max-attempt cap. (prod)**
See [§2.0](#20-the-core-problem-retry-storm--backlog-amplification-prod) root cause 2. ~724 retries per dead endpoint is by design today.

**P1 — Head-of-line blocking on the consumer hot path.**
`processMessages` sends webhooks **inline, sequentially, inside the broker handler** ([pkg/worker/module.go:126](pkg/worker/module.go:126)). One slow endpoint (up to the 30s timeout) stalls the whole topic/partition; with N matching configs a single message can hold the handler for N×30s. Consumer lag builds up under load and *every* customer is penalized by one bad endpoint. This is the single biggest scalability flaw.

**P1 — Broker redelivery causes duplicate sends to healthy endpoints.**
If `InsertOneAttempt` fails for one config's retryable attempt, the handler nacks the whole message ([pkg/worker/module.go:143-151](pkg/worker/module.go:143)). On redelivery, the event is re-sent to **all** matching configs, including those that already received it successfully. There is no per-(event, config) deduplication (no unique constraint, `msg.UUID` is not used as an idempotency key in storage).

**P1 — Crash window produces duplicate retries.**
In `processWebhookRetry`, the new attempt is inserted *before* the claimed rows are flipped out of `retrying` ([pkg/worker/worker.go:150-160](pkg/worker/worker.go:150)), and the two writes are **not in a transaction**. A crash in between leaves old rows in `retrying`; stale recovery resets them to `to retry` 5 minutes later, and the webhook is sent again even though a fresh `to retry` attempt row already exists — duplicate deliveries and divergent attempt counters.

**P2 — Deleted configs strand attempts forever.**
The claim query filters on `configs.active = true` via a join. Deleting a config (`DELETE /configs/{id}`) leaves its `to retry` attempts orphaned: never claimable, never transitioned to `failed`, polluting the partial indexes indefinitely. Deactivating then reactivating a config also silently *resumes* old retries, which may be surprising.

**P2 — Graceful shutdown is unreliable.**
`Retrier.Stop` has a `default:` branch that gives up immediately if the loop is mid-batch ("no communication", [pkg/worker/worker.go:75](pkg/worker/worker.go:75)). The loop itself uses bare `time.Sleep` rather than a context-aware timer, and `run()` launches it with `context.Background()` ([pkg/worker/module.go:50](pkg/worker/module.go:50)), so fx shutdown cannot cancel it. In-flight batches can be killed mid-write on pod termination, feeding the stale-recovery duplicate path above.

**P3 — Lost audit rows on insert failure after success.**
If the HTTP delivery succeeded but `InsertOneAttempt` fails, we `continue` and ack: delivery happened but no trace of it exists in the DB.

### 2.2 Security

**P1 — No SSRF protection.**
`endpoint` is any URL that `url.Parse` accepts ([pkg/config.go:50](pkg/config.go:50)). The worker will happily POST signed payloads to `http://169.254.169.254/`, internal services, or `localhost`. No scheme allow-list (plain `http` accepted), no private/link-local IP deny-list, no DNS-rebinding protection, and the default `http.Client` follows redirects (a public endpoint can 302 to an internal one).

**P1 — Secrets are over-exposed.**
- Returned in clear text by every config API response (`Config` embeds `ConfigUser` with `json:"secret"`), including `GET /configs` and the `/test` response (which embeds the full config snapshot in the attempt).
- **Snapshotted into every `attempts.config` JSONB row** — rotating a secret does not remove the old one from potentially millions of historical rows.
- Stored in plain text in Postgres (no envelope encryption).

**P2 — Unbounded response body read.**
`io.ReadAll(resp.Body)` in `MakeAttempt` ([pkg/attempt.go:91](pkg/attempt.go:91)) reads whatever the remote endpoint returns, with no `io.LimitReader`. A malicious or buggy endpoint can OOM the worker. The body is only used for a debug log.

### 2.3 Scalability & data growth

**P0 — Unbounded `attempts` table → cross-AZ cost. (prod)**
No retention, no archival, no partitioning. Every delivery stores the full payload + full config snapshot; `success` rows are kept forever. Large attempts tables drive cross-AZ RDS replication traffic, inflate backups, and degrade the retry-claim CTE (`NOT EXISTS` anti-join over a large `to retry` backlog). Retention is **not optional**, it is the proven highest-ROI fix.

**P2 — Retry throughput ceiling.**
Polling claims ≤ 50 webhook IDs per 3s tick per worker (~16/s). The claim query joins on the JSONB expression `attempts.config->>'id'` (no expression index) and re-reads attempts twice per webhook (`FindAttemptsToRetryByWebhookID`, then `UpdateAttemptsStatus` does another SELECT before its UPDATE). During a large outage of a popular endpoint, the backlog drains slowly and the herd retries without jitter, synchronizing load spikes onto the recovering endpoint.

**P2 — `GET /configs` has no pagination.**
`FindManyConfigs` selects all rows ([pkg/storage/postgres/postgres.go:27](pkg/storage/postgres/postgres.go:27)); the handler wraps the full result in a fake `Cursor` ([pkg/server/get.go](pkg/server/get.go)). Same for the per-event-type lookup on the hot path — fine today, unbounded by design.

### 2.4 API & product gaps

- **No delivery visibility**: there is no endpoint to list attempts/deliveries, inspect failures, or **manually replay** a webhook. Support has to query Postgres by hand.
- **PUT `/configs/{id}` on an unknown id returns 200**: `Store.UpdateOneConfig` never checks existence, so the `ErrConfigNotFound` branch in [pkg/server/update.go:35](pkg/server/update.go:35) is dead code. It also doesn't bump `updated_at`.
- **Update replaces the secret silently**: `PUT /configs/{id}` regenerates a secret when none is provided (via `Validate()`), breaking the consumer's signature verification without warning.
- **No secret rotation overlap**: `/secret/change` is immediate; Svix/Stripe-style dual-secret grace periods are impossible, so rotation breaks verification until the consumer redeploys.
- **No wildcard or prefix event subscriptions** (`ledger.*`), no event-type catalog endpoint.
- **No automatic disabling** of endpoints failing for days (Stripe/Svix do this) and no customer notification when a webhook is finally marked `failed`.

### 2.5 Operability & code health

- **No metrics, no per-dependency split. (prod)** Only traces + logs. No delivery success rate, retry-queue depth, end-to-end latency, or per-endpoint error rate → incidents are debugged with SQL (cf. the recent `skip locked` / `active configs` firefighting in #147, #150). Worse, outbound delivery latency is not attributed clearly enough and can be misdiagnosed as internal service latency. Dashboards must split by `service.name`, route, and external dependency.
- **Health checks are no-ops**: server `/_healthcheck` returns an empty 200 with no DB check; the worker healthcheck logs `health check OK` at *Info* level on every probe (log spam).
- **Mixed dependency generations**: `go-libs/v2` everywhere with `go-libs/v5` bolted on for audit only; `logrus` + go-libs logging coexist; `pkg/errors` is deprecated.
- **Package layout**: the root `pkg` package (`webhooks`) is a grab-bag of domain model + HTTP delivery + backoff contract; the generated Speakeasy SDK (~100 files) is vendored in-tree with a `replace`, inflating the module and the diff noise.
- **Handler style**: deeply nested `if err == nil { … } else { … }` blocks ([pkg/server/test.go](pkg/server/test.go)), repeated response-encoding boilerplate, no `http.MaxBytesReader` on request bodies.
- **Docs drift**: this README previously documented "3 starting modes" and stale defaults; `docs/` is good but not linked from anywhere.

---

## 3. Improvement plan

Phased so that each step ships independently and de-risks the next one. Within a phase, items are ordered by value/effort.

### Phase 0 — Safety net (1 sprint)

Goal: observe before changing anything.

1. **OTel metrics** (S): counters/histograms for `webhooks_delivery_attempts_total{status,status_class}`, `webhooks_delivery_duration_seconds`, `webhooks_retry_queue_depth` (gauge from a cheap `count(*) where status='to retry'`), `webhooks_consumer_lag`. Wire to the existing OTLP pipeline; build the Grafana dashboard + alerts (queue depth, failure rate).
2. **Real health checks** (S): DB ping on `/_healthcheck`; drop the per-probe Info log in the worker handler.
3. **Load test harness** (M): a reproducible benchmark (k6 or Go) simulating one slow endpoint among N, to quantify the head-of-line problem and validate Phase 2.

### Phase 1 — Stop the retry storms (1 sprint, **highest ROI**, mostly no schema change)

These directly kill the [§2.0](#20-the-core-problem-retry-storm--backlog-amplification-prod) failure mode and the AWS cost bleed. Ship these first.

1. **Terminal 4xx classification** (S): in `MakeAttempt`, mark `4xx` (except `408`/`429`) as `failed` immediately instead of `to retry`. Honor `Retry-After` for `429`. Kills the bulk of every observed storm (403/404/405 were the top error codes).
2. **Circuit breaker + max-attempt cap** (M): per-config, after X consecutive failures stop claiming for a cooldown; hard-cap attempts (e.g. 15) independent of `--abort-after`. Lower the default `--abort-after` from 30d (720 retries) to something sane (e.g. 72h). Auto-disable endpoints failing > N days and flag them.
3. **Retention / compaction of `attempts`** (M): scheduled purge of `success` (30d) and `failed` (90d) rows + reclaim orphaned `to retry` for deleted/inactive configs → `failed`. This makes the proven manual cleanup path permanent and automatic.
4. **Per-dependency observability** (S): the Phase 0 metrics, split by `service.name` / route / outbound dependency so the next endpoint incident is caught from a dashboard, not later by infrastructure billing.

### Phase 1b — Security & correctness quick wins (1–2 sprints, no schema change)

1. **SSRF guard** (M): dedicated `http.Client` for deliveries with `CheckRedirect` disabled (or re-validated per hop), a dialer-level deny-list of private/link-local/loopback ranges (validate the *resolved* IP, not the hostname, to kill DNS rebinding), and an `https`-only policy with an env escape hatch for on-prem/dev. Validate at config creation *and* at send time.
2. **Bound response reads** (S): `io.LimitReader(resp.Body, 64<<10)` in `MakeAttempt`; persist a truncated body excerpt on failure (useful for the future deliveries API).
3. **Stop leaking secrets** (M): redact `secret` from list/get responses (keep it on create/rotate only — breaking change to coordinate with SDK/fctl); stop snapshotting the secret inside `attempts.config` (strip it before insert; the retry path re-reads it from `configs`).
4. **Add jitter to backoff** (S): full jitter (`rand(0, delay)`) to de-synchronize retry herds onto recovering endpoints.
5. **Transactional retry processing** (M): wrap `InsertOneAttempt` + `UpdateAttemptsStatus` in one transaction; make `UpdateAttemptsStatus` a single `UPDATE … RETURNING` (drop the pre-SELECT).
6. **Fix graceful shutdown** (M): replace `time.Sleep` with `select { case <-ctx.Done(); case <-ticker.C }`, run the loop off the fx lifecycle context, remove the `default:` escape in `Stop`, drain the pond pool with a deadline.
7. **API correctness** (S): `UpdateOneConfig` checks existence (404) and bumps `updated_at`; reject secret regeneration on PUT unless explicitly requested.

### Phase 2 — Delivery pipeline redesign (2–3 sprints, schema change)

The structural fix for head-of-line blocking, duplicates, and the convoluted claim query. Target model:

```text
events ──▶ consumer: INSERT deliveries (outbox) ──▶ ACK immediately
                              │
              dispatcher pool (same claim pattern,
              but on a one-row-per-delivery table)
                              │
                        HTTP send ──▶ append-only delivery_attempts
```

1. **Introduce a `deliveries` table** (one row per event×config): `id`, `config_id` (FK), `event_id`, `event_type`, `payload`, `status (pending|delivering|succeeded|failed|cancelled)`, `attempt_count`, `next_attempt_at`, timestamps. Add `UNIQUE (event_id, config_id)` → **idempotent broker redelivery for free**. Keep `delivery_attempts` as an append-only log (id, delivery_id, status_code, error, duration, response excerpt).
2. **Consumer becomes an outbox writer**: on message receipt, insert `pending` deliveries for all matching configs in one statement (`ON CONFLICT DO NOTHING`), ack. No HTTP on the hot path → consumer throughput decoupled from endpoint health; per-message work is one SELECT + one INSERT.
3. **Dispatcher replaces the Retrier**: same `FOR UPDATE SKIP LOCKED` claim, but now a trivial `WHERE status='pending' AND next_attempt_at <= now() ORDER BY next_attempt_at LIMIT $n` on an FK-indexed table — no JSONB join, no `NOT EXISTS` anti-join, no multi-row status smearing. First attempts and retries share one code path.
4. **Per-endpoint fairness & circuit breaking**: cap concurrent in-flight deliveries per config; after X consecutive failures open a circuit (skip claims for that config for a cooldown), and auto-disable + flag configs failing for > N days.
5. **Migration strategy**: dual-write window — new code path behind a flag, backfill `to retry` attempts into `deliveries`, keep the legacy retrier reading the old table until drained, then drop the old claim machinery. The e2e suite plus the Phase 0 load test gate the cutover.

### Phase 3 — Data lifecycle hardening (1 sprint, after Phase 2)

Phase 1 ships a retention job on the legacy `attempts` table; this phase makes it first-class on the new model.

1. **Retention/archival**: purge (or archive to object storage) `succeeded` deliveries after 30d and `failed` after 90d (configurable); attempts follow their delivery via `ON DELETE CASCADE`.
2. **Payload cap** (e.g. 1 MiB) validated at ingestion.
3. **Optional**: monthly partitioning of `delivery_attempts` by `created_at` if volume warrants it (cheap drops instead of `DELETE` churn → less cross-AZ traffic).

### Phase 4 — API & product (1–2 sprints, parallelizable with Phase 3)

1. **Deliveries API**: `GET /deliveries?config_id=&status=&cursor=` and `GET /deliveries/{id}/attempts` — gives support and customers visibility.
2. **Manual replay**: `POST /deliveries/{id}/retry` (and bulk per config) — the #1 support request for any webhook system.
3. **Real pagination** on `GET /configs` (bunpaginate is already a dependency).
4. **Dual-secret rotation**: `/secret/change` keeps the previous secret valid for a grace period; sign with the new, send both signatures space-separated (the `Verify` helper already iterates multiple signatures).
5. **Wildcard event types** (`ledger.*`) + `GET /event-types` catalog.
6. **OpenAPI/SDK sync** for all of the above.

### Phase 5 — Code health (continuous)

1. Migrate fully to `go-libs/v5`; drop `logrus` and `pkg/errors` (use `fmt.Errorf`/`errors.Join`).
2. Restructure: `internal/{api,delivery,storage,signing}` with a slim public `pkg/` (model + `security` verification helper, which customers may import); move the generated SDK out of the main module.
3. Flatten handler error handling (early returns), shared `respondJSON`/`respondError` helpers, `http.MaxBytesReader` on request bodies.
4. Unit tests for the dispatcher state machine and backoff (table-driven), keeping ginkgo for e2e.

### Sequencing summary

| Phase | Theme | Risk addressed | Effort |
|-------|-------|----------------|--------|
| 0 | Metrics & health | Flying blind | ~1 sprint |
| **1** | **Stop retry storms (terminal 4xx, circuit breaker, retention, per-dep metrics)** | **Retry storms, cross-AZ cost, undetected dead endpoints** | **~1 sprint** |
| 1b | Security/correctness quick wins | SSRF, secret leaks, dup retries, shutdown | 1–2 sprints |
| 2 | Outbox + deliveries model | Head-of-line blocking, duplicates, claim complexity | 2–3 sprints |
| 3 | Retention hardening | Unbounded growth (new model) | ~1 sprint |
| 4 | Deliveries API, replay, rotation | Supportability, product parity | 1–2 sprints |
| 5 | Code modernization | Maintenance drag | continuous |

> **If you do only one thing this quarter:** Phase 1. Terminal-4xx + circuit breaker + automatic retention would have prevented the incident class described in [§2.0](#20-the-core-problem-retry-storm--backlog-amplification-prod) and the recurring DB I/O and cost spikes.

---

## 4. Development

Run the linters:
```sh
earthly +lint
```

Run the tests:
```sh
earthly -P +tests
```

### Usage

```text
Usage:
  webhooks [command]

Available Commands:
  serve       Run webhooks server (add --worker to embed the worker)
  worker      Run webhooks worker
  version     Get webhooks version

Key flags:
      --listen string                        HTTP bind address (default ":8080")
      --worker                               Enable worker inside the server process
      --kafka-topics strings                 Topics to consume (default [default])
      --retry-period duration                Retry poll period (default 3s)
      --retry-batch-size int                 Webhook IDs claimed per tick (default 50)
      --min-backoff-delay duration           Minimum backoff delay (default 1m)
      --max-backoff-delay duration           Maximum backoff delay (default 1h)
      --abort-after duration                 Mark failed after retrying this long (default 72h)
      --max-attempts int                     Hard cap on delivery attempts per webhook (default 15)
      --retention-period duration            Attempts-table cleanup interval (default 1h)
      --retention-success-delay duration     Retain success attempts before purging (default 720h)
      --retention-failed-delay duration      Retain failed attempts before purging (default 2160h)
      --auto-migrate                         Run DB migrations on startup
      --storage-postgres-conn-string string  Postgres connection string
```

Further reading: [docs/architecture.md](docs/architecture.md), [docs/retry-mechanism.md](docs/retry-mechanism.md), [docs/message-processing.md](docs/message-processing.md), [docs/security.md](docs/security.md).
