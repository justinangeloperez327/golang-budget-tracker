package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/justinangeloperez327/golang-budget-tracker/internal/app"
	"github.com/justinangeloperez327/golang-budget-tracker/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           app.NewHandler(logger, cfg),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("HTTP server starting", "addr", listener.Addr().String(), "env", cfg.AppEnv)
	if err := serve(ctx, server, listener, cfg.ShutdownTimeout); err != nil {
		return err
	}
	logger.Info("HTTP server stopped")
	return nil
}

// serve exits on listener errors or gracefully shuts down on context cancellation.
// Shutdown uses a new bounded context so a canceled signal context cannot abort it.
func serve(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown HTTP: %w", err)
	}
	if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}
	return nil
}
