package contributions

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

func advisory(ctx context.Context, tx pgx.Tx, workspace, key string) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, workspace, key)
	return e
}
func classify(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return ErrResolved
		case "23503":
			return ErrNotFound
		case "23514":
			return ErrInvalid
		}
	}
	return err
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (s *Store) Create(ctx context.Context, ws string, in NewObligation) (string, error) {
	if !accounts.IsValidKey(ws) {
		return "", ErrInvalid
	}
	n, e := normalizeObligation(in)
	if e != nil {
		return "", e
	}
	var id string
	e = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if e := advisory(ctx, tx, ws, "contribution-create:"+n.Key); e != nil {
			return e
		}
		var prior []byte
		e := tx.QueryRow(ctx, `SELECT id,fingerprint FROM contribution_obligations
   WHERE workspace_id=$1 AND idempotency_key=$2`, ws, n.Key).Scan(&id, &prior)
		if e == nil {
			if !bytes.Equal(prior, n.Hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if n.Scheme == "mp2" {
			var kind string
			var archived bool
			e = tx.QueryRow(ctx, `SELECT kind,archived_at IS NOT NULL FROM accounts
    WHERE workspace_id=$1 AND id=$2 AND currency='PHP' FOR SHARE`,
				ws, n.MP2AccountID).Scan(&kind, &archived)
			if errors.Is(e, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if e != nil {
				return e
			}
			if kind != "savings" || archived {
				return ErrInvalid
			}
		} else {
			var kind string
			var archived bool
			e = tx.QueryRow(ctx, `SELECT kind,archived_at IS NOT NULL FROM finance_categories
    WHERE workspace_id=$1 AND id=$2 AND currency='PHP' FOR SHARE`,
				ws, n.ExpenseCategoryID).Scan(&kind, &archived)
			if errors.Is(e, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if e != nil {
				return e
			}
			if kind != "expense" || archived {
				return ErrInvalid
			}
		}
		id, e = accounts.NewSetupKey()
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO contribution_obligations
   (id,workspace_id,scheme,period_on,due_on,amount_minor,expense_category_id,
    mp2_savings_account_id,note,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4::date,$5::date,$6,$7,$8,$9,$10,$11)`,
			id, ws, n.Scheme, n.PeriodOn, nullable(n.DueOn), n.AmountMinor,
			nullable(n.ExpenseCategoryID), nullable(n.MP2AccountID), n.Note, n.Key, n.Hash[:])
		return e
	})
	if e != nil {
		return "", classify(e)
	}
	return id, nil
}

// Resolve records an existing payment or an explicit skipped period.
// Neither path creates an expense, bank transfer, or ledger journal.
func (s *Store) Resolve(ctx context.Context, ws string, in NewResolution) (string, error) {
	if !accounts.IsValidKey(ws) {
		return "", ErrInvalid
	}
	n, e := normalizeResolution(in)
	if e != nil {
		return "", e
	}
	var id string
	e = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if e := advisory(ctx, tx, ws, "contribution-resolve:"+n.Key); e != nil {
			return e
		}
		var prior []byte
		e := tx.QueryRow(ctx, `SELECT id,fingerprint FROM contribution_resolutions
   WHERE workspace_id=$1 AND idempotency_key=$2`, ws, n.Key).Scan(&id, &prior)
		if e == nil {
			if !bytes.Equal(prior, n.Hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		var scheme, categoryID, mp2ID string
		var amount int64
		e = tx.QueryRow(ctx, `SELECT scheme,COALESCE(expense_category_id,''),
   COALESCE(mp2_savings_account_id,''),amount_minor
   FROM contribution_obligations WHERE workspace_id=$1 AND id=$2 FOR UPDATE`,
			ws, n.ObligationID).Scan(&scheme, &categoryID, &mp2ID, &amount)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		var exists bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contribution_resolutions
   WHERE workspace_id=$1 AND obligation_id=$2)`, ws, n.ObligationID).Scan(&exists)
		if e != nil {
			return e
		}
		if exists {
			return ErrResolved
		}
		if n.Action == "paid" {
			if scheme == "mp2" {
				if n.TransferID == "" || n.ExpenseID != "" {
					return ErrInvalid
				}
				var valid bool
				e = tx.QueryRow(ctx, `SELECT EXISTS(
      SELECT 1 FROM transfer_operations t
       JOIN accounts source ON source.workspace_id=t.workspace_id
         AND source.id=t.source_account_id
       WHERE t.workspace_id=$1 AND t.id=$2 AND t.source_currency='PHP'
        AND t.target_currency='PHP' AND t.target_account_id=$3 AND t.send_minor=$4
        AND t.receive_minor=$4 AND t.fee_minor=0
        AND source.kind IN ('bank','cash','savings','ewallet')
     )`, ws, n.TransferID, mp2ID, amount).Scan(&valid)
				if e != nil {
					return e
				}
				if !valid {
					return ErrInvalid
				}
			} else {
				if n.ExpenseID == "" || n.TransferID != "" {
					return ErrInvalid
				}
				var valid bool
				e = tx.QueryRow(ctx, `SELECT EXISTS(
      SELECT 1 FROM finance_transactions t
      JOIN finance_transaction_splits sp
        ON sp.workspace_id=t.workspace_id AND sp.transaction_id=t.id
      WHERE t.workspace_id=$1 AND t.id=$2 AND t.kind='expense' AND t.currency='PHP'
       AND sp.category_id=$3 AND sp.amount_minor=$4
       AND NOT EXISTS(SELECT 1 FROM recurring_occurrence_actions r
        WHERE r.workspace_id=$1 AND r.expense_transaction_id=t.id)
     )`, ws, n.ExpenseID, categoryID, amount).Scan(&valid)
				if e != nil {
					return e
				}
				if !valid {
					return ErrInvalid
				}
			}
		}
		id, e = accounts.NewSetupKey()
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO contribution_resolutions
   (id,workspace_id,obligation_id,action,expense_transaction_id,
    transfer_id,resolved_on,note,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10)`,
			id, ws, n.ObligationID, n.Action, nullable(n.ExpenseID), nullable(n.TransferID),
			n.ResolvedOn, n.Note, n.Key, n.Hash[:])
		return e
	})
	if e != nil {
		return "", classify(e)
	}
	return id, nil
}
func (s *Store) Obligations(ctx context.Context, ws string, now time.Time) ([]Obligation, error) {
	if !accounts.IsValidKey(ws) {
		return nil, ErrInvalid
	}
	rows, e := s.db.Query(ctx, `
  SELECT o.id,o.scheme,o.period_on::text,COALESCE(o.due_on::text,''),
    o.amount_minor,COALESCE(c.name,''),COALESCE(a.name,''),o.note,
    COALESCE(r.action,''),COALESCE(o.expense_category_id,''),
    COALESCE(o.mp2_savings_account_id,'')
  FROM contribution_obligations o
  LEFT JOIN contribution_resolutions r ON r.obligation_id=o.id AND r.workspace_id=o.workspace_id
  LEFT JOIN finance_categories c ON c.id=o.expense_category_id AND c.workspace_id=o.workspace_id
  LEFT JOIN accounts a ON a.id=o.mp2_savings_account_id AND a.workspace_id=o.workspace_id
  WHERE o.workspace_id=$1
  ORDER BY o.period_on DESC,o.created_at DESC,o.id DESC LIMIT 100`, ws)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]Obligation, 0)
	for rows.Next() {
		var v Obligation
		var action, period string
		if e := rows.Scan(&v.ID, &v.Scheme, &period, &v.DueOn,
			&v.AmountMinor, &v.CategoryName, &v.MP2AccountName, &v.Note, &action,
			&v.ExpenseCategoryID, &v.MP2AccountID); e != nil {
			return nil, e
		}
		v.Period = period[:7]
		v.SchemeLabel = SchemeLabel(v.Scheme)
		v.Status = Status(v.DueOn, action, now)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) History(ctx context.Context, ws string) ([]Resolution, error) {
	if !accounts.IsValidKey(ws) {
		return nil, ErrInvalid
	}
	rows, e := s.db.Query(ctx, `
  SELECT r.id,r.obligation_id,o.scheme,o.period_on::text,r.action,
   r.resolved_on::text,COALESCE(r.expense_transaction_id,''),
   COALESCE(r.transfer_id,''),r.note,o.amount_minor
  FROM contribution_resolutions r
  JOIN contribution_obligations o ON o.id=r.obligation_id AND o.workspace_id=r.workspace_id
  WHERE r.workspace_id=$1 ORDER BY r.created_at DESC,r.id DESC LIMIT 100`, ws)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]Resolution, 0)
	for rows.Next() {
		var v Resolution
		var period string
		if e := rows.Scan(&v.ID, &v.ObligationID, &v.Scheme, &period, &v.Action,
			&v.ResolvedOn, &v.ExpenseID, &v.TransferID, &v.Note, &v.AmountMinor); e != nil {
			return nil, e
		}
		v.Period = period[:7]
		out = append(out, v)
	}
	return out, rows.Err()
}
