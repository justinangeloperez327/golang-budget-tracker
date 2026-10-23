// Package accounts provides workspace-isolated account setup without posting ledger entries.
package accounts

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxOpeningMinor int64 = 99999999999999

var (
	ErrInvalid   = errors.New("invalid account")
	ErrDuplicate = errors.New("duplicate account request")
	ErrNotFound  = errors.New("account not found")
)

var minorPattern = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,2})?$`)

type CreateInput struct {
	Name     string
	Kind     string
	Currency string
	Opening  string
	SetupKey string
}

type Account struct {
	ID           string
	Name         string
	Kind         string
	Currency     string
	OpeningMinor int64
	Archived     bool
	CreatedAt    time.Time
}

func Nature(kind string) string {
	if kind == "credit" || kind == "loan" {
		return "liability"
	}
	return "asset"
}

func KindLabel(kind string) string {
	switch kind {
	case "cash":
		return "Cash"
	case "bank":
		return "Bank account"
	case "savings":
		return "Savings account"
	case "ewallet":
		return "E-wallet"
	case "credit":
		return "Credit card"
	case "loan":
		return "Loan or liability"
	}
	return "Unknown"
}

// ParseMinor accepts exact base-10 amounts with at most two decimals.
// No floats, locale-dependent separators, exponents or negative amounts.
func ParseMinor(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if !minorPattern.MatchString(s) {
		return 0, ErrInvalid
	}
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > MaxOpeningMinor/100 {
		return 0, ErrInvalid
	}
	var frac int64
	if len(parts) == 2 {
		f := parts[1]
		if len(f) == 1 {
			f += "0"
		}
		frac, err = strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, ErrInvalid
		}
	}
	n := whole*100 + frac
	if n > MaxOpeningMinor {
		return 0, ErrInvalid
	}
	return n, nil
}

func FormatMinor(n int64) string {
	whole, fraction := n/100, n%100
	s := strconv.FormatInt(whole, 10)
	var chunks []string
	for len(s) > 3 {
		chunks = append([]string{s[len(s)-3:]}, chunks...)
		s = s[:len(s)-3]
	}
	chunks = append([]string{s}, chunks...)
	return fmt.Sprintf("%s.%02d", strings.Join(chunks, ","), fraction)
}

func NewSetupKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func IsValidKey(key string) bool {
	if len(key) != 32 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

func Validate(in CreateInput) (CreateInput, int64, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if len(in.Name) == 0 || len([]rune(in.Name)) > 80 || len(in.Name) > 320 ||
		strings.ContainsAny(in.Name, "\x00\r\n") ||
		(in.Currency != "AED" && in.Currency != "PHP") ||
		(in.Kind != "cash" && in.Kind != "bank" && in.Kind != "savings" &&
			in.Kind != "ewallet" && in.Kind != "credit" && in.Kind != "loan") ||
		!IsValidKey(in.SetupKey) {
		return CreateInput{}, 0, ErrInvalid
	}
	minor, err := ParseMinor(in.Opening)
	if err != nil {
		return CreateInput{}, 0, ErrInvalid
	}
	return in, minor, nil
}
