# Group 12 — Debt, loan amortization and ledger-backed installments

## Scope and accounting decisions

This phase plans repayments against **existing loan account liabilities** (Group 6 account type `loan`). Register an outstanding amount when creating the loan account; Group 7 already posts its opening liability and opening equity. Creating a debt plan **does not originate the loan a second time**, nor does it transfer borrowed funds into an asset account.

Each plan is tied to one active loan and one active funding account of the **same currency** (AED or PHP). Funding must be cash, bank, savings or e-wallet; a credit card or another loan cannot fund an installment in this release. One immutable plan per loan account is allowed, preserving a single authoritative repayment schedule. Plans capture the **actual signed loan liability balance** from the ledger at creation as principal. An empty loan liability cannot be planned.

### Amortization policy

Group 12 uses **equal-principal, declining-balance, monthly simple interest**. Annual interest is expressed as an APR percentage with up to two decimal places, stored in whole basis points (e.g. 12.50% = 1250). For each of 1–360 monthly periods:

- Principal due = floor(initial principal / installments), with the final installment paying the exact remainder.
- Interest due = round-half-up(remaining scheduled principal × APR basis points / 120000).
- Total installment = principal due + interest due.

All inputs and calculations use integer minor units (AED fils/PHP centavos) and arbitrary-precision integers for interest multiplication. Floating-point money is not permitted. Monthly due dates stay anchored to the original day: January 31 → February 28 → March 31; leap years preserve February 29.

**Important:** interest is a fixed scheduled calculation and is recognized as expense **when the installment is paid**, not accrued daily or capitalized. Paying early does not automatically recompute interest or generate a prepayment discount; renegotiated contracts and partial/balloon/variable-rate loan arrangements require future functionality.

### Exact ledger entries

For an example AED 112 monthly installment comprising AED 100 principal and AED 12 interest, the backend posts **one balanced journal**:

| Ledger account | Debit | Credit |
|---|---:|---:|
| Loan liability | 100.00 | — |
| Loan interest expense (`loan_interest`) | 12.00 | — |
| Funding bank/cash account | — | 112.00 |

A zero-interest installment posts only **two** lines. Principal repayment reduces the outstanding liability and is **never an expense**. Interest is recognized against a dedicated AED/PHP system expense ledger account. No Group 8 `finance_transaction` is generated, so there is no duplicate expense or double cash movement. Group 10 category budgets only aggregate Group 8 finance expense splits; system-account loan interest is **not yet allocated to a budget category**. Reporting integration arrives in Groups 16–17 and should incorporate this system expense.

The backend ensures funding asset balance covers the full installment and that the loan's authoritative ledger balance equals initial principal minus all previously paid principal. If reconciliation or another posting has changed the loan outside its plan, settlement fails safely with a conflict instead of double- or under-paying the debt. Installments are paid in order and in full.

## PostgreSQL schema and immutability

Goose `00009_debts.sql` adds:

- `debt_plans`: single workspace/currency-owned immutable contract with loan and funding account, initial principal, rate in basis points, number of periods, first due date, idempotency fingerprint.
- `debt_installments`: immutable principal/interest/total splits with dated ordered installments.
- `debt_payments`: append-only payment metadata, source accounts, immutable linked journal, planned splits, payment date and idempotency fingerprint.

Composite workspace/currency foreign keys prevent cross-workspace contamination; unique constraints reject reused installments and journal links. A PostgreSQL trigger checks that each payment exactly matches its parent contract and installment **and that the linked journal contains the correct 2 or 3 exact debit/credit lines**. Posted ledger entries and debt records cannot be updated or deleted. Migration downgrade is blocked when a loan plan or payment record exists. CI checks an empty-schema down/up cycle before creating finance records.

## Security and reliability

Payment processing is one `pgx` transaction: verify idempotency, lock plan, validate due installment and order, lock the two accounts in stable ID order, read current ledger balances, create/select interest expense ledger account, post and seal a balanced journal, then persist debt payment metadata. Any failure rolls back the whole operation. Per-workspace transactional advisory locks serialize repeated keys; identical retries return the original payment/plan ID, while changed payloads return a conflict.

Browser writes to `POST /debts` and `POST /debts/payments` require authenticated session, exact Origin, form content type, and session-bound CSRF. All workspace references come from the actor, never user-submitted IDs. New forms use server-rendered templ, native HTML fallback, HTMX redirects and accessible error feedback. Read `GET /debts` for plans, remaining principal, installment schedule and payment history.

## Verification and boundaries

Tests cover month-end/leap dates, rate rounding, final principal remainder, interest precision/limits, idempotent concurrent payments, early out-of-order payment rejection, no double ledger posting, AED/PHP isolation, insufficient asset balance, ledger drift, historical immutability, workspace isolation, browser authentication, CSRF and HTMX. CI: Go 1.27.3 Linux/Windows, PostgreSQL 17 + Goose, sqlc 1.31.1, Node 24 and Docker.

**Not included:** new loan origination into cash, credit-card revolving interest/minimum-payment calculation, variable APR, daily accrual, partial payments, refinancing, penalties, early payoff adjustment, loan statement imports, interest budget allocation, automated bank payments, or reminders. Do not use the equal-principal planner as a substitute for a lender's contractual amortization schedule if its methodology differs.
