# Security

## Webhook Signing

Every webhook delivery is signed using HMAC-SHA256. The signature allows recipients to verify that the webhook originated from the Formance webhooks service and has not been tampered with.

### Secret Management

- Secrets are 24 random bytes, base64-encoded
- A secret is auto-generated when creating a config if none is provided
- Secrets can be rotated via `PUT /configs/{id}/secret/change`
- Custom secrets must be exactly 24 bytes (before base64 encoding)

### Signature Format

The `formance-webhook-signature` header contains: `v1,<base64-hmac-sha256>`

The signed content is: `{webhook_id}.{unix_timestamp}.{json_body}`

The `v1` prefix enables future signature scheme upgrades without breaking existing integrations.

## Log Hygiene

The service follows strict rules about what appears in logs:

### What is NOT logged

- **Environment variables** — `os.Environ()` is never dumped to logs, as it typically contains database credentials, API keys, and other secrets
- **Webhook secrets** — Config objects are never logged with `%+v` or any format that would expose the `secret` field. Only `config.ID` and `config.Endpoint` appear in log messages
- **Event payloads in traces** — Raw event payloads are not stored as OpenTelemetry span attributes, as they may contain sensitive business data

### What IS logged (at debug level)

- Event types being processed
- Config IDs and endpoints matched for delivery
- Webhook IDs and delivery status
- Retry claim counts and webhook IDs

## Authentication

The REST API supports OAuth2 client credentials authentication via the `--auth-*` flags. When enabled, all config management endpoints require a valid bearer token. The `/_healthcheck` and `/_info` endpoints are unauthenticated.

## Input Validation

- **Endpoint URLs** are validated (must be parseable, non-empty)
- **Event types** must be non-empty strings
- **Secrets** must be valid base64 encoding exactly 24 bytes when decoded
- **Request bodies** reject unknown JSON fields (`DisallowUnknownFields`)
- **Query filters** reject unknown filter keys with an error (no silent pass-through)

## Database Security

- All queries use parameterized statements (via bun ORM) — no SQL injection risk
- The atomic claim pattern for retries uses `WHERE status = 'to retry'` scoping to prevent double-processing
- Config deletion verifies existence before deleting (SELECT then DELETE)

## Configuration API secret handling

Request models accept a `secret`; ordinary configuration responses omit it.
`GET /configs` (including filtered reads), activation, deactivation and the
configuration nested in a test-attempt response never return the stored key.
They retain the other configuration fields, including `name`.

`POST /configs` returns the supplied or generated key in its creation response.
`PUT /configs/{id}/secret/change` returns the supplied or generated replacement
in its rotation response. These are disclosure points for the key involved in
that operation; subsequent reads cannot retrieve it. Save the key securely when
processing these responses. Repeating an explicit rotation request can return
that request's key again; these endpoints do not implement a one-use retrieval
token.

| `secret` input | Creation | Configuration update |
| --- | --- | --- |
| Omitted, `null`, or `""` | Generate a key | Preserve the current key |
| Non-empty string | Validate and use supplied key | Validate and replace current key |

Replacement keys must be base64 strings decoding to exactly 24 bytes. An invalid
replacement fails validation before storage changes. An ordinary update excludes
the secret column from its SQL statement when no replacement is supplied. There
is no read-merge-write step and no `redacted` sentinel. A concurrent rotation
therefore keeps its replacement key when another update changes the endpoint or
event types without supplying a key.

The signer still reads the current key from the configuration table. HMAC format
and verification are unchanged. Stored attempt snapshots retain their existing
format; the test-attempt response uses a separate representation without the
key. No database schema migration or historical-record cleanup is included in
this change.

### Migration

Clients that read `secret` from list or activation responses must store the key
from creation or rotation instead. A key that was not retained requires an
explicit rotation and coordinated receiver update; an ordinary configuration
edit no longer rotates it implicitly.

In the Go SDK, ordinary `WebhooksConfig` values have no `Secret` field. Creation
and rotation use `ConfigWithSecretResponse` with a `WebhooksConfigWithSecret`
value. Replace `response.ConfigResponse` with
`response.ConfigWithSecretResponse` for `InsertConfig` and `ChangeConfigSecret`.
Request models continue to accept supplied secrets.

### Exposure assessment

This fix prevents future ordinary API disclosure. It does not revoke keys that
were already exposed or remove them from historical database snapshots. Before
release, the service owner must assess affected deployments and read-access
history, identify keys that require rotation, and coordinate each replacement
with the receiver. No production exposure, rotation or deployment is verified by
local regression tests.
