// Package reports provides read-only, workspace-scoped financial reports.
// Transactions use occurred_on; ledger balances/fees use journal posting time.
package reports

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/budgets"
)

var ErrInvalid = errors.New("invalid report period or currency")
var ErrOverflow = errors.New("report total exceeds int64 precision")

type Query struct{ Period, Currency string }
type Range struct {
	Start, End, Label string
	Annual            bool
	Year              int
}
type Category struct {
	ID, Name, Kind                    string
	GrossMinor, RefundMinor, NetMinor int64
}
type Month struct {
	Period                                                                      string
	IncomeMinor, ExpensesMinor, RefundMinor, FeesMinor, InterestMinor, NetMinor int64
}
type AccountMovement struct {
	ID, Name, Kind                                                          string
	OpeningMinor, DebitsMinor, CreditsMinor, ClosingMinor, NetMovementMinor int64
}
type BudgetRow struct {
	CategoryID, Name                                        string
	PlannedMinor, CarriedMinor, ActualMinor, RemainingMinor int64
}
type Report struct {
	Query                                                                                                  Query
	Period                                                                                                 Range
	IncomeMinor, ExpensesMinor, RefundMinor, NetSpendingMinor, FeesMinor, InterestMinor, OperatingNetMinor int64
	OpeningAssetsMinor, OpeningLiabilitiesMinor, OpeningNetWorthMinor                                      int64
	ClosingAssetsMinor, ClosingLiabilitiesMinor, ClosingNetWorthMinor                                      int64
	Categories                                                                                             []Category
	Monthly                                                                                                []Month
	Accounts                                                                                               []AccountMovement
	Budgets                                                                                                []BudgetRow
	BudgetConfigured                                                                                       bool
	BudgetPlannedMinor, BudgetCarriedMinor, BudgetActualMinor, BudgetRemainingMinor                        int64
}
type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

func Parse(q Query) (Range, error) {
	if q.Currency != "AED" && q.Currency != "PHP" {
		return Range{}, ErrInvalid
	}
	if _, err := budgets.ValidateMonth(q.Period); err == nil {
		t, _ := time.Parse("2006-01", q.Period)
		return Range{Start: t.Format("2006-01-02"), End: t.AddDate(0, 1, 0).Format("2006-01-02"), Label: q.Period, Year: t.Year()}, nil
	}
	if len(q.Period) != 4 {
		return Range{}, ErrInvalid
	}
	year, err := strconv.Atoi(q.Period)
	if err != nil || year < 2000 || year > 2100 || fmt.Sprintf("%04d", year) != q.Period {
		return Range{}, ErrInvalid
	}
	return Range{Start: q.Period + "-01-01", End: fmt.Sprintf("%04d-01-01", year+1), Label: q.Period, Annual: true, Year: year}, nil
}
func add(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}
func sub(a, b int64) (int64, error) {
	if b == math.MinInt64 {
		return 0, ErrOverflow
	}
	return add(a, -b)
}
func parse(raw string) (int64, error) {
	v, e := strconv.ParseInt(raw, 10, 64)
	if e != nil {
		return 0, ErrOverflow
	}
	return v, nil
}
func addTo(dst *int64, v int64) error {
	n, e := add(*dst, v)
	if e == nil {
		*dst = n
	}
	return e
}
func asset(kind string) bool {
	return kind == "bank" || kind == "cash" || kind == "savings" || kind == "ewallet"
}
func liability(kind string) bool { return kind == "loan" || kind == "credit" }

// Load computes one period/currency from immutable finance transactions and
// the corresponding account-backed ledger journals.
func (s *Store) Load(ctx context.Context, ws string, q Query) (Report, error) {
	if !accounts.IsValidKey(ws) {
		return Report{}, ErrInvalid
	}
	span, e := Parse(q)
	if e != nil {
		return Report{}, e
	}
	out := Report{Query: q, Period: span, Categories: []Category{}, Monthly: []Month{}, Accounts: []AccountMovement{}, Budgets: []BudgetRow{}}
	// Repeatable read ensures all sections reference the same committed snapshot.
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return Report{}, e
	}
	defer tx.Rollback(ctx)
	if e = s.transactions(ctx, tx, ws, &out); e != nil {
		return Report{}, e
	}
	if e = s.accountHistory(ctx, tx, ws, &out); e != nil {
		return Report{}, e
	}
	if e = s.costs(ctx, tx, ws, &out); e != nil {
		return Report{}, e
	}
	if e = s.budgets(ctx, tx, ws, &out); e != nil {
		return Report{}, e
	}
	out.NetSpendingMinor, e = sub(out.ExpensesMinor, out.RefundMinor)
	if e != nil {
		return Report{}, e
	}
	out.OperatingNetMinor, e = sub(out.IncomeMinor, out.NetSpendingMinor)
	if e != nil {
		return Report{}, e
	}
	out.OperatingNetMinor, e = sub(out.OperatingNetMinor, out.FeesMinor)
	if e != nil {
		return Report{}, e
	}
	out.OperatingNetMinor, e = sub(out.OperatingNetMinor, out.InterestMinor)
	if e != nil {
		return Report{}, e
	}
	for i := range out.Monthly {
		m := &out.Monthly[i]
		m.NetMinor, e = sub(m.IncomeMinor, m.ExpensesMinor)
		if e != nil {
			return Report{}, e
		}
		for _, v := range []int64{-m.FeesMinor, -m.InterestMinor, m.RefundMinor} {
			m.NetMinor, e = add(m.NetMinor, v)
			if e != nil {
				return Report{}, e
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return Report{}, e
	}
	return out, nil
}
func (s *Store) transactions(ctx context.Context, tx pgx.Tx, ws string, out *Report) error {
	r := out.Period
	rows, e := tx.Query(ctx, `SELECT t.kind,SUM(t.total_minor::numeric)::text
 FROM finance_transactions t
 WHERE t.workspace_id=$1 AND t.currency=$2 AND t.occurred_on >= $3::date AND t.occurred_on < $4::date
 GROUP BY t.kind`, ws, out.Query.Currency, r.Start, r.End)
	if e != nil {
		return e
	}
	for rows.Next() {
		var kind, raw string
		if e = rows.Scan(&kind, &raw); e != nil {
			rows.Close()
			return e
		}
		n, err := parse(raw)
		if err != nil {
			rows.Close()
			return err
		}
		switch kind {
		case "income":
			out.IncomeMinor = n
		case "expense":
			out.ExpensesMinor = n
		case "refund":
			out.RefundMinor = n
		default:
			rows.Close()
			return ErrInvalid
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = tx.Query(ctx, `
 SELECT c.id,c.name,c.kind,
  COALESCE(SUM(CASE WHEN t.kind<>'refund' THEN sp.amount_minor::numeric ELSE 0 END),0)::text,
  COALESCE(SUM(CASE WHEN t.kind='refund' THEN sp.amount_minor::numeric ELSE 0 END),0)::text
 FROM finance_transactions t
 JOIN finance_transaction_splits sp ON sp.transaction_id=t.id AND sp.workspace_id=t.workspace_id
 JOIN finance_categories c ON c.id=sp.category_id AND c.workspace_id=t.workspace_id
 WHERE t.workspace_id=$1 AND t.currency=$2 AND t.occurred_on >= $3::date AND t.occurred_on < $4::date
 GROUP BY c.id,c.name,c.kind ORDER BY c.kind,c.name,c.id`, ws, out.Query.Currency, r.Start, r.End)
	if e != nil {
		return e
	}
	for rows.Next() {
		var c Category
		var gross, refund string
		if e = rows.Scan(&c.ID, &c.Name, &c.Kind, &gross, &refund); e != nil {
			rows.Close()
			return e
		}
		if c.GrossMinor, e = parse(gross); e != nil {
			rows.Close()
			return e
		}
		if c.RefundMinor, e = parse(refund); e != nil {
			rows.Close()
			return e
		}
		if c.NetMinor, e = sub(c.GrossMinor, c.RefundMinor); e != nil {
			rows.Close()
			return e
		}
		out.Categories = append(out.Categories, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = tx.Query(ctx, `SELECT to_char(t.occurred_on,'YYYY-MM'),t.kind,SUM(t.total_minor::numeric)::text
 FROM finance_transactions t
 WHERE t.workspace_id=$1 AND t.currency=$2 AND t.occurred_on >= $3::date AND t.occurred_on < $4::date
 GROUP BY to_char(t.occurred_on,'YYYY-MM'),t.kind
 ORDER BY to_char(t.occurred_on,'YYYY-MM')`, ws, out.Query.Currency, r.Start, r.End)
	if e != nil {
		return e
	}
	monthly := make(map[string]*Month)
	for rows.Next() {
		var month, kind, raw string
		if e = rows.Scan(&month, &kind, &raw); e != nil {
			rows.Close()
			return e
		}
		n, err := parse(raw)
		if err != nil {
			rows.Close()
			return err
		}
		m := monthly[month]
		if m == nil {
			m = &Month{Period: month}
			monthly[month] = m
		}
		switch kind {
		case "income":
			m.IncomeMinor = n
		case "expense":
			m.ExpensesMinor = n
		case "refund":
			m.RefundMinor = n
		default:
			rows.Close()
			return ErrInvalid
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	first, last := r.Start, r.End
	for t, _ := time.Parse("2006-01-02", first); t.Format("2006-01-02") < last; t = t.AddDate(0, 1, 0) {
		month := t.Format("2006-01")
		m := monthly[month]
		if m == nil {
			m = &Month{Period: month}
		}
		out.Monthly = append(out.Monthly, *m)
	}
	return nil
}

// accountHistory reconstructs opening/closing as-of *journal posting time*.
// Liability account postings are credit-normal; assets are debit-normal.
func (s *Store) accountHistory(ctx context.Context, tx pgx.Tx, ws string, out *Report) error {
	rows, e := tx.Query(ctx, `SELECT a.id,a.name,a.kind,
  COALESCE(SUM(CASE WHEN j.created_at < $3::date THEN l.debit_minor::numeric-l.credit_minor::numeric ELSE 0 END),0)::text,
  COALESCE(SUM(CASE WHEN j.created_at >= $3::date AND j.created_at < $4::date THEN l.debit_minor::numeric ELSE 0 END),0)::text,
  COALESCE(SUM(CASE WHEN j.created_at >= $3::date AND j.created_at < $4::date THEN l.credit_minor::numeric ELSE 0 END),0)::text
 FROM accounts a
 LEFT JOIN ledger_accounts la ON la.source_account_id=a.id AND la.workspace_id=a.workspace_id
 LEFT JOIN ledger_lines l ON l.ledger_account_id=la.id AND l.workspace_id=la.workspace_id
 LEFT JOIN ledger_journals j ON j.id=l.journal_id AND j.workspace_id=l.workspace_id
 WHERE a.workspace_id=$1 AND a.currency=$2
 GROUP BY a.id,a.name,a.kind ORDER BY a.name,a.id`,
		ws, out.Query.Currency, out.Period.Start, out.Period.End)
	if e != nil {
		return e
	}
	for rows.Next() {
		var a AccountMovement
		var opening, debit, credit string
		if e = rows.Scan(&a.ID, &a.Name, &a.Kind, &opening, &debit, &credit); e != nil {
			rows.Close()
			return e
		}
		if a.OpeningMinor, e = parse(opening); e != nil {
			rows.Close()
			return e
		}
		if a.DebitsMinor, e = parse(debit); e != nil {
			rows.Close()
			return e
		}
		if a.CreditsMinor, e = parse(credit); e != nil {
			rows.Close()
			return e
		}
		a.NetMovementMinor, e = sub(a.DebitsMinor, a.CreditsMinor)
		if e != nil {
			rows.Close()
			return e
		}
		if liability(a.Kind) {
			a.OpeningMinor, e = sub(0, a.OpeningMinor)
			if e != nil {
				rows.Close()
				return e
			}
			a.ClosingMinor, e = sub(a.OpeningMinor, a.NetMovementMinor)
			if e != nil {
				rows.Close()
				return e
			}
			if e = addTo(&out.OpeningLiabilitiesMinor, a.OpeningMinor); e != nil {
				rows.Close()
				return e
			}
			if e = addTo(&out.ClosingLiabilitiesMinor, a.ClosingMinor); e != nil {
				rows.Close()
				return e
			}
		} else if asset(a.Kind) {
			a.ClosingMinor, e = add(a.OpeningMinor, a.NetMovementMinor)
			if e != nil {
				rows.Close()
				return e
			}
			if e = addTo(&out.OpeningAssetsMinor, a.OpeningMinor); e != nil {
				rows.Close()
				return e
			}
			if e = addTo(&out.ClosingAssetsMinor, a.ClosingMinor); e != nil {
				rows.Close()
				return e
			}
		} else {
			rows.Close()
			return ErrInvalid
		}
		out.Accounts = append(out.Accounts, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	out.OpeningNetWorthMinor, e = sub(out.OpeningAssetsMinor, out.OpeningLiabilitiesMinor)
	if e != nil {
		return e
	}
	out.ClosingNetWorthMinor, e = sub(out.ClosingAssetsMinor, out.ClosingLiabilitiesMinor)
	return e
}
func (s *Store) costs(ctx context.Context, tx pgx.Tx, ws string, out *Report) error {
	rows, e := tx.Query(ctx, `SELECT la.system_key,to_char(j.created_at,'YYYY-MM'),
  SUM(l.debit_minor::numeric-l.credit_minor::numeric)::text
 FROM ledger_lines l
 JOIN ledger_accounts la ON la.id=l.ledger_account_id AND la.workspace_id=l.workspace_id
 JOIN ledger_journals j ON j.id=l.journal_id AND j.workspace_id=l.workspace_id
 WHERE l.workspace_id=$1 AND la.currency=$2
  AND la.system_key IN ('transfer_fee','loan_interest')
  AND j.created_at >= $3::date AND j.created_at < $4::date
 GROUP BY la.system_key,to_char(j.created_at,'YYYY-MM')`,
		ws, out.Query.Currency, out.Period.Start, out.Period.End)
	if e != nil {
		return e
	}
	for rows.Next() {
		var key, month, raw string
		if e = rows.Scan(&key, &month, &raw); e != nil {
			rows.Close()
			return e
		}
		n, err := parse(raw)
		if err != nil {
			rows.Close()
			return err
		}
		for i := range out.Monthly {
			if out.Monthly[i].Period == month {
				if key == "transfer_fee" {
					if e = addTo(&out.Monthly[i].FeesMinor, n); e != nil {
						rows.Close()
						return e
					}
				}
				if key == "loan_interest" {
					if e = addTo(&out.Monthly[i].InterestMinor, n); e != nil {
						rows.Close()
						return e
					}
				}
			}
		}
		switch key {
		case "transfer_fee":
			e = addTo(&out.FeesMinor, n)
		case "loan_interest":
			e = addTo(&out.InterestMinor, n)
		default:
			e = ErrInvalid
		}
		if e != nil {
			rows.Close()
			return e
		}
	}
	e = rows.Err()
	rows.Close()
	return e
}
func (s *Store) budgets(ctx context.Context, tx pgx.Tx, ws string, out *Report) error {
	// Budget store uses the same PostgreSQL pool but its own read queries; the
	// event-based budget engine is authoritative for rollover and allocations.
	// These reads do not affect the ledger/report accounting figures.
	_ = tx
	aggregate := map[string]*BudgetRow{}
	months := []string{}
	if out.Period.Annual {
		for m := 1; m <= 12; m++ {
			months = append(months, fmt.Sprintf("%04d-%02d", out.Period.Year, m))
		}
	} else {
		months = []string{out.Period.Label}
	}
	store := budgets.NewStore(s.db)
	for _, month := range months {
		overview, _, e := store.Overview(ctx, ws, month, out.Query.Currency)
		if e != nil {
			return e
		}
		if !overview.Configured {
			continue
		}
		out.BudgetConfigured = true
		for _, field := range []struct {
			dest *int64
			val  int64
		}{
			{&out.BudgetPlannedMinor, overview.PlannedMinor}, {&out.BudgetCarriedMinor, overview.CarriedMinor},
			{&out.BudgetActualMinor, overview.ActualMinor}, {&out.BudgetRemainingMinor, overview.RemainingMinor},
		} {
			if e = addTo(field.dest, field.val); e != nil {
				return e
			}
		}
		for _, row := range overview.Rows {
			item := aggregate[row.CategoryID]
			if item == nil {
				item = &BudgetRow{CategoryID: row.CategoryID, Name: row.Name}
				aggregate[row.CategoryID] = item
			}
			for _, field := range []struct {
				dest *int64
				val  int64
			}{
				{&item.PlannedMinor, row.AllocatedMinor}, {&item.CarriedMinor, row.CarriedMinor},
				{&item.ActualMinor, row.ActualMinor}, {&item.RemainingMinor, row.RemainingMinor},
			} {
				if e = addTo(field.dest, field.val); e != nil {
					return e
				}
			}
		}
	}
	for _, cat := range out.Categories {
		if row := aggregate[cat.ID]; row != nil {
			out.Budgets = append(out.Budgets, *row)
			delete(aggregate, cat.ID)
		}
	}
	for _, row := range aggregate {
		out.Budgets = append(out.Budgets, *row)
	}
	return nil
}
