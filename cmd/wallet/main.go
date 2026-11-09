// Command wallet runs the wallet microservice.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	httpadapter "github.com/GnuBurn/sds_wallet/internal/wallet/adapter/inbound/http"
	"github.com/GnuBurn/sds_wallet/internal/wallet/adapter/outbound/postgres"
	"github.com/GnuBurn/sds_wallet/internal/wallet/core/service"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "wallet")
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := getenv("HTTP_ADDR", ":8080")
	dbURL := getenv("DATABASE_URL", "postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable")

	// --- outbound adapters ---
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	listener := postgres.NewListener(pool, log)
	go listener.Run(ctx)

	// --- core ---
	svc := service.New(service.Deps{
		UnitOfWork:   postgres.NewUnitOfWork(pool),
		Wallets:      postgres.NewWalletRepository(pool),
		Transactions: postgres.NewTransactionRepository(pool),
		Subscriber:   listener,
		NewID:        func() string { return uuid.Must(uuid.NewV7()).String() },
	})

	// --- inbound adapter ---
	streams, closeStreams := context.WithCancel(context.Background())
	defer closeStreams()

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpadapter.NewHandler(svc, log, streams),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second, // SSE handler lifts this per-stream
		IdleTimeout:       120 * time.Second,
	}
	srv.RegisterOnShutdown(closeStreams)

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
