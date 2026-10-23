// Command server runs the rostr API. It reads DATABASE_URL and PORT (default 8080), applies migrations, and serves.
package main

import (
	"context"
	"errors"
	"log"
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

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Connect and migrate
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := api.Migrate(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// Serve until SIGINT or SIGTERM, then drain in-flight requests
	server := &http.Server{
		Addr: ":" + port,
		Handler: api.NewHandler(db, api.Config{
			RegistrationsPerHour: envInt("RATE_LIMIT_REGISTRATIONS_PER_HOUR", 20),
			RequestsPerMinute:    envInt("RATE_LIMIT_REQUESTS_PER_MINUTE", 300),
			LiveURL:              os.Getenv("LIVE_URL"),
			LiveSecret:           os.Getenv("LIVE_SECRET"),
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
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("rostr API listening on :%s", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}
