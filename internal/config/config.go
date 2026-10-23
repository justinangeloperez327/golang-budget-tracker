// Package config validates process-level configuration.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv            string
	HTTPAddr          string
	LogLevel          slog.Level
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	RequestTimeout    time.Duration
	ShutdownTimeout   time.Duration
	MaxBodyBytes      int64
	MaxHeaderBytes    int
}

func Load() (Config, error) {
	cfg := Config{
		AppEnv:   envOr("APP_ENV", "development"),
		HTTPAddr: envOr("HTTP_ADDR", ":8080"),
	}
	if cfg.AppEnv != "development" && cfg.AppEnv != "test" && cfg.AppEnv != "production" {
		return Config{}, fmt.Errorf("APP_ENV must be development, test, or production")
	}
	if strings.TrimSpace(cfg.HTTPAddr) != cfg.HTTPAddr || strings.ContainsAny(cfg.HTTPAddr, " \t\r\n") {
		return Config{}, fmt.Errorf("HTTP_ADDR must be a host:port without whitespace")
	}
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR must be host:port: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("HTTP_ADDR requires a valid TCP port (1-65535)")
	}
	switch strings.ToLower(envOr("LOG_LEVEL", "info")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
	durations := []struct {
		name     string
		fallback string
		target   *time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", "5s", &cfg.ReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", "15s", &cfg.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", "20s", &cfg.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", "60s", &cfg.IdleTimeout},
		{"HTTP_REQUEST_TIMEOUT", "12s", &cfg.RequestTimeout},
		{"HTTP_SHUTDOWN_TIMEOUT", "10s", &cfg.ShutdownTimeout},
	}
	for _, field := range durations {
		d, err := time.ParseDuration(envOr(field.name, field.fallback))
		if err != nil || d <= 0 || d > 10*time.Minute {
			return Config{}, fmt.Errorf("%s must be a duration >0 and <=10m", field.name)
		}
		*field.target = d
	}
	if cfg.ReadHeaderTimeout > cfg.ReadTimeout {
		return Config{}, fmt.Errorf("HTTP_READ_HEADER_TIMEOUT cannot exceed HTTP_READ_TIMEOUT")
	}
	if cfg.RequestTimeout >= cfg.WriteTimeout {
		return Config{}, fmt.Errorf("HTTP_REQUEST_TIMEOUT must be less than HTTP_WRITE_TIMEOUT")
	}
	body, err := parseBoundedInt("HTTP_MAX_BODY_BYTES", "1048576", 1, 10<<20)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxBodyBytes = body
	headers, err := parseBoundedInt("HTTP_MAX_HEADER_BYTES", "16384", 1024, 1<<20)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxHeaderBytes = int(headers)
	return cfg, nil
}

func parseBoundedInt(key, fallback string, min, max int64) (int64, error) {
	v, err := strconv.ParseInt(envOr(key, fallback), 10, 64)
	if err != nil || v < min || v > max {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, min, max)
	}
	return v, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
