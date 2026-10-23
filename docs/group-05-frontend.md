# Group 5 — templ + HTMX + Tailwind front-end

The runtime UI uses Go's Chi server with `github.com/a-h/templ` v0.3.1020 for server-rendered HTML, HTMX 2.0.11 for progressive partial updates, and Tailwind 4.3.3 for styling. None are fetched from public CDNs at request time.

## Routes and progressive enhancement

| Route | Behavior |
| --- | --- |
| GET / | Redirect to /dashboard for valid sessions or /login |
| GET /login, /register | Accessible HTML forms with native submission fallback |
| POST /login, /register | Validate form; issue session cookie; 303 redirect or HX-Redirect |
| GET /dashboard | Authenticated shell with responsive navigation |
| GET /dashboard/activity | Authenticated HTMX fragment |
| POST /logout | Session revocation; requires exact Origin and form CSRF token |
| GET /assets/app.css, app.js, htmx.min.js | Self-hosted embedded assets |

The original `/api/v1/auth/*` JSON endpoints from Group 4 remain unchanged. The browser form handlers call the same database and password service, issue the same cookie policy, and enforce the same origin and rate limits. `/dashboard` does **not** accept caller-supplied workspace IDs.

## Security properties

- templ escapes dynamic user email and attribute values. Do not use unescaped HTML strings for user-controlled content.
- Login/register must supply an exact `Origin`; POST logout also requires the per-session CSRF token. JSON mutation endpoints preserve Group 4 protections.
- Authentication errors reveal no raw database errors or credentials. HTMX invalid-form feedback is a small HTML fragment and uses HTTP 200 for standard htmx swapping; native HTML requests preserve 4xx status codes.
- Logout is a normal server POST, with a hidden token; it works even when HTMX/JS are disabled.
- The web page references only same-origin scripts; the existing Content Security Policy blocks third-party script sources. Cookies remain HttpOnly/SameSite=Lax, with Secure and `__Host-` on HTTPS production.
- All sensitive pages include `Cache-Control: no-store`.
- Theme is stored in the browser's localStorage as a non-sensitive preference. It is not a security or session store.

## Asset generation

`npm run assets:build` compiles `web/styles/base.css` via pinned Tailwind CLI, copies htmx from the installed npm package, and copies progressive `web/scripts/app.js`. Generated files are placed under `internal/assets/` and embedded into the Go binary. The editable inputs remain in the repository; generated artifacts and `internal/views/*_templ.go` are ignored and recreated reproducibly in CI and Docker.

The templ command `go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate` builds Go renderers from `internal/views/pages.templ`. Do **not** hand-edit the generated Go code.

## Current boundaries

The dashboard cards intentionally show **Not set up** instead of fabricated balances. Accounts/wallets, ledger, and budgeting features arrive in Groups 6–10. Full production design auditing, notification UI, and financial reporting are later roadmap items. The initial Theme toggle is a small progressive enhancement, while native form submission, navigation, and logout do not depend on JavaScript.
