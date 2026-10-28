// Package contributions tracks PHP government contribution obligations without
// posting a second expense or moving money when recording their status.
package contributions

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

var (
	ErrInvalid  = errors.New("invalid contribution obligation or resolution")
	ErrNotFound = errors.New("contribution, account, category or payment unavailable")
	ErrConflict = errors.New("idempotency key already used with different content")
	ErrResolved = errors.New("contribution period already recorded")
)

type NewObligation struct {
	Scheme, Period, DueOn, Amount, ExpenseCategoryID, MP2AccountID, Note, Key string
}
type NewResolution struct {
	ObligationID, Action, ExpenseID, TransferID, ResolvedOn, Note, Key string
}
type Obligation struct {
	ID, Scheme, SchemeLabel, Period, DueOn, CategoryName, MP2AccountName, Status, Note string
	ExpenseCategoryID, MP2AccountID, PayKey, SkipKey                                   string
	AmountMinor                                                                        int64
}
type Resolution struct {
	ID, ObligationID, Scheme, Period, Action, ResolvedOn, ExpenseID, TransferID, Note string
	AmountMinor                                                                       int64
}
type normalizedObligation struct {
	Scheme, PeriodOn, DueOn, ExpenseCategoryID, MP2AccountID, Note, Key string
	AmountMinor                                                         int64
	Hash                                                                [32]byte
}
type normalizedResolution struct {
	ObligationID, Action, ExpenseID, TransferID, ResolvedOn, Note, Key string
	Hash                                                               [32]byte
}

func validDay(v string) bool {
	d, e := time.Parse("2006-01-02", v)
	return e == nil && d.Format("2006-01-02") == v && d.Year() >= 2000 && d.Year() <= 2100
}
func validScheme(s string) bool {
	return s == "sss" || s == "philhealth" || s == "pagibig" || s == "mp2"
}
func SchemeLabel(s string) string {
	switch s {
	case "sss":
		return "SSS"
	case "philhealth":
		return "PhilHealth"
	case "pagibig":
		return "Pag-IBIG"
	case "mp2":
		return "Pag-IBIG MP2"
	}
	return s
}
func normalizeObligation(in NewObligation) (normalizedObligation, error) {
	n := normalizedObligation{
		Scheme: strings.ToLower(strings.TrimSpace(in.Scheme)), DueOn: in.DueOn,
		ExpenseCategoryID: in.ExpenseCategoryID, MP2AccountID: in.MP2AccountID,
		Note: strings.TrimSpace(in.Note), Key: in.Key,
	}
	if len(in.Period) != 7 {
		return normalizedObligation{}, ErrInvalid
	}
	n.PeriodOn = in.Period + "-01"
	if !validScheme(n.Scheme) || !validDay(n.PeriodOn) ||
		(n.DueOn != "" && (!validDay(n.DueOn) || n.DueOn < n.PeriodOn)) ||
		!accounts.IsValidKey(n.Key) || utf8.RuneCountInString(n.Note) > 500 ||
		strings.ContainsAny(n.Note, "\x00\r\n") {
		return normalizedObligation{}, ErrInvalid
	}
	if n.Scheme == "mp2" {
		if !accounts.IsValidKey(n.MP2AccountID) || n.ExpenseCategoryID != "" {
			return normalizedObligation{}, ErrInvalid
		}
	} else if !accounts.IsValidKey(n.ExpenseCategoryID) || n.MP2AccountID != "" {
		return normalizedObligation{}, ErrInvalid
	}
	amount, e := accounts.ParseMinor(in.Amount)
	if e != nil || amount <= 0 {
		return normalizedObligation{}, ErrInvalid
	}
	n.AmountMinor = amount
	n.Hash = hash(struct {
		Scheme, Period, DueOn, Category, MP2Account, Note string
		Amount                                            int64
	}{n.Scheme, n.PeriodOn, n.DueOn, n.ExpenseCategoryID, n.MP2AccountID, n.Note, n.AmountMinor})
	return n, nil
}
func normalizeResolution(in NewResolution) (normalizedResolution, error) {
	n := normalizedResolution{
		ObligationID: in.ObligationID, Action: in.Action, ExpenseID: in.ExpenseID,
		TransferID: in.TransferID, ResolvedOn: in.ResolvedOn, Note: strings.TrimSpace(in.Note), Key: in.Key,
	}
	if !accounts.IsValidKey(n.ObligationID) || !accounts.IsValidKey(n.Key) ||
		!validDay(n.ResolvedOn) || (n.Action != "paid" && n.Action != "skipped") ||
		utf8.RuneCountInString(n.Note) > 500 || strings.ContainsAny(n.Note, "\x00\r\n") {
		return normalizedResolution{}, ErrInvalid
	}
	if n.Action == "skipped" {
		if n.ExpenseID != "" || n.TransferID != "" {
			return normalizedResolution{}, ErrInvalid
		}
	} else if (n.ExpenseID == "") == (n.TransferID == "") {
		return normalizedResolution{}, ErrInvalid
	}
	if n.ExpenseID != "" && !accounts.IsValidKey(n.ExpenseID) {
		return normalizedResolution{}, ErrInvalid
	}
	if n.TransferID != "" && !accounts.IsValidKey(n.TransferID) {
		return normalizedResolution{}, ErrInvalid
	}
	n.Hash = hash(struct{ Obligation, Action, Expense, Transfer, ResolvedOn, Note string }{
		n.ObligationID, n.Action, n.ExpenseID, n.TransferID, n.ResolvedOn, n.Note,
	})
	return n, nil
}
func Status(dueOn string, action string, now time.Time) string {
	if action != "" {
		return action
	}
	if dueOn == "" {
		return "pending"
	}
	today := now.UTC().Format("2006-01-02")
	if dueOn < today {
		return "overdue"
	}
	if dueOn == today {
		return "due"
	}
	return "upcoming"
}
func hash(v any) [32]byte {
	b, _ := json.Marshal(v)
	return sha256.Sum256(b)
}
