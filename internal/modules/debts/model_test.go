package debts

import (
	"errors"
	"testing"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func TestAmortizationAndMonthEnds(t *testing.T) {
	lines, err := Schedule(120000, 1200, 12, "2026-01-31")
	if err != nil || len(lines) != 12 {
		t.Fatalf("schedule %v %+v", err, lines)
	}
	if lines[0].PrincipalMinor != 10000 || lines[0].InterestMinor != 1200 ||
		lines[0].AmountMinor != 11200 || lines[0].DueOn != "2026-01-31" {
		t.Fatalf("first installment %+v", lines[0])
	}
	if lines[1].DueOn != "2026-02-28" || lines[1].InterestMinor != 1100 ||
		lines[2].DueOn != "2026-03-31" || lines[11].InterestMinor != 100 {
		t.Fatalf("declining rate or date %+v %+v %+v", lines[1], lines[2], lines[11])
	}
	var principal int64
	for _, v := range lines {
		principal += v.PrincipalMinor
	}
	if principal != 120000 {
		t.Fatalf("principal drift %d", principal)
	}
	leap, err := Schedule(100, 0, 3, "2024-01-31")
	if err != nil || leap[1].DueOn != "2024-02-29" || leap[2].DueOn != "2024-03-31" ||
		leap[0].PrincipalMinor != 33 || leap[2].PrincipalMinor != 34 {
		t.Fatalf("final remainder or leap: %+v %v", leap, err)
	}
	tiny, err := Schedule(100, 600, 1, "2026-10-28")
	if err != nil || tiny[0].InterestMinor != 1 {
		t.Fatalf("half-up interest %+v %v", tiny, err)
	}
}
func TestInvalidDebtInput(t *testing.T) {
	loan, _ := accounts.NewSetupKey()
	funding, _ := accounts.NewSetupKey()
	key, _ := accounts.NewSetupKey()
	v := NewPlan{Name: "Loan", LoanAccountID: loan, FundingAccountID: funding,
		APR: "12.50", FirstDue: "2026-10-28", Months: 12, Key: key}
	a, err := normalizePlan(v)
	if err != nil || a.AnnualRateBps != 1250 {
		t.Fatalf("valid input %+v %v", a, err)
	}
	changed := v
	changed.APR = "12.51"
	b, err := normalizePlan(changed)
	if err != nil || a.Hash == b.Hash {
		t.Fatal("rate not in request hash")
	}
	for _, rate := range []string{"-1", "1000.01", "1e2", "1.111", "not a number"} {
		if _, err := ParseAPR(rate); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q %v", rate, err)
		}
	}
	for _, x := range []NewPlan{
		{Name: "Loan", LoanAccountID: loan, FundingAccountID: loan, APR: "0", FirstDue: v.FirstDue, Months: 12, Key: key},
		{Name: "Loan", LoanAccountID: loan, FundingAccountID: funding, APR: "0", FirstDue: "bad", Months: 12, Key: key},
		{Name: "Loan", LoanAccountID: loan, FundingAccountID: funding, APR: "0", FirstDue: v.FirstDue, Months: 361, Key: key},
	} {
		if _, err := normalizePlan(x); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v", x)
		}
	}
	if _, err := Schedule(10, 1200, 12, "2026-10-28"); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero-principal payments allowed")
	}
	if _, err := Schedule(MaxMinor, 100000, 1, "2026-10-28"); !errors.Is(err, ErrOverflow) {
		t.Fatal("interest overflow accepted")
	}
	p := PayInput{PlanID: loan, InstallmentID: funding, PaidOn: "2026-10-28", Key: key}
	if _, err := normalizePay(p); err != nil {
		t.Fatal(err)
	}
	p.PaidOn = "2026-02-31"
	if _, err := normalizePay(p); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid payment date accepted")
	}
}
