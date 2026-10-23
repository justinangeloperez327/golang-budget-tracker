# Finalized 20-group roadmap

1. Repository bootstrap: Go 1.27.3, Node 24, Docker, Makefile, CI and docs
2. HTTP infrastructure: Chi, logging, middleware, config and shutdown
3. PostgreSQL infrastructure: pgx, sqlc, Goose, transactions and integration tests
4. Authentication: users, secure sessions, access controls, CSRF
5. Frontend: templ + HTMX + Tailwind, responsive UI
6. Accounts and wallets: AED/PHP accounts and opening balances
7. Double-entry ledger: postings, idempotency, reversals and consistency
8. Income, expenses, categories, splits, refunds, searches
9. Transfers and account reconciliation, cross-currency settlement
10. Monthly budgets, allocations, actuals and rollover policies
11. Recurring bills, payment links, due dates and subscriptions
12. Debts, installment payments and interest
13. Savings accounts, savings goals and progress
14. AED/PHP remittances, fees, effective exchange rates
15. SSS, PhilHealth, Pag-IBIG and MP2 histories
16. Financial dashboard, cash flow, trends and net worth
17. Reports, CSV/PDF exports and historical analysis
18. In-app reminders, failed-save recovery and safe retries
19. Comprehensive security, performance, accessibility, data and E2E audit
20. Production-ready Docker/release checklist and documented manual deployment

All groups require appropriate automated tests. Delivery is incremental. Completing Group 1 does not mean later functionality exists.
