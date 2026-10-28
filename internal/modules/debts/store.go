package debts

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }
func advisory(ctx context.Context, tx pgx.Tx, ws, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, ws, key)
	return err
}
func pgErr(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "23505":
			return ErrRecorded
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

func lockAccounts(ctx context.Context, tx pgx.Tx, ws string, ids ...string) (map[string]account, error) {
	rows, err := tx.Query(ctx, `
  SELECT id,currency,kind,archived_at IS NOT NULL FROM accounts
  WHERE workspace_id=$1 AND id=ANY($2::text[]) ORDER BY id FOR UPDATE`, ws, ids)
	if err != nil {
		return nil, err
	}
	result := map[string]account{}
	for rows.Next() {
		var a account
		if err := rows.Scan(&a.ID, &a.Currency, &a.Kind, &a.Archived); err != nil {
			rows.Close()
			return nil, err
		}
		result[a.ID] = a
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
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
func ledgerBalance(ctx context.Context, tx pgx.Tx, ws, id string, liability bool) (int64, error) {
	var raw string
	err := tx.QueryRow(ctx, `
  SELECT COALESCE(SUM(CASE WHEN $3::boolean
    THEN credit_minor::numeric-debit_minor::numeric
    ELSE debit_minor::numeric-credit_minor::numeric END),0)::text
  FROM ledger_lines WHERE workspace_id=$1 AND ledger_account_id=$2`,
		ws, id, liability).Scan(&raw)
	if err != nil {
		return 0, err
	}
	return parseMinorSQL(raw)
}

// Create creates an immutable repayment schedule against an EXISTING ledger
// liability: the plan itself does not originate or increase principal.
func (s *Store) Create(ctx context.Context, ws string, input NewPlan) (string, error) {
	if !accounts.IsValidKey(ws) {
		return "", ErrInvalid
	}
	n, err := normalizePlan(input)
	if err != nil {
		return "", err
	}
	var id string
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := advisory(ctx, tx, ws, "debt-create:"+n.Key); err != nil {
			return err
		}
		var previous []byte
		err := tx.QueryRow(ctx, `
   SELECT id,fingerprint FROM debt_plans WHERE workspace_id=$1 AND idempotency_key=$2`,
			ws, n.Key).Scan(&id, &previous)
		if err == nil {
			if !bytes.Equal(previous, n.Hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		aa, err := lockAccounts(ctx, tx, ws, n.LoanAccountID, n.FundingAccountID)
		if err != nil {
			return err
		}
		loan, funding := aa[n.LoanAccountID], aa[n.FundingAccountID]
		if loan.Kind != "loan" || loan.Currency != funding.Currency ||
			(funding.Kind != "cash" && funding.Kind != "bank" && funding.Kind != "savings" && funding.Kind != "ewallet") {
			return ErrInvalid
		}
		principal, err := ledgerBalance(ctx, tx, ws, n.LoanAccountID, true)
		if err != nil {
			return err
		}
		if principal < int64(n.Months) || principal > MaxMinor {
			return ErrBalance
		}
		schedule, err := Schedule(principal, n.AnnualRateBps, n.Months, n.FirstDue)
		if err != nil {
			return err
		}
		id, err = accounts.NewSetupKey()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
   INSERT INTO debt_plans(id,workspace_id,currency,name,loan_account_id,
    funding_account_id,principal_minor,annual_rate_bps,installments,
    first_due_on,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date,$11,$12)`,
			id, ws, loan.Currency, n.Name, n.LoanAccountID, n.FundingAccountID,
			principal, n.AnnualRateBps, n.Months, n.FirstDue, n.Key, n.Hash[:])
		if err != nil {
			return err
		}
		for _, part := range schedule {
			installmentID, e := accounts.NewSetupKey()
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `
    INSERT INTO debt_installments(id,workspace_id,plan_id,currency,
     installment_number,due_on,principal_minor,interest_minor,amount_minor)
    VALUES($1,$2,$3,$4,$5,$6::date,$7,$8,$9)`,
				installmentID, ws, id, loan.Currency, part.Number, part.DueOn,
				part.PrincipalMinor, part.InterestMinor, part.AmountMinor)
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return "", pgErr(err)
	}
	return id, nil
}

// Pay records one FULL scheduled installment. Principal is a balance-sheet
// movement, interest is a real expense, and the bank is credited exactly once.
func (s *Store) Pay(ctx context.Context, ws string, input PayInput) (string, error) {
	if !accounts.IsValidKey(ws) {
		return "", ErrInvalid
	}
	n, err := normalizePay(input)
	if err != nil {
		return "", err
	}
	var id string
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := advisory(ctx, tx, ws, "debt-pay:"+n.Key); err != nil {
			return err
		}
		var previous []byte
		err := tx.QueryRow(ctx, `
   SELECT id,fingerprint FROM debt_payments WHERE workspace_id=$1 AND idempotency_key=$2`,
			ws, n.Key).Scan(&id, &previous)
		if err == nil {
			if !bytes.Equal(previous, n.Hash[:]) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var currency, loanID, fundingID string
		var original int64
		err = tx.QueryRow(ctx, `
   SELECT currency,loan_account_id,funding_account_id,principal_minor
   FROM debt_plans WHERE id=$1 AND workspace_id=$2 FOR UPDATE`,
			n.PlanID, ws).Scan(&currency, &loanID, &fundingID, &original)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var number int
		var principal, interest, total int64
		err = tx.QueryRow(ctx, `
   SELECT installment_number,principal_minor,interest_minor,amount_minor
   FROM debt_installments
   WHERE id=$1 AND plan_id=$2 AND workspace_id=$3 AND currency=$4`,
			n.InstallmentID, n.PlanID, ws, currency).
			Scan(&number, &principal, &interest, &total)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var already bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(
   SELECT 1 FROM debt_payments WHERE workspace_id=$1 AND installment_id=$2)`,
			ws, n.InstallmentID).Scan(&already)
		if err != nil {
			return err
		}
		if already {
			return ErrRecorded
		}
		var priorCount int
		var priorPrincipal int64
		err = tx.QueryRow(ctx, `
   SELECT COUNT(*),COALESCE(SUM(d.principal_minor),0)
   FROM debt_installments i JOIN debt_payments d ON d.installment_id=i.id
   WHERE i.workspace_id=$1 AND i.plan_id=$2 AND i.installment_number<$3`,
			ws, n.PlanID, number).Scan(&priorCount, &priorPrincipal)
		if err != nil {
			return err
		}
		if priorCount != number-1 {
			return ErrInvalid
		} // pay installments in order
		aa, err := lockAccounts(ctx, tx, ws, loanID, fundingID)
		if err != nil {
			return err
		}
		if aa[loanID].Kind != "loan" || aa[fundingID].Currency != currency ||
			aa[loanID].Currency != currency {
			return ErrInvalid
		}
		loanBalance, err := ledgerBalance(ctx, tx, ws, loanID, true)
		if err != nil {
			return err
		}
		if loanBalance != original-priorPrincipal || loanBalance < principal {
			return ErrBalance
		}
		cashBalance, err := ledgerBalance(ctx, tx, ws, fundingID, false)
		if err != nil {
			return err
		}
		if cashBalance < total {
			return ErrFunds
		}
		interestID := ""
		if interest > 0 {
			interestID, err = accounts.NewSetupKey()
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `
    INSERT INTO ledger_accounts(id,workspace_id,currency,kind,system_key)
    VALUES($1,$2,$3,'expense','loan_interest')
    ON CONFLICT(workspace_id,currency,system_key) DO NOTHING`,
				interestID, ws, currency)
			if err != nil {
				return err
			}
			err = tx.QueryRow(ctx, `SELECT id FROM ledger_accounts
    WHERE workspace_id=$1 AND currency=$2 AND system_key='loan_interest'`,
				ws, currency).Scan(&interestID)
			if err != nil {
				return err
			}
		}
		lines := []ledger.Line{
			{AccountID: loanID, DebitMinor: principal},
			{AccountID: fundingID, CreditMinor: total},
		}
		if interest > 0 {
			lines = append(lines, ledger.Line{AccountID: interestID, DebitMinor: interest})
		}
		journalID, err := ledger.PostInTx(ctx, tx, ws, currency, "debt-payment:"+n.Key, lines)
		if err != nil {
			return err
		}
		id, err = accounts.NewSetupKey()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
   INSERT INTO debt_payments(id,workspace_id,plan_id,installment_id,currency,
    funding_account_id,loan_account_id,principal_minor,interest_minor,amount_minor,
    journal_id,paid_on,idempotency_key,fingerprint)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::date,$13,$14)`,
			id, ws, n.PlanID, n.InstallmentID, currency, fundingID, loanID,
			principal, interest, total, journalID, n.PaidOn, n.Key, n.Hash[:])
		return err
	})
	if err != nil {
		return "", pgErr(err)
	}
	return id, nil
}

// Plans reads the authoritative outstanding principal from immutable ledger
// postings, not an independently mutable debt balance.
func (s *Store) Plans(ctx context.Context, ws string) ([]Plan, error) {
	if !accounts.IsValidKey(ws) {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query(ctx, `
  SELECT p.id,p.name,p.currency,p.loan_account_id,p.funding_account_id,
   la.name,fa.name,p.principal_minor,p.annual_rate_bps,p.installments,
   p.first_due_on::text,
   (SELECT COUNT(*) FROM debt_payments d
    WHERE d.workspace_id=p.workspace_id AND d.plan_id=p.id),
   COALESCE((SELECT SUM(l.credit_minor::numeric-l.debit_minor::numeric)
    FROM ledger_lines l WHERE l.workspace_id=p.workspace_id
      AND l.ledger_account_id=p.loan_account_id),0)::text
  FROM debt_plans p
  JOIN accounts la ON la.id=p.loan_account_id AND la.workspace_id=p.workspace_id
  JOIN accounts fa ON fa.id=p.funding_account_id AND fa.workspace_id=p.workspace_id
  WHERE p.workspace_id=$1 ORDER BY p.created_at DESC,p.id DESC LIMIT 100`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Plan, 0)
	for rows.Next() {
		var p Plan
		var raw string
		if err := rows.Scan(&p.ID, &p.Name, &p.Currency, &p.LoanAccountID, &p.FundingAccountID,
			&p.LoanName, &p.FundingName, &p.PrincipalMinor, &p.AnnualRateBps,
			&p.Months, &p.FirstDue, &p.Completed, &raw); err != nil {
			return nil, err
		}
		p.OutstandingMinor, err = parseMinorSQL(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Installments(ctx context.Context, ws string) ([]Installment, error) {
	if !accounts.IsValidKey(ws) {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query(ctx, `
  SELECT i.id,i.plan_id,i.currency,i.due_on::text,i.installment_number,
   i.principal_minor,i.interest_minor,i.amount_minor,
   CASE WHEN d.id IS NULL THEN 'pending' ELSE 'paid' END
  FROM debt_installments i JOIN debt_plans p ON p.id=i.plan_id
   AND p.workspace_id=i.workspace_id
  LEFT JOIN debt_payments d ON d.installment_id=i.id
   AND d.workspace_id=i.workspace_id
  WHERE i.workspace_id=$1 ORDER BY i.due_on,i.plan_id,i.installment_number LIMIT 500`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Installment, 0)
	for rows.Next() {
		var i Installment
		if err := rows.Scan(&i.ID, &i.PlanID, &i.Currency, &i.DueOn, &i.Number,
			&i.PrincipalMinor, &i.InterestMinor, &i.AmountMinor, &i.Status); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Store) Payments(ctx context.Context, ws string) ([]Payment, error) {
	if !accounts.IsValidKey(ws) {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query(ctx, `
  SELECT d.id,d.plan_id,p.name,d.currency,d.paid_on::text,
   i.installment_number,d.principal_minor,d.interest_minor,d.amount_minor
  FROM debt_payments d JOIN debt_plans p ON p.id=d.plan_id
   AND p.workspace_id=d.workspace_id
  JOIN debt_installments i ON i.id=d.installment_id
  WHERE d.workspace_id=$1 ORDER BY d.created_at DESC,d.id DESC LIMIT 100`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Payment, 0)
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.PlanID, &p.PlanName, &p.Currency, &p.PaidOn,
			&p.Number, &p.PrincipalMinor, &p.InterestMinor, &p.AmountMinor); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Counts(ctx context.Context, ws string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM debt_plans WHERE workspace_id=$1`, ws).Scan(&n)
	return n, err
}
