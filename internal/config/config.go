// Package config validates process-level configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppEnv   string
	HTTPAddr string
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
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
