# Group 14 — AED→PHP remittances and settlement

## Accounting and status semantics

Group 14 builds on the **existing Group 9 transfer engine**, rather than creating its own FX ledger implementation. This prevents two independent transfer paths from diverging or posting an identical remittance twice.

An immutable `remittance_orders` row represents a **pending intent only**, with no journal or balance impact. It records the workspace's AED source account, PHP destination account, send amount **excluding** fees, AED fee, quoted PHP received, FX quote, fee-adjusted effective rate, note, date, and SHA-256 idempotency fingerprint. Each account must be an active workspace-owned asset account (cash, bank, savings or e-wallet). Liabilities and reverse PHP→AED requests are not supported by this UAE→Philippines workflow.

`POST /remittances/resolve` takes one of two terminal actions:

- `settled`: with available AED funds for the send plus fee, and enough representable PHP balance capacity, it invokes `transfers.Store.TransferTx` **inside the same PostgreSQL transaction** that writes the linked `remittance_resolutions` event. Group 9 creates exactly two currency-local balanced journals: AED source credit, AED FX clearing debit, AED fee-expense debit (when fee > 0); and PHP recipient debit, PHP FX clearing credit. Its own immutable `transfer_operations` row is linked from the remittance resolution. A failed resolution rolls back both money and metadata.
- `cancelled`: stores an immutable terminal event and **never calls the transfer engine**. No ledger entry, fee or receipt is posted.

The unique workspace/order resolution constraint prevents both duplicate settlements and cancellation after settlement. Retried identical requests return the original resolution ID; a reused key with a different payload causes a conflict. A PostgreSQL trigger verifies that a `settled` resolution refers to the single Group 9 transfer with the exact order accounts, AED/PHP currencies, send, fee, received, quoted rate and settlement date. The transfer idempotency key is the immutable remittance order ID. The database also prevents a transfer from being associated with multiple remittances. Both orders and terminal events are append-only; the Goose down migration refuses to delete existing remittance history.

**Important distinction:** “settled” means **posted to the personal ledger**; the app does not initiate a real transfer, and does not confirm provider payout or actual bank settlement. Record it as settled only after independently verifying the AED debit and PHP credit. Pending orders do not reserve money, nor do they create pending receivables.

## Exact currency and FX rules

The quoted rate is **PHP major units per 1 AED major unit** (up to eight decimal places). The receive amount must exactly equal `round_half_up(send_minor * quoted_rate_units / 100000000)` using arbitrary-precision integers; the user must enter both quoted rate and received amount. Values are stored in minor units (AED fils, PHP centavos). No floating-point monetary arithmetic is used.

An effective rate includes AED transfer costs:

```
total_aed_cost_minor = send_aed_minor + fee_aed_minor
effective_php_per_aed_units =
  round_half_up(received_php_minor * 100000000 / total_aed_cost_minor)
```

The numerator and denominator are both minor units, so scaling is exact. The effective rate is reported to eight decimal places. Example:

| Field | Example |
|---|---:|
| AED amount sent | 1,000.00 |
| AED transfer fee | 5.00 |
| Quoted rate (PHP/AED) | 15.50000000 |
| PHP received | 15,500.00 |
| Total AED cost | 1,005.00 |
| Effective PHP/AED | 15.42288557 |

The amount sent is not treated as an expense or new income; only the explicit AED fee is recorded as a ledger expense. The Group 10 category budget view does not automatically assign transfer fees to a category. Group 16–17 reports should classify fee expense and FX clearing correctly without converting both currencies into a single unsourced figure.

## Security and concurrency

All remittance actions are workspace-scoped from the authenticated session (not a form field), require correct Origin and session-bound CSRF, and use native-HTML or HTMX forms. Each unique request is serialized by a workspace-specific PostgreSQL transaction advisory lock. Settlement locks the remittance row, then locks source/destination accounts in deterministic ID order, reads authoritative ledger balances, and calls Group 9's reusable transaction-scoped settlement API. This preserves atomicity with other authenticated account transfers, reconciliations and goal postings that lock those same account rows.

Transactions have unique workspace-scoped request keys and canonical fingerprints; retries are safe, and a different action cannot resolve a finalized order. The UI renders a bounded history of 100 orders and displays pending/settled/cancelled states with clear explanations.

Routes:

- `GET /remittances` — authenticated remittance entry and history.
- `POST /remittances` — pending order.
- `POST /remittances/resolve` — settle or cancel.

## Verification

Go unit tests validate quotes, precision, effective fee-adjusted rates, date and account validation, fingerprints, and settlement state inputs. PostgreSQL integration tests cover concurrent exactly-once settlement, balanced AED and PHP journals, cost/fee allocation, cancelled orders with no money movements, liquidity failures, workspace isolation, currency rejection, immutable records, direct SQL mismatched transfer rejection, browser CSRF, native HTTP forms, and HTMX feedback.

GitHub Actions checks Go 1.27.3 Linux/Windows (including Linux race tests), PostgreSQL 17 migration + integration, sqlc 1.31.1 generation, Node.js 24 assets, and Docker/Compose. Goose migration `00011_remittances.sql` must be applied explicitly before starting the server.

**Not included:** real remittance provider integration, payout notifications, cancelled/bounced external transfers, variable settlement-time exchange rates or bank fee confirmation. Rate and amounts cannot be edited after an order is created: cancel and create a new order with a corrected quote.
