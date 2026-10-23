package config

import (
	"log/slog"
	"testing"
	"time"
)

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"APP_ENV", "HTTP_ADDR", "LOG_LEVEL",
		"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT",
		"HTTP_IDLE_TIMEOUT", "HTTP_REQUEST_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT",
		"HTTP_MAX_BODY_BYTES", "HTTP_MAX_HEADER_BYTES",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppEnv != "development" || cfg.HTTPAddr != ":8080" ||
		cfg.LogLevel != slog.LevelInfo || cfg.RequestTimeout != 12*time.Second ||
		cfg.MaxBodyBytes != 1048576 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadCustom(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("HTTP_REQUEST_TIMEOUT", "3s")
	t.Setenv("HTTP_MAX_BODY_BYTES", "4096")
	cfg, err := Load()
	if err != nil || cfg.AppEnv != "test" || cfg.HTTPAddr != "127.0.0.1:9090" ||
		cfg.LogLevel != slog.LevelDebug || cfg.RequestTimeout != 3*time.Second ||
		cfg.MaxBodyBytes != 4096 {
		t.Fatalf("load = %+v, %v", cfg, err)
	}
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name, key, value string
	}{
		{"env", "APP_ENV", "invalid"},
		{"missing port", "HTTP_ADDR", "localhost"},
		{"zero port", "HTTP_ADDR", ":0"},
		{"bad port", "HTTP_ADDR", ":abc"},
		{"url", "HTTP_ADDR", "http://localhost:8080"},
		{"space", "HTTP_ADDR", " :8080"},
		{"level", "LOG_LEVEL", "trace"},
		{"negative timeout", "HTTP_REQUEST_TIMEOUT", "-1s"},
		{"malformed timeout", "HTTP_REQUEST_TIMEOUT", "100"},
		{"read timeout smaller than header", "HTTP_READ_TIMEOUT", "2s"},
		{"write timeout smaller than request", "HTTP_WRITE_TIMEOUT", "4s"},
		{"body limit", "HTTP_MAX_BODY_BYTES", "0"},
		{"header limit", "HTTP_MAX_HEADER_BYTES", "100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatalf("expected validation failure for %s=%s", tc.key, tc.value)
			}
		})
	}
}
