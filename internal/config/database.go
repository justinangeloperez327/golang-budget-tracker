package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Database struct {
	URL             string
	MinConns        int32
	MaxConns        int32
	ConnectTimeout  time.Duration
	ReadyTimeout    time.Duration
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

func loadDatabase(appEnv string) (Database, error) {
	var db Database
	db.URL = envOr("DATABASE_URL", "")
	if db.URL == "" {
		return db, fmt.Errorf("DATABASE_URL is required")
	}
	u, err := url.Parse(db.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || strings.Trim(u.Path, "/") == "" {
		return Database{}, fmt.Errorf("DATABASE_URL must be a PostgreSQL URL with hostname and database name")
	}
	if u.User == nil || u.User.Username() == "" {
		return Database{}, fmt.Errorf("DATABASE_URL must contain a database username")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return Database{}, fmt.Errorf("DATABASE_URL contains an invalid port")
		}
	}
	if appEnv == "production" {
		mode := u.Query().Get("sslmode")
		if mode == "disable" || mode == "allow" || mode == "prefer" {
			return Database{}, fmt.Errorf("production DATABASE_URL must not disable or downgrade TLS")
		}
	}
	if host := u.Hostname(); net.ParseIP(host) == nil && strings.ContainsAny(host, " \t\r\n") {
		return Database{}, fmt.Errorf("DATABASE_URL hostname is invalid")
	}
	min, err := parseBoundedInt("DB_MIN_CONNS", "0", 0, 32)
	if err != nil {
		return Database{}, err
	}
	max, err := parseBoundedInt("DB_MAX_CONNS", "8", 1, 64)
	if err != nil {
		return Database{}, err
	}
	if min > max {
		return Database{}, fmt.Errorf("DB_MIN_CONNS cannot exceed DB_MAX_CONNS")
	}
	db.MinConns = int32(min)
	db.MaxConns = int32(max)
	durations := []struct {
		name, fallback string
		target         *time.Duration
	}{
		{"DB_CONNECT_TIMEOUT", "5s", &db.ConnectTimeout},
		{"DB_READY_TIMEOUT", "2s", &db.ReadyTimeout},
		{"DB_MAX_CONN_LIFETIME", "30m", &db.MaxConnLifetime},
		{"DB_MAX_CONN_IDLE_TIME", "5m", &db.MaxConnIdleTime},
	}
	for _, field := range durations {
		d, err := time.ParseDuration(envOr(field.name, field.fallback))
		if err != nil || d <= 0 || d > 24*time.Hour {
			return Database{}, fmt.Errorf("%s must be a positive duration at most 24h", field.name)
		}
		*field.target = d
	}
	return db, nil
}
