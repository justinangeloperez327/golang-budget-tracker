# Group 4 — Authentication, sessions and workspace ownership

## Features

- Email normalization and unique users, each with a single owned workspace.
- Argon2id PHC password hashes: 19 MiB, 2 iterations, one parallel lane, random 128-bit salts. Password length 12–128 bytes.
- Random 256-bit session tokens, stored **only as SHA-256 hashes** in PostgreSQL; seven-day absolute expiry and server-side logout revocation.
- Secure, HttpOnly, SameSite=Lax cookies with production `__Host-` prefix.
- Random session-bound CSRF token retrievable after login via `GET /api/v1/auth/csrf`. All unsafe authenticated requests must send it in `X-CSRF-Token`.
- Mandatory exact `Origin` for register/login/logout; cross-site Fetch Metadata rejection; JSON-only mutation bodies.
- Persistent fixed-window attempt throttling: 6 registration requests/IP/15m, 30 logins/IP/15m and 20 logins/email/15m.
- Ownership-scoped workspace retrieval; unauthorized workspace IDs return HTTP 404 without returning another account's records.

## Endpoints

| Method | Path | Requirements |
| --- | --- | --- |
| POST | /api/v1/auth/register | JSON email/password, same Origin |
| POST | /api/v1/auth/login | JSON email/password, same Origin |
| GET | /api/v1/auth/me | Valid session |
| GET | /api/v1/auth/csrf | Valid session |
| POST | /api/v1/auth/logout | Valid session, Origin, CSRF header |
| GET | /api/v1/workspaces/{workspaceID} | Valid session and workspace ownership |

`PUBLIC_ORIGIN` must match the exact external browser origin. In production it must be HTTPS. Do not accept forwarded user identity or IP headers without explicitly configured trusted proxy support.

## Limitations and further work

This stage implements JSON APIs, not templ/HTMX forms (Group 5). It has no password reset, email verification, MFA, shared organizations or external OAuth yet. Account and IP throttling behind an ingress proxy should be reviewed with trusted proxy/edge protections before production; currently the socket peer IP is used. Expired sessions remain in the table until routine maintenance, but cannot authenticate.

Future financial writes must always derive workspace IDs from the validated session and apply CSRF protection to every state-changing handler. No ledger, expense or budget endpoints exist yet. The server never performs migrations automatically. Isolated integration tests verify ownership and authentication behavior.
