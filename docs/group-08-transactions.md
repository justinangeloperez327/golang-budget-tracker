# Group 8 — Income, expenses, splits and refunds

Group 8 extends Group 7's immutable double-entry ledger rather than maintaining a separate running balance.

## Accounting policy

* **Income into an asset:** debit the bank/cash/savings/e-wallet account and credit its income category ledger account.
* **Expense from an asset:** debit one or more expense category ledger accounts and credit the funding account.
* **Credit-card purchase:** debit expense categories and credit the card's liability account, increasing the amount owed without reducing a bank account.
* **Refund of an expense:** debit the *same* source asset or card liability account and credit the original expense categories. The original expense remains unchanged.
* Refunds are distinct **refund transactions**, never counted as fresh income. Refunded amounts are limited to the unused original allocation for each category.
* AED and PHP remain separate. Every category is tied to its currency and class, and a journal cannot post cross-currency lines.

All monetary values are integer minor units, never floating-point. No expense is created by an internal transfer; that comes in Group 9. Opening equity is independent of income and expense categories.

## Tables and migrations

Apply Goose migration `00005_transactions.sql`. It extends `ledger_accounts` with `income` and `expense` classifications and creates:

* `finance_categories`: workspace-owned, currency-specific income or expense categories with dedicated immutable ledger accounts. Category labels and classification are stable; archive marks unused categories.
* `finance_transactions`: immutable workspace-scoped income, expense and refund metadata; external idempotency key and SHA-256 request fingerprint; account, currency, date, note, associated journal, optional refunded expense.
* `finance_transaction_splits`: one or more immutable allocations; currency/workspace foreign keys; a deferred database trigger rejects totals that do not match the parent transaction amount.

The transaction + all splits + balanced journal are saved in **one PostgreSQL transaction**. A failed validation or commit constraint rolls back every layer. Duplicate keys with identical content return the original transaction ID, including under concurrent requests. Reusing the key with different metadata, account or allocations returns a conflict. Refunds lock the original expense to serialize the remaining refundable amount across requests.

**Rollback policy:** the Group 8 down migration refuses to run after a category or financial transaction exists. Dropping journal-associated transaction classifications is destructive and is deliberately blocked. CI verifies the up/down/up cycle *before* posting test data; production data must be preserved.

## UI and navigation

`GET /transactions` renders a server-side templ page with income/expense/refund entry, category creation, expense linking for refunds, account and category selectors, and a searchable activity list. Four split rows are visible (the backend supports up to eight). Native HTML forms work without JavaScript; HTMX enhances filtered history, feedback and successful redirects.

`POST /transactions`, `POST /categories` and `POST /categories/{id}/archive` require an authenticated, workspace-scoped session, exact browser Origin and per-session CSRF token. The server **ignores client workspace IDs**, checks all account/category/currency ownership, and escapes account names, category names and user notes in templ.

Filters include type, account, note/account/category search and inclusive date range. Paging is limited to 25 records per page with bounded page numbers. The refund selector shows recent expenses; the service accepts older expense IDs only if they belong to that account and workspace.

## Boundaries

* No public unrestricted posting endpoint; all new ledger writes are created by the transaction domain.
* No editing/deleting posted transactions. Use correctly categorized refunds or account reconciliation adjustments, rather than rewriting journal history. Transaction corrections are a later workflow.
* Payments between owned accounts, credit card repayments, FX/remittances and reconciliation will be implemented in Group 9.
* Archive a category only to prevent new non-refund use; historical splits and refunds retain the original category.
* A category must exist before recording an expense/income. No seeded assumptions about a user's financial categories.
* The HTML form offers four allocation rows and a fixed set of recent refund references; the backend supports eight allocations.
* This phase doesn't implement receipt uploads, recurring templates, attachments or tax classification.

## Verification

`go test ./...` checks normalization, exact money parsing, canonical request hashing and filtering. The isolated `go test -tags=integration ./tests/integration` suite checks real PostgreSQL journals and category currency/ownership, credit card debt, split totals, refunds and over-refund rejection, concurrent idempotency, immutable database history, CSRF, HTML escaping and HTMX navigation. CI also validates Go 1.27.3 on Windows and Linux, Node 24, sqlc generation and Docker.
