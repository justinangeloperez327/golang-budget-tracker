package config

import "testing"

func TestProductionPublicOrigin(t *testing.T) {
	t.Setenv("PUBLIC_ORIGIN", "http://localhost:8080")
	if _, err := loadAuth("production"); err == nil {
		t.Fatal("production must require https")
	}
	t.Setenv("PUBLIC_ORIGIN", "https://budget.example.invalid")
	if cfg, err := loadAuth("production"); err != nil || !cfg.CookieSecure {
		t.Fatalf("production config=%+v err=%v", cfg, err)
	}
}
func TestMalformedPublicOrigin(t *testing.T) {
	for _, value := range []string{"https://example.invalid/path", "https://bob:pw@example.invalid", "javascript:alert(1)", ""} {
		if value == "" {
			continue
		}
		t.Setenv("PUBLIC_ORIGIN", value)
		if _, err := loadAuth("test"); err == nil {
			t.Fatalf("unexpected accepted origin %q", value)
		}
	}
}
