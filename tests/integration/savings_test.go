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
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/savings"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)

func savingsFixture(t *testing.T) (*savings.Store, *accounts.Store, *ledger.Store, string) {
	t.Helper()
	_, _, _, ws, _ := ledgerFixture(t)
	db := openDB(t)
	return savings.NewStore(db.Pool()), accounts.NewStore(db.Pool()), ledger.NewStore(db.Pool()), ws
}
func savingsKey(t *testing.T) string {
	t.Helper()
	key, e := accounts.NewSetupKey()
	if e != nil {
		t.Fatal(e)
	}
	return key
}
func savingGoal(t *testing.T, s *savings.Store, ws, account, name, target string) string {
	t.Helper()
	id, e := s.Create(context.Background(), ws, savings.CreateInput{
		Name: name, SavingsAccountID: account, TargetAmount: target,
		TargetOn: "2027-12-31", Key: savingsKey(t),
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func savingMove(t *testing.T, s *savings.Store, ws, goal, other, direction, amount string) string {
	t.Helper()
	id, e := s.Move(context.Background(), ws, savings.MovementInput{
		GoalID: goal, OtherAccountID: other, Direction: direction, Amount: amount,
		OccurredOn: "2026-10-28", Key: savingsKey(t),
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestSavingsContributionWithdrawalProgressAndLedger(t *testing.T) {
	s, a, l, ws := savingsFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, ws, "Bank", "bank", "AED", "500.00")
	vault := newAccount(t, ctx, a, ws, "Goal AED", "savings", "AED", "25.00")
	php := newAccount(t, ctx, a, ws, "PHP Savings", "savings", "PHP", "20.00")
	goal := savingGoal(t, s, ws, vault, "Emergency Fund", "100.00")
	goals, e := s.Goals(ctx, ws, time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC))
	if e != nil || len(goals) != 1 || goals[0].BalanceMinor != 2500 ||
		goals[0].ProgressBps != 2500 || goals[0].RemainingMinor != 7500 {
		t.Fatalf("opening funds not shown %+v %v", goals, e)
	}
	if balances := balancesByAccount(t, ctx, l, ws); balances[bank] != 50000 || balances[vault] != 2500 {
		t.Fatalf("goal creation mutated money %+v", balances)
	}
	move := savings.MovementInput{
		GoalID: goal, OtherAccountID: bank, Direction: "contribution",
		Amount: "40.00", OccurredOn: "2026-10-28", Key: savingsKey(t),
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); id, e := s.Move(context.Background(), ws, move); ids <- id; errs <- e }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("parallel save %v", e)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Fatalf("duplicate IDs %s != %s", first, id)
		}
	}
	if first == "" {
		t.Fatal("missing movement ID")
	}
	diff := move
	diff.Amount = "40.01"
	if _, e := s.Move(ctx, ws, diff); !errors.Is(e, savings.ErrConflict) {
		t.Fatalf("different payload with duplicate key %v", e)
	}
	with := savingMove(t, s, ws, goal, bank, "withdrawal", "15.00")
	balances := balancesByAccount(t, ctx, l, ws)
	if balances[bank] != 47500 || balances[vault] != 5000 {
		t.Fatalf("savings transfer accounting %+v", balances)
	}
	if _, e := s.Move(ctx, ws, savings.MovementInput{GoalID: goal, OtherAccountID: bank,
		Direction: "withdrawal", Amount: "50.01", OccurredOn: "2026-10-28", Key: savingsKey(t)}); !errors.Is(e, savings.ErrFunds) {
		t.Fatalf("overdraft accepted %v", e)
	}
	goals, e = s.Goals(ctx, ws, time.Now().UTC())
	if e != nil || goals[0].ProgressBps != 5000 || goals[0].RemainingMinor != 5000 {
		t.Fatalf("progress not ledger derived %+v %v", goals, e)
	}
	movements, e := s.Movements(ctx, ws)
	if e != nil || len(movements) != 2 {
		t.Fatalf("missing movements %+v %v", movements, e)
	}
	db := openDB(t)
	var count int
	e = db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM finance_transactions WHERE workspace_id=$1`, ws).Scan(&count)
	if e != nil || count != 0 {
		t.Fatalf("savings transfers counted as expenses %d %v", count, e)
	}
	if _, e := db.Pool().Exec(ctx, `DELETE FROM savings_goal_movements WHERE id=$1`, with); e == nil {
		t.Fatal("movement is mutable")
	}
	if _, e := db.Pool().Exec(ctx, `UPDATE savings_goals SET target_minor=1 WHERE id=$1`, goal); e == nil {
		t.Fatal("goal contract is mutable")
	}
	if _, e := s.Create(ctx, ws, savings.CreateInput{Name: "Duplicate", SavingsAccountID: vault,
		TargetAmount: "50", Key: savingsKey(t)}); !errors.Is(e, savings.ErrAllocated) {
		t.Fatalf("savings account linked to two goals %v", e)
	}
	if _, e := s.Move(ctx, ws, savings.MovementInput{GoalID: goal, OtherAccountID: php,
		Direction: "contribution", Amount: "5", OccurredOn: "2026-10-28", Key: savingsKey(t)}); !errors.Is(e, savings.ErrInvalid) {
		t.Fatalf("currency leakage %v", e)
	}
	if e := s.Archive(ctx, ws, goal); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Move(ctx, ws, savings.MovementInput{GoalID: goal, OtherAccountID: bank,
		Direction: "withdrawal", Amount: "1", OccurredOn: "2026-10-28", Key: savingsKey(t)}); !errors.Is(e, savings.ErrInvalid) {
		t.Fatalf("archived goal accepted movement %v", e)
	}
}
func TestSavingsGoalAccountIsolationAndCrossWorkspace(t *testing.T) {
	s, a, _, ws := savingsFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, ws, "AED Bank", "bank", "AED", "100")
	vaultA := newAccount(t, ctx, a, ws, "AED Goal", "savings", "AED", "0")
	vaultB := newAccount(t, ctx, a, ws, "AED Goal 2", "savings", "AED", "20")
	goalA := savingGoal(t, s, ws, vaultA, "AED Goal", "150")
	goalB := savingGoal(t, s, ws, vaultB, "AED Goal 2", "200")
	if _, e := s.Move(ctx, ws, savings.MovementInput{GoalID: goalA, OtherAccountID: vaultB,
		Direction: "contribution", Amount: "10", OccurredOn: "2026-10-28", Key: savingsKey(t)}); !errors.Is(e, savings.ErrAllocated) {
		t.Fatalf("goal-to-goal contribution accepted: %v", e)
	}
	other, otherAc, _, otherWS := savingsFixture(t)
	foreign := newAccount(t, ctx, otherAc, otherWS, "Outside Bank", "bank", "AED", "100")
	foreignGoal := savingGoal(t, other, otherWS, newAccount(t, ctx, otherAc, otherWS, "Outside Savings", "savings", "AED", "0"), "Outside", "100")
	if _, e := s.Move(ctx, ws, savings.MovementInput{GoalID: foreignGoal, OtherAccountID: bank,
		Direction: "contribution", Amount: "1", OccurredOn: "2026-10-28", Key: savingsKey(t)}); !errors.Is(e, savings.ErrNotFound) {
		t.Fatalf("cross workspace goal movement accepted: %v", e)
	}
	if _, e := s.Move(ctx, ws, savings.MovementInput{GoalID: goalB, OtherAccountID: foreign,
		Direction: "contribution", Amount: "1", OccurredOn: "2026-10-28", Key: savingsKey(t)}); !errors.Is(e, savings.ErrNotFound) {
		t.Fatalf("foreign funding account accepted: %v", e)
	}
	if e := s.Archive(ctx, otherWS, goalA); !errors.Is(e, savings.ErrNotFound) {
		t.Fatalf("cross workspace archive succeeded: %v", e)
	}
	if _, e := s.Create(ctx, ws, savings.CreateInput{Name: "Bad", SavingsAccountID: bank,
		TargetAmount: "100", Key: savingsKey(t)}); !errors.Is(e, savings.ErrInvalid) {
		t.Fatalf("bank account used as dedicated goal savings account: %v", e)
	}
}
func TestSavingsExternalTransferUpdatesRealProgress(t *testing.T) {
	s, a, _, ws := savingsFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, ws, "Bank", "bank", "PHP", "200")
	vault := newAccount(t, ctx, a, ws, "Goal", "savings", "PHP", "50")
	_ = savingGoal(t, s, ws, vault, "Holiday", "100")
	transfer := transferInput(t, bank, vault, "25")
	transfer.Date = "2026-10-28"
	db := openDB(t)
	if _, e := transfers.NewStore(db.Pool()).Transfer(ctx, ws, transfer); e != nil {
		t.Fatal(e)
	}
	goals, e := s.Goals(ctx, ws, time.Now().UTC())
	if e != nil || len(goals) != 1 || goals[0].BalanceMinor != 7500 || goals[0].ProgressBps != 7500 {
		t.Fatalf("external ledger posting ignored %+v %v", goals, e)
	}
	moves, e := s.Movements(ctx, ws)
	if e != nil || len(moves) != 0 {
		t.Fatalf("ordinary transfer invented goal-specific movement %+v", moves)
	}
}
func TestSavingsBrowserNativeHTMXAndCSRF(t *testing.T) {
	handler := uiFixture(t)
	actor, cookie := registration(t, handler, fmt.Sprintf("savings-%d@example.test", time.Now().UnixNano()))
	ctx := context.Background()
	db := openDB(t)
	ac := accounts.NewStore(db.Pool())
	vault := newAccount(t, ctx, ac, actor.WorkspaceID, "Savings vault", "savings", "AED", "0")
	bank := newAccount(t, ctx, ac, actor.WorkspaceID, "Bank", "bank", "AED", "150")
	page := formRequest(handler, "GET", "/savings", nil, cookie, false, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Savings goals") {
		t.Fatalf("savings page %d %s", page.Code, page.Body.String())
	}
	if rec := formRequest(handler, "GET", "/savings", nil, nil, false, ""); rec.Code != 303 {
		t.Fatalf("unauthorized savings %d", rec.Code)
	}
	form := url.Values{"csrf_token": {actor.CSRFToken}, "request_key": {savingsKey(t)},
		"name": {"Emergency"}, "savings_account_id": {vault},
		"target_amount": {"100.00"}, "target_on": {"2027-10-28"}}
	if rec := formRequest(handler, "POST", "/savings", form, cookie, false, ""); rec.Code != 403 {
		t.Fatalf("missing origin accepted %d", rec.Code)
	}
	bad := url.Values{}
	for k, v := range form {
		bad[k] = append([]string{}, v...)
	}
	bad.Set("csrf_token", "bad")
	if rec := formRequest(handler, "POST", "/savings", bad, cookie, false, "http://localhost:8080"); rec.Code != 403 {
		t.Fatalf("bad CSRF accepted %d", rec.Code)
	}
	result := formRequest(handler, "POST", "/savings", form, cookie, true, "http://localhost:8080")
	if result.Code != 204 || result.Header().Get("HX-Redirect") != "/savings" {
		t.Fatalf("create HTMX %d %s", result.Code, result.Body.String())
	}
	store := savings.NewStore(db.Pool())
	goals, e := store.Goals(ctx, actor.WorkspaceID, time.Now().UTC())
	if e != nil || len(goals) != 1 {
		t.Fatalf("created goal %+v %v", goals, e)
	}
	move := url.Values{"csrf_token": {actor.CSRFToken}, "request_key": {savingsKey(t)},
		"goal_id": {goals[0].ID}, "other_account_id": {bank}, "direction": {"contribution"},
		"amount": {"20.00"}, "occurred_on": {"2026-10-28"}}
	record := formRequest(handler, "POST", "/savings/movements", move, cookie, false, "http://localhost:8080")
	if record.Code != 303 {
		t.Fatalf("native goal transfer %d %s", record.Code, record.Body.String())
	}
	loaded := formRequest(handler, "GET", "/savings", nil, cookie, false, "")
	if loaded.Code != 200 || !strings.Contains(loaded.Body.String(), "20.00") ||
		!strings.Contains(loaded.Body.String(), "Emergency") {
		t.Fatalf("savings history missing %d %s", loaded.Code, loaded.Body.String())
	}
	invalid := url.Values{}
	for k, v := range move {
		invalid[k] = append([]string{}, v...)
	}
	invalid.Set("request_key", savingsKey(t))
	invalid.Set("amount", "1000")
	feedback := formRequest(handler, "POST", "/savings/movements", invalid, cookie, true, "http://localhost:8080")
	if feedback.Code != 200 || !strings.Contains(feedback.Body.String(), "savings-feedback") {
		t.Fatalf("HTMX invalid feedback %d %s", feedback.Code, feedback.Body.String())
	}
}
