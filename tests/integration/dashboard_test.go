//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/budgets"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/contributions"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/dashboard"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/debts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/recurring"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/savings"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
)

func TestDashboardLedgerAndCrossModuleFinancialSummary(t *testing.T) {
	ctx := context.Background()
	_, _, _, ws, _ := ledgerFixture(t)
	db := openDB(t)
	ac := accounts.NewStore(db.Pool())
	finance := transactions.NewStore(db.Pool())
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	bankAED := newAccount(t, ctx, ac, ws, "AED Main", "bank", "AED", "100")
	creditAED := newAccount(t, ctx, ac, ws, "AED Credit", "credit", "AED", "30")
	bankPHP := newAccount(t, ctx, ac, ws, "PHP Main", "bank", "PHP", "200")
	savingsPHP := newAccount(t, ctx, ac, ws, "PHP Savings", "savings", "PHP", "100")
	loanPHP := newAccount(t, ctx, ac, ws, "PHP Loan", "loan", "PHP", "50")
	if bankAED == "" || creditAED == "" || bankPHP == "" {
		t.Fatal("missing accounts")
	}
	groceries := financeCategory(t, finance, ws, "expense", "AED", "Groceries")
	salary := financeCategory(t, finance, ws, "income", "AED", "Salary")
	sssCategory := financeCategory(t, finance, ws, "expense", "PHP", "SSS")
	income, e := finance.Create(ctx, ws, financeInput(t, "income", bankAED, "2026-10-25",
		[]transactions.SplitInput{{CategoryID: salary, Amount: "50.00"}}))
	if e != nil || income == "" {
		t.Fatalf("income %v", e)
	}
	expense, e := finance.Create(ctx, ws, financeInput(t, "expense", bankAED, "2026-10-26",
		[]transactions.SplitInput{{CategoryID: groceries, Amount: "20.00"}}))
	if e != nil {
		t.Fatal(e)
	}
	refund := financeInput(t, "refund", bankAED, "2026-10-27",
		[]transactions.SplitInput{{CategoryID: groceries, Amount: "5.00"}})
	refund.RefundOf = expense
	if _, e := finance.Create(ctx, ws, refund); e != nil {
		t.Fatal(e)
	}
	budgetStore := budgets.NewStore(db.Pool())
	if _, e := budgetStore.Set(ctx, ws, budgets.Update{
		Month: "2026-10", Currency: "AED", Action: "allocation",
		CategoryID: groceries, Amount: "30.00", Key: financeKey(t),
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := savings.NewStore(db.Pool()).Create(ctx, ws, savings.CreateInput{
		Name: "Emergency fund", SavingsAccountID: savingsPHP, TargetAmount: "200.00",
		TargetOn: "2027-12-31", Key: financeKey(t),
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := debts.NewStore(db.Pool()).Create(ctx, ws, debts.NewPlan{
		Name: "Personal loan", LoanAccountID: loanPHP, FundingAccountID: bankPHP,
		APR: "0.00", Months: 2, FirstDue: "2026-11-28", Key: financeKey(t),
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := recurring.NewStore(db.Pool()).Create(ctx, ws, recurring.NewSchedule{
		Name: "Grocery reminder", Kind: "bill", Currency: "AED",
		AccountID: bankAED, CategoryID: groceries, Amount: "10.00",
		Frequency: "monthly", StartOn: "2026-10-27", Key: financeKey(t),
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := contributions.NewStore(db.Pool()).Create(ctx, ws, contributions.NewObligation{
		Scheme: "sss", Period: "2026-10", DueOn: "2026-10-27",
		Amount: "500.00", ExpenseCategoryID: sssCategory, Key: financeKey(t),
	}); e != nil {
		t.Fatal(e)
	}
	snap, e := dashboard.NewStore(db.Pool()).Load(ctx, ws, "2026-10", now)
	if e != nil {
		t.Fatal(e)
	}
	if len(snap.Currencies) != 2 || snap.Currencies[0].Code != "AED" || snap.Currencies[1].Code != "PHP" {
		t.Fatalf("currencies merged %+v", snap.Currencies)
	}
	a, p := snap.Currencies[0], snap.Currencies[1]
	if a.AssetsMinor != 13500 || a.LiabilitiesMinor != 3000 || a.NetWorthMinor != 10500 {
		t.Fatalf("AED balances %+v", a)
	}
	if a.IncomeMinor != 5000 || a.ExpensesMinor != 2000 || a.RefundsMinor != 500 ||
		a.SpendingMinor != 1500 || a.NetFlowMinor != 3500 {
		t.Fatalf("AED income/spending + refund %+v", a)
	}
	if !a.Budget.Configured || a.Budget.PlannedMinor != 3000 ||
		a.Budget.ActualMinor != 1500 || a.Budget.RemainingMinor != 1500 {
		t.Fatalf("budget not reused %+v", a.Budget)
	}
	if p.AssetsMinor != 30000 || p.LiabilitiesMinor != 5000 || p.NetWorthMinor != 25000 {
		t.Fatalf("PHP balances %+v", p)
	}
	if p.IncomeMinor != 0 || p.SpendingMinor != 0 || p.Budget.Configured {
		t.Fatalf("cross-currency totals leaked %+v", p)
	}
	if len(snap.Goals) != 1 || snap.Goals[0].BalanceMinor != 10000 || snap.Goals[0].ProgressBps != 5000 {
		t.Fatalf("savings goal %+v", snap.Goals)
	}
	if len(snap.Loans) != 1 || snap.Loans[0].OutstandingMinor != 5000 {
		t.Fatalf("loan outstanding %+v", snap.Loans)
	}
	if len(snap.Bills) != 2 || snap.Bills[0].Status != "overdue" || snap.Bills[1].Status != "upcoming" {
		t.Fatalf("recurring due %+v", snap.Bills)
	}
	if len(snap.Contributions) != 1 || snap.Contributions[0].Status != "overdue" {
		t.Fatalf("contribution overdue %+v", snap.Contributions)
	}
	if len(snap.Recent) != 3 {
		t.Fatalf("recent transactions %+v", snap.Recent)
	}
	otherMonth, e := dashboard.NewStore(db.Pool()).Load(ctx, ws, "2026-09", now)
	if e != nil {
		t.Fatal(e)
	}
	if otherMonth.Currencies[0].IncomeMinor != 0 || otherMonth.Currencies[0].SpendingMinor != 0 ||
		otherMonth.Currencies[0].AssetsMinor != 13500 {
		t.Fatalf("month filter changed current balance or leaked flows %+v", otherMonth.Currencies[0])
	}
}
func TestDashboardAccessMonthHTMXAndWorkspaceIsolation(t *testing.T) {
	handler := uiFixture(t)
	actor, cookie := registration(t, handler, fmt.Sprintf("dashboard16-%d@example.test", time.Now().UnixNano()))
	_, otherCookie := registration(t, handler, fmt.Sprintf("dashboard16-other-%d@example.test", time.Now().UnixNano()))
	first := formRequest(handler, "GET", "/dashboard?month=2026-10", nil, cookie, false, "")
	if first.Code != 200 || !strings.Contains(first.Body.String(), "Your dashboard") ||
		!strings.Contains(first.Body.String(), "AED overview") || !strings.Contains(first.Body.String(), "PHP overview") ||
		!strings.Contains(first.Body.String(), actor.Email) || !strings.Contains(first.Body.String(), "csrf_token") {
		t.Fatalf("dashboard initial render %d %s", first.Code, first.Body.String())
	}
	partial := formRequest(handler, "GET", "/dashboard/summary?month=2026-10", nil, cookie, true, "")
	if partial.Code != 200 || !strings.Contains(partial.Body.String(), "dashboard-summary") ||
		!strings.Contains(partial.Body.String(), "Income & spending") {
		t.Fatalf("dashboard HTMX fragment %d %s", partial.Code, partial.Body.String())
	}
	invalid := formRequest(handler, "GET", "/dashboard?month=2026-13", nil, cookie, false, "")
	if invalid.Code != 400 {
		t.Fatalf("invalid month accepted %d", invalid.Code)
	}
	invalid = formRequest(handler, "GET", "/dashboard/summary?month=bad", nil, cookie, true, "")
	if invalid.Code != 400 {
		t.Fatalf("invalid summary month accepted %d", invalid.Code)
	}
	anonymous := formRequest(handler, "GET", "/dashboard/summary?month=2026-10", nil, nil, true, "")
	if anonymous.Code != 303 {
		t.Fatalf("anonymous partial %d", anonymous.Code)
	}
	// A second authenticated workspace may not view the first user's accounts.
	db := openDB(t)
	ac := accounts.NewStore(db.Pool())
	_ = newAccount(t, context.Background(), ac, actor.WorkspaceID, "Private dashboard asset", "bank", "AED", "99.99")
	mine := formRequest(handler, "GET", "/dashboard", nil, cookie, false, "")
	theirs := formRequest(handler, "GET", "/dashboard", nil, otherCookie, false, "")
	if !strings.Contains(mine.Body.String(), "Private dashboard asset") ||
		strings.Contains(theirs.Body.String(), "Private dashboard asset") {
		t.Fatalf("dashboard leaked between workspaces mine=%d other=%d", mine.Code, theirs.Code)
	}
	activity := formRequest(handler, "GET", "/dashboard/activity", nil, cookie, true, "")
	if activity.Code != 200 || !strings.Contains(activity.Body.String(), "Recent activity") {
		t.Fatalf("legacy activity endpoint broken %d %s", activity.Code, activity.Body.String())
	}
}
