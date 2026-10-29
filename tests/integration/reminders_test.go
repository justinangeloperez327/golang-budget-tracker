//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/recurring"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/reminders"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transactions"
)

func TestReminderIsolationAndIdempotentDismissal(t *testing.T) {
	s, finance, ac, _, workspace := recurringFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	account := newAccount(t, ctx, ac, workspace, "Reminder bank", "bank", "AED", "200")
	category := financeCategory(t, finance, workspace, "expense", "AED", "Reminder bill")
	schedule, err := s.Create(ctx, workspace, recurringInput(t, account, category, "AED", "2026-10-29", "50"))
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	inbox := reminders.NewStore(db.Pool())
	entries, err := inbox.List(ctx, workspace, now)
	if err != nil {
		t.Fatal(err)
	}
	var match *reminders.Item
	for i := range entries {
		if entries[i].Kind == "recurring" && entries[i].ResourceID == schedule {
			match = &entries[i]
			break
		}
	}
	if match == nil {
		t.Fatal("recurring reminder not visible")
	}
	if err := inbox.Dismiss(ctx, workspace, "recurring", schedule, "2026-10-29", now); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Dismiss(ctx, workspace, "recurring", schedule, "2026-10-29", now); err != nil {
		t.Fatalf("replay: %v", err)
	}
	after, err := inbox.List(ctx, workspace, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range after {
		if item.Kind == "recurring" && item.ResourceID == schedule {
			t.Fatal("dismissed reminder still listed")
		}
	}
	_, _, _, _, foreign := recurringFixture(t)
	if err := inbox.Dismiss(ctx, foreign, "recurring", schedule, "2026-10-29", now); !errors.Is(err, reminders.ErrNotFound) {
		t.Fatalf("foreign dismiss returned %v", err)
	}
	if err := inbox.Dismiss(ctx, workspace, "recurring", schedule, "2026-10-30", now); !errors.Is(err, reminders.ErrNotFound) {
		t.Fatalf("invalid due date accepted: %v", err)
	}
	if err := inbox.Dismiss(ctx, workspace, "recurring", schedule, "2026-02-30", now); !errors.Is(err, reminders.ErrInvalid) {
		t.Fatalf("invalid calendar accepted: %v", err)
	}
	var count int
	if err := db.Pool().QueryRow(ctx, "SELECT count(*) FROM finance_transactions WHERE workspace_id=$1", workspace).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("reminders wrote a financial transaction")
	}
	_ = accounts.IsValidKey
	_ = transactions.Filter{}
}
