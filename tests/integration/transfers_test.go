//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)

func movementFixture(t *testing.T) (*transfers.Store, *accounts.Store, *ledger.Store, string) {
	t.Helper()
	_, _, _, workspace, _ := ledgerFixture(t)
	db := openDB(t)
	return transfers.NewStore(db.Pool()), accounts.NewStore(db.Pool()), ledger.NewStore(db.Pool()), workspace
}
func transferInput(t *testing.T, from, to, amount string) transfers.Input {
	t.Helper()
	key, e := accounts.NewSetupKey()
	if e != nil {
		t.Fatal(e)
	}
	return transfers.Input{FromID: from, ToID: to, Amount: amount, Fee: "0", Key: key, Date: "2026-10-26"}
}
func TestSameCurrencyTransferCardRepaymentFees(t *testing.T) {
	s, a, l, workspace := movementFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, workspace, "Bank", "bank", "AED", "500.00")
	savings := newAccount(t, ctx, a, workspace, "Savings", "savings", "AED", "0")
	card := newAccount(t, ctx, a, workspace, "Card", "credit", "AED", "100.00")
	transfer := transferInput(t, bank, savings, "75.00")
	transfer.Fee = "2.00"
	id, err := s.Transfer(ctx, workspace, transfer)
	if err != nil || id == "" {
		t.Fatalf("transfer %q %v", id, err)
	}
	repeat, err := s.Transfer(ctx, workspace, transfer)
	if err != nil || repeat != id {
		t.Fatalf("idempotency %q %v", repeat, err)
	}
	moved := transfer
	moved.Note = "Altered"
	if _, err := s.Transfer(ctx, workspace, moved); !errors.Is(err, transfers.ErrConflict) {
		t.Fatalf("different payload accepted %v", err)
	}
	repay := transferInput(t, bank, card, "40.00")
	if _, err := s.Transfer(ctx, workspace, repay); err != nil {
		t.Fatal(err)
	}
	balances := balancesByAccount(t, ctx, l, workspace)
	if balances[bank] != 38300 || balances[savings] != 7500 || balances[card] != 6000 {
		t.Fatalf("bank/card settlement wrong %+v", balances)
	}
	db := openDB(t)
	var expense int64
	if err := db.Pool().QueryRow(ctx, `
  SELECT COALESCE(SUM(l.debit_minor-l.credit_minor),0)
  FROM ledger_lines l JOIN ledger_accounts a ON a.id=l.ledger_account_id
  WHERE l.workspace_id=$1 AND a.kind='expense' AND a.system_key='transfer_fee'`, workspace).Scan(&expense); err != nil {
		t.Fatal(err)
	}
	if expense != 200 {
		t.Fatalf("fee-only expense expected 200, got %d", expense)
	}
	var financeCount int
	if err := db.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM finance_transactions WHERE workspace_id=$1`, workspace).Scan(&financeCount); err != nil {
		t.Fatal(err)
	}
	if financeCount != 0 {
		t.Fatalf("transfers double counted expenses: %d", financeCount)
	}
	rows, err := s.Transfers(ctx, workspace)
	if err != nil || len(rows) != 2 {
		t.Fatalf("transfer history: %+v %v", rows, err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE transfer_operations SET fee_minor=0 WHERE id=$1`, id); err == nil {
		t.Fatal("mutable transfer metadata accepted")
	}
}
func TestFXSettlementUsesSeparateBalancedCurrencyJournals(t *testing.T) {
	s, a, l, workspace := movementFixture(t)
	ctx := context.Background()
	aed := newAccount(t, ctx, a, workspace, "AED account", "bank", "AED", "200")
	php := newAccount(t, ctx, a, workspace, "PHP account", "savings", "PHP", "0")
	move := transferInput(t, aed, php, "100.00")
	move.Fee = "1.50"
	move.Rate = "15.50000000"
	move.Received = "1550.00"
	id, err := s.Transfer(ctx, workspace, move)
	if err != nil {
		t.Fatal(err)
	}
	balances := balancesByAccount(t, ctx, l, workspace)
	if balances[aed] != 9850 || balances[php] != 155000 {
		t.Fatalf("FX balances %+v", balances)
	}
	db := openDB(t)
	var sourceJournal, targetJournal string
	err = db.Pool().QueryRow(ctx, `SELECT source_journal_id,target_journal_id FROM transfer_operations WHERE id=$1`, id).Scan(&sourceJournal, &targetJournal)
	if err != nil || sourceJournal == targetJournal {
		t.Fatalf("FX journals not separate: %q %q %v", sourceJournal, targetJournal, err)
	}
	for _, journal := range []string{sourceJournal, targetJournal} {
		var count int
		var debit, credit int64
		var currency string
		err = db.Pool().QueryRow(ctx, `
   SELECT COUNT(*),SUM(l.debit_minor),SUM(l.credit_minor),MIN(l.currency)
   FROM ledger_lines l WHERE l.journal_id=$1`, journal).Scan(&count, &debit, &credit, &currency)
		if err != nil || debit != credit || count < 2 {
			t.Fatalf("unbalanced %s %d %d %d %v", journal, count, debit, credit, err)
		}
		if currency != "AED" && currency != "PHP" {
			t.Fatal("invalid journal currency")
		}
	}
	if _, err := s.Transfer(ctx, workspace, move); err != nil {
		t.Fatalf("idempotent FX retry %v", err)
	}
	invalid := transferInput(t, aed, php, "100.00")
	invalid.Rate = "15.50000000"
	invalid.Received = "1550.01"
	if _, err := s.Transfer(ctx, workspace, invalid); !errors.Is(err, transfers.ErrInvalid) {
		t.Fatalf("mismatched rate/amount accepted %v", err)
	}
}
func TestConcurrentTransfersAndCrossWorkspaceRejection(t *testing.T) {
	s, a, l, workspace := movementFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, workspace, "Bank", "cash", "AED", "1000")
	savings := newAccount(t, ctx, a, workspace, "Vault", "savings", "AED", "0")
	in := transferInput(t, bank, savings, "10")
	const workers = 8
	var wg sync.WaitGroup
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.Transfer(context.Background(), workspace, in)
			ids <- id
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	var expected string
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range ids {
		if expected == "" {
			expected = id
		}
		if id != expected {
			t.Fatalf("duplicate transfer %q vs %q", id, expected)
		}
	}
	balances := balancesByAccount(t, ctx, l, workspace)
	if balances[bank] != 99000 || balances[savings] != 1000 {
		t.Fatalf("duplicate transfer postings %+v", balances)
	}
	_, a2, _, workspace2 := movementFixture(t)
	foreign := newAccount(t, ctx, a2, workspace2, "Foreign", "bank", "AED", "100")
	outsider := transferInput(t, bank, foreign, "1")
	if _, err := s.Transfer(ctx, workspace, outsider); !errors.Is(err, transfers.ErrNotFound) {
		t.Fatalf("foreign account accepted: %v", err)
	}
	forbidden := transferInput(t, foreign, savings, "1")
	if _, err := s.Transfer(ctx, workspace, forbidden); !errors.Is(err, transfers.ErrNotFound) {
		t.Fatalf("foreign source accepted %v", err)
	}
}
func TestReconciliationAtomicAndIdempotent(t *testing.T) {
	s, a, l, workspace := movementFixture(t)
	ctx := context.Background()
	bank := newAccount(t, ctx, a, workspace, "Statement bank", "bank", "PHP", "75.00")
	key, _ := accounts.NewSetupKey()
	in := transfers.ReconcileInput{AccountID: bank, Observed: "80.00", Key: key, Date: "2026-10-26", Note: "Statement"}
	id, err := s.Reconcile(ctx, workspace, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Reconcile(ctx, workspace, in)
	if err != nil || again != id {
		t.Fatalf("replay %+v %v", again, err)
	}
	if b := balancesByAccount(t, ctx, l, workspace); b[bank] != 8000 {
		t.Fatalf("reconciliation %+v", b)
	}
	items, err := s.Reconciliations(ctx, workspace)
	if err != nil || len(items) != 1 || items[0].BeforeMinor != 7500 || items[0].DeltaMinor != 500 {
		t.Fatalf("reconciliation history %+v %v", items, err)
	}
	zero := transfers.ReconcileInput{AccountID: bank, Observed: "80", Key: financeKey(t), Date: "2026-10-26"}
	if _, err := s.Reconcile(ctx, workspace, zero); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	var journalPresent bool
	if err := db.Pool().QueryRow(ctx, `SELECT journal_id IS NOT NULL FROM account_reconciliations WHERE idempotency_key=$1`, zero.Key).Scan(&journalPresent); err != nil || journalPresent {
		t.Fatalf("zero reconciliation must have no journal %t %v", journalPresent, err)
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM account_reconciliations WHERE id=$1`, id); err == nil {
		t.Fatal("immutable reconciliation deleted")
	}
	card := newAccount(t, ctx, a, workspace, "Credit", "credit", "AED", "100.00")
	creditRecon := transfers.ReconcileInput{AccountID: card, Observed: "90", Key: financeKey(t), Date: "2026-10-26"}
	if _, err := s.Reconcile(ctx, workspace, creditRecon); err != nil {
		t.Fatal(err)
	}
	if b := balancesByAccount(t, ctx, l, workspace); b[card] != 9000 {
		t.Fatalf("liability reconciliation %+v", b)
	}
}
func TestMovementBrowserCSRFAndHTMX(t *testing.T) {
	handler := uiFixture(t)
	actor, cookie := registration(t, handler, fmt.Sprintf("movements-%d@example.test", time.Now().UnixNano()))
	db := openDB(t)
	ac := accounts.NewStore(db.Pool())
	source := newAccount(t, context.Background(), ac, actor.WorkspaceID, "My bank", "bank", "AED", "100")
	target := newAccount(t, context.Background(), ac, actor.WorkspaceID, "My savings", "savings", "AED", "0")
	page := formRequest(handler, "GET", "/transfers", nil, cookie, false, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Transfers & reconciliation") {
		t.Fatalf("GET /transfers %d %s", page.Code, page.Body.String())
	}
	if rec := formRequest(handler, "GET", "/transfers", nil, nil, false, ""); rec.Code != 303 {
		t.Fatalf("anonymous access %d", rec.Code)
	}
	form := url.Values{
		"csrf_token": {actor.CSRFToken}, "request_key": {financeKey(t)},
		"from_id": {source}, "to_id": {target}, "amount": {"10.00"}, "fee": {"0"},
		"occurred_on": {"2026-10-26"},
	}
	if rec := formRequest(handler, "POST", "/transfers", form, cookie, false, ""); rec.Code != 403 {
		t.Fatalf("missing Origin accepted: %d", rec.Code)
	}
	form.Set("csrf_token", "incorrect")
	if rec := formRequest(handler, "POST", "/transfers", form, cookie, false, "http://localhost:8080"); rec.Code != 403 {
		t.Fatalf("CSRF accepted: %d", rec.Code)
	}
	form.Set("csrf_token", actor.CSRFToken)
	rec := formRequest(handler, "POST", "/transfers", form, cookie, true, "http://localhost:8080")
	if rec.Code != 204 || rec.Header().Get("HX-Redirect") != "/transfers" {
		t.Fatalf("HTMX transfer: %d %s", rec.Code, rec.Body.String())
	}
	recon := url.Values{"csrf_token": {actor.CSRFToken}, "request_key": {financeKey(t)},
		"account_id": {target}, "observed": {"11"}, "occurred_on": {"2026-10-26"}}
	result := formRequest(handler, "POST", "/reconciliations", recon, cookie, false, "http://localhost:8080")
	if result.Code != 303 {
		t.Fatalf("HTML reconciliation %d %s", result.Code, result.Body.String())
	}
	history := formRequest(handler, "GET", "/transfers", nil, cookie, false, "")
	if history.Code != 200 || !strings.Contains(history.Body.String(), "My savings") || !strings.Contains(history.Body.String(), "11.00") {
		t.Fatalf("missing settled history %d %s", history.Code, history.Body.String())
	}
	_ = http.StatusOK
}
