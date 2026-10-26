# Group 11 — Recurring bills and subscriptions

## Financial semantics

A recurring schedule is **not an expense or a ledger posting**. Its generated due occurrences are read-time projections in UTC calendar days and do not create financial transactions. A due occurrence may be marked:

- `upcoming`, `due` or `overdue` — derived from the schedule and today's UTC date, with no financial side effect.
- `paid` — references exactly one pre-existing, workspace-owned **expense** transaction that matches the funding account, currency, expense category and scheduled category-split amount.
- `skipped` — permanently records that occurrence as skipped and makes no expense or ledger entry.

Paid/skipped records are append-only; an occurrence cannot be both. One financial expense may be linked to at most one recurring occurrence. A payment action NEVER calls ledger posting or `transactions.Create`, so marking an already-recorded expense as paid cannot double-count spending or trigger credit-card repayment. The user records actual money spent on the Transactions page, then chooses the matching expense on the Recurring page.

**Amount-matching policy:** the selected expense must contain a category split matching the scheduled amount exactly. An expense with multiple categories can match a recurring line via the relevant split, but the same expense cannot be reused for another due occurrence. This phase does not implement partial/instalment settlement or an invoice payment gateway.

## Frequency rules and due dates

Supported frequency values are `daily`, `weekly`, `monthly`, `quarterly` and `yearly`. UTC date arithmetic is deterministic. Monthly/quarterly/yearly schedules always anchor to the **original start day**, never to a previously clamped day:

- Jan 31 monthly -> Feb 28 (or Feb 29), then Mar 31.
- Feb 29 yearly -> Feb 28 in non-leap years, then Feb 29 in leap years.
- Every weekly occurrence is precisely seven calendar days after the original due date.

The optional schedule end date is inclusive. Past occurrences remain inspectable when a schedule is archived, but archival stops future unsatisfied projections and rejects new payment/skipped actions. Existing payment history is retained unchanged. To change a frequency, amount, category or account, archive the old schedule and create a new one (no retroactive mutation of the existing agreement).

`GET /recurring` shows occurrences within **30 days in the past and 90 days ahead**, with an upper bound to protect rendering. Payment and skip history shows the most recent 100 decisions, and schedule listings are capped at 100. This is an in-app view; there are no emails, push notifications or background jobs in Group 11. Such reminders belong in Group 18.

## Schema

Goose migration `00008_recurring.sql` adds:

- `recurring_schedules` — workspace-owned immutable terms with schedule type, exact integer minor amount, AED/PHP, funding account, expense category, frequency, inclusive start/end dates, request hash, idempotency key and one-way `archived_at`.
- `recurring_occurrence_actions` — immutable payment/skip history, due date, status, optional linked finance expense ID, idempotency key and request fingerprint.
- Foreign keys are workspace- and currency-scoped. A database trigger checks that paid links are genuine expense transactions, with matching account/category/split amount, even for direct SQL writers.
- A uniqueness constraint prevents two actions on one scheduled due date, and another prevents reusing an expense transaction for multiple obligations. Idempotency uniqueness is scoped per workspace.
- Triggers reject delete or update of actions; schedules only permit a one-way archive transition. Goose downgrade refuses when there is live schedule or occurrence data.

## Concurrency and security

PostgreSQL transactions ensure atomic checks and writes. Transaction-scoped advisory locks serialize identical request keys and competing outcomes for the same schedule/due date; racing requests with identical payloads return the same ID, and conflicting keys or a different action on the already settled due date fail. All account, category, schedule and expense lookups are constrained to the authenticated workspace.

The browser routes `GET /recurring`, `POST /recurring`, `POST /recurring/record` and `POST /recurring/{id}/archive` use server-rendered templ forms. Native forms work without JavaScript; the new-schedule and paid forms also support HTMX partial error feedback and `HX-Redirect`. Writes require an authenticated session, matching Origin and session-bound CSRF token, and never accept a workspace ID from the browser. Notes/names render through templ escaping.

## Verification

Unit tests validate anchoring, leap years, date windows, fingerprints and invalid inputs. PostgreSQL 17 integration tests validate valid linked expense, idempotent concurrent payment marking, duplicate expense prevention, insufficient/wrong-amount or cross-workspace links, skip history, no ledger effect, immutable payment data, archive behavior, native forms and Origin/CSRF enforcement. CI verifies Go 1.27.3 Linux/Windows, race testing, sqlc 1.31.1 output, Node.js 24 assets, Goose up/down/up on an empty finance database and Docker.

## Limitations

- Upcoming/overdue is shown on the web page only. No alert delivery or scheduled background processing.
- The payment form lists the latest 25 expense transactions from the workspace. Older matches still work via the service, but a search/selector for older expenses is future work.
- No automatic charges, card processing, reminders, partial payments, variable invoices, receipt attachments or automatic vendor sync.
- Future plan changes require archiving and recreating rather than editing historical schedule records.
