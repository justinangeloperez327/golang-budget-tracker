package dashboard

import (
	"errors"
	"math"
	"testing"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func TestClassifyAssetsLiabilitiesAndArchived(t *testing.T) {
	items := []accounts.Account{
		{ID: "one", Name: "Bank", Currency: "AED", Kind: "bank", LedgerMinor: 12000},
		{ID: "two", Name: "Credit", Currency: "AED", Kind: "credit", LedgerMinor: 4000},
		{ID: "three", Name: "Loan", Currency: "AED", Kind: "loan", LedgerMinor: 1000, Archived: true},
		{ID: "four", Name: "Savings", Currency: "PHP", Kind: "savings", LedgerMinor: 9000},
	}
	a, err := classify(items, "AED")
	if err != nil || a.AssetsMinor != 12000 || a.LiabilitiesMinor != 5000 ||
		a.NetWorthMinor != 7000 || len(a.Accounts) != 3 {
		t.Fatalf("AED classification %+v %v", a, err)
	}
	p, err := classify(items, "PHP")
	if err != nil || p.AssetsMinor != 9000 || p.LiabilitiesMinor != 0 ||
		p.NetWorthMinor != 9000 || len(p.Accounts) != 1 {
		t.Fatalf("PHP classification %+v %v", p, err)
	}
	if a.AssetsMinor+p.AssetsMinor == a.NetWorthMinor {
		t.Fatal("test calculation incorrect")
	}
}
func TestSignedAggregatesGuardOverflow(t *testing.T) {
	if _, e := Add(math.MaxInt64, 1); !errors.Is(e, ErrOverflow) {
		t.Fatal("overflow accepted")
	}
	if _, e := Add(math.MinInt64, -1); !errors.Is(e, ErrOverflow) {
		t.Fatal("underflow accepted")
	}
	if n, e := Add(-200, 300); e != nil || n != 100 {
		t.Fatalf("sum %d %v", n, e)
	}
	if _, e := Sub(0, math.MinInt64); !errors.Is(e, ErrOverflow) {
		t.Fatal("sub overflow accepted")
	}
	if n, e := Sub(100, 300); e != nil || n != -200 {
		t.Fatalf("signed subtraction %d %v", n, e)
	}
	if _, e := parseAmount("9999999999999999999999999"); !errors.Is(e, ErrOverflow) {
		t.Fatal("large DB sum accepted")
	}
}
