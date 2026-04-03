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
