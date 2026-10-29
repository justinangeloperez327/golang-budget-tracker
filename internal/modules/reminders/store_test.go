package reminders

import (
	"testing"
)

func TestDateValidation(t *testing.T) {
	for _, v := range []string{"2026-02-30", "2026-2-01", "1999-12-31", "2101-01-01", "' OR 1=1 --"} {
		if validDate(v) {
			t.Fatalf("accepted %q", v)
		}
	}
	if !validDate("2028-02-29") {
		t.Fatal("rejected leap day")
	}
}
