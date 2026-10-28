// Package remittances records AED-to-PHP remittance intents and explicitly
// settles them using the existing Group 9 cross-currency transfer ledger.
package remittances

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/transfers"
)

const scale int64 = 100000000
const maxMinor int64 = 99999999999999

var (
	ErrInvalid  = errors.New("invalid remittance data")
	ErrNotFound = errors.New("remittance or account unavailable")
	ErrConflict = errors.New("idempotency key reused with different details")
	ErrResolved = errors.New("remittance already settled or cancelled")
	ErrFunds    = errors.New("source funds insufficient")
	ErrOverflow = errors.New("remittance exceeds supported amount")
)

type CreateInput struct {
	SourceAccountID, DestinationAccountID, Send, Fee, Receive, Rate, RequestedOn, Note, Key string
}
type ResolveInput struct {
	OrderID, Action, Date, Key string
}
type Order struct {
	ID, FromName, ToName, Note, RequestedOn, Status, ResolvedOn, TransferID string
	SettleKey, CancelKey                                                    string
	SendMinor, FeeMinor, ReceiveMinor, RateUnits, EffectiveRateUnits        int64
}
type normalizedOrder struct {
	SourceAccountID, DestinationAccountID, RequestedOn, Note, Key    string
	SendMinor, FeeMinor, ReceiveMinor, RateUnits, EffectiveRateUnits int64
	Hash                                                             [32]byte
}
type normalizedResolution struct {
	OrderID, Action, Date, Key string
	Hash                       [32]byte
}

func dayValid(day string) bool {
	parsed, e := time.Parse("2006-01-02", day)
	return e == nil && parsed.Format("2006-01-02") == day && parsed.Year() >= 2000 && parsed.Year() <= 2100
}
func digest(x any) [32]byte {
	raw, _ := json.Marshal(x)
	return sha256.Sum256(raw)
}
func effectiveRate(send, fee, received int64) (int64, error) {
	if send <= 0 || fee < 0 || received <= 0 || send > maxMinor || fee > maxMinor-send || received > maxMinor {
		return 0, ErrInvalid
	}
	denominator := big.NewInt(send + fee)
	result := new(big.Int).Mul(big.NewInt(received), big.NewInt(scale))
	result.Add(result, new(big.Int).Div(new(big.Int).Set(denominator), big.NewInt(2)))
	result.Div(result, denominator)
	if !result.IsInt64() || result.Int64() < 1 || result.Int64() > 999999999999999 {
		return 0, ErrOverflow
	}
	return result.Int64(), nil
}
func normalizeCreate(v CreateInput) (normalizedOrder, error) {
	n := normalizedOrder{
		SourceAccountID: v.SourceAccountID, DestinationAccountID: v.DestinationAccountID,
		RequestedOn: v.RequestedOn, Note: strings.TrimSpace(v.Note), Key: v.Key,
	}
	if !accounts.IsValidKey(n.Key) || !accounts.IsValidKey(n.SourceAccountID) ||
		!accounts.IsValidKey(n.DestinationAccountID) || n.SourceAccountID == n.DestinationAccountID ||
		!dayValid(n.RequestedOn) || utf8.RuneCountInString(n.Note) > 500 ||
		strings.ContainsAny(n.Note, "\x00\r\n") {
		return normalizedOrder{}, ErrInvalid
	}
	var e error
	n.SendMinor, e = accounts.ParseMinor(v.Send)
	if e != nil || n.SendMinor <= 0 {
		return normalizedOrder{}, ErrInvalid
	}
	fee := strings.TrimSpace(v.Fee)
	if fee == "" {
		fee = "0"
	}
	n.FeeMinor, e = accounts.ParseMinor(fee)
	if e != nil || n.FeeMinor > maxMinor-n.SendMinor {
		return normalizedOrder{}, ErrInvalid
	}
	n.ReceiveMinor, e = accounts.ParseMinor(v.Receive)
	if e != nil || n.ReceiveMinor <= 0 {
		return normalizedOrder{}, ErrInvalid
	}
	n.RateUnits, e = transfers.ParseRate(v.Rate)
	if e != nil {
		return normalizedOrder{}, ErrInvalid
	}
	expected, e := transfers.ConvertedMinor(n.SendMinor, n.RateUnits)
	if e != nil {
		return normalizedOrder{}, ErrOverflow
	}
	if expected != n.ReceiveMinor {
		return normalizedOrder{}, ErrInvalid
	}
	n.EffectiveRateUnits, e = effectiveRate(n.SendMinor, n.FeeMinor, n.ReceiveMinor)
	if e != nil {
		return normalizedOrder{}, e
	}
	n.Hash = digest(struct {
		Source, Destination, RequestedOn, Note string
		Send, Fee, Receive, Rate               int64
	}{n.SourceAccountID, n.DestinationAccountID, n.RequestedOn, n.Note,
		n.SendMinor, n.FeeMinor, n.ReceiveMinor, n.RateUnits})
	return n, nil
}
func normalizeResolution(v ResolveInput) (normalizedResolution, error) {
	n := normalizedResolution{OrderID: v.OrderID, Action: v.Action, Date: v.Date, Key: v.Key}
	if !accounts.IsValidKey(n.OrderID) || !accounts.IsValidKey(n.Key) ||
		(n.Action != "settled" && n.Action != "cancelled") || !dayValid(n.Date) {
		return normalizedResolution{}, ErrInvalid
	}
	n.Hash = digest(struct{ Order, Action, Date string }{n.OrderID, n.Action, n.Date})
	return n, nil
}
