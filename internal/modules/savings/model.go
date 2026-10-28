// Package savings tracks goals assigned to existing ledger-backed savings accounts.
// Goal transfers are balanced asset-to-asset journals, never income or expense.
package savings

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

const MaxMinor int64 = 99999999999999

var (
	ErrInvalid   = errors.New("invalid savings goal or movement")
	ErrNotFound  = errors.New("goal or account unavailable")
	ErrConflict  = errors.New("request key already used with different details")
	ErrAllocated = errors.New("savings account already allocated to a goal")
	ErrFunds     = errors.New("insufficient available funds for movement")
	ErrOverflow  = errors.New("monetary amount exceeds supported range")
)

type CreateInput struct {
	Name, SavingsAccountID, TargetAmount, TargetOn, Key string
}
type MovementInput struct {
	GoalID, OtherAccountID, Direction, Amount, OccurredOn, Key string
}
type Goal struct {
	ID, Name, SavingsAccountID, SavingsAccountName, Currency, TargetOn, Status string
	ContributeKey, WithdrawKey                                                 string
	TargetMinor, BalanceMinor, RemainingMinor                                  int64
	ProgressBps                                                                int
	Archived                                                                   bool
}
type Movement struct {
	ID, GoalID, GoalName, OtherAccountName, Direction, Currency, OccurredOn string
	AmountMinor                                                             int64
}
type normalizedCreate struct {
	Name, SavingsAccountID, TargetOn, Key string
	TargetMinor                           int64
	Hash                                  [32]byte
}
type normalizedMovement struct {
	GoalID, OtherAccountID, Direction, OccurredOn, Key string
	AmountMinor                                        int64
	Hash                                               [32]byte
}

func validDate(s string) bool {
	d, e := time.Parse("2006-01-02", s)
	return e == nil && d.Format("2006-01-02") == s && d.Year() >= 2000 && d.Year() <= 2100
}
func digest(v any) [32]byte {
	body, _ := json.Marshal(v)
	return sha256.Sum256(body)
}
func normalizeCreate(v CreateInput) (normalizedCreate, error) {
	n := normalizedCreate{Name: strings.TrimSpace(v.Name), SavingsAccountID: v.SavingsAccountID, TargetOn: v.TargetOn, Key: v.Key}
	if !accounts.IsValidKey(n.Key) || !accounts.IsValidKey(n.SavingsAccountID) ||
		len(n.Name) == 0 || utf8.RuneCountInString(n.Name) > 100 || strings.ContainsAny(n.Name, "\x00\r\n") ||
		(n.TargetOn != "" && !validDate(n.TargetOn)) {
		return normalizedCreate{}, ErrInvalid
	}
	amount, e := accounts.ParseMinor(v.TargetAmount)
	if e != nil || amount <= 0 {
		return normalizedCreate{}, ErrInvalid
	}
	n.TargetMinor = amount
	n.Hash = digest(struct {
		Name, Account, TargetOn string
		TargetMinor             int64
	}{n.Name, n.SavingsAccountID, n.TargetOn, n.TargetMinor})
	return n, nil
}
func normalizeMovement(v MovementInput) (normalizedMovement, error) {
	n := normalizedMovement{GoalID: v.GoalID, OtherAccountID: v.OtherAccountID,
		Direction: v.Direction, OccurredOn: v.OccurredOn, Key: v.Key}
	if !accounts.IsValidKey(n.GoalID) || !accounts.IsValidKey(n.OtherAccountID) ||
		!accounts.IsValidKey(n.Key) || !validDate(n.OccurredOn) ||
		(n.Direction != "contribution" && n.Direction != "withdrawal") {
		return normalizedMovement{}, ErrInvalid
	}
	amount, e := accounts.ParseMinor(v.Amount)
	if e != nil || amount <= 0 {
		return normalizedMovement{}, ErrInvalid
	}
	n.AmountMinor = amount
	n.Hash = digest(struct {
		Goal, Other, Direction, OccurredOn string
		AmountMinor                        int64
	}{n.GoalID, n.OtherAccountID, n.Direction, n.OccurredOn, n.AmountMinor})
	return n, nil
}

// Progress is computed from the authoritative account balance; only the visual
// percentage is capped. Excess deposits remain visible in the real balance.
func Progress(balance, target int64) (remaining int64, bps int, err error) {
	if target <= 0 || target > MaxMinor || balance > MaxMinor || balance < -MaxMinor {
		return 0, 0, ErrInvalid
	}
	diff := new(big.Int).Sub(big.NewInt(target), big.NewInt(balance))
	if !diff.IsInt64() || diff.Int64() > MaxMinor || diff.Int64() < -MaxMinor {
		return 0, 0, ErrOverflow
	}
	remaining = diff.Int64()
	if balance <= 0 {
		return remaining, 0, nil
	}
	if balance >= target {
		return remaining, 10000, nil
	}
	numerator := new(big.Int).Mul(big.NewInt(balance), big.NewInt(10000))
	numerator.Div(numerator, big.NewInt(target))
	return remaining, int(numerator.Int64()), nil
}
func Status(balance, target int64, targetOn string, archived bool, now time.Time) string {
	if archived {
		return "archived"
	}
	if balance >= target {
		return "achieved"
	}
	if targetOn != "" && targetOn < now.UTC().Format("2006-01-02") {
		return "past target"
	}
	return "in progress"
}
