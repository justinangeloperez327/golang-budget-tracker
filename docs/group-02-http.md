# Group 2: HTTP infrastructure

## Delivered

- Chi v5.5.0 router with `GET /`, `GET /healthz`, `GET /readyz`, `GET /api/v1/status`.
- Standard JSON 404 and 405 response envelopes with safe messages and correlation IDs.
- Random server-generated 128-bit request IDs (`X-Request-ID`), not trusted from incoming headers.
- JSON structured request logs with method, route template, status, bytes, duration and correlation ID; query strings, request bodies and cookies are not logged.
- Panic recovery with internal stack logs and a sanitized HTTP 500 response if headers were not already written.
- Defensive HTTP headers, request-body length bounds, network timeouts and configurable request-context deadlines.
- Configured max header size, signal-driven graceful shutdown and shutdown regression tests.
- Clean Linux/Windows Go CI with pinned Chi dependency verification.

## Configuration

Environment is read from **process environment**, not loaded automatically from a `.env` file. The application defaults to development values; `.env.example` documents overrides and Docker Compose reads `.env`.

| Name | Default | Description |
| --- | --- | --- |
| `APP_ENV` | `development` | development, test, production |
| `HTTP_ADDR` | `:8080` | listen host:port |
| `LOG_LEVEL` | `info` | debug, info, warn, error |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | header read deadline |
| `HTTP_READ_TIMEOUT` | `15s` | request read deadline |
| `HTTP_WRITE_TIMEOUT` | `20s` | response write deadline |
| `HTTP_IDLE_TIMEOUT` | `60s` | connection idle deadline |
| `HTTP_REQUEST_TIMEOUT` | `12s` | handler context deadline |
| `HTTP_SHUTDOWN_TIMEOUT` | `10s` | graceful shutdown window |
| `HTTP_MAX_BODY_BYTES` | `1048576` | max request payload bytes |
| `HTTP_MAX_HEADER_BYTES` | `16384` | max header bytes |

Timeout values must be positive and <=10m, read-header <= read, and request < write. Body length range is 1–10 MiB, header bound 1 KiB–1 MiB.

## Caveats and next steps

- Chi's timeout middleware cancels the request context. Future handlers must respect `r.Context().Done()`. It does not guarantee all handlers automatically return 504 or stop executing.
- `http.MaxBytesReader` rejects oversized streaming bodies **when read**. Upcoming JSON handlers must map `*http.MaxBytesError` to a safe 413 response.
- The readiness endpoint currently verifies that the HTTP process can respond, **not** PostgreSQL. Add database readiness in Group 3.
- No reverse-proxy headers are trusted for client identity. Proxy trust configuration should be implemented explicitly before deployment behind a reverse proxy.
- Security headers assume self-hosted assets; the Content Security Policy will need explicit, reviewed changes for dynamic frontend assets in Group 5.
- No authentication, CSRF validation, ledger or database integration exists in Group 2. Group 4 adds authenticated sessions.
- Go handlers have no automated reload yet. Development hot reload is not part of the Group 2 scope.

## Verification

Run `go test ./...`, `go test -race ./...`, `go vet ./...`, `go mod verify`, `go mod tidy -diff`, `go build ./cmd/server` and GitHub Actions. CI does not deploy or publish.
