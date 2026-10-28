//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/debts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)

func debtFixture(t *testing.T) (*debts.Store, *accounts.Store, *ledger.Store, string) {
	t.Helper()
	_, _, _, workspace, _ := ledgerFixture(t)
	db := openDB(t)
	return debts.NewStore(db.Pool()), accounts.NewStore(db.Pool()), ledger.NewStore(db.Pool()), workspace
}
func debtKey(t *testing.T) string {
	t.Helper()
	k, e := accounts.NewSetupKey()
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func debtCreate(t *testing.T, ws string, s *debts.Store, loan, fund, name, rate string, months int) string {
	t.Helper()
	id, e := s.Create(context.Background(), ws, debts.NewPlan{
		Name: name, LoanAccountID: loan, FundingAccountID: fund,
		APR: rate, Months: months, FirstDue: "2026-01-31", Key: debtKey(t),
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestDebtLoanInterestPrincipalAndIdempotency(t *testing.T) {
	s, ac, l, ws := debtFixture(t)
	ctx := context.Background()
	loan := newAccount(t, ctx, ac, ws, "Loan", "loan", "AED", "1200.00")
	bank := newAccount(t, ctx, ac, ws, "Bank", "bank", "AED", "2000.00")
	planInput := debts.NewPlan{Name: "Personal loan", LoanAccountID: loan, FundingAccountID: bank,
		APR: "12", Months: 12, FirstDue: "2026-01-31", Key: debtKey(t)}
	plan, e := s.Create(ctx, ws, planInput)
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := s.Create(ctx, ws, planInput)
	if e != nil || duplicate != plan {
		t.Fatalf("plan idempotency %q %v", duplicate, e)
	}
	changed := planInput
	changed.APR = "13"
	if _, e := s.Create(ctx, ws, changed); !errors.Is(e, debts.ErrConflict) {
		t.Fatalf("different loan terms accepted %v", e)
	}
	if balances := balancesByAccount(t, ctx, l, ws); balances[loan] != 120000 || balances[bank] != 200000 {
		t.Fatalf("plan must not post money %+v", balances)
	}
	installments, e := s.Installments(ctx, ws)
	if e != nil || len(installments) != 12 {
		t.Fatalf("installments %+v %v", installments, e)
	}
	first, second := installments[0], installments[1]
	if first.PrincipalMinor != 10000 || first.InterestMinor != 1200 ||
		first.AmountMinor != 11200 || second.DueOn != "2026-02-28" ||
		second.InterestMinor != 1100 {
		t.Fatalf("wrong schedule first=%+v second=%+v", first, second)
	}
	outOfOrder := debts.PayInput{PlanID: plan, InstallmentID: second.ID, PaidOn: "2026-02-28", Key: debtKey(t)}
	if _, e := s.Pay(ctx, ws, outOfOrder); !errors.Is(e, debts.ErrInvalid) {
		t.Fatalf("out-of-order payment accepted %v", e)
	}
	payment := debts.PayInput{PlanID: plan, InstallmentID: first.ID, PaidOn: "2026-01-31", Key: debtKey(t)}
	const retries = 6
	ids := make(chan string, retries)
	errs := make(chan error, retries)
	var wg sync.WaitGroup
	for i := 0; i < retries; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); id, err := s.Pay(context.Background(), ws, payment); ids <- id; errs <- err }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent debt payment %v", err)
		}
	}
	expected := ""
	for id := range ids {
		if expected == "" {
			expected = id
		}
		if expected != id {
			t.Fatalf("different payment id %q != %q", id, expected)
		}
	}
	if expected == "" {
		t.Fatal("payment has no ID")
	}
	altered := payment
	altered.PaidOn = "2026-02-01"
	if _, e := s.Pay(ctx, ws, altered); !errors.Is(e, debts.ErrConflict) {
		t.Fatalf("changed idempotent payment accepted %v", e)
	}
	otherKey := payment
	otherKey.Key = debtKey(t)
	if _, e := s.Pay(ctx, ws, otherKey); !errors.Is(e, debts.ErrRecorded) {
		t.Fatalf("duplicate installment accepted %v", e)
	}
	balances := balancesByAccount(t, ctx, l, ws)
	if balances[loan] != 110000 || balances[bank] != 188800 {
		t.Fatalf("incorrect principal/funding after payment %+v", balances)
	}
	plans, e := s.Plans(ctx, ws)
	if e != nil || len(plans) != 1 || plans[0].OutstandingMinor != 110000 || plans[0].Completed != 1 {
		t.Fatalf("outstanding ledger balance %+v %v", plans, e)
	}
	db := openDB(t)
	var expense int64
	e = db.Pool().QueryRow(ctx, `SELECT COALESCE(SUM(l.debit_minor-l.credit_minor),0)
  FROM ledger_lines l JOIN ledger_accounts a ON a.id=l.ledger_account_id
  WHERE l.workspace_id=$1 AND a.system_key='loan_interest'`, ws).Scan(&expense)
	if e != nil || expense != 1200 {
		t.Fatalf("interest expense %d %v", expense, e)
	}
	var count int
	e = db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM finance_transactions WHERE workspace_id=$1`, ws).Scan(&count)
	if e != nil || count != 0 {
		t.Fatalf("debt payments duplicated regular expense %d %v", count, e)
	}
	if _, e := db.Pool().Exec(ctx, `UPDATE debt_payments SET principal_minor=1 WHERE id=$1`, expected); e == nil {
		t.Fatal("payment history can be altered")
	}
	if _, e := db.Pool().Exec(ctx, `DELETE FROM debt_installments WHERE id=$1`, first.ID); e == nil {
		t.Fatal("installment history can be deleted")
	}
	secondPay := debts.PayInput{PlanID: plan, InstallmentID: second.ID, PaidOn: "2026-02-28", Key: debtKey(t)}
	if _, e := s.Pay(ctx, ws, secondPay); e != nil {
		t.Fatal(e)
	}
	if balancesByAccount(t, ctx, l, ws)[loan] != 100000 {
		t.Fatal("second repayment did not reduce principal")
	}
}
func TestDebtValidationFundsCurrencyAndOwnership(t *testing.T) {
	s, ac, l, ws := debtFixture(t)
	ctx := context.Background()
	loan := newAccount(t, ctx, ac, ws, "AED Loan", "loan", "AED", "100")
	bank := newAccount(t, ctx, ac, ws, "AED Bank", "bank", "AED", "20")
	php := newAccount(t, ctx, ac, ws, "PHP Bank", "bank", "PHP", "1000")
	foreign, otherAc, _, otherWS := debtFixture(t)
	outsideLoan := newAccount(t, ctx, otherAc, otherWS, "Outside", "loan", "AED", "100")
	try := debts.NewPlan{Name: "Bad", LoanAccountID: loan, FundingAccountID: php,
		APR: "0", Months: 1, FirstDue: "2026-10-28", Key: debtKey(t)}
	if _, e := s.Create(ctx, ws, try); !errors.Is(e, debts.ErrInvalid) {
		t.Fatalf("cross-currency %v", e)
	}
	try.LoanAccountID = outsideLoan
	try.FundingAccountID = bank
	try.Key = debtKey(t)
	if _, e := s.Create(ctx, ws, try); !errors.Is(e, debts.ErrNotFound) {
		t.Fatalf("cross-workspace %v", e)
	}
	plan := debtCreate(t, ws, s, loan, bank, "Loan", "0", 1)
	payments, e := s.Installments(ctx, ws)
	if e != nil || len(payments) != 1 {
		t.Fatalf("missing installment %+v %v", payments, e)
	}
	pay := debts.PayInput{PlanID: plan, InstallmentID: payments[0].ID, PaidOn: "2026-10-28", Key: debtKey(t)}
	if _, e := s.Pay(ctx, ws, pay); !errors.Is(e, debts.ErrFunds) {
		t.Fatalf("no funds accepted %v", e)
	}
	if balancesByAccount(t, ctx, l, ws)[loan] != 10000 {
		t.Fatal("failed repayment changed principal")
	}
	if _, e := foreign.Pay(ctx, otherWS, pay); !errors.Is(e, debts.ErrNotFound) {
		t.Fatalf("foreign debt payment accepted %v", e)
	}
	// A reconciliation changed the loan outside the amortization contract.
	loan2 := newAccount(t, ctx, ac, ws, "Another Loan", "loan", "AED", "50")
	bank2 := newAccount(t, ctx, ac, ws, "Another Bank", "bank", "AED", "100")
	plan2 := debtCreate(t, ws, s, loan2, bank2, "Plan2", "0", 1)
	var due string
	e = openDB(t).Pool().QueryRow(ctx, `SELECT id FROM debt_installments WHERE plan_id=$1`, plan2).Scan(&due)
	if e != nil {
		t.Fatal(e)
	}
	adj := transfers.ReconcileInput{AccountID: loan2, Observed: "60", Key: debtKey(t), Date: "2026-10-28"}
	if _, e := transfers.NewStore(openDB(t).Pool()).Reconcile(ctx, ws, adj); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Pay(ctx, ws, debts.PayInput{PlanID: plan2, InstallmentID: due, PaidOn: "2026-10-28", Key: debtKey(t)}); !errors.Is(e, debts.ErrBalance) {
		t.Fatalf("ledger drift ignored %v", e)
	}
}
func TestDebtBrowserFormsAndCSRF(t *testing.T) {
	handler := uiFixture(t)
	actor, cookie := registration(t, handler, fmt.Sprintf("debt-%d@example.test", time.Now().UnixNano()))
	ctx := context.Background()
	db := openDB(t)
	ac := accounts.NewStore(db.Pool())
	loan := newAccount(t, ctx, ac, actor.WorkspaceID, "Personal loan", "loan", "PHP", "150.00")
	bank := newAccount(t, ctx, ac, actor.WorkspaceID, "Bank", "bank", "PHP", "250.00")
	page := formRequest(handler, "GET", "/debts", nil, cookie, false, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Loans & installments") {
		t.Fatalf("debt page %d %s", page.Code, page.Body.String())
	}
	if no := formRequest(handler, "GET", "/debts", nil, nil, false, ""); no.Code != 303 {
		t.Fatalf("unauthenticated debt page %d", no.Code)
	}
	form := url.Values{"csrf_token": {actor.CSRFToken}, "request_key": {debtKey(t)},
		"name": {"Personal plan"}, "loan_account_id": {loan}, "funding_account_id": {bank},
		"apr": {"0"}, "months": {"2"}, "first_due_on": {"2026-10-28"}}
	if r := formRequest(handler, "POST", "/debts", form, cookie, false, ""); r.Code != 403 {
		t.Fatalf("missing origin %d", r.Code)
	}
	bad := url.Values{}
	for k, v := range form {
		bad[k] = append([]string{}, v...)
	}
	bad.Set("csrf_token", "wrong")
	if r := formRequest(handler, "POST", "/debts", bad, cookie, false, "http://localhost:8080"); r.Code != 403 {
		t.Fatalf("invalid csrf %d", r.Code)
	}
	saved := formRequest(handler, "POST", "/debts", form, cookie, true, "http://localhost:8080")
	if saved.Code != 204 || saved.Header().Get("HX-Redirect") != "/debts" {
		t.Fatalf("save form %d %s", saved.Code, saved.Body.String())
	}
	store := debts.NewStore(db.Pool())
	plans, e := store.Plans(ctx, actor.WorkspaceID)
	if e != nil || len(plans) != 1 {
		t.Fatalf("plans %+v %v", plans, e)
	}
	inst, e := store.Installments(ctx, actor.WorkspaceID)
	if e != nil || len(inst) != 2 {
		t.Fatalf("installments %+v %v", inst, e)
	}
	payment := url.Values{"csrf_token": {actor.CSRFToken}, "request_key": {debtKey(t)},
		"plan_id": {plans[0].ID}, "installment_id": {inst[0].ID}, "paid_on": {"2026-10-28"}}
	paid := formRequest(handler, "POST", "/debts/payments", payment, cookie, false, "http://localhost:8080")
	if paid.Code != 303 {
		t.Fatalf("payment form %d %s", paid.Code, paid.Body.String())
	}
	loaded := formRequest(handler, "GET", "/debts", nil, cookie, false, "")
	if loaded.Code != 200 || !strings.Contains(loaded.Body.String(), "Payment history") ||
		!strings.Contains(loaded.Body.String(), "75.00") {
		t.Fatalf("payment missing from page %d %s", loaded.Code, loaded.Body.String())
	}
}
