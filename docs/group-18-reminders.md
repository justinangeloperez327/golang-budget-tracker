# Group 18 — In-app reminders and safe actions

The reminder inbox at `GET /reminders` derives due, overdue, and next-14-day items from existing recurring schedules and PHP contributions. It uses a bounded 30-day overdue lookback and displays at most 100 items. This does not create expenses, mutate finance ledgers, or send external notifications.

A session is required; the workspace is determined only by the authenticated actor. Dismissal requires same-origin form submission and CSRF. `POST /reminders/dismiss` validates kind, ID, and ISO date; checks the reminder against outstanding workspace-owned obligations, and persists the dismissal with a composite unique key. Exact request replay is idempotent. Other users cannot read or dismiss another workspace's reminders. A dismissed reminder remains a financial obligation until a normal payment or skip is recorded.

Existing financial create/record operations already use scoped idempotency keys and fingerprint checks. On uncertain save results, retry the original submission **with the same request key**, rather than generating a new one. A changed payload with the same key must fail rather than create another posting. The reminder feature does not introduce an alternate money-posting endpoint.

Scope limits: the inbox is on demand, not a background scheduler; it does not enqueue email, push notifications, or persist unsent form drafts. A retry after a page refresh that generates a new request key is not guaranteed to deduplicate. Historical reminder dismissal does not erase the underlying obligation.
