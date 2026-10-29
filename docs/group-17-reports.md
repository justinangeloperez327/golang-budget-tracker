# Group 17 — Financial reports, account histories and secure exports

## Routes

Authenticated, session-owned workspace only:

- `GET /reports?period=2026-10&currency=AED`: full templ monthly or annual report page.
- `GET /reports/summary?period=2026&currency=PHP`: HTMX fragment containing the same report.
- `GET /reports/export.csv?period=2026&currency=PHP`: UTF-8 RFC 4180 CSV download, with formula-cell mitigation and fixed seven-column record width.
- `GET /reports/export.pdf?period=2026-10&currency=AED`: paginated printable PDF 1.4 using Go standard library, with escaped text streams.

The period is an exact `YYYY-MM` month or `YYYY` year (2000–2100), never an unbounded or injectable SQL expression. Currency must be AED or PHP. Defaults are current UTC month and AED. Invalid filters return HTTP 400. Every route checks the session before retrieving any data, and the workspace ID is never accepted from the URL. Downloads set `Cache-Control: no-store`, a fixed safe filename, `Content-Disposition: attachment`, a typed content type and `X-Content-Type-Options: nosniff`. All user data in templ is HTML-escaped; CSV cell content that could be treated as a spreadsheet formula is quoted defensively; PDF text strings are escaped for PDF operators.

## Reporting definitions and date basis

**Every report is denominated in exactly one currency.** Never aggregate AED and PHP into one total; cross-currency FX clearing is intentionally excluded from operating income/expense.

`finance_transactions.occurred_on` determines **monthly/annual** income, expenses, refunds, category allocation and the month-comparison series. These are the same immutable transaction records used in Group 8 and dashboard Group 16.

`ledger_journals.created_at` determines transfer-fee expense and loan-interest expense, plus **historical account positions** and movement. This is a real *posting time* report, **not** a backdated bank statement and **not** a month-end balance generated from the user-selected `occurred_on` dates. If an expense is recorded today against an old occurred date, its categorized amount appears in the old period but its bank-account debit/credit appears in the current posting month.

- `gross_expenses = SUM(finance_transactions.total_minor WHERE kind='expense')`
- `refunds = SUM(finance_transactions.total_minor WHERE kind='refund')`
- `net_categorized_spending = gross_expenses - refunds`
- `other_expenses = transfer_fee ledger debit minus credit + loan_interest ledger debit minus credit`
- `operating_net = income - net_categorized_spending - transfer_fees - loan_interest`

**Operating net is a categorized operating-flow indicator, not a full bank cashflow statement**. It excludes internal transfers, loan principal, credit repayment, opening balances, currency settlement and reconciliations. Those are nevertheless included in account-by-account posted movements below.

## Category breakdown

The report groups immutable `finance_transaction_splits` by workspace-owned category and shows gross allocations, refund allocations and net values. Income category rows remain positive; expense-category refunds reduce the original expense. The report does not simply subtract a refund as new negative income. The category row aggregation is independent of Group 10 configured budgets, so spending is visible even for unbudgeted categories.

## Monthly comparison and annual rollup

A monthly report has one month row. An annual report has all twelve month rows, including months without transactions, with income, gross expense, refund, fee, loan-interest and net figures. Annual totals and individual monthly rows follow the same date basis. No 25-record UI pagination is used to calculate totals: SQL aggregates all workspace-owned rows in the selected period.

## Account movement and historical net worth

For every owned account in the selected currency, including archived accounts:

- Opening = sum of ledger postings strictly before `start`, evaluated with the account's normal debit/credit direction.
- Debits / credits = journal `created_at` in `[start, end)`.
- Closing = opening + debit minus credit for assets, or opening + credit minus debit for credit/loan liabilities.
- Opening assets, opening liabilities and opening net worth are separately totaled.
- Closing assets, closing liabilities and closing net worth are separately totaled.

This ledger-derived boundary history is supported by Group 7's sealed immutable journals. Account histories include internal movements, savings allocations, loan principal payments and reconciliations as *movements*, without reclassifying them as spending. The report does not compute daily FX-valued historical consolidated wealth; **no AED-to-PHP translation** occurs.

## Budget-versus-actual

Group 10's existing `budgets.Store.Overview` is the authoritative budget implementation. For a monthly report, use the selected month; for an annual report, independently compute each month and sum configured months. Each report shows planned allocations, carried positive balances, refund-aware actual spending, remaining budget and per-category breakdown. A configured-budget total is not falsely presented for unconfigured months. Annual carried totals are the per-month rollover amounts, **not new money entering the account**.

Core financial values are read inside a PostgreSQL `REPEATABLE READ, READ ONLY` transaction, providing internally consistent snapshots of posted account movements, category totals and monthly operating figures. Group 10 budget projections currently execute via their existing pool-backed implementation, so budget figures may briefly lag other sections during concurrent writes; refreshing restores consistency. The app does not create a new report balance or duplicate journal.

## Exports

The CSV contains a summary, category report, month comparison, account movement and budget variance sections. It uses seven fields for every row, UTF-8 output and Go's standard CSV quoting. User-provided cell content beginning with spreadsheet formula sigils (even after whitespace) receives a leading apostrophe.

The PDF contains the same major sections, wraps lengthy names, paginates on US Letter pages, escapes parentheses and backslashes in PDF text operators, and emits proper Catalog/Pages/Page/Font/Contents/xref/trailer structures. To avoid unpinned new dependencies and embedding private font files, the PDF uses PDF's standard Helvetica font and replaces non-ASCII characters with `?`; for complete Unicode names, use the UTF-8 CSV.

Downloads are read-only, workspace-authorized, dynamically generated and not written to disk.

## Verification and limitations

Unit tests cover monthly/year validation, signed integer precision, overflow guards, CSV spreadsheet-injection escaping, PDF structure and pagination. PostgreSQL 17 integration tests verify refund-aware category totals, fee treatment, current and past posted balances, account movements, AED/PHP isolation, annual 12-month rollups, budget variance and authenticated native/HTMX/export endpoints (including anonymous denial and cross-workspace privacy). Go 1.27.3 Ubuntu/Windows, sqlc 1.31.1, Node.js 24 and Docker/Compose remain required CI jobs. No migration is necessary for this read-only Group 17.

Not included: true historical bank-statement reconciliation, daily converted net-worth series, arbitrary custom report columns, queued long-running exports, automated scheduled delivery, or cashflow analysis by counterparty. These should not be represented as implemented features.
