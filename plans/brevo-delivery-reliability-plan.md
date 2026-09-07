# Brevo delivery reliability

- Correct documented invalid-email mapping, assign deterministic callback IDs, validate terminal payloads, and keep provider IDs separate from Notifuse IDs in `internal/service/inbound_webhook_event_service.go`. Cover mapping, replay, batches, timestamps, and suppression/status updates in `internal/service/brevo_webhook_test.go`.
- Record Brevo temporary/blocked delivery failures in message history without changing the existing suppression threshold. Preserve retryable processing after an event has already been stored.
- Define malformed webhook errors in `internal/domain/inbound_webhook_event.go`; verify via parser/HTTP tests and existing domain suite.
- Return Brevo's documented retryable 429 response for temporary processing failures and reject malformed payloads in `internal/http/inbound_webhook_event_handler.go`; cover both paths in its test file.
- Update subscriptions in place and report actual coverage in `internal/service/brevo_service.go`; test update errors, registration idempotency, URL identity, and coverage in `internal/service/brevo_service_test.go`.
- Exercise HTTP ingestion, database deduplication, message history, and list suppression in `tests/integration/brevo_webhook_e2e_test.go` using an isolated PostgreSQL test database.
- Run the domain, service, and HTTP unit suites and the focused Brevo integration suite. Review the diff and transfer only changed source/tests to the server checkout.
