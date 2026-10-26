// Package recurring models scheduled obligations separately from posted expenses.
package recurring

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
	ErrInvalid  = errors.New("invalid recurring schedule or occurrence")
	ErrNotFound = errors.New("recurring schedule or transaction not found")
	ErrConflict = errors.New("idempotency key reused with different content")
	ErrRecorded = errors.New("this occurrence or expense was already linked")
)

type NewSchedule struct {
	Name, Kind, Currency, AccountID, CategoryID, Amount, Frequency, StartOn, EndOn, Key string
}
type NewAction struct {
	ScheduleID, DueOn, Action, TransactionID, Key string
}
type Schedule struct {
	ID, Name, Kind, Currency, AccountID, CategoryID, AccountName, CategoryName, Frequency, StartOn, EndOn string
	AmountMinor                                                                                           int64
	Archived                                                                                              bool
}
type Occurrence struct {
	ScheduleID, ScheduleName, Currency, DueOn, AccountName, CategoryName, Status, TransactionID string
	PayKey, SkipKey                                                                             string
	AmountMinor                                                                                 int64
}
type Payment struct {
	ScheduleID, ScheduleName, Currency, DueOn, Action, TransactionID string
	AmountMinor                                                      int64
}
type normalizedSchedule struct {
	Name, Kind, Currency, AccountID, CategoryID, Frequency, StartOn, EndOn, Key string
	AmountMinor                                                                 int64
	Hash                                                                        [32]byte
}
type normalizedAction struct {
	ScheduleID, DueOn, Action, TransactionID, Key string
	Hash                                          [32]byte
}

func validDay(v string) bool {
	if len(v) != 10 {
		return false
	}
	d, e := time.Parse("2006-01-02", v)
	return e == nil && d.Format("2006-01-02") == v && d.Year() >= 2000 && d.Year() <= 2100
}
func validFrequency(f string) bool {
	switch f {
	case "daily", "weekly", "monthly", "quarterly", "yearly":
		return true
	}
	return false
}
func fingerprint(v any) [32]byte {
	data, _ := json.Marshal(v)
	return sha256.Sum256(data)
}
func normalizeSchedule(v NewSchedule) (normalizedSchedule, error) {
	n := normalizedSchedule{
		Name: strings.TrimSpace(v.Name), Kind: v.Kind, Currency: v.Currency,
		AccountID: v.AccountID, CategoryID: v.CategoryID, Frequency: v.Frequency,
		StartOn: v.StartOn, EndOn: v.EndOn, Key: v.Key,
	}
	if !accounts.IsValidKey(n.Key) || !accounts.IsValidKey(n.AccountID) || !accounts.IsValidKey(n.CategoryID) ||
		(n.Kind != "bill" && n.Kind != "subscription") || (n.Currency != "AED" && n.Currency != "PHP") ||
		!validFrequency(n.Frequency) || !validDay(n.StartOn) ||
		(n.EndOn != "" && (!validDay(n.EndOn) || n.EndOn < n.StartOn)) ||
		utf8.RuneCountInString(n.Name) < 1 || utf8.RuneCountInString(n.Name) > 100 ||
		strings.ContainsAny(n.Name, "\x00\r\n") {
		return normalizedSchedule{}, ErrInvalid
	}
	minor, e := accounts.ParseMinor(v.Amount)
	if e != nil || minor == 0 {
		return normalizedSchedule{}, ErrInvalid
	}
	n.AmountMinor = minor
	n.Hash = fingerprint(struct {
		Name, Kind, Currency, AccountID, CategoryID, Frequency, StartOn, EndOn string
		AmountMinor                                                            int64
	}{n.Name, n.Kind, n.Currency, n.AccountID, n.CategoryID, n.Frequency, n.StartOn, n.EndOn, n.AmountMinor})
	return n, nil
}
func normalizeAction(v NewAction) (normalizedAction, error) {
	n := normalizedAction{ScheduleID: v.ScheduleID, DueOn: v.DueOn, Action: v.Action, TransactionID: v.TransactionID, Key: v.Key}
	if !accounts.IsValidKey(n.Key) || !accounts.IsValidKey(n.ScheduleID) || !validDay(n.DueOn) ||
		(n.Action != "paid" && n.Action != "skipped") || ((n.Action == "paid") != (n.TransactionID != "")) ||
		(n.TransactionID != "" && !accounts.IsValidKey(n.TransactionID)) {
		return normalizedAction{}, ErrInvalid
	}
	n.Hash = fingerprint(struct{ ScheduleID, DueOn, Action, TransactionID string }{
		n.ScheduleID, n.DueOn, n.Action, n.TransactionID,
	})
	return n, nil
}

// occurrenceAt anchors to the original day, not the previously clamped day:
// Jan 31 -> Feb 28 -> Mar 31. Feb 29 yearly -> Feb 28 -> Feb 29.
func occurrenceAt(start time.Time, frequency string, index int) time.Time {
	switch frequency {
	case "daily":
		return start.AddDate(0, 0, index)
	case "weekly":
		return start.AddDate(0, 0, index*7)
	case "monthly", "quarterly", "yearly":
		months := index
		if frequency == "quarterly" {
			months *= 3
		}
		if frequency == "yearly" {
			months *= 12
		}
		first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, months, 0)
		last := first.AddDate(0, 1, -1).Day()
		day := start.Day()
		if day > last {
			day = last
		}
		return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}
func indexNear(start, cutoff time.Time, frequency string) int {
	if !cutoff.After(start) {
		return 0
	}
	days := int(cutoff.Sub(start).Hours() / 24)
	switch frequency {
	case "daily":
		return days
	case "weekly":
		return days / 7
	case "monthly", "quarterly", "yearly":
		months := (cutoff.Year()-start.Year())*12 + int(cutoff.Month()-start.Month())
		divisor := 1
		if frequency == "quarterly" {
			divisor = 3
		}
		if frequency == "yearly" {
			divisor = 12
		}
		n := months / divisor
		if n > 1 {
			n--
		}
		return n
	}
	return 0
}
func IsDue(s Schedule, day string) bool {
	if !validDay(day) || day < s.StartOn || (s.EndOn != "" && day > s.EndOn) {
		return false
	}
	start, _ := time.Parse("2006-01-02", s.StartOn)
	target, _ := time.Parse("2006-01-02", day)
	i := indexNear(start, target, s.Frequency)
	for j := i; j <= i+2; j++ {
		if occurrenceAt(start, s.Frequency, j).Equal(target) {
			return true
		}
	}
	return false
}
func project(s Schedule, from, to, timeToday string, actions map[string]Payment) []Occurrence {
	start, _ := time.Parse("2006-01-02", s.StartOn)
	cutoff, _ := time.Parse("2006-01-02", from)
	first := indexNear(start, cutoff, s.Frequency)
	result := make([]Occurrence, 0)
	for i := first; i < first+200; i++ {
		date := occurrenceAt(start, s.Frequency, i).Format("2006-01-02")
		if date > to || (s.EndOn != "" && date > s.EndOn) {
			break
		}
		if date < from || date < s.StartOn {
			continue
		}
		action, recorded := actions[date]
		if s.Archived && !recorded {
			continue
		}
		status := "upcoming"
		switch {
		case recorded:
			status = action.Action
		case date < timeToday:
			status = "overdue"
		case date == timeToday:
			status = "due"
		}
		result = append(result, Occurrence{
			ScheduleID: s.ID, ScheduleName: s.Name, Currency: s.Currency, DueOn: date,
			AccountName: s.AccountName, CategoryName: s.CategoryName,
			AmountMinor: s.AmountMinor, Status: status, TransactionID: action.TransactionID,
		})
	}
	return result
}
