//go:build integration

package integration

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/platform/database"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/platform/database/sqlc"
)

func testURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is unset; use dedicated local budget_tracker_test database")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test database must be a PostgreSQL URL")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); !(strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		t.Fatal("integration tests require a loopback-only host")
	}
	if u.Path != "/budget_tracker_test" {
		t.Fatal("integration tests require database name budget_tracker_test")
	}
	return raw
}

func openDB(t *testing.T) *database.Store {
	t.Helper()
	cfg := config.Database{
		URL:             testURL(t),
		MaxConns:        4,
		MinConns:        0,
		ConnectTimeout:  3 * time.Second,
		ReadyTimeout:    2 * time.Second,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 5 * time.Minute,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func TestDatabaseReadinessAndGeneratedQueries(t *testing.T) {
	store := openDB(t)
	ctx := context.Background()
	if err := store.Ready(ctx); err != nil {
		t.Fatalf("readiness after migrations: %v", err)
	}
	if marker, err := sqlc.New(store.Pool()).GetSchemaMarker(ctx); err != nil || marker != "group14" {
		t.Fatalf("marker = %q, err = %v", marker, err)
	}
}

func TestTransactionCommitAndRollback(t *testing.T) {
	store := openDB(t)
	ctx := context.Background()
	key := "group3_test_" + strings.ReplaceAll(t.Name(), "/", "_")
	q := sqlc.New(store.Pool())
	t.Cleanup(func() { _ = q.DeleteAppMetadata(context.Background(), key) })
	rolledBack := errors.New("intentional rollback")
	err := store.WithinTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		if err := queries.UpsertAppMetadata(ctx, sqlc.UpsertAppMetadataParams{Key: key, Value: "rolled_back"}); err != nil {
			return err
		}
		return rolledBack
	})
	if !errors.Is(err, rolledBack) {
		t.Fatalf("rollback error = %v", err)
	}
	if _, err := q.GetAppMetadata(ctx, key); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected rollback to remove write, got %v", err)
	}
	if err := store.WithinTx(ctx, func(tx pgx.Tx) error {
		return sqlc.New(tx).UpsertAppMetadata(ctx, sqlc.UpsertAppMetadataParams{Key: key, Value: "committed"})
	}); err != nil {
		t.Fatal(err)
	}
	if value, err := q.GetAppMetadata(ctx, key); err != nil || value != "committed" {
		t.Fatalf("transaction commit value = %q, err = %v", value, err)
	}
}

func TestReadinessBeforeMigrations(t *testing.T) {
	if os.Getenv("TEST_MIGRATIONS_DOWN") != "1" {
		t.Skip("only run after dedicated CI Goose down step")
	}
	store := openDB(t)
	if err := store.Ready(context.Background()); !errors.Is(err, database.ErrNotReady) {
		t.Fatalf("expected unready without schema marker, got %v", err)
	}
}
