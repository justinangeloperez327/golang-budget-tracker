package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// PostAdjustment posts a balanced, single-currency journal atomically.
// Workspace identity must be supplied by a trusted authenticated context.
func (s *Store) PostAdjustment(ctx context.Context, workspaceID, currency, key string, lines []Line) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var e error
		id, e = postTx(ctx, tx, workspaceID, currency, key, "adjustment", "", lines)
		return e
	})
	return id, err
}

func postTx(ctx context.Context, tx pgx.Tx, workspaceID, currency, key, kind, reverses string, lines []Line) (string, error) {
	if !validID(workspaceID) || (currency != "AED" && currency != "PHP") || validateKey(key) != nil ||
		(kind != "opening" && kind != "adjustment" && kind != "reversal") ||
		(kind == "reversal") != (reverses != "") || ValidateLines(lines) != nil {
		return "", ErrInvalid
	}
	fingerprint := fingerprint(currency, kind, reverses, lines)
	id, err := newID()
	if err != nil {
		return "", err
	}
	var inserted string
	err = tx.QueryRow(ctx, `
		INSERT INTO ledger_journals(id,workspace_id,currency,idempotency_key,kind,fingerprint,reverses_journal_id)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(workspace_id,idempotency_key) DO NOTHING
		RETURNING id`, id, workspaceID, currency, key, kind, fingerprint[:], nullableID(reverses)).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var previous []byte
		if err := tx.QueryRow(ctx, `
			SELECT id,fingerprint FROM ledger_journals
			WHERE workspace_id=$1 AND idempotency_key=$2`, workspaceID, key).
			Scan(&inserted, &previous); err != nil {
			return "", err
		}
		if !bytes.Equal(previous, fingerprint[:]) {
			return "", ErrConflict
		}
		return inserted, nil
	}
	if err != nil {
		return "", ledgerError(err)
	}
	for _, l := range lines {
		_, err := tx.Exec(ctx, `
			INSERT INTO ledger_lines(journal_id,ledger_account_id,workspace_id,currency,debit_minor,credit_minor)
			VALUES($1,$2,$3,$4,$5,$6)`, inserted, l.AccountID, workspaceID, currency, l.DebitMinor, l.CreditMinor)
		if err != nil {
			return "", ledgerError(err)
		}
	}
	// Sealing forbids any later lines being appended to committed entries.
	if _, err := tx.Exec(ctx, `UPDATE ledger_journals SET sealed=TRUE WHERE id=$1 AND sealed=FALSE`, inserted); err != nil {
		return "", ledgerError(err)
	}
	// Deferred database constraint trigger validates exactly 2+ lines and
	// balancing again at commit, including for non-Go clients.
	return inserted, nil
}

func nullableID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func ledgerError(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "23503":
			return ErrNotFound
		case "23505":
			return ErrReversed
		case "23514":
			return ErrInvalid
		}
	}
	return err
}

// Reverse creates a second, immutable journal containing inverse postings.
// Only one reversal per original journal can be committed.
func (s *Store) Reverse(ctx context.Context, workspaceID, originalID, key string) (string, error) {
	if !validID(workspaceID) || !validID(originalID) || validateKey(key) != nil {
		return "", ErrInvalid
	}
	var newJournal string
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var currency, kind string
		err := tx.QueryRow(ctx, `
			SELECT currency,kind FROM ledger_journals
			WHERE workspace_id=$1 AND id=$2 FOR SHARE`, workspaceID, originalID).Scan(&currency, &kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if kind == "reversal" {
			return ErrInvalid
		}
		rows, err := tx.Query(ctx, `
			SELECT ledger_account_id,debit_minor,credit_minor
			FROM ledger_lines WHERE journal_id=$1 AND workspace_id=$2
			ORDER BY ledger_account_id`, originalID, workspaceID)
		if err != nil {
			return err
		}
		var lines []Line
		for rows.Next() {
			var l Line
			if err := rows.Scan(&l.AccountID, &l.CreditMinor, &l.DebitMinor); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		var e error
		newJournal, e = postTx(ctx, tx, workspaceID, currency, key, "reversal", originalID, lines)
		return e
	})
	return newJournal, err
}

// Balances derives signed balances from immutable lines. No mutable balance
// cache or cross-currency aggregation is used.
func (s *Store) Balances(ctx context.Context, workspaceID string) ([]Balance, error) {
	if !validID(workspaceID) {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.source_account_id,a.currency,a.kind,
			COALESCE(SUM(CASE WHEN a.kind='asset'
				THEN l.debit_minor::numeric-l.credit_minor::numeric
				ELSE l.credit_minor::numeric-l.debit_minor::numeric END),0)::text
		FROM ledger_accounts a
		LEFT JOIN ledger_lines l ON l.ledger_account_id=a.id AND l.workspace_id=a.workspace_id
		WHERE a.workspace_id=$1 AND a.source_account_id IS NOT NULL
		GROUP BY a.id,a.source_account_id,a.currency,a.kind ORDER BY a.source_account_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Balance, 0)
	for rows.Next() {
		var b Balance
		var amount string
		if err := rows.Scan(&b.AccountID, &b.Currency, &b.Kind, &amount); err != nil {
			return nil, err
		}
		b.Minor, err = strconv.ParseInt(amount, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: account %s", ErrOverflow, b.AccountID)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type History struct {
	ID         string  `json:"id"`
	Currency   string  `json:"currency"`
	Kind       string  `json:"kind"`
	ReversesID *string `json:"reverses_id,omitempty"`
	Lines      []Line  `json:"lines"`
}

// Recent is explicitly limited and workspace-scoped. It never returns
// someone else's journal ID or line contents.
func (s *Store) Recent(ctx context.Context, workspaceID string, limit int) ([]History, error) {
	if !validID(workspaceID) || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query(ctx, `
		SELECT id,currency,kind,reverses_journal_id
		FROM ledger_journals WHERE workspace_id=$1
		ORDER BY created_at DESC,id DESC LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	items := make([]History, 0)
	for rows.Next() {
		var h History
		if err := rows.Scan(&h.ID, &h.Currency, &h.Kind, &h.ReversesID); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range items {
		ls, e := s.db.Query(ctx, `
			SELECT ledger_account_id,debit_minor,credit_minor FROM ledger_lines
			WHERE journal_id=$1 AND workspace_id=$2 ORDER BY id`, items[i].ID, workspaceID)
		if e != nil {
			return nil, e
		}
		items[i].Lines = make([]Line, 0)
		for ls.Next() {
			var l Line
			if e := ls.Scan(&l.AccountID, &l.DebitMinor, &l.CreditMinor); e != nil {
				ls.Close()
				return nil, e
			}
			items[i].Lines = append(items[i].Lines, l)
		}
		e = ls.Err()
		ls.Close()
		if e != nil {
			return nil, e
		}
	}
	return items, nil
}
