// Package api implements the rostr HTTP API (see docs/api.md) on top of Postgres.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the server's rate limits and where it pushes live stats.
type Config struct {
	RegistrationsPerHour int    // per client IP
	RequestsPerMinute    int    // per auth token
	LiveURL              string // the live Worker's base URL; nothing is pushed unless both it and LiveSecret are set
	LiveSecret           string
}

// Server serves the rostr API.
type Server struct {
	db                  *pgxpool.Pool
	registrationLimiter *rateLimiter
	requestLimiter      *rateLimiter
	live                *livePusher // nil when live stats are off
}

// NewHandler returns the API's HTTP handler.
func NewHandler(db *pgxpool.Pool, cfg Config) http.Handler {
	s := &Server{
		db:                  db,
		registrationLimiter: newRateLimiter(cfg.RegistrationsPerHour, time.Hour),
		requestLimiter:      newRateLimiter(cfg.RequestsPerMinute, time.Minute),
	}
	if cfg.LiveURL != "" && cfg.LiveSecret != "" {
		s.live = &livePusher{url: cfg.LiveURL, secret: cfg.LiveSecret, delay: 500 * time.Millisecond, db: db}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /v1/register", s.handleRegister)

	// The public endpoints. CORS allows any origin so browser dashboards and the site can call them.
	mux.Handle("GET /v1/rosters", withPublicCORS(s.authed(s.handleListRosters)))
	mux.Handle("OPTIONS /v1/rosters", withPublicCORS(http.HandlerFunc(handlePreflight)))

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
	mux.Handle("GET /v1/sessions/{session}", s.authed(s.handleGetSession))
	mux.Handle("GET /v1/next", s.authed(s.handleGetNext))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiError{http.StatusNotFound, "not_found", "No such endpoint.", nil})
	})
	return mux
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

// authed resolves the bearer token to an owner, applying the per-token rate limit, then runs h.
func (s *Server) authed(h ownerHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeError(w, errUnauthorized)
			return
		}

		hash := hashAuthToken(token)
		if allowed, retryAfter := s.requestLimiter.allow(hash); !allowed {
			writeRateLimited(w, retryAfter)
			return
		}

		var ownerID string
		err := s.db.QueryRow(r.Context(), `select id from owners where auth_token_hash = $1`, hash).Scan(&ownerID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, errUnauthorized)
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}

		if err := h(w, r, ownerID); err != nil {
			writeError(w, err)
		}
	})
}

// withOwnerTx runs fn in a transaction holding the owner's row lock, so each owner's writes run one at a time. Before
// fn runs, it releases the owner's drafts not seen within their draft expiry and deletes drafts released more than 7
// days ago. This is the only cleanup; there's no background job. Once the transaction commits, it pushes live stats:
// nearly every owner write can change the counts, and pushes are coalesced.
func (s *Server) withOwnerTx(ctx context.Context, ownerID string, fn func(tx pgx.Tx) error) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select 1 from owners where id = $1 for update`, ownerID); err != nil {
			return err
		}

		// Expire stale drafts
		_, err := tx.Exec(ctx, `
			update drafts d set benched_at = now()
			from rosters r, owners o
			where d.roster_id = r.id and r.owner_id = $1 and o.id = $1
			  and d.benched_at is null and d.last_seen_at < now() - o.draft_expiry`, ownerID)
		if err != nil {
			return err
		}

		// Prune released drafts past the 7-day resume window, and expired reservations
		_, err = tx.Exec(ctx, `
			delete from drafts d using rosters r
			where d.roster_id = r.id and r.owner_id = $1 and d.benched_at < now() - interval '7 days'`, ownerID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			delete from reservations v using rosters r
			where v.roster_id = r.id and r.owner_id = $1 and v.expires_at <= now()`, ownerID)
		if err != nil {
			return err
		}

		return fn(tx)
	})
	if err == nil {
		s.live.notify()
	}
	return err
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
		log.Printf("internal error: %v", err)
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
		log.Printf("write response: %v", err)
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
