# Group 10 — Monthly budgets and AED/PHP allocations

## Model and accounting boundaries

Budget plans are **not ledger postings**. They are forecasting metadata. Actuals are derived directly from immutable Group 8 `finance_transactions` and `finance_transaction_splits`; credit-card purchases count as spending once, even though repayment through Group 9 is only an internal transfer. Group 6 opening balances, Group 7 opening equity, Group 9 account transfers, transfer fees and reconciliation-equity adjustments do not appear in category actuals. Transfer fees currently use a system expense account and are **not** allocated to a user category; classify them separately in reports later.

Every budget has a separate **workspace / month / currency / expense category** context. Never add AED and PHP into a combined total. All amounts are integer minor units (fils or centavos), with no floating-point arithmetic.

## Schema and updates

Goose migration `00007_monthly_budgets.sql` adds `monthly_budget_events` and a typed foreign-key constraint that only permits **expense** categories belonging to the same workspace and currency. Events have immutable action `allocation` or `policy`, a first-of-month date, unique workspace-scoped idempotency key, SHA-256 fingerprint, and server timestamp. Database triggers reject UPDATE and DELETE.

The latest event for a category sets its **allocated amount**, including zero to clear an allocation. Each month also has its independently configurable rollover policy, defaulting to `none`. `carry_positive` enables positive unused category balances from the **immediately prior configured month**, with separate balances for each category. Allocation and policy changes are appended, not overwritten. Historical revisions remain auditable.

Retries using the same idempotency key + identical inputs return the original event ID. Reusing a key with different inputs is rejected. A PostgreSQL advisory transaction lock serializes concurrently submitted keys. Writes validate exact decimal money, calendar month, currency, active owned category and expense classification before committing.

## Net actuals and rollover

For the selected budget month, category net actual is:

```
Actual(category, month) =
  SUM(expense split amount for transactions dated that month)
- SUM(refund split amount for refund transactions dated that month)

Effective(category, month) =
  Allocated(category, month) + Carried(category, month)

Remaining(category, month) = Effective - Actual
Overspent = Remaining < 0
```

Refunds are applied in the **refund posting month** (not retroactively to the original expense month). This can produce negative monthly net spending and increases that month's available amount; it is **not income**. Actuals include unbudgeted expense categories, so no plan means a negative remaining amount when spending is recorded.

The `carry_positive` computation uses the previous month's **positive** remaining category balance, including that month's own carry, if and only if the previous month had at least one budget event. Missing months interrupt the chain, and `none` resets carry. Overspending never carries forward as a negative debt. Edits or backdated transactions automatically recompute subsequent rollover projections on the next read. All monthly totals remain bounded by supported integer limits.

## Browser

* `GET /budgets?month=YYYY-MM&currency=AED` or `currency=PHP`: authenticated templ monthly overview and currency-specific category breakdown. Default month uses server UTC.
* `POST /budgets/allocations`: change a category's allocation with a one-time form key.
* `POST /budgets/policy`: set `none` or `carry_positive` for that month.
* Forms are native HTML enhanced by HTMX; accessible status feedback, loading indicators, responsive navigation and overspending labels are provided.
* All POSTs check authenticated session, matching Origin, Content-Type and session-bound CSRF; the workspace ID is taken only from the authenticated actor, never client input.
* HTMX GET swaps only the `#budget-main` fragment; successful writes use `HX-Redirect` and native forms use HTTP 303.

To add a category, use the existing **Transactions** page. Archived expense categories remain in historical breakdowns but cannot receive new allocation revisions.

## Verification

PostgreSQL integration tests cover accurate expense/refund splits, credit-card charges, transfers excluded from actuals, back-to-back rollover, overspending, AED/PHP isolation, future and backdated plans, unique concurrent idempotency, immutable history, foreign/archived categories and browser CSRF/HTMX workflows. Unit tests cover decimal precision, normalization, policy semantics, missing-month rollover breaks, unbudgeted actuals and overflow. CI checks Go 1.27.3 Linux/Windows, PostgreSQL 17 + Goose down/up **before** financial records, sqlc 1.31.1, Node 24 and Docker.

Down migrations deliberately fail when a workspace has budget events; dropping immutable planning history is unsafe. The server never automatically applies Goose migrations.

## Limits

Group 10 does not automate budget targets, schedule bills, alert users, calculate currency conversions, or display transaction-category reports outside the selected month. Rollover calculations are live views and may change when prior periods receive new transaction postings; future accounting period locks are not part of Group 10.
