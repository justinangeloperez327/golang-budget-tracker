// Package transactions records income, expenses and category-preserving refunds
// in the same database transaction as balanced ledger postings.
package transactions

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/ledger"
)

var (
	ErrInvalid     = errors.New("invalid transaction")
	ErrNotFound    = errors.New("transaction or category not found")
	ErrConflict    = errors.New("duplicate request with different details")
	ErrRefundLimit = errors.New("refund exceeds remaining expense")
)

type SplitInput struct {
	CategoryID string
	Amount     string
}
type Input struct {
	Kind       string
	AccountID  string
	RefundOf   string
	Note       string
	OccurredOn string
	Key        string
	Splits     []SplitInput
}
type split struct {
	CategoryID string
	Minor      int64
}
type normalized struct {
	Kind      string
	AccountID string
	RefundOf  string
	Note      string
	Date      string
	Key       string
	Splits    []split
	Total     int64
	Hash      [32]byte
}
type Category struct {
	ID       string
	Name     string
	Kind     string
	Currency string
	Archived bool
}
type Record struct {
	ID          string
	Kind        string
	AccountID   string
	AccountName string
	Currency    string
	TotalMinor  int64
	Note        string
	OccurredOn  time.Time
	RefundOf    string
	Categories  string
}
type Filter struct {
	Kind      string
	AccountID string
	Query     string
	From      string
	To        string
	Page      int
}

func ValidID(s string) bool { return accounts.IsValidKey(s) }

func normalize(in Input) (normalized, error) {
	n := normalized{Kind: strings.TrimSpace(in.Kind), AccountID: in.AccountID, RefundOf: in.RefundOf,
		Note: strings.TrimSpace(in.Note), Date: in.OccurredOn, Key: in.Key}
	if (n.Kind != "income" && n.Kind != "expense" && n.Kind != "refund") || !ValidID(n.AccountID) ||
		(n.Kind == "refund") != (n.RefundOf != "") || (n.RefundOf != "" && !ValidID(n.RefundOf)) ||
		len(n.Key) != 32 || !ValidID(n.Key) || utf8.RuneCountInString(n.Note) > 500 ||
		strings.ContainsRune(n.Note, 0) || len(in.Splits) == 0 || len(in.Splits) > 8 {
		return normalized{}, ErrInvalid
	}
	d, e := time.Parse("2006-01-02", n.Date)
	if e != nil || d.Year() < 2000 || d.Year() > 2100 {
		return normalized{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, entry := range in.Splits {
		if !ValidID(entry.CategoryID) || seen[entry.CategoryID] {
			return normalized{}, ErrInvalid
		}
		seen[entry.CategoryID] = true
		value, e := accounts.ParseMinor(entry.Amount)
		if e != nil || value == 0 || value > ledger.MaxMinor-n.Total {
			return normalized{}, ErrInvalid
		}
		n.Total += value
		n.Splits = append(n.Splits, split{entry.CategoryID, value})
	}
	sort.Slice(n.Splits, func(i, j int) bool { return n.Splits[i].CategoryID < n.Splits[j].CategoryID })
	hashInput := struct {
		Kind, AccountID, RefundOf, Note, Date string
		Splits                                []split
	}{n.Kind, n.AccountID, n.RefundOf, n.Note, n.Date, n.Splits}
	serialized, _ := json.Marshal(hashInput)
	n.Hash = sha256.Sum256(serialized)
	return n, nil
}

func ValidateFilter(f Filter) (Filter, error) {
	if f.Kind != "" && f.Kind != "income" && f.Kind != "expense" && f.Kind != "refund" {
		return f, ErrInvalid
	}
	if f.AccountID != "" && !ValidID(f.AccountID) {
		return f, ErrInvalid
	}
	f.Query = strings.TrimSpace(f.Query)
	if utf8.RuneCountInString(f.Query) > 100 || f.Page < 0 || f.Page > 1000 {
		return f, ErrInvalid
	}
	for _, d := range []string{f.From, f.To} {
		if d != "" {
			v, e := time.Parse("2006-01-02", d)
			if e != nil || v.Year() < 2000 || v.Year() > 2100 {
				return f, ErrInvalid
			}
		}
	}
	if f.From != "" && f.To != "" && f.From > f.To {
		return f, ErrInvalid
	}
	return f, nil
}
