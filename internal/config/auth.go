package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type Auth struct {
	PublicOrigin string
	CookieSecure bool
	SessionTTL time.Duration
}

func loadAuth(appEnv string) (Auth, error) {
	cfg := Auth{
		PublicOrigin: envOr("PUBLIC_ORIGIN", "http://localhost:8080"),
		CookieSecure: appEnv == "production",
		SessionTTL: 7 * 24 * time.Hour,
	}
	u, err := url.Parse(cfg.PublicOrigin)
	if err != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		strings.TrimSpace(cfg.PublicOrigin) != cfg.PublicOrigin {
		return Auth{}, fmt.Errorf("PUBLIC_ORIGIN must be an absolute http(s) origin without path, query, or credentials")
	}
	if appEnv == "production" && u.Scheme != "https" {
		return Auth{}, fmt.Errorf("PUBLIC_ORIGIN must use https in production")
	}
	return cfg, nil
}
