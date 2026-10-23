package ledger

import (
	"errors"
	"testing"
)

func TestValidateBalancedCurrencyLines(t *testing.T) {
	a, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	valid := []Line{{AccountID: a, DebitMinor: 12345}, {AccountID: b, CreditMinor: 12345}}
	if err := ValidateLines(valid); err != nil {
		t.Fatal(err)
	}
	tests := [][]Line{
		nil,
		{{AccountID: a, DebitMinor: 4}},
		{{AccountID: a, DebitMinor: 4}, {AccountID: b, CreditMinor: 3}},
		{{AccountID: a, DebitMinor: -1}, {AccountID: b, CreditMinor: 1}},
		{{AccountID: a, DebitMinor: 1, CreditMinor: 1}, {AccountID: b, CreditMinor: 1}},
		{{AccountID: a, DebitMinor: 1}, {AccountID: a, CreditMinor: 1}},
		{{AccountID: a, DebitMinor: MaxMinor + 1}, {AccountID: b, CreditMinor: MaxMinor + 1}},
	}
	for _, lines := range tests {
		if err := ValidateLines(lines); err == nil {
			t.Fatalf("accepted %+v", lines)
		}
	}
	if fingerprint("AED", "adjustment", "", valid) != fingerprint("AED", "adjustment", "", []Line{valid[1], valid[0]}) {
		t.Fatal("fingerprint depends on line order")
	}
	if fingerprint("PHP", "adjustment", "", valid) == fingerprint("AED", "adjustment", "", valid) {
		t.Fatal("fingerprint must include currency")
	}
}

func TestNaturalBalances(t *testing.T) {
	for _, tc := range []struct {
		kind                string
		debit, credit, want int64
	}{
		{"asset", 100, 20, 80}, {"liability", 20, 100, 80}, {"equity", 20, 100, 80},
		{"asset", 0, 50, -50},
	} {
		got, err := normalBalance(tc.kind, tc.debit, tc.credit)
		if err != nil || got != tc.want {
			t.Fatalf("%+v got=%d %v", tc, got, err)
		}
	}
	if _, err := normalBalance("wrong", 1, 1); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
}
