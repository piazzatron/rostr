package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The tests run against a real Postgres (the compose one; see server/README.md). Every test registers its own owners
// with random tokens, so tests run in parallel and leave each other alone.
var (
	testDB      *pgxpool.Pool
	testHandler http.Handler
)

func TestMain(m *testing.M) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "TEST_DATABASE_URL is not set. Start Postgres with `docker compose up -d postgres`; see server/README.md.")
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := Migrate(ctx, db); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	testHandler = NewHandler(db, Config{RegistrationsPerHour: 1_000_000, RequestsPerMinute: 1_000_000})

	code := m.Run()
	db.Close()
	os.Exit(code)
}

// client calls the handler as one owner.
type client struct {
	t       *testing.T
	handler http.Handler
	token   string
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// newOwner registers a fresh owner.
func newOwner(t *testing.T) *client {
	t.Helper()
	c := &client{t: t, handler: testHandler, token: newToken()}
	call[settingsResponse](c, "POST", "/v1/register", map[string]any{"authToken": c.token}, http.StatusCreated)
	return c
}

func (c *client) do(method, path string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = bytes.NewBufferString(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec
}

// call makes a request, checks the status, and decodes the response body as T.
func call[T any](c *client, method, path string, body any, wantStatus int) T {
	c.t.Helper()
	rec := c.do(method, path, body)
	if rec.Code != wantStatus {
		c.t.Fatalf("%s %s: status %d, want %d; body: %s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	var out T
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			c.t.Fatalf("%s %s: decode %T: %v; body: %s", method, path, out, err, rec.Body.String())
		}
	}
	return out
}

type errorBody struct {
	Error struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Rosters []string `json:"rosters"`
	} `json:"error"`
}

// callError makes a request that should fail, and checks its status and error code.
func callError(c *client, method, path string, body any, wantStatus int, wantCode string) errorBody {
	c.t.Helper()
	resp := call[errorBody](c, method, path, body, wantStatus)
	if resp.Error.Code != wantCode || resp.Error.Message == "" {
		c.t.Fatalf("%s %s: error %+v, want code %q with a message", method, path, resp.Error, wantCode)
	}
	return resp
}

func members(names ...string) []member {
	out := make([]member, len(names))
	for i, name := range names {
		out[i] = member{Name: name, Emoji: "⭐"}
	}
	return out
}

func createRoster(c *client, name string, aTeam, bTeam []member) rosterResponse {
	c.t.Helper()
	return call[rosterResponse](c, "POST", "/v1/rosters",
		map[string]any{"name": name, "aTeam": aTeam, "bTeam": bTeam}, http.StatusCreated)
}

func draft(c *client, body map[string]any) draftResponse {
	c.t.Helper()
	return call[draftResponse](c, "POST", "/v1/agents/draft", body, http.StatusOK)
}

func draftSessionNamed(c *client, session string) draftResponse {
	c.t.Helper()
	return draft(c, map[string]any{"session": session})
}

// fakeCiphertext stands in for AES-GCM output: a 12-byte nonce, the bytes, and a 16-byte tag, base64-encoded.
func fakeCiphertext(plaintext string) string {
	b := append(make([]byte, 12), plaintext...)
	return base64.StdEncoding.EncodeToString(append(b, make([]byte, 16)...))
}

func fakeRepoHash(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return hex.EncodeToString(sum[:])
}

func execSQL(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := testDB.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func queryInt(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := testDB.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func displayOf(d draftResponse) string {
	return displayName(d.Name, d.Copy)
}

type listResponse struct {
	Rosters []listedRoster `json:"rosters"`
}

func list(c *client) listResponse {
	c.t.Helper()
	return call[listResponse](c, "GET", "/v1/stats", nil, http.StatusOK)
}

func draftedNames(l listResponse, roster string) []string {
	var names []string
	for _, r := range l.Rosters {
		if r.Name == roster {
			for _, d := range r.Drafted {
				names = append(names, d.Name)
			}
		}
	}
	return names
}

// session makes a session ID unique to this owner, so tests can update its rows directly without touching others'.
func (c *client) session(name string) string {
	return c.token[:12] + "-" + name
}

// decodeDraft is safe to call from other goroutines: it reports errors without stopping the test.
func decodeDraft(t *testing.T, body []byte) draftResponse {
	var d draftResponse
	if err := json.Unmarshal(body, &d); err != nil {
		t.Errorf("decode draft: %v", err)
	}
	return d
}
