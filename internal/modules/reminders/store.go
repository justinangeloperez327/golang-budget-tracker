// Package reminders projects outstanding obligations into a workspace-scoped inbox.
// Dismissal never posts a financial transaction or changes an obligation.
package reminders

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/recurring"
)

var ErrInvalid = errors.New("invalid reminder")
var ErrNotFound = errors.New("reminder is not outstanding")

type Item struct {
	Kind        string `json:"kind"`
	ResourceID  string `json:"resource_id"`
	Title       string `json:"title"`
	Currency    string `json:"currency"`
	DueOn       string `json:"due_on"`
	Status      string `json:"status"`
	AmountMinor int64  `json:"amount_minor"`
}
type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// List is capped and uses UTC calendar days for consistent deadlines.
// A dismissed reminder never hides a recorded paid/skipped obligation.
func (s *Store) List(ctx context.Context, workspace string, now time.Time) ([]Item, error) {
	if !accounts.IsValidKey(workspace) {
		return nil, ErrInvalid
	}
	today := now.UTC().Format("2006-01-02")
	projected, err := recurring.NewStore(s.db).Upcoming(ctx, workspace, now)
	if err != nil {
		return nil, err
	}
	result := make([]Item, 0)
	for _, p := range projected {
		if p.Status != "due" && p.Status != "overdue" && p.Status != "upcoming" {
			continue
		}
		if p.DueOn > now.UTC().AddDate(0, 0, 14).Format("2006-01-02") {
			continue
		}
		result = append(result, Item{Kind: "recurring", ResourceID: p.ScheduleID, Title: p.ScheduleName, Currency: p.Currency, DueOn: p.DueOn, Status: p.Status, AmountMinor: p.AmountMinor})
	}
	rows, err := s.db.Query(ctx, `SELECT o.id,o.scheme,o.due_on::text,o.amount_minor
 FROM contribution_obligations o LEFT JOIN contribution_resolutions r
 ON r.workspace_id=o.workspace_id AND r.obligation_id=o.id
 WHERE o.workspace_id=$1 AND r.id IS NULL AND o.due_on IS NOT NULL
 AND o.due_on<=($2::date + 14) AND o.due_on>=($2::date - 30)
 ORDER BY o.due_on,o.id LIMIT 150`, workspace, today)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, scheme, due string
		var amount int64
		if err := rows.Scan(&id, &scheme, &due, &amount); err != nil {
			rows.Close()
			return nil, err
		}
		status := "upcoming"
		if due < today {
			status = "overdue"
		} else if due == today {
			status = "due"
		}
		result = append(result, Item{Kind: "contribution", ResourceID: id, Title: scheme, Currency: "PHP", DueOn: due, Status: status, AmountMinor: amount})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	dismissed, err := s.db.Query(ctx, `SELECT kind,resource_id,due_on::text FROM reminder_dismissals WHERE workspace_id=$1`, workspace)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for dismissed.Next() {
		var kind, id, day string
		if err = dismissed.Scan(&kind, &id, &day); err != nil {
			dismissed.Close()
			return nil, err
		}
		seen[kind+":"+id+":"+day] = true
	}
	err = dismissed.Err()
	dismissed.Close()
	if err != nil {
		return nil, err
	}
	filtered := make([]Item, 0, len(result))
	for _, item := range result {
		if !seen[item.Kind+":"+item.ResourceID+":"+item.DueOn] {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) > 100 {
		filtered = filtered[:100]
	}
	return filtered, nil
}

// Dismiss uses one atomic INSERT, and replays of the same action are harmless.
// Existence is verified against the workspace's currently outstanding inbox.
func (s *Store) Dismiss(ctx context.Context, workspace, kind, id, day string, now time.Time) error {
	if !accounts.IsValidKey(workspace) || !accounts.IsValidKey(id) || (kind != "recurring" && kind != "contribution") || !validDate(day) {
		return ErrInvalid
	}
	items, err := s.List(ctx, workspace, now)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Kind == kind && item.ResourceID == id && item.DueOn == day {
			_, err = s.db.Exec(ctx, `INSERT INTO reminder_dismissals(workspace_id,kind,resource_id,due_on)
  VALUES($1,$2,$3,$4::date) ON CONFLICT DO NOTHING`, workspace, kind, id, day)
			return err
		}
	}
	// A duplicate dismissal is still a successful replay.
	var exists bool
	err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reminder_dismissals
 WHERE workspace_id=$1 AND kind=$2 AND resource_id=$3 AND due_on=$4::date)`, workspace, kind, id, day).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return ErrNotFound
}
func validDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	t, e := time.Parse("2006-01-02", s)
	return e == nil && t.Format("2006-01-02") == s && t.Year() >= 2000 && t.Year() <= 2100
}
