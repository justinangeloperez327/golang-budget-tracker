// Package database owns PostgreSQL connections and transaction boundaries.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/platform/database/sqlc"
)

var ErrNotReady = errors.New("database or schema is not ready")

type Store struct {
	pool         *pgxpool.Pool
	readyTimeout time.Duration
}

func Open(ctx context.Context, cfg config.Database) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid PostgreSQL connection configuration")
	}
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = 30 * time.Second
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, ErrNotReady
	}
	return &Store{pool: pool, readyTimeout: cfg.ReadyTimeout}, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ready requires a working database connection and Group 3 migration marker.
// It intentionally does not apply migrations or expose connection details.
func (s *Store) Ready(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return ErrNotReady
	}
	checkCtx, cancel := context.WithTimeout(ctx, s.readyTimeout)
	defer cancel()
	if err := s.pool.Ping(checkCtx); err != nil {
		return ErrNotReady
	}
	marker, err := sqlc.New(s.pool).GetSchemaMarker(checkCtx)
	if err != nil || marker != "group3" {
		return ErrNotReady
	}
	return nil
}

// WithinTx wraps all related writes in a single transaction.
// The callback returns an error to roll back the complete operation.
func (s *Store) WithinTx(ctx context.Context, fn func(pgx.Tx) error) error {
	if s == nil || s.pool == nil {
		return ErrNotReady
	}
	return pgx.BeginFunc(ctx, s.pool, fn)
}
