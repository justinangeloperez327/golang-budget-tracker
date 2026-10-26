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
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/recurring"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
)

func recurringFixture(t *testing.T) (*recurring.Store, *transactions.Store, *accounts.Store, *ledger.Store, string) {
	t.Helper()
	_, _, _, ws, _ := ledgerFixture(t)
	db := openDB(t)
	return recurring.NewStore(db.Pool()), transactions.NewStore(db.Pool()), accounts.NewStore(db.Pool()),
		ledger.NewStore(db.Pool()), ws
}
func recurringKey(t *testing.T) string {
	t.Helper()
	k, err := accounts.NewSetupKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}
func recurringInput(t *testing.T, account, category, currency, date, amount string) recurring.NewSchedule {
	t.Helper()
	return recurring.NewSchedule{
		Name: "Monthly electricity", Kind: "bill", Currency: currency,
		AccountID: account, CategoryID: category, Amount: amount,
		Frequency: "monthly", StartOn: date, Key: recurringKey(t),
	}
}
func TestRecurringSchedulePaymentAndLedgerIntegrity(t *testing.T) {
	s, finance, ac, l, ws := recurringFixture(t)
	ctx := context.Background()
	account := newAccount(t, ctx, ac, ws, "Bank", "bank", "AED", "500.00")
	category := financeCategory(t, finance, ws, "expense", "AED", "Utilities")
	in := recurringInput(t, account, category, "AED", "2026-01-31", "45.00")
	id, err := s.Create(ctx, ws, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Create(ctx, ws, in)
	if err != nil || again != id {
		t.Fatalf("schedule replay %s %v", again, err)
	}
	diff := in
	diff.Amount = "46"
	if _, err := s.Create(ctx, ws, diff); !errors.Is(err, recurring.ErrConflict) {
		t.Fatalf("changed schedule reused key %v", err)
	}
	items, err := s.Upcoming(ctx, ws, time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.DueOn == "2026-02-28" && item.ScheduleID == id && item.Status == "due" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing clamped February due %+v", items)
	}
	balance := balancesByAccount(t, ctx, l, ws)[account]
	if balance != 50000 {
		t.Fatal("creating a schedule posted money")
	}
	e := financeInput(t, "expense", account, "2026-02-28", []transactions.SplitInput{
		{CategoryID: category, Amount: "45.00"},
	})
	expenseID, err := finance.Create(ctx, ws, e)
	if err != nil {
		t.Fatal(err)
	}
	action := recurring.NewAction{ScheduleID: id, DueOn: "2026-02-28",
		Action: "paid", TransactionID: expenseID, Key: recurringKey(t)}
	var wg sync.WaitGroup
	const n = 6
	results := make(chan string, n)
	errorsCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record, err := s.Record(context.Background(), ws, action)
			results <- record
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent payment %v", err)
		}
	}
	var first string
	for value := range results {
		if first == "" {
			first = value
		}
		if value != first {
			t.Fatalf("duplicate payment records %s %s", first, value)
		}
	}
	if balancesByAccount(t, ctx, l, ws)[account] != 45500 {
		t.Fatal("linking expense double-posted money")
	}
	duplicate := action
	duplicate.Key = recurringKey(t)
	if _, err := s.Record(ctx, ws, duplicate); !errors.Is(err, recurring.ErrRecorded) {
		t.Fatalf("same occurrence allowed second link %v", err)
	}
	duplicate.DueOn = "2026-03-31"
	if _, err := s.Record(ctx, ws, duplicate); !errors.Is(err, recurring.ErrRecorded) {
		t.Fatalf("same expense used for another occurrence %v", err)
	}
	history, err := s.Payments(ctx, ws)
	if err != nil || len(history) != 1 || history[0].Action != "paid" || history[0].TransactionID != expenseID {
		t.Fatalf("payment history %+v %v", history, err)
	}
	db := openDB(t)
	var adjustments int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM finance_transactions WHERE workspace_id=$1`, ws).Scan(&adjustments); err != nil || adjustments != 1 {
		t.Fatalf("schedule caused extra financial transaction %d %v", adjustments, err)
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM recurring_occurrence_actions WHERE id=$1`, first); err == nil {
		t.Fatal("payment history deleted")
	}
	if err := s.Archive(ctx, ws, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Record(ctx, ws, recurring.NewAction{ScheduleID: id, DueOn: "2026-04-30", Action: "skipped", Key: recurringKey(t)}); !errors.Is(err, recurring.ErrInvalid) {
		t.Fatalf("archived schedule accepted action %v", err)
	}
}

func TestRecurringExpenseLinkConstraintsAndIsolation(t *testing.T) {
	s, finance, ac, _, ws := recurringFixture(t)
	ctx := context.Background()
	a := newAccount(t, ctx, ac, ws, "AED Bank", "bank", "AED", "200")
	b := newAccount(t, ctx, ac, ws, "Other AED bank", "bank", "AED", "200")
	category := financeCategory(t, finance, ws, "expense", "AED", "Phone")
	php := financeCategory(t, finance, ws, "expense", "PHP", "Phone")
	income := financeCategory(t, finance, ws, "income", "AED", "Salary")
	_, f2, ac2, _, otherWS := recurringFixture(t)
	foreign := newAccount(t, ctx, ac2, otherWS, "Outside bank", "bank", "AED", "100")
	foreignCategory := financeCategory(t, f2, otherWS, "expense", "AED", "Foreign")
	for _, tc := range []struct{ account, category, currency string }{
		{foreign, category, "AED"}, {a, foreignCategory, "AED"}, {a, php, "AED"}, {a, income, "AED"},
	} {
		v := recurringInput(t, tc.account, tc.category, tc.currency, "2026-10-26", "25.00")
		if _, err := s.Create(ctx, ws, v); err == nil {
			t.Fatalf("foreign/invalid schedule accepted %+v", tc)
		}
	}
	schedule, err := s.Create(ctx, ws, recurringInput(t, a, category, "AED", "2026-10-26", "25.00"))
	if err != nil {
		t.Fatal(err)
	}
	wrongAccount := financeInput(t, "expense", b, "2026-10-26", []transactions.SplitInput{{CategoryID: category, Amount: "25.00"}})
	wrongID, err := finance.Create(ctx, ws, wrongAccount)
	if err != nil {
		t.Fatal(err)
	}
	action := recurring.NewAction{ScheduleID: schedule, DueOn: "2026-10-26",
		Action: "paid", TransactionID: wrongID, Key: recurringKey(t)}
	if _, err := s.Record(ctx, ws, action); !errors.Is(err, recurring.ErrInvalid) {
		t.Fatalf("other account expense accepted %v", err)
	}
	wrongAmt := financeInput(t, "expense", a, "2026-10-26", []transactions.SplitInput{{CategoryID: category, Amount: "25.01"}})
	wrongAmtID, err := finance.Create(ctx, ws, wrongAmt)
	if err != nil {
		t.Fatal(err)
	}
	action.TransactionID = wrongAmtID
	action.Key = recurringKey(t)
	if _, err := s.Record(ctx, ws, action); !errors.Is(err, recurring.ErrInvalid) {
		t.Fatalf("wrong amount accepted %v", err)
	}
	validExpense := financeInput(t, "expense", a, "2026-10-26", []transactions.SplitInput{{CategoryID: category, Amount: "25.00"}})
	goodID, err := finance.Create(ctx, ws, validExpense)
	if err != nil {
		t.Fatal(err)
	}
	action.TransactionID = goodID
	action.Key = recurringKey(t)
	if _, err := s.Record(ctx, otherWS, action); !errors.Is(err, recurring.ErrNotFound) {
		t.Fatalf("foreign schedule accepted %v", err)
	}
	// A one-time skipped occurrence never touches the financial ledger.
	skip := recurring.NewAction{ScheduleID: schedule, DueOn: "2026-10-26", Action: "skipped", Key: recurringKey(t)}
	if _, err := s.Record(ctx, ws, skip); err != nil {
		t.Fatal(err)
	}
	action.Key = recurringKey(t)
	if _, err := s.Record(ctx, ws, action); !errors.Is(err, recurring.ErrRecorded) {
		t.Fatalf("skipped occurrence was overwritten %v", err)
	}
}

func TestRecurringBrowserAuthorizationAndNativeForms(t *testing.T) {
	handler := uiFixture(t)
	actor, cookie := registration(t, handler, fmt.Sprintf("recurring-%d@example.test", time.Now().UnixNano()))
	page := formRequest(handler, "GET", "/recurring", nil, cookie, false, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "New recurring schedule") {
		t.Fatalf("recurring page %d %s", page.Code, page.Body.String())
	}
	if rec := formRequest(handler, "GET", "/recurring", nil, nil, false, ""); rec.Code != 303 {
		t.Fatalf("anonymous page was accessible %d", rec.Code)
	}
	db := openDB(t)
	ctx := context.Background()
	acc := newAccount(t, ctx, accounts.NewStore(db.Pool()), actor.WorkspaceID, "Savings", "bank", "AED", "300")
	cat := financeCategory(t, transactions.NewStore(db.Pool()), actor.WorkspaceID, "expense", "AED", "Rent")
	today := time.Now().UTC().Format("2006-01-02")
	form := url.Values{
		"csrf_token": {actor.CSRFToken}, "request_key": {recurringKey(t)},
		"name": {"Internet subscription"}, "kind": {"subscription"}, "currency": {"AED"},
		"account_id": {acc}, "category_id": {cat}, "amount": {"100.00"},
		"frequency": {"monthly"}, "start_on": {today},
	}
	if rec := formRequest(handler, "POST", "/recurring", form, cookie, false, ""); rec.Code != 403 {
		t.Fatalf("missing origin accepted %d", rec.Code)
	}
	bad := url.Values{}
	for k, v := range form {
		bad[k] = append([]string(nil), v...)
	}
	bad.Set("csrf_token", "wrong")
	if rec := formRequest(handler, "POST", "/recurring", bad, cookie, false, "http://localhost:8080"); rec.Code != 403 {
		t.Fatalf("invalid CSRF accepted %d", rec.Code)
	}
	save := formRequest(handler, "POST", "/recurring", form, cookie, true, "http://localhost:8080")
	if save.Code != 204 || save.Header().Get("HX-Redirect") != "/recurring" {
		t.Fatalf("save schedule %d %s", save.Code, save.Body.String())
	}
	displayed := formRequest(handler, "GET", "/recurring", nil, cookie, false, "")
	if displayed.Code != 200 || !strings.Contains(displayed.Body.String(), "Internet subscription") {
		t.Fatalf("schedule missing from page %d %s", displayed.Code, displayed.Body.String())
	}
	schedules, err := recurring.NewStore(db.Pool()).Schedules(ctx, actor.WorkspaceID)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("schedules %+v %v", schedules, err)
	}
	paymentForm := url.Values{"csrf_token": {actor.CSRFToken}, "request_key": {recurringKey(t)},
		"schedule_id": {schedules[0].ID}, "due_on": {today}, "action": {"skipped"}}
	skipped := formRequest(handler, "POST", "/recurring/record", paymentForm, cookie, false, "http://localhost:8080")
	if skipped.Code != 303 {
		t.Fatalf("skip response %d %s", skipped.Code, skipped.Body.String())
	}
	history, err := recurring.NewStore(db.Pool()).Payments(ctx, actor.WorkspaceID)
	if err != nil || len(history) != 1 || history[0].Action != "skipped" {
		t.Fatalf("skip history %+v %v", history, err)
	}
}
