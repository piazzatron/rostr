// Package api implements the rostr HTTP API (see docs/cloud.md) on top of Postgres.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the server's rate limits and where it pushes live counts.
type Config struct {
	RegistrationsPerHour int    // per client IP
	RequestsPerMinute    int    // per auth token
	LiveURL              string // the live Worker's base URL; nothing is pushed unless both it and LiveSecret are set
	LiveSecret           string
	SweepEvery           time.Duration // how often the background sweep expires and prunes drafts; 0 runs none
	Logger               *slog.Logger  // where request and event logs go; nil uses slog.Default()
}

// Server serves the rostr API.
type Server struct {
	db                  *pgxpool.Pool
	registrationLimiter *rateLimiter
	requestLimiter      *rateLimiter
	draftLocks          ownerLocks
	live                *livePusher // nil when live counts are off
	logger              *slog.Logger
}

// NewHandler returns the API's HTTP handler.
func NewHandler(db *pgxpool.Pool, cfg Config) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		logger:              logger,
		db:                  db,
		registrationLimiter: newRateLimiter(cfg.RegistrationsPerHour, time.Hour),
		requestLimiter:      newRateLimiter(cfg.RequestsPerMinute, time.Minute),
	}
	if cfg.LiveURL != "" && cfg.LiveSecret != "" {
		s.live = &livePusher{url: cfg.LiveURL, secret: cfg.LiveSecret, delay: 500 * time.Millisecond, db: db, logger: logger}
	}
	if cfg.SweepEvery > 0 {
		go s.sweepEvery(cfg.SweepEvery)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /v1/register", s.handleRegister)

	// The public endpoints. CORS allows any origin so browser dashboards and the site can call them.
	mux.Handle("GET /v1/stats", withPublicCORS(s.authed(s.handleStats)))
	mux.Handle("OPTIONS /v1/stats", withPublicCORS(http.HandlerFunc(handlePreflight)))

	mux.Handle("POST /v1/rosters", s.authed(s.handleCreateRoster))
	mux.Handle("GET /v1/rosters/{name}", s.authed(s.handleGetRoster))
	mux.Handle("PUT /v1/rosters/{name}", s.authed(s.handleEditRoster))
	mux.Handle("PUT /v1/rosters/{name}/cast", s.authed(s.handleReplaceCast))
	mux.Handle("DELETE /v1/rosters/{name}", s.authed(s.handleDisbandRoster))

	mux.Handle("GET /v1/settings", s.authed(s.handleGetSettings))
	mux.Handle("PATCH /v1/settings", s.authed(s.handlePatchSettings))

	mux.Handle("GET /v1/links", s.authed(s.handleListLinks))
	mux.Handle("GET /v1/links/{repoHash}", s.authed(s.handleGetLink))
	mux.Handle("PUT /v1/links/{repoHash}", s.authed(s.handlePutLink))
	mux.Handle("DELETE /v1/links/{repoHash}", s.authed(s.handleDeleteLink))

	mux.Handle("POST /v1/agents/draft", s.authed(s.handleDraft))
	mux.Handle("POST /v1/agents/keepalive", s.authed(s.handleKeepAlive))
	mux.Handle("POST /v1/agents/bench", s.authed(s.handleBench))
	mux.Handle("POST /v1/agents/task", s.authed(s.handleSetTask))
	mux.Handle("POST /v1/agents/turn", s.authed(s.handleTurn))
	mux.Handle("GET /v1/sessions/{session}", s.authed(s.handleGetSession))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiError{http.StatusNotFound, "not_found", "No such endpoint.", nil})
	})
	return withRequestLog(logger, mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeError(w, &apiError{http.StatusServiceUnavailable, "unavailable", "Database unreachable.", nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ownerHandler handles a request from an authenticated owner. A returned error is written as the error response.
type ownerHandler func(w http.ResponseWriter, r *http.Request, ownerID string) error

// authed applies the per-token rate limit and runs h with the owner the bearer token names: an owner's ID is the hash
// of their auth token, so there's nothing to look up. Handlers answer 401 when they find no such owner (see
// ownerMissing), checking only when a query comes back empty, so a known owner's request costs nothing extra.
func (s *Server) authed(h ownerHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeError(w, errUnauthorized)
			return
		}

		ownerID := hashAuthToken(token)
		logOwner(r.Context(), ownerID)
		if allowed, retryAfter := s.requestLimiter.allow(ownerID); !allowed {
			writeRateLimited(w, retryAfter)
			return
		}
		if err := h(w, r, ownerID); err != nil {
			writeError(w, err)
		}
	})
}

// ownerMissing reports whether there's no owner with this ID: the token was never registered, or its owner disbanded
// their last roster. Handlers call it when a query finds nothing, to answer 401 rather than an empty result.
func ownerMissing(ctx context.Context, q querier, ownerID string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `select exists (select 1 from owners where id = $1)`, ownerID).Scan(&exists)
	return !exists, err
}

// orUnauthorized turns notFound into 401 when the owner doesn't exist.
func orUnauthorized(ctx context.Context, q querier, ownerID string, notFound error) error {
	missing, err := ownerMissing(ctx, q, ownerID)
	if err != nil {
		return err
	}
	if missing {
		return errUnauthorized
	}
	return notFound
}

// withOwnerTx runs fn in a transaction holding the owner's row lock, so each owner's roster, link, and settings writes
// run one at a time (drafts don't take it: see draftSession). With no owner row it answers 401. Once the transaction commits,
// it pushes live counts: nearly every owner write can change the counts, and pushes are coalesced.
func (s *Server) withOwnerTx(ctx context.Context, ownerID string, fn func(tx pgx.Tx) error) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `select 1 from owners where id = $1 for update`, ownerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errUnauthorized
		}
		return fn(tx)
	})
	if err == nil {
		s.live.notify()
	}
	return err
}

// sweepEvery runs sweepDrafts every interval for as long as the server runs, pushing live counts when it changed
// anything. A failed sweep is logged and tried again next time.
func (s *Server) sweepEvery(interval time.Duration) {
	for range time.Tick(interval) {
		benched, deleted, err := sweepDrafts(context.Background(), s.db)
		if err != nil {
			s.logger.Error("sweep drafts", "error", err)
			continue
		}
		if benched > 0 || deleted > 0 {
			s.logger.Info("sweep drafts", "benched", benched, "deleted", deleted)
		}
		if benched > 0 {
			s.live.notify()
		}
	}
}

// sweepDrafts is the only cleanup: it benches drafts not seen within their owner's draft expiry, freeing their names,
// and deletes drafts benched more than 7 days ago, past the window a resumed session can ask for its old name back.
// Every instance sweeps; the statements are safe to run at once. It reports how many drafts it benched and deleted.
func sweepDrafts(ctx context.Context, db querier) (benched, deleted int64, err error) {
	batch := &pgx.Batch{}
	batch.Queue(`
		update drafts d set benched_at = now() from owners o
		where o.id = d.owner_id and d.benched_at is null and d.last_seen_at < now() - o.draft_expiry`).Exec(
		func(tag pgconn.CommandTag) error {
			benched = tag.RowsAffected()
			return nil
		})
	batch.Queue(`delete from drafts where benched_at < now() - interval '7 days'`).Exec(
		func(tag pgconn.CommandTag) error {
			deleted = tag.RowsAffected()
			return nil
		})
	err = db.SendBatch(ctx, batch).Close()
	return benched, deleted, err
}

func withPublicCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	})
}

func handlePreflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

// apiError is an error with an HTTP status and a stable code, written as {"error": {"code", "message"}}.
type apiError struct {
	status  int
	code    string
	message string
	rosters []string // the matching rosters, for ambiguous_name
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

var errUnauthorized = &apiError{http.StatusUnauthorized, "unauthorized", "Missing or invalid auth token.", nil}

func invalidRequest(message string) *apiError {
	return &apiError{http.StatusBadRequest, "invalid_request", message, nil}
}

func writeError(w http.ResponseWriter, err error) {
	var apiErr *apiError
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &apiErr):
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		apiErr = &apiError{http.StatusConflict, "conflict", "Conflicts with existing data (" + pgErr.ConstraintName + ").", nil}
	default:
		if rec, ok := w.(*statusRecorder); ok {
			rec.log.mu.Lock()
			rec.log.err = err
			rec.log.mu.Unlock()
		} else {
			slog.Error("internal error", "error", err)
		}
		apiErr = &apiError{http.StatusInternalServerError, "internal", "Internal server error.", nil}
	}

	body := map[string]any{"code": apiErr.code, "message": apiErr.message}
	if apiErr.rosters != nil {
		body["rosters"] = apiErr.rosters
	}
	writeJSON(w, apiErr.status, map[string]any{"error": body})
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds()+0.999)))
	writeError(w, &apiError{http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", nil})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "error", err)
	}
}

const maxBodyBytes = 1 << 20

// decodeJSON reads the request body into v, rejecting unknown fields and bodies over 1 MiB.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &apiError{http.StatusRequestEntityTooLarge, "too_large", "Request body is over 1 MiB.", nil}
		}
		return invalidRequest("Invalid JSON body: " + err.Error())
	}
	return nil
}

// bearerToken returns the decoded auth token from the Authorization header: 32 bytes, base64url without padding.
func bearerToken(r *http.Request) ([]byte, bool) {
	value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, false
	}
	return decodeAuthToken(value)
}

func decodeAuthToken(value string) ([]byte, bool) {
	token, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil || len(token) != 32 {
		return nil, false
	}
	return token, true
}

func hashAuthToken(token []byte) string {
	sum := sha256.Sum256(token)
	return hex.EncodeToString(sum[:])
}

// clientIP is the caller's IP: Railway's proxy sets X-Real-IP; direct connections use the socket address.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
