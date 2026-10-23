package database

import (
	"context"
	"errors"
	"testing"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
)

func TestUninitializedStoreIsNotReady(t *testing.T) {
	var store *Store
	if !errors.Is(store.Ready(context.Background()), ErrNotReady) {
		t.Fatal("nil store must be unready")
	}
	if !errors.Is(store.WithinTx(context.Background(), nil), ErrNotReady) {
		t.Fatal("nil store must reject transactions")
	}
	store.Close()
}

func TestOpenRejectsInvalidConnectionConfiguration(t *testing.T) {
	_, err := Open(context.Background(), config.Database{URL:"not-a-postgres-url"})
	if err == nil {
		t.Fatal("invalid PostgreSQL URL should fail")
	}
}
