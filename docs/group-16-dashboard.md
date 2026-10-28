# Group 16 — Unified financial dashboard

## Scope and accounting policy

`GET /dashboard` provides a **read-only** financial overview derived from existing Group 6–15 records. It creates no account balances, materialized snapshots, expense transactions or journals. No database migration is needed: the current **Group 15 schema marker** remains authoritative. Historic finance features and ledger invariants are unchanged.

The dashboard displays **AED and PHP side by side, never merged**. There is no inferred base currency, no implicit FX conversion and no misleading cross-currency net worth total.

### Balance sheet (live balances, not month-end snapshots)

For each currency:

- **Assets:** sum of current account-backed ledger balances for `bank`, `cash`, `savings` and `ewallet`.
- **Liabilities:** sum of current balances for `credit` and `loan` accounts; these already use the correct credit-normal accounting convention.
- **Net worth:** assets minus liabilities.

Archived accounts with remaining balances are still included; archival does not erase a real financial position. Balances are *current*, even when a historical reporting month is selected. This dashboard must not be described as a historical balance sheet or an FX-adjusted total.

### Monthly income and expenses

For the selected `YYYY-MM` reporting month, aggregate the immutable `finance_transactions` using `occurred_on`:

- Income = positive `kind='income'` totals.
- Gross categorized expense = positive `kind='expense'` totals.
- Refunds = positive `kind='refund'` totals.
- **Net categorized spending = gross categorized expense − refunds.**
- **Other posted costs = dedicated `transfer_fee` and `loan_interest` ledger debits minus credits, grouped by posting month (`ledger_journals.created_at`).**
- Net monthly activity = income − net categorized spending − other posted costs.

These figures are not account-balance deltas or a strict cashflow statement. Transfers between accounts, principal debt repayments, MP2 savings deposits, reconciliations, opening balances and FX clearing are not classified as income or categorized spending. Transfer fees and loan interest are **expenses in the ledger**, shown separately because Group 8 category splits do not currently include them. Since these costs are grouped by journal creation timestamp, a backdated transaction can contribute to the selected transaction month while its associated fee is shown in the later posting month. This distinction is explicit in the UI.

The aggregation performs money arithmetic as signed `int64` minor units and uses PostgreSQL `numeric` sums, rejecting results that exceed representable `int64` rather than silently wrapping.

### Existing module projections

- **Budgets:** call `budgets.Store.Overview` for both AED and PHP for the selected month. This reuses immutable budget revisions, refund-aware actuals, category allocations, overspending flags and the Group 10 rollover policy rather than duplicating the budget engine.
- **Savings:** call `savings.Store.Goals`, showing the existing ledger-derived balances, targets and percentage progress. Do not interpret savings deposits as expenses.
- **Debts:** call `debts.Store.Plans` for outstanding loan principal and completed scheduled installments, and `Installments` for future repayment information. Principal is a liability reduction; loan interest is included in separately posted expenses.
- **Bills:** call `recurring.Store.Upcoming` and show up to eight actionable due, overdue or upcoming occurrences through 30 days ahead, from its bounded 30-day-back/90-day-forward projection. Recording a recurring bill as paid does not create a second expense.
- **Philippine contributions:** call `contributions.Store.Obligations` and display up to eight due/overdue PHP obligations from the 100 most recent periods. MP2 transfers are not expenses, and due dates are user-entered, not official statutory calculations.
- **Recent activity:** call `transactions.Store.List` and display up to eight latest immutable categorized financial events.

### UX and security

The native GET form `/dashboard?month=YYYY-MM` produces a complete server-rendered templ page. With HTMX, the same form requests `GET /dashboard/summary?month=YYYY-MM` and replaces `#dashboard-summary` without a full navigation. The existing authenticated `/dashboard/activity` endpoint remains supported, now rendering real recent finance activity instead of a placeholder. The response is `Cache-Control: no-store`.

All three endpoints require a trusted authenticated session; the workspace ID comes from the session, **never from URL or form values**. Month selection is validated within `2000-01`–`2100-12`, and untrusted form text is escaped by templ. No dashboard POST endpoints or mutations are introduced.

The dashboard is a live aggregation across existing module reads, not a single MVCC point-in-time transaction spanning all modules. Under simultaneous postings, briefly inconsistent cross-panel read timing is possible; refreshing resolves this. Group 19 security/performance review may upgrade to a shared read-only consistent snapshot if needed.

## Verification

Go unit tests validate asset/liability classification, archived balances, signed arithmetic and overflow handling. PostgreSQL integration tests verify AED/PHP isolation, current net worth, income, gross expense, refund-aware net spending, Group 10 budget allocations/remaining totals, savings goals, outstanding debts, overdue bills/contributions, recent activity, month selection, authenticated full/HTMX requests, invalid month rejection and cross-workspace privacy.

Standard CI verifies Go 1.27.3 Ubuntu 24.04 (including race tests), Go 1.27.3 Windows 2022, PostgreSQL 17 migration up/down/up and integration, sqlc 1.31.1, Node.js 24 asset checks and Docker/Compose.

## Out of scope

This release does not implement downloadable CSV/PDF financial reports (Group 17), scheduled reminders (Group 18), FX-adjusted cross-currency consolidated financial statements, bank reconciliation forecasts, historical month-end balance snapshots or a fully audited consolidated cashflow statement. These remain separate future tasks.
