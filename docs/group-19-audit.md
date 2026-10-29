# Group 19 — Security, data integrity, accessibility and resilience audit

## Verified controls

- Existing session-based workspace isolation, same-origin and CSRF enforcement on financial forms, fixed-size idempotency keys and ledger uniqueness constraints.
- Existing Go unit/race testing, PostgreSQL integration testing, sqlc generated model verification, Node.js 24 tests, Docker build, and dual-platform Go build in CI.
- Cross-workspace reminder dismissal and duplicate dismissal regression tests from Group 18.

## Group 19 findings and remediation

1. **Misleading save confirmation after session expiry.** A financial POST redirected to `/login` can result in a 200 HTML response after fetch redirect following. The client now recognizes a login/register redirect and explicitly reports the save as unconfirmed, rather than showing `Saved`. The original in-page form and request key remain intact. Do not automatically resubmit financial writes after session expiry.
2. **Unbounded reminder dismissal history scan.** The reminder inbox only projects a 30-day past and 14-day future window. Restrict dismissal lookup to the same range to avoid scanning historical dismissal records. A composite primary key still guarantees idempotent dismissals.
3. **Form exfiltration defense in depth.** The global CSP now restricts `form-action` to the same origin. Origin and CSRF checks remain authoritative on the server.

## Test evidence

`internal/app/handler_test.go` asserts the CSP form policy; `scripts/assets.test.mjs` guards the browser recovery logic. Existing PostgreSQL reminder integration tests exercise scoped dismissal, repeat attempts, and no financial posting. CI verifies Go Linux/Windows, sqlc, Node 24, database integration and Docker.

## Remaining risks and limitations

This is a focused code-and-CI audit, **not** an independently verified penetration test, browser-wide accessibility certification, or production load test. E2E tests under `tests/e2e` remain environment-dependent and need a real running browser/session. Automated checks cannot establish accessibility conformance on their own. Do not claim all authorization paths or business transitions have been exhaustively exercised. Production hardening, backup/restore drills, secret management and deployment validation belong in Group 20.
