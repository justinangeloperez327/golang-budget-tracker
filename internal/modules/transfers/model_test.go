package transfers

import (
	"errors"
	"testing"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/modules/accounts"
)

func TestExactRates(t *testing.T) {
	examples := map[string]int64{"1": 100000000, "15.75": 1575000000,
		"0.00000001": 1, "12.34567891": 1234567891}
	for raw, want := range examples {
		units, e := ParseRate(raw)
		if e != nil || units != want {
			t.Fatalf("%s got %d err %v", raw, units, e)
		}
	}
	for _, v := range []string{"", "0", "0.00000000", "1.000000001", "1e2", "-1", "15,5", "10000000"} {
		if _, err := ParseRate(v); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid rate accepted: %s", v)
		}
	}
	received, err := ConvertedMinor(101, 150000000)
	if err != nil || received != 152 {
		t.Fatalf("half-up rounding got %d %v", received, err)
	}
	if _, err := ConvertedMinor(99999999999999, 999999999999999); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow not caught %v", err)
	}
}
func TestTransferNormalization(t *testing.T) {
	a, _ := accounts.NewSetupKey()
	b, _ := accounts.NewSetupKey()
	key, _ := accounts.NewSetupKey()
	in := Input{FromID: a, ToID: b, Key: key, Date: "2026-10-26", Amount: "100.00", Fee: "2.50"}
	n, err := normalizeTransfer(in)
	if err != nil || n.Send != 10000 || n.Fee != 250 || n.Receive != 10000 || n.RateUnits != 0 {
		t.Fatalf("bad same currency model %+v %v", n, err)
	}
	changed := in
	changed.Fee = "2.51"
	newer, _ := normalizeTransfer(changed)
	if newer.Hash == n.Hash {
		t.Fatal("fees must be in request fingerprint")
	}
	fx := in
	fx.Rate = "4.00000000"
	fx.Received = "400.00"
	f, err := normalizeTransfer(fx)
	if err != nil || f.Receive != 40000 {
		t.Fatalf("invalid FX %+v %v", f, err)
	}
	fx.Received = "399.99"
	if _, err := normalizeTransfer(fx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid received amount %v", err)
	}
	fx = in
	fx.ToID = a
	if _, err := normalizeTransfer(fx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same account accepted %v", err)
	}
}
func TestReconcileValidation(t *testing.T) {
	id, _ := accounts.NewSetupKey()
	key, _ := accounts.NewSetupKey()
	v := ReconcileInput{AccountID: id, Key: key, Observed: "0.00", Date: "2026-10-26"}
	n, err := normalizeReconcile(v)
	if err != nil || n.Observed != 0 {
		t.Fatalf("%+v %v", n, err)
	}
	v.Observed = "-1"
	if _, err := normalizeReconcile(v); !errors.Is(err, ErrInvalid) {
		t.Fatal("negative observed amount accepted")
	}
}
