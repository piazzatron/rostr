package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// requestLog collects attributes a handler adds to its request's log line (see logAttrs).
type requestLog struct {
	mu    sync.Mutex
	attrs []slog.Attr
	err   error // an internal error, logged at error level
}

type requestLogKey struct{}

// logAttrs adds attributes to the request's log line, such as what it created or drafted. Outside a logged request
// it does nothing.
func logAttrs(ctx context.Context, attrs ...slog.Attr) {
	if rl, ok := ctx.Value(requestLogKey{}).(*requestLog); ok {
		rl.mu.Lock()
		rl.attrs = append(rl.attrs, attrs...)
		rl.mu.Unlock()
	}
}

// logOwner names the owner on the request's log line by a short prefix of their ID: enough to follow one owner
// through the logs, not their full ID.
func logOwner(ctx context.Context, ownerID string) {
	logAttrs(ctx, slog.String("owner", ownerID[:min(8, len(ownerID))]))
}

// statusRecorder captures the response status for the log line.
type statusRecorder struct {
	http.ResponseWriter
	status int
	log    *requestLog
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// withRequestLog logs one line per request: its route, status, duration, and whatever the handler added with
// logAttrs. Health checks log only when they fail, so probes don't flood the logs.
func withRequestLog(logger *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rl := &requestLog{}
		rec := &statusRecorder{ResponseWriter: w, log: rl}
		r = r.WithContext(context.WithValue(r.Context(), requestLogKey{}, rl))
		h.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		route := r.Pattern // the matched pattern, so path values like roster names aren't in the route
		if route == "/" {
			route = r.Method + " (unmatched)"
		}
		if route == "GET /health" && status < 400 {
			return
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}
		attrs := []slog.Attr{
			slog.String("route", route),
			slog.Int("status", status),
			slog.Int64("ms", time.Since(start).Milliseconds()),
		}
		rl.mu.Lock()
		attrs = append(attrs, rl.attrs...)
		if rl.err != nil {
			attrs = append(attrs, slog.String("error", rl.err.Error()))
		}
		rl.mu.Unlock()
		logger.LogAttrs(r.Context(), level, "request", attrs...)
	})
}
