package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_ADDR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppEnv != "development" || cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadCustom(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	cfg, err := Load()
	if err != nil || cfg.AppEnv != "test" || cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Fatalf("load = %+v, %v", cfg, err)
	}
}

func TestInvalid(t *testing.T) {
	for _, tc := range []struct {
		name, env, addr string
	}{
		{"env", "invalid", ":8080"},
		{"missing port", "test", "localhost"},
		{"zero port", "test", ":0"},
		{"bad port", "test", ":abc"},
		{"url", "test", "http://localhost:8080"},
		{"space", "test", " :8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tc.env)
			t.Setenv("HTTP_ADDR", tc.addr)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %q, %q", tc.env, tc.addr)
			}
		})
	}
}
