package transactions

import (
	"errors"
	"testing"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func TestMoneySplitsExactAndCanonical(t *testing.T) {
	a, _ := accounts.NewSetupKey()
	b, _ := accounts.NewSetupKey()
	wallet, _ := accounts.NewSetupKey()
	key, _ := accounts.NewSetupKey()
	first := Input{Kind: "expense", AccountID: wallet, Key: key, OccurredOn: "2026-10-24", Note: " Groceries ",
		Splits: []SplitInput{{CategoryID: b, Amount: "3.25"}, {CategoryID: a, Amount: "1.1"}}}
	n, err := normalize(first)
	if err != nil || n.Total != 435 || n.Note != "Groceries" {
		t.Fatalf("normalized %+v error %v", n, err)
	}
	first.Splits[0], first.Splits[1] = first.Splits[1], first.Splits[0]
	m, err := normalize(first)
	if err != nil || n.Hash != m.Hash {
		t.Fatal("split order changed intent fingerprint")
	}
	if n.Splits[0].CategoryID > n.Splits[1].CategoryID {
		t.Fatal("splits are not canonical")
	}
}

func TestRejectInvalidFinancialInput(t *testing.T) {
	a, _ := accounts.NewSetupKey()
	wallet, _ := accounts.NewSetupKey()
	key, _ := accounts.NewSetupKey()
	valid := Input{Kind: "expense", AccountID: wallet, Key: key, OccurredOn: "2026-10-24",
		Splits: []SplitInput{{CategoryID: a, Amount: "1.00"}}}
	examples := []Input{
		{Kind: "expense", AccountID: wallet, Key: key, OccurredOn: "2026-10-24"},
		{Kind: "refund", AccountID: wallet, Key: key, OccurredOn: "2026-10-24", Splits: valid.Splits},
		{Kind: "wrong", AccountID: wallet, Key: key, OccurredOn: "2026-10-24", Splits: valid.Splits},
		{Kind: "expense", AccountID: "bad", Key: key, OccurredOn: "2026-10-24", Splits: valid.Splits},
		{Kind: "expense", AccountID: wallet, Key: key, OccurredOn: "bad", Splits: valid.Splits},
		{Kind: "expense", AccountID: wallet, Key: key, OccurredOn: "2026-10-24", Splits: []SplitInput{{CategoryID: a, Amount: "0"}}},
		{Kind: "expense", AccountID: wallet, Key: key, OccurredOn: "2026-10-24", Splits: []SplitInput{{CategoryID: a, Amount: "-1"}}},
		{Kind: "expense", AccountID: wallet, Key: key, OccurredOn: "2026-10-24", Splits: []SplitInput{{CategoryID: a, Amount: "1"}, {CategoryID: a, Amount: "2"}}},
	}
	for _, e := range examples {
		if _, err := normalize(e); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v (%v)", e, err)
		}
	}
}
func TestTransactionFilters(t *testing.T) {
	if _, err := ValidateFilter(Filter{Page: 1001}); err == nil {
		t.Fatal("accepted unbounded page")
	}
	if _, err := ValidateFilter(Filter{From: "2026-10-25", To: "2026-10-24"}); err == nil {
		t.Fatal("accepted reversed date range")
	}
	if _, err := ValidateFilter(Filter{Kind: "both"}); err == nil {
		t.Fatal("accepted invalid kind")
	}
	if _, err := ValidateFilter(Filter{Query: "small"}); err != nil {
		t.Fatal(err)
	}
}
