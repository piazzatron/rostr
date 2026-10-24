// Command server runs the rostr API. It reads DATABASE_URL and PORT (default 8080), applies migrations, and serves.
package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/piazzatron/rostr/server/internal/api"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// JSON logs, one object per line, at LOG_LEVEL (debug, info, warn, or error; default info)
	var level slog.Level
	if err := level.UnmarshalText([]byte(cmp.Or(os.Getenv("LOG_LEVEL"), "info"))); err != nil {
		fatal("invalid LOG_LEVEL", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fatal("DATABASE_URL is required", nil)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Connect and migrate
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fatal("connect", err)
	}
	defer db.Close()
	if err := api.Migrate(ctx, db); err != nil {
		fatal("migrate", err)
	}

	// Serve until SIGINT or SIGTERM, then drain in-flight requests
	server := &http.Server{
		Addr: ":" + port,
		Handler: api.NewHandler(db, api.Config{
			RegistrationsPerHour: envInt("RATE_LIMIT_REGISTRATIONS_PER_HOUR", 20),
			RequestsPerMinute:    envInt("RATE_LIMIT_REQUESTS_PER_MINUTE", 300),
			LiveURL:              os.Getenv("LIVE_URL"),
			LiveSecret:           os.Getenv("LIVE_SECRET"),
			SweepEvery:           15 * time.Minute,
			Logger:               logger,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		slog.Info("shutting down")
		_ = server.Shutdown(shutdownCtx)
	}()

	slog.Info("rostr API listening", "port", port, "liveCounts", os.Getenv("LIVE_URL") != "")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("serve", err)
	}
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func fatal(msg string, err error) {
	if err != nil {
		slog.Error(msg, "error", err)
	} else {
		slog.Error(msg)
	}
	os.Exit(1)
}
