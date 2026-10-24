package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// TestRequestLog checks each request logs one line naming its route, status, and owner, with what the handler added,
// and never the auth token.
func TestRequestLog(t *testing.T) {
	var buf bytes.Buffer
	handler := NewHandler(testDB, Config{RegistrationsPerHour: 1_000_000, RequestsPerMinute: 1_000_000,
		Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	c := &client{t: t, handler: handler, token: newToken()}
	call[settingsResponse](c, "POST", "/v1/register", map[string]any{"authToken": c.token}, http.StatusCreated)
	call[rosterResponse](c, "POST", "/v1/rosters", map[string]any{"name": "logs", "aTeam": members("Ada", "Bo")}, http.StatusCreated)
	call[draftResponse](c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("s")}, http.StatusOK)
	callError(c, "GET", "/v1/rosters/nope", nil, http.StatusNotFound, "roster_not_found")
	c.do("GET", "/health", nil)

	if strings.Contains(buf.String(), c.token) {
		t.Fatal("logs contain the auth token")
	}
	var lines []map[string]any
	for line := range strings.Lines(buf.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 4 { // the health check isn't logged
		t.Fatalf("got %d log lines, want 4: %s", len(lines), buf.String())
	}
	owner := hashAuthToken(mustDecodeToken(t, c.token))[:8]
	want := []map[string]any{
		{"route": "POST /v1/register", "status": 201.0, "level": "INFO", "imported": false},
		{"route": "POST /v1/rosters", "status": 201.0, "roster": "logs", "members": 2.0},
		{"route": "POST /v1/agents/draft", "status": 200.0, "roster": "logs", "copy": 1.0},
		{"route": "GET /v1/rosters/{name}", "status": 404.0, "level": "WARN"},
	}
	for i, fields := range want {
		if lines[i]["owner"] != owner {
			t.Errorf("line %d: owner = %v, want %v", i, lines[i]["owner"], owner)
		}
		for k, v := range fields {
			if lines[i][k] != v {
				t.Errorf("line %d: %s = %v, want %v", i, k, lines[i][k], v)
			}
		}
	}
	if m := lines[2]["member"]; m != "Ada" && m != "Bo" {
		t.Errorf("draft member = %v", m)
	}
}

func mustDecodeToken(t *testing.T, token string) []byte {
	t.Helper()
	b, ok := decodeAuthToken(token)
	if !ok {
		t.Fatal("bad token")
	}
	return b
}
