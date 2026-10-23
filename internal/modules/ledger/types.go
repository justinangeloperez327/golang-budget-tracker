// Package ledger is the authoritative, append-only, double-entry accounting layer.
package ledger

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const MaxMinor int64 = 99999999999999

var (
	ErrInvalid  = errors.New("invalid journal")
	ErrConflict = errors.New("idempotency key conflict")
	ErrNotFound = errors.New("journal or account not found")
	ErrReversed = errors.New("journal already reversed")
	ErrOverflow = errors.New("balance out of range")
)

type Line struct {
	AccountID   string
	DebitMinor  int64
	CreditMinor int64
}

type Journal struct {
	ID         string
	Currency   string
	Kind       string
	ReversesID string
	Lines      []Line
}

type Balance struct {
	AccountID string `json:"account_id"`
	Currency  string `json:"currency"`
	Kind      string `json:"kind"`
	Minor     int64  `json:"balance_minor"`
}

// Minor is normal-side signed: debit-credit for assets; credit-debit for
// liabilities and equity. Negative values are permitted for adjustments.
func normalBalance(kind string, debit, credit int64) (int64, error) {
	if (kind != "asset" && kind != "liability" && kind != "equity") ||
		(debit > 0 && credit < math.MinInt64+debit) ||
		(credit > 0 && debit < math.MinInt64+credit) {
		return 0, ErrOverflow
	}
	if kind == "asset" {
		return debit - credit, nil
	}
	return credit - debit, nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func ValidateLines(lines []Line) error {
	if len(lines) < 2 || len(lines) > 32 {
		return ErrInvalid
	}
	var debit, credit int64
	seen := make(map[string]bool, len(lines))
	for _, l := range lines {
		if !validID(l.AccountID) || seen[l.AccountID] ||
			l.DebitMinor < 0 || l.CreditMinor < 0 ||
			l.DebitMinor > MaxMinor || l.CreditMinor > MaxMinor ||
			(l.DebitMinor == 0) == (l.CreditMinor == 0) {
			return ErrInvalid
		}
		seen[l.AccountID] = true
		if debit > math.MaxInt64-l.DebitMinor || credit > math.MaxInt64-l.CreditMinor {
			return ErrOverflow
		}
		debit += l.DebitMinor
		credit += l.CreditMinor
	}
	if debit != credit {
		return ErrInvalid
	}
	return nil
}

// fingerprint binds an idempotency key to immutable monetary intent.
// Lines are canonicalized by account ID before hashing; no float arithmetic.
func fingerprint(currency, kind, reverses string, lines []Line) [32]byte {
	copyLines := append([]Line(nil), lines...)
	for i := 0; i < len(copyLines); i++ {
		for j := i + 1; j < len(copyLines); j++ {
			if copyLines[j].AccountID < copyLines[i].AccountID {
				copyLines[i], copyLines[j] = copyLines[j], copyLines[i]
			}
		}
	}
	var b strings.Builder
	b.WriteString(currency + "|" + kind + "|" + reverses + "|")
	for _, l := range copyLines {
		b.WriteString(l.AccountID + ":" + strconv.FormatInt(l.DebitMinor, 10) + ":" + strconv.FormatInt(l.CreditMinor, 10) + ";")
	}
	return sha256.Sum256([]byte(b.String()))
}

func validateKey(key string) error {
	if key == "" || len(key) > 128 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\x00\r\n") {
		return fmt.Errorf("%w: invalid idempotency key", ErrInvalid)
	}
	return nil
}
