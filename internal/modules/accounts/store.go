package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

type Store struct{ db *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{db: pool} }

// Create is atomic: the account and opening record appear together, or neither.
// setup_key is unique per workspace, rejecting duplicate POSTs.
func (s *Store) Create(ctx context.Context, workspaceID string, input CreateInput) (string, error) {
	in, minor, err := Validate(input)
	if err != nil {
		return "", err
	}
	id, err := NewSetupKey()
	if err != nil {
		return "", err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO accounts (id,workspace_id,name,kind,currency,setup_key)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			id, workspaceID, in.Name, in.Kind, in.Currency, in.SetupKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO account_openings (account_id,workspace_id,currency,amount_minor)
			VALUES ($1,$2,$3,$4)`, id, workspaceID, in.Currency, minor)
		if err != nil {
			return err
		}
		return ledger.EnsureOpeningTx(ctx, tx, workspaceID, id)
	})
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			return "", ErrDuplicate
		}
		return "", fmt.Errorf("save account: %w", err)
	}
	return id, nil
}

// List requires a trusted workspace ID from the authenticated session.
func (s *Store) List(ctx context.Context, workspaceID string) ([]Account, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id,a.name,a.kind,a.currency,o.amount_minor,
		       a.archived_at IS NOT NULL,a.created_at
		FROM accounts a
		JOIN account_openings o ON o.workspace_id=a.workspace_id
		  AND o.account_id=a.id AND o.currency=a.currency
		WHERE a.workspace_id=$1
		ORDER BY a.created_at DESC,a.id DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	items := make([]Account, 0)
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Kind, &a.Currency, &a.OpeningMinor, &a.Archived, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("read account: %w", err)
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	balances, err := ledger.NewStore(s.db).Balances(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("read ledger balances: %w", err)
	}
	byID := make(map[string]int64, len(balances))
	for _, b := range balances {
		byID[b.AccountID] = b.Minor
	}
	for i := range items {
		items[i].LedgerMinor = byID[items[i].ID]
	}
	return items, nil
}

func (s *Store) Archive(ctx context.Context, workspaceID, id string) error {
	if !IsValidKey(id) {
		return ErrNotFound
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE accounts SET archived_at=now()
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL`, id, workspaceID)
	if err != nil {
		return fmt.Errorf("archive account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
