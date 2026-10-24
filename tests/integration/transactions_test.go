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
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
)

func financeFixture(t *testing.T) (*transactions.Store, *accounts.Store, *ledger.Store, string) {
	t.Helper()
	_, _, _, workspace, _ := ledgerFixture(t)
	db := openDB(t)
	return transactions.NewStore(db.Pool()), accounts.NewStore(db.Pool()), ledger.NewStore(db.Pool()), workspace
}
func financeCategory(t *testing.T, s *transactions.Store, workspace, kind, currency, name string) string {
	t.Helper()
	id, err := s.CreateCategory(context.Background(), workspace, kind, currency, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func financeKey(t *testing.T) string {
	t.Helper()
	k, e := accounts.NewSetupKey()
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func financeInput(t *testing.T, kind, account, date string, splits []transactions.SplitInput) transactions.Input {
	t.Helper()
	return transactions.Input{Kind: kind, AccountID: account, OccurredOn: date, Key: financeKey(t), Splits: splits}
}
func TestFinanceSplitIncomeExpenseAndRefund(t *testing.T) {
	s, a, l, ws := financeFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, ws, "Main AED", "bank", "AED", "100.00")
	card := newAccount(t, ctx, a, ws, "Credit PHP", "credit", "PHP", "200.00")
	groc := financeCategory(t, s, ws, "expense", "AED", "Groceries")
	trans := financeCategory(t, s, ws, "expense", "AED", "Transport")
	pay := financeCategory(t, s, ws, "income", "AED", "Salary")
	phpFood := financeCategory(t, s, ws, "expense", "PHP", "Food")
	inc := financeInput(t, "income", bank, "2026-10-24", []transactions.SplitInput{{CategoryID: pay, Amount: "50.50"}})
	_, err := s.Create(ctx, ws, inc)
	if err != nil {
		t.Fatal(err)
	}
	expense := financeInput(t, "expense", bank, "2026-10-24",
		[]transactions.SplitInput{{CategoryID: groc, Amount: "12.25"}, {CategoryID: trans, Amount: "3.75"}})
	expense.Note = "Market run"
	orig, err := s.Create(ctx, ws, expense)
	if err != nil {
		t.Fatal(err)
	}
	if orig == "" {
		t.Fatal("missing expense ID")
	}
	refund := financeInput(t, "refund", bank, "2026-10-24",
		[]transactions.SplitInput{{CategoryID: groc, Amount: "2.25"}})
	refund.RefundOf = orig
	if _, err = s.Create(ctx, ws, refund); err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	badRefund := financeInput(t, "refund", bank, "2026-10-24",
		[]transactions.SplitInput{{CategoryID: groc, Amount: "11.00"}})
	badRefund.RefundOf = orig
	if _, err = s.Create(ctx, ws, badRefund); !errors.Is(err, transactions.ErrRefundLimit) {
		t.Fatalf("over refund accepted: %v", err)
	}
	if _, err = s.Create(ctx, ws, refund); err != nil {
		t.Fatalf("refund retry: %v", err)
	}
	cardExpense := financeInput(t, "expense", card, "2026-10-24",
		[]transactions.SplitInput{{CategoryID: phpFood, Amount: "35.00"}})
	if _, err = s.Create(ctx, ws, cardExpense); err != nil {
		t.Fatalf("credit card expense: %v", err)
	}
	balances := balancesByAccount(t, ctx, l, ws)
	if balances[bank] != 13675 {
		t.Fatalf("unexpected asset: %+v", balances)
	}
	if balances[card] != 23500 {
		t.Fatalf("credit liability should increase: %+v", balances)
	}
	history, err := s.List(ctx, ws, transactions.Filter{Kind: "expense", Query: "Market"})
	if err != nil || len(history) != 1 || history[0].TotalMinor != 1600 {
		t.Fatalf("expense filters: %+v %v", history, err)
	}
	byDate, err := s.List(ctx, ws, transactions.Filter{From: "2026-10-24", To: "2026-10-24"})
	if err != nil || len(byDate) != 4 {
		t.Fatalf("date-filter result=%d err=%v", len(byDate), err)
	}
	var total int
	db := openDB(t)
	if err := db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM finance_transactions WHERE workspace_id=$1`, ws).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("duplicate or rejected refunds persisted: %d", total)
	}
	var ledgerTotal int
	if err := db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM ledger_journals WHERE workspace_id=$1 AND kind='adjustment'`, ws).Scan(&ledgerTotal); err != nil {
		t.Fatal(err)
	}
	if ledgerTotal != 4 {
		t.Fatalf("orphan or duplicated postings: %d", ledgerTotal)
	}
}
func TestFinanceWorkspaceCurrencyKindsAndArchive(t *testing.T) {
	s, a, _, ws := financeFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, ws, "AED Bank", "bank", "AED", "0")
	php := financeCategory(t, s, ws, "expense", "PHP", "Food")
	income := financeCategory(t, s, ws, "income", "AED", "Consulting")
	exp := financeCategory(t, s, ws, "expense", "AED", "Food")
	other, _, _, otherWS := financeFixture(t)
	foreign := financeCategory(t, other, otherWS, "expense", "AED", "Foreign")
	for _, cat := range []string{php, income, foreign} {
		in := financeInput(t, "expense", bank, "2026-10-24", []transactions.SplitInput{{CategoryID: cat, Amount: "1"}})
		if _, err := s.Create(ctx, ws, in); err == nil {
			t.Fatalf("foreign kind, workspace or currency accepted: %s", cat)
		}
	}
	if err := s.ArchiveCategory(ctx, otherWS, exp); !errors.Is(err, transactions.ErrNotFound) {
		t.Fatalf("foreign archive accepted: %v", err)
	}
	if err := s.ArchiveCategory(ctx, ws, exp); err != nil {
		t.Fatal(err)
	}
	archived := financeInput(t, "expense", bank, "2026-10-24", []transactions.SplitInput{{CategoryID: exp, Amount: "1"}})
	if _, err := s.Create(ctx, ws, archived); !errors.Is(err, transactions.ErrInvalid) {
		t.Fatalf("archived category accepted: %v", err)
	}
	zero := financeInput(t, "income", bank, "2026-10-24", []transactions.SplitInput{{CategoryID: income, Amount: "0"}})
	if _, err := s.Create(ctx, ws, zero); !errors.Is(err, transactions.ErrInvalid) {
		t.Fatal(err)
	}
}
func TestFinanceIdempotencyConcurrentAndImmutability(t *testing.T) {
	s, a, l, ws := financeFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, ws, "Bank", "bank", "AED", "50")
	salary := financeCategory(t, s, ws, "income", "AED", "Salary")
	in := financeInput(t, "income", bank, "2026-10-24", []transactions.SplitInput{{CategoryID: salary, Amount: "10"}})
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); id, e := s.Create(context.Background(), ws, in); ids <- id; errs <- e }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	var original string
	for e := range errs {
		if e != nil {
			t.Fatalf("parallel transaction: %v", e)
		}
	}
	for id := range ids {
		if original == "" {
			original = id
		}
		if id != original {
			t.Fatalf("duplicate IDs %s %s", id, original)
		}
	}
	conflicting := in
	conflicting.Note = "Changed merchant"
	if _, err := s.Create(ctx, ws, conflicting); !errors.Is(err, transactions.ErrConflict) {
		t.Fatalf("same key changed note accepted: %v", err)
	}
	if balances := balancesByAccount(t, ctx, l, ws); balances[bank] != 6000 {
		t.Fatalf("duplicate income posted: %+v", balances)
	}
	db := openDB(t)
	if _, err := db.Pool().Exec(ctx, `UPDATE finance_transactions SET total_minor=1 WHERE id=$1`, original); err == nil {
		t.Fatal("immutable transaction updated")
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM finance_transaction_splits WHERE transaction_id=$1`, original); err == nil {
		t.Fatal("immutable split deleted")
	}
}
func TestFinanceBrowserFlowCSRFAndHistory(t *testing.T) {
	handler := uiFixture(t)
	actor, cookie := registration(t, handler, fmt.Sprintf("finance-ui-%d@example.test", time.Now().UnixNano()))
	page := formRequest(handler, "GET", "/transactions", nil, cookie, false, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Income & expenses") {
		t.Fatalf("transactions page: %d %s", page.Code, page.Body.String())
	}
	if rec := formRequest(handler, "GET", "/transactions", nil, nil, false, ""); rec.Code != 303 {
		t.Fatalf("unauthorized transactions page: %d", rec.Code)
	}
	catForm := url.Values{"csrf_token": {actor.CSRFToken}, "name": {"Groceries"}, "kind": {"expense"}, "currency": {"AED"}}
	if rec := formRequest(handler, "POST", "/categories", catForm, cookie, false, ""); rec.Code != 403 {
		t.Fatalf("category csrf origin missing: %d", rec.Code)
	}
	rec := formRequest(handler, "POST", "/categories", catForm, cookie, false, "http://localhost:8080")
	if rec.Code != 303 {
		t.Fatalf("create category status %d body %s", rec.Code, rec.Body.String())
	}
	db := openDB(t)
	cats := transactions.NewStore(db.Pool())
	list, err := cats.Categories(context.Background(), actor.WorkspaceID)
	if err != nil || len(list) != 1 {
		t.Fatalf("categories %+v %v", list, err)
	}
	acct := newAccount(t, context.Background(), accounts.NewStore(db.Pool()), actor.WorkspaceID, "Cash", "cash", "AED", "80")
	form := url.Values{"csrf_token": {actor.CSRFToken}, "request_key": {financeKey(t)},
		"account_id": {acct}, "kind": {"expense"}, "occurred_on": {"2026-10-24"},
		"note": {"<script>alert(1)</script>"}, "category_id": {list[0].ID}, "split_amount": {"2.50"}}
	form.Set("csrf_token", "bad")
	if rec := formRequest(handler, "POST", "/transactions", form, cookie, false, "http://localhost:8080"); rec.Code != 403 {
		t.Fatalf("missing valid CSRF: %d", rec.Code)
	}
	form.Set("csrf_token", actor.CSRFToken)
	rec = formRequest(handler, "POST", "/transactions", form, cookie, true, "http://localhost:8080")
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/transactions" {
		t.Fatalf("HTMX post %d %s", rec.Code, rec.Body.String())
	}
	rows := formRequest(handler, "GET", "/transactions?q=Groceries", nil, cookie, true, "")
	if rows.Code != 200 || !strings.Contains(rows.Body.String(), "AED 2.50") || strings.Contains(rows.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("transaction list invalid or XSS %d %s", rows.Code, rows.Body.String())
	}
	if !strings.Contains(rows.Body.String(), "&lt;script&gt;") {
		t.Fatal("note not escaped")
	}
	if !strings.Contains(rows.Body.String(), "transactions-list") {
		t.Fatal("missing HTMX fragment")
	}
}
