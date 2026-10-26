package budgets

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// Set appends an auditable revision. Identical concurrent keys return exactly
// one event ID; conflicting payloads are rejected without mutating history.
func (s *Store) Set(ctx context.Context, workspace string, input Update) (string, error) {
	if !accounts.IsValidKey(workspace) {
		return "", ErrInvalid
	}
	n, err := normalize(input)
	if err != nil {
		return "", err
	}
	var result string
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, workspace, "budget:"+n.Key)
		if err != nil {
			return err
		}
		var existingHash []byte
		err = tx.QueryRow(ctx, `
   SELECT id,fingerprint FROM monthly_budget_events
   WHERE workspace_id=$1 AND idempotency_key=$2`, workspace, n.Key).Scan(&result, &existingHash)
		if err == nil {
			if !bytes.Equal(existingHash, n.Hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var categoryID, allocation, policy any
		if n.Action == "allocation" {
			var kind, currency string
			var archived bool
			err = tx.QueryRow(ctx, `
    SELECT kind,currency,archived_at IS NOT NULL FROM finance_categories
    WHERE workspace_id=$1 AND id=$2 FOR SHARE`, workspace, n.CategoryID).
				Scan(&kind, &currency, &archived)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if kind != "expense" || currency != n.Currency || archived {
				return ErrInvalid
			}
			categoryID = n.CategoryID
			allocation = n.AmountMinor
		} else {
			policy = n.Policy
		}
		id, err := accounts.NewSetupKey()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
   INSERT INTO monthly_budget_events(id,workspace_id,currency,budget_month,action,
    category_id,amount_minor,rollover_policy,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4::date,$5,$6,$7,$8,$9,$10)`,
			id, workspace, n.Currency, monthDate(n.Month), n.Action, categoryID,
			allocation, policy, n.Key, n.Hash[:])
		if err != nil {
			return err
		}
		result = id
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("budget update: %w", err)
	}
	return result, nil
}

func (s *Store) Categories(ctx context.Context, workspace, currency string) ([]Category, error) {
	if !accounts.IsValidKey(workspace) || !ValidCurrency(currency) {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query(ctx, `
  SELECT id,name,archived_at IS NOT NULL FROM finance_categories
  WHERE workspace_id=$1 AND currency=$2 AND kind='expense'
  ORDER BY name,id`, workspace, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Category, 0)
	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.ID, &category.Name, &category.Archived); err != nil {
			return nil, err
		}
		items = append(items, category)
	}
	return items, rows.Err()
}

func (s *Store) Overview(ctx context.Context, workspace, month, currency string) (Overview, []Category, error) {
	if !accounts.IsValidKey(workspace) {
		return Overview{}, nil, ErrInvalid
	}
	if _, err := ValidateMonth(month); err != nil || !ValidCurrency(currency) {
		return Overview{}, nil, ErrInvalid
	}
	categories, err := s.Categories(ctx, workspace, currency)
	if err != nil {
		return Overview{}, nil, err
	}
	plans := map[string]*monthPlan{}
	rows, err := s.db.Query(ctx, `
  SELECT to_char(budget_month,'YYYY-MM'),action,COALESCE(category_id,''),
         COALESCE(amount_minor,0),COALESCE(rollover_policy,'')
  FROM monthly_budget_events WHERE workspace_id=$1 AND currency=$2
    AND budget_month <= $3::date
  ORDER BY created_at,id`, workspace, currency, monthDate(month))
	if err != nil {
		return Overview{}, nil, err
	}
	for rows.Next() {
		var key, action, category, policy string
		var amount int64
		if err := rows.Scan(&key, &action, &category, &amount, &policy); err != nil {
			rows.Close()
			return Overview{}, nil, err
		}
		plan := planFor(plans, key)
		plan.exists = true
		if action == "policy" {
			plan.policy = policy
		} else {
			plan.allocations[category] = amount
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Overview{}, nil, err
	}
	spent := map[string]map[string]int64{}
	rows, err = s.db.Query(ctx, `
  SELECT to_char(t.occurred_on,'YYYY-MM'),sp.category_id,
    SUM(CASE WHEN t.kind='refund' THEN -sp.amount_minor::numeric
             ELSE sp.amount_minor::numeric END)::text
  FROM finance_transactions t
  JOIN finance_transaction_splits sp ON sp.transaction_id=t.id
    AND sp.workspace_id=t.workspace_id AND sp.currency=t.currency
  WHERE t.workspace_id=$1 AND t.currency=$2
    AND t.kind IN ('expense','refund')
    AND t.occurred_on >= DATE '2000-01-01'
    AND t.occurred_on < ($3::date + INTERVAL '1 month')
  GROUP BY to_char(t.occurred_on,'YYYY-MM'),sp.category_id`,
		workspace, currency, monthDate(month))
	if err != nil {
		return Overview{}, nil, err
	}
	for rows.Next() {
		var key, category, raw string
		if err := rows.Scan(&key, &category, &raw); err != nil {
			rows.Close()
			return Overview{}, nil, err
		}
		amount, err := numericMinor(raw)
		if err != nil {
			rows.Close()
			return Overview{}, nil, err
		}
		if spent[key] == nil {
			spent[key] = map[string]int64{}
		}
		spent[key][category] = amount
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Overview{}, nil, err
	}
	overview, err := Calculate(month, currency, plans, spent, categories)
	return overview, categories, err
}
