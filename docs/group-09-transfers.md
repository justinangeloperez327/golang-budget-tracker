# Group 9 — Internal transfers, FX and reconciliation

## Purpose

Group 9 moves existing value between **accounts owned by the same workspace** without classifying it as income or expenses. Credit-card repayments reduce the liability, not add a new expense. Explicit transfer fees are expenses. Reconciliation posts differences to a dedicated equity adjustment account rather than disguising unexplained changes as income/expenses. Balances remain derived exclusively from immutable ledger journals.

## Rules

* **Source account:** active cash, bank, savings or e-wallet; never credit/loan funding (cash advances and loan movements are later work). **Target account:** active asset or credit card; not a loan (dedicated principal/interest debt workflows are Group 12).
* Same-currency transfer: `DR destination asset or liability / CR source asset`; if there's a fee, `DR transfer_fee expense` with an additional source credit. Thus only the fee counts as expense.
* Cross-currency transfer: debit a source-currency `fx_clearing` equity account, credit source asset (plus a debit to the source-currency fee expense if charged). Independently debit destination account, credit its own currency's `fx_clearing`. **Both journals balance separately in AED or PHP.** No numeric AED+PHP summing.
* FX is always explicitly provided as destination *major units* per one source major unit, with up to 8 fractional decimals, stored as an integer scaled by 100,000,000. All amounts are integer minor units; expected destination amount is `round_half_up(source_minor * rate_units / 100000000)` using arbitrary-precision integer arithmetic to avoid floating-point error. The user must also supply the expected received amount; mismatches are rejected before posting.
* Source amount plus fee must be within supported limits; zero/negative source payments, FX overflow, invalid rates, currency and workspace mismatches and transactions to the same account are rejected.
* **Card repayment:** debit the credit-card liability, credit bank/cash; purchases were already expensed in Group 8. This operation creates **no new purchase expense**.
* **Reconciliation:** lock an account row, calculate the authoritative ledger balance at the posting instant, and post the signed difference to `reconciliation_equity` in the same currency. For an asset, an increase debits the account; for a liability, an increase credits the liability. Zero differences create a durable no-op reconciliation record with no journal. The observed statement amount is nonnegative; use positive outstanding debt for cards/loans. This is a current-balance adjustment, not retrospective replay of historical statements.

## Data model

Goose migration `00006_transfers.sql` expands allowable ledger system accounts and introduces append-only `transfer_operations` and `account_reconciliations`. The schema binds source/target accounts, currencies, journals and workspace through composite foreign keys. Transfers store send/receive/fee, optional scaled FX rate, original request hash, source and optional destination journal IDs. Reconciliation records preserve previous/observed/delta amounts, note, date and optional journal ID. Triggers prevent post-commit mutation or deletion. Migration down is **blocked after money movement, reconciliation or any associated ledger clearing accounts exist**.

## Idempotency and concurrency

The Go domain service uses `pgx.BeginFunc` for all related metadata and ledger postings. PostgreSQL transaction advisory locks serialize repeated workspace-scoped idempotency keys; each operation stores a SHA-256 hash of canonical financial inputs. Identical requests return the original operation ID; different requests with the same key are rejected. Transfers lock source/destination account records in deterministic ID order. Reconciliation locks its account and calculates the current ledger balance under that lock. These locks prevent reconciliation racing against ordinary account-domain writes, while journal tables enforce per-currency balanced, immutable, sealed postings.

The service's trust boundary accepts the workspace from the authenticated session, never from POST input. Form writes require exact `Origin` and per-session CSRF token. No generic public ledger journal-write endpoint is provided.

## Pages

* `GET /transfers`: templ dashboard with account selectors, source fee, FX rate/received amount, an accessible reconciliation form and histories.
* `POST /transfers`: native-HTML or HTMX transfer. Successful requests redirect to `/transfers`; validation errors have inline HTMX feedback.
* `POST /reconciliations`: native-HTML or HTMX reconciliation. Only active owned accounts may be reconciled.
* Account balances on `/accounts` and `GET /api/v1/ledger/balances` automatically reflect posted transfers and reconciliation entries.

## Operational notes

Apply migrations explicitly using a migration-capable DB account; the server never auto-applies migrations. PostgreSQL and Go CI exercise same-currency and FX balance changes, fee accounting, credit-card repayment, concurrent idempotency, workspace isolation, zero/delta reconciliation, database immutability, migration down/up before recording money, and browser Origin/CSRF/HTMX behavior. Linux/Windows Go 1.27.3, sqlc 1.31.1, Node 24 and Docker must all pass.

**Deliberate limitations:** no actual bank API settlement, bank imports or statement matching, FX realized-gain valuation, FX provider quotes, transfer cancellation, external remittance or loan repayments. The `fx_clearing` accounts record currency-local clearing legs; later reporting should explicitly distinguish transfer valuation from expense/income rather than interpreting either as a profit. Reports remain currency-separated until a rigorously defined exchange-rate basis is introduced.
