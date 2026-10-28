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
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/contributions"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/recurring"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)

func contributionFixture(t *testing.T) (*contributions.Store, *transactions.Store, *accounts.Store, *ledger.Store, string) {
	t.Helper()
	_, _, _, ws, _ := ledgerFixture(t)
	db := openDB(t)
	return contributions.NewStore(db.Pool()), transactions.NewStore(db.Pool()),
		accounts.NewStore(db.Pool()), ledger.NewStore(db.Pool()), ws
}
func contributionKey(t *testing.T) string {
	t.Helper()
	k, e := accounts.NewSetupKey()
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func contributionObligation(t *testing.T, store *contributions.Store, ws, scheme, period, category, mp2, amount string) string {
	t.Helper()
	id, e := store.Create(context.Background(), ws, contributions.NewObligation{
		Scheme: scheme, Period: period, DueOn: period + "-28", Amount: amount,
		ExpenseCategoryID: category, MP2AccountID: mp2, Key: contributionKey(t),
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestPHContributionsExpenseExactlyOnce(t *testing.T) {
	s, finance, ac, l, ws := contributionFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, ac, ws, "PHP Bank", "bank", "PHP", "10000.00")
	cat := financeCategory(t, finance, ws, "expense", "PHP", "SSS contribution")
	id := contributionObligation(t, s, ws, "sss", "2026-10", cat, "", "1000.00")
	obligations, e := s.Obligations(ctx, ws, time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC))
	if e != nil || len(obligations) != 1 || obligations[0].Status != "due" ||
		obligations[0].SchemeLabel != "SSS" {
		t.Fatalf("monthly due %+v %v", obligations, e)
	}
	if b := balancesByAccount(t, ctx, l, ws)[bank]; b != 1000000 {
		t.Fatalf("obligation debited bank %d", b)
	}
	request := contributions.NewObligation{Scheme: "sss", Period: "2026-10", DueOn: "2026-10-28",
		Amount: "1000.00", ExpenseCategoryID: cat, Key: contributionKey(t)}
	if _, e := s.Create(ctx, ws, request); !errors.Is(e, contributions.ErrResolved) {
		t.Fatalf("duplicate scheme-period accepted %v", e)
	}
	expense := financeInput(t, "expense", bank, "2026-10-28", []transactions.SplitInput{
		{CategoryID: cat, Amount: "1000.00"},
	})
	posted, e := finance.Create(ctx, ws, expense)
	if e != nil {
		t.Fatal(e)
	}
	paid := contributions.NewResolution{ObligationID: id, Action: "paid", ExpenseID: posted,
		ResolvedOn: "2026-10-28", Key: contributionKey(t)}
	var wg sync.WaitGroup
	const workers = 6
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			newID, err := s.Resolve(context.Background(), ws, paid)
			ids <- newID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent idempotency %v", e)
		}
	}
	first := ""
	for v := range ids {
		if first == "" {
			first = v
		}
		if first != v {
			t.Fatalf("duplicate payment records %s %s", first, v)
		}
	}
	if first == "" {
		t.Fatal("missing resolution")
	}
	diff := paid
	diff.Action = "skipped"
	diff.ExpenseID = ""
	if _, e := s.Resolve(ctx, ws, diff); !errors.Is(e, contributions.ErrConflict) {
		t.Fatalf("different request with same key accepted %v", e)
	}
	other := paid
	other.Key = contributionKey(t)
	if _, e := s.Resolve(ctx, ws, other); !errors.Is(e, contributions.ErrResolved) {
		t.Fatalf("recorded period paid twice %v", e)
	}
	if b := balancesByAccount(t, ctx, l, ws)[bank]; b != 900000 {
		t.Fatalf("link posted expense twice: %d", b)
	}
	db := openDB(t)
	var finances, adjustments int
	if e := db.Pool().QueryRow(ctx, `SELECT count(*) FROM finance_transactions WHERE workspace_id=$1`, ws).Scan(&finances); e != nil {
		t.Fatal(e)
	}
	if e := db.Pool().QueryRow(ctx, `SELECT count(*) FROM ledger_journals
   WHERE workspace_id=$1 AND kind='adjustment'`, ws).Scan(&adjustments); e != nil {
		t.Fatal(e)
	}
	if finances != 1 || adjustments != 1 {
		t.Fatalf("extra accounting entries finance=%d adjustments=%d", finances, adjustments)
	}
	if _, e := db.Pool().Exec(ctx, `UPDATE contribution_resolutions SET action='skipped' WHERE id=$1`, first); e == nil {
		t.Fatal("paid status mutable")
	}
	if _, e := db.Pool().Exec(ctx, `DELETE FROM contribution_obligations WHERE id=$1`, id); e == nil {
		t.Fatal("obligation history deleted")
	}
	history, e := s.History(ctx, ws)
	if e != nil || len(history) != 1 || history[0].ExpenseID != posted || history[0].Action != "paid" {
		t.Fatalf("payment history %+v %v", history, e)
	}
}
func TestPHContributionsMP2UsesTransferNotExpense(t *testing.T) {
	s, finance, ac, l, ws := contributionFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, ac, ws, "PHP Bank", "bank", "PHP", "3000.00")
	savings := newAccount(t, ctx, ac, ws, "MP2 savings", "savings", "PHP", "0")
	obligation := contributionObligation(t, s, ws, "mp2", "2026-10", "", savings, "500.00")
	transfer := transferInput(t, bank, savings, "500.00")
	transfer.Date = "2026-10-28"
	transferID, e := transfers.NewStore(openDB(t).Pool()).Transfer(ctx, ws, transfer)
	if e != nil {
		t.Fatal(e)
	}
	resolution := contributions.NewResolution{ObligationID: obligation, Action: "paid", TransferID: transferID,
		ResolvedOn: "2026-10-28", Key: contributionKey(t)}
	id, e := s.Resolve(ctx, ws, resolution)
	if e != nil {
		t.Fatal(e)
	}
	if b := balancesByAccount(t, ctx, l, ws); b[bank] != 250000 || b[savings] != 50000 {
		t.Fatalf("MP2 transfer posted twice %+v", b)
	}
	db := openDB(t)
	var expenseCount int
	if e := db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM finance_transactions WHERE workspace_id=$1`, ws).Scan(&expenseCount); e != nil || expenseCount != 0 {
		t.Fatalf("MP2 incorrectly recorded as an expense: %d %v", expenseCount, e)
	}
	if _, e := db.Pool().Exec(ctx, `DELETE FROM contribution_resolutions WHERE id=$1`, id); e == nil {
		t.Fatal("MP2 resolution mutable")
	}
	// Wrong destination and amount must never be linked.
	otherSavings := newAccount(t, ctx, ac, ws, "Other savings", "savings", "PHP", "0")
	next := contributionObligation(t, s, ws, "mp2", "2026-11", "", otherSavings, "500.00")
	bad := contributions.NewResolution{ObligationID: next, Action: "paid", TransferID: transferID,
		ResolvedOn: "2026-11-28", Key: contributionKey(t)}
	if _, e := s.Resolve(ctx, ws, bad); !errors.Is(e, contributions.ErrInvalid) {
		t.Fatalf("wrong transfer destination accepted %v", e)
	}
	// Wrong linked type cannot pass (expense reference for an MP2 obligation).
	phpCat := financeCategory(t, finance, ws, "expense", "PHP", "Misc")
	expense, e := finance.Create(ctx, ws, financeInput(t, "expense", bank, "2026-10-28",
		[]transactions.SplitInput{{CategoryID: phpCat, Amount: "10.00"}}))
	if e != nil {
		t.Fatal(e)
	}
	bad.TransferID = ""
	bad.ExpenseID = expense
	bad.Key = contributionKey(t)
	if _, e := s.Resolve(ctx, ws, bad); !errors.Is(e, contributions.ErrInvalid) {
		t.Fatalf("MP2 expense link accepted %v", e)
	}
}
func TestPHContributionWorkspacePHPOnlyAndReusedExpense(t *testing.T) {
	s, finance, ac, _, ws := contributionFixture(t)
	ctx := context.Background()
	phpBank := newAccount(t, ctx, ac, ws, "PHP Bank", "bank", "PHP", "300")
	phpCat := financeCategory(t, finance, ws, "expense", "PHP", "SSS")
	aedCat := financeCategory(t, finance, ws, "expense", "AED", "UAE")
	incomeCat := financeCategory(t, finance, ws, "income", "PHP", "Salary")
	other, otherFinance, otherAc, _, foreignWS := contributionFixture(t)
	foreignCat := financeCategory(t, otherFinance, foreignWS, "expense", "PHP", "Foreign")
	foreignBank := newAccount(t, ctx, otherAc, foreignWS, "Foreign Bank", "bank", "PHP", "300")
	for _, cat := range []string{aedCat, incomeCat, foreignCat} {
		_, e := s.Create(ctx, ws, contributions.NewObligation{Scheme: "sss", Period: "2026-09",
			Amount: "50", ExpenseCategoryID: cat, Key: contributionKey(t)})
		if e == nil {
			t.Fatalf("invalid/foreign category accepted %s", cat)
		}
	}
	if _, e := s.Create(ctx, ws, contributions.NewObligation{Scheme: "mp2", Period: "2026-10",
		Amount: "50", MP2AccountID: phpBank, Key: contributionKey(t)}); !errors.Is(e, contributions.ErrInvalid) {
		t.Fatalf("MP2 bank destination accepted %v", e)
	}
	obligation := contributionObligation(t, s, ws, "sss", "2026-10", phpCat, "", "50.00")
	foreignExpense, e := otherFinance.Create(ctx, foreignWS, financeInput(t, "expense", foreignBank,
		"2026-10-28", []transactions.SplitInput{{CategoryID: foreignCat, Amount: "50.00"}}))
	if e != nil {
		t.Fatal(e)
	}
	payment := contributions.NewResolution{ObligationID: obligation, Action: "paid", ExpenseID: foreignExpense,
		ResolvedOn: "2026-10-28", Key: contributionKey(t)}
	if _, e := s.Resolve(ctx, ws, payment); !errors.Is(e, contributions.ErrInvalid) {
		t.Fatalf("foreign payment accepted %v", e)
	}
	if _, e := other.Resolve(ctx, foreignWS, contributions.NewResolution{ObligationID: obligation,
		Action: "skipped", ResolvedOn: "2026-10-28", Key: contributionKey(t)}); !errors.Is(e, contributions.ErrNotFound) {
		t.Fatalf("foreign status accepted %v", e)
	}
	wrongCat := financeCategory(t, finance, ws, "expense", "PHP", "Food")
	wrong, e := finance.Create(ctx, ws, financeInput(t, "expense", phpBank,
		"2026-10-28", []transactions.SplitInput{{CategoryID: wrongCat, Amount: "50.00"}}))
	if e != nil {
		t.Fatal(e)
	}
	payment.ExpenseID = wrong
	payment.Key = contributionKey(t)
	if _, e := s.Resolve(ctx, ws, payment); !errors.Is(e, contributions.ErrInvalid) {
		t.Fatalf("wrong category accepted %v", e)
	}
	valid, e := finance.Create(ctx, ws, financeInput(t, "expense", phpBank,
		"2026-10-28", []transactions.SplitInput{{CategoryID: phpCat, Amount: "50.00"}}))
	if e != nil {
		t.Fatal(e)
	}
	recurringStore := recurring.NewStore(openDB(t).Pool())
	schedule, e := recurringStore.Create(ctx, ws, recurring.NewSchedule{
		Name: "Contribution reminder", Kind: "bill", Currency: "PHP", AccountID: phpBank,
		CategoryID: phpCat, Amount: "50.00", Frequency: "monthly", StartOn: "2026-10-28", Key: contributionKey(t),
	})
	if e != nil {
		t.Fatal(e)
	}
	payment.ExpenseID = valid
	payment.Key = contributionKey(t)
	if _, e := s.Resolve(ctx, ws, payment); e != nil {
		t.Fatal(e)
	}
	// Reverse direction: recurring must not double-classify the same expense.
	_, e = recurringStore.Record(ctx, ws, recurring.NewAction{ScheduleID: schedule,
		DueOn: "2026-10-28", Action: "paid", TransactionID: valid, Key: contributionKey(t)})
	if e == nil {
		t.Fatal("same expense linked to recurring and government contribution")
	}
}
func TestPHContributionSkipAndBrowserCSRF(t *testing.T) {
	handler := uiFixture(t)
	actor, cookie := registration(t, handler, fmt.Sprintf("contribution-%d@example.test", time.Now().UnixNano()))
	ctx := context.Background()
	db := openDB(t)
	phpCat := financeCategory(t, transactions.NewStore(db.Pool()), actor.WorkspaceID, "expense", "PHP", "PhilHealth")
	page := formRequest(handler, "GET", "/contributions", nil, cookie, false, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "PhilHealth") {
		t.Fatalf("contributions page %d %s", page.Code, page.Body.String())
	}
	if unauth := formRequest(handler, "GET", "/contributions", nil, nil, false, ""); unauth.Code != 303 {
		t.Fatalf("anonymous GET %d", unauth.Code)
	}
	form := url.Values{
		"csrf_token": {actor.CSRFToken}, "request_key": {contributionKey(t)},
		"scheme": {"philhealth"}, "period": {"2026-10"}, "due_on": {"2026-10-28"},
		"amount": {"500.00"}, "expense_category_id": {phpCat},
	}
	if missing := formRequest(handler, "POST", "/contributions", form, cookie, false, ""); missing.Code != 403 {
		t.Fatalf("missing Origin accepted: %d", missing.Code)
	}
	invalid := url.Values{}
	for k, v := range form {
		invalid[k] = append([]string{}, v...)
	}
	invalid.Set("csrf_token", "not real")
	if r := formRequest(handler, "POST", "/contributions", invalid, cookie, false, "http://localhost:8080"); r.Code != 403 {
		t.Fatalf("invalid CSRF accepted %d", r.Code)
	}
	created := formRequest(handler, "POST", "/contributions", form, cookie, true, "http://localhost:8080")
	if created.Code != 204 || created.Header().Get("HX-Redirect") != "/contributions" {
		t.Fatalf("HTMX new period %d %s", created.Code, created.Body.String())
	}
	store := contributions.NewStore(db.Pool())
	obligations, e := store.Obligations(ctx, actor.WorkspaceID, time.Now().UTC())
	if e != nil || len(obligations) != 1 {
		t.Fatalf("period %+v %v", obligations, e)
	}
	resolution := url.Values{
		"csrf_token": {actor.CSRFToken}, "request_key": {contributionKey(t)},
		"obligation_id": {obligations[0].ID}, "action": {"skipped"}, "resolved_on": {"2026-10-28"},
	}
	skipped := formRequest(handler, "POST", "/contributions/resolve", resolution, cookie, false, "http://localhost:8080")
	if skipped.Code != 303 {
		t.Fatalf("native skip %d %s", skipped.Code, skipped.Body.String())
	}
	history, e := store.History(ctx, actor.WorkspaceID)
	if e != nil || len(history) != 1 || history[0].Action != "skipped" {
		t.Fatalf("skip history %+v %v", history, e)
	}
	replay := formRequest(handler, "POST", "/contributions/resolve", resolution, cookie, true, "http://localhost:8080")
	if replay.Code != 204 || replay.Header().Get("HX-Redirect") != "/contributions" {
		t.Fatalf("idempotent skip replay %d %s", replay.Code, replay.Body.String())
	}
	loaded := formRequest(handler, "GET", "/contributions", nil, cookie, false, "")
	if loaded.Code != 200 || !strings.Contains(loaded.Body.String(), "skipped") {
		t.Fatalf("skipped period not shown %d %s", loaded.Code, loaded.Body.String())
	}
}
