package remittances

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
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

type account struct {
	ID, Currency, Kind string
	Archived           bool
}

func accountsLocked(ctx context.Context, tx pgx.Tx, ws string, ids ...string) (map[string]account, error) {
	rows, e := tx.Query(ctx, `SELECT id,currency,kind,archived_at IS NOT NULL
 FROM accounts WHERE workspace_id=$1 AND id=ANY($2::text[])
 ORDER BY id FOR UPDATE`, ws, ids)
	if e != nil {
		return nil, e
	}
	result := map[string]account{}
	for rows.Next() {
		var a account
		if e := rows.Scan(&a.ID, &a.Currency, &a.Kind, &a.Archived); e != nil {
			rows.Close()
			return nil, e
		}
		result[a.ID] = a
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if len(result) != len(ids) {
		return nil, ErrNotFound
	}
	for _, id := range ids {
		if result[id].Archived {
			return nil, ErrInvalid
		}
	}
	return result, nil
}
func acceptableAsset(kind string) bool {
	return kind == "cash" || kind == "bank" || kind == "savings" || kind == "ewallet"
}
func assetBalance(ctx context.Context, tx pgx.Tx, ws, id string) (int64, error) {
	var raw string
	e := tx.QueryRow(ctx, `SELECT COALESCE(SUM(l.debit_minor::numeric-l.credit_minor::numeric),0)::text
 FROM ledger_lines l WHERE l.workspace_id=$1 AND l.ledger_account_id=$2`, ws, id).Scan(&raw)
	if e != nil {
		return 0, e
	}
	minor, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || minor > maxMinor || minor < -maxMinor {
		return 0, ErrOverflow
	}
	return minor, nil
}

// Create records a pending intention without posting ledger entries.
func (s *Store) Create(ctx context.Context, ws string, input CreateInput) (string, error) {
	if !accounts.IsValidKey(ws) {
		return "", ErrInvalid
	}
	n, e := normalizeCreate(input)
	if e != nil {
		return "", e
	}
	var id string
	e = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if e := advisory(ctx, tx, ws, "remittance-order:"+n.Key); e != nil {
			return e
		}
		var previous []byte
		e := tx.QueryRow(ctx, `SELECT id,fingerprint FROM remittance_orders
   WHERE workspace_id=$1 AND idempotency_key=$2`, ws, n.Key).Scan(&id, &previous)
		if e == nil {
			if !bytes.Equal(previous, n.Hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		aa, e := accountsLocked(ctx, tx, ws, n.SourceAccountID, n.DestinationAccountID)
		if e != nil {
			return e
		}
		source, dest := aa[n.SourceAccountID], aa[n.DestinationAccountID]
		if !acceptableAsset(source.Kind) || !acceptableAsset(dest.Kind) ||
			source.Currency != "AED" || dest.Currency != "PHP" {
			return ErrInvalid
		}
		id, e = accounts.NewSetupKey()
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `
   INSERT INTO remittance_orders
    (id,workspace_id,idempotency_key,fingerprint,source_account_id,destination_account_id,
     send_minor,fee_minor,receive_minor,rate_units,effective_rate_units,note,requested_on)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::date)`,
			id, ws, n.Key, n.Hash[:], n.SourceAccountID, n.DestinationAccountID,
			n.SendMinor, n.FeeMinor, n.ReceiveMinor, n.RateUnits, n.EffectiveRateUnits, n.Note, n.RequestedOn)
		return e
	})
	if e != nil {
		return "", classify(e)
	}
	return id, nil
}

// Resolve settles via the *same* Group 9 transfer service, inside this
// transaction. Cancelling an intention does not move any money.
func (s *Store) Resolve(ctx context.Context, ws string, input ResolveInput) (string, error) {
	if !accounts.IsValidKey(ws) {
		return "", ErrInvalid
	}
	n, e := normalizeResolution(input)
	if e != nil {
		return "", e
	}
	var id string
	e = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if e := advisory(ctx, tx, ws, "remittance-resolve:"+n.Key); e != nil {
			return e
		}
		var previous []byte
		e := tx.QueryRow(ctx, `SELECT id,fingerprint FROM remittance_resolutions
    WHERE workspace_id=$1 AND idempotency_key=$2`, ws, n.Key).Scan(&id, &previous)
		if e == nil {
			if !bytes.Equal(previous, n.Hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		var source, dest, note string
		var send, fee, receive, rate int64
		e = tx.QueryRow(ctx, `SELECT source_account_id,destination_account_id,note,
   send_minor,fee_minor,receive_minor,rate_units
   FROM remittance_orders WHERE workspace_id=$1 AND id=$2 FOR UPDATE`,
			ws, n.OrderID).Scan(&source, &dest, &note, &send, &fee, &receive, &rate)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		var present bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remittance_resolutions
    WHERE workspace_id=$1 AND order_id=$2)`, ws, n.OrderID).Scan(&present)
		if e != nil {
			return e
		}
		if present {
			return ErrResolved
		}
		var transferID any
		if n.Action == "settled" {
			aa, e := accountsLocked(ctx, tx, ws, source, dest)
			if e != nil {
				return e
			}
			if !acceptableAsset(aa[source].Kind) || !acceptableAsset(aa[dest].Kind) ||
				aa[source].Currency != "AED" || aa[dest].Currency != "PHP" {
				return ErrInvalid
			}
			cash, e := assetBalance(ctx, tx, ws, source)
			if e != nil {
				return e
			}
			if cash < send+fee {
				return ErrFunds
			}
			destination, e := assetBalance(ctx, tx, ws, dest)
			if e != nil {
				return e
			}
			if destination > maxMinor-receive {
				return ErrOverflow
			}
			// A transfer's key is the unique remittance order ID. The transfer
			// and resolution either commit together or roll back together.
			tid, e := transfers.NewStore(s.db).TransferTx(ctx, tx, ws, transfers.Input{
				FromID: source, ToID: dest, Amount: fmt.Sprintf("%d.%02d", send/100, send%100),
				Fee: fmt.Sprintf("%d.%02d", fee/100, fee%100), Received: fmt.Sprintf("%d.%02d", receive/100, receive%100),
				Rate: transfers.RateText(rate), Date: n.Date, Note: note, Key: n.OrderID,
			})
			if e != nil {
				return e
			}
			transferID = tid
		}
		id, e = accounts.NewSetupKey()
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `
   INSERT INTO remittance_resolutions(id,workspace_id,order_id,action,
    transfer_id,settled_on,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4,$5,$6::date,$7,$8)`,
			id, ws, n.OrderID, n.Action, transferID, n.Date, n.Key, n.Hash[:])
		return e
	})
	if e != nil {
		return "", classify(e)
	}
	return id, nil
}
func (s *Store) Orders(ctx context.Context, ws string) ([]Order, error) {
	if !accounts.IsValidKey(ws) {
		return nil, ErrInvalid
	}
	rows, e := s.db.Query(ctx, `
  SELECT o.id,source.name,dest.name,o.note,o.requested_on::text,
   COALESCE(r.action,'pending'),COALESCE(r.settled_on::text,''),
   COALESCE(r.transfer_id,''),o.send_minor,o.fee_minor,o.receive_minor,
   o.rate_units,o.effective_rate_units
  FROM remittance_orders o
   JOIN accounts source ON source.id=o.source_account_id AND source.workspace_id=o.workspace_id
   JOIN accounts dest ON dest.id=o.destination_account_id AND dest.workspace_id=o.workspace_id
   LEFT JOIN remittance_resolutions r ON r.order_id=o.id AND r.workspace_id=o.workspace_id
  WHERE o.workspace_id=$1 ORDER BY o.created_at DESC,o.id DESC LIMIT 100`, ws)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]Order, 0)
	for rows.Next() {
		var o Order
		if e := rows.Scan(&o.ID, &o.FromName, &o.ToName, &o.Note, &o.RequestedOn,
			&o.Status, &o.ResolvedOn, &o.TransferID, &o.SendMinor, &o.FeeMinor,
			&o.ReceiveMinor, &o.RateUnits, &o.EffectiveRateUnits); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
