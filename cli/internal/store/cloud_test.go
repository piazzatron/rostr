package store

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piazzatron/rostr/cli/internal/crypto"
)

// fakeAPI records every request and answers with respond.
type fakeAPI struct {
	mu       sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	method, path, auth string
	body               map[string]any
	raw                string
}

func newFakeAPI(t *testing.T, respond func(w http.ResponseWriter, r recordedRequest)) (*fakeAPI, *Cloud, crypto.DerivedKeys) {
	t.Helper()
	api := &fakeAPI{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec := recordedRequest{method: r.Method, path: r.URL.EscapedPath(), auth: r.Header.Get("Authorization"),
			raw: string(raw)}
		_ = json.Unmarshal(raw, &rec.body)
		api.mu.Lock()
		api.requests = append(api.requests, rec)
		api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		respond(w, rec)
	}))
	t.Cleanup(server.Close)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := NewCloud(server.URL+"/", key)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := key.Derive()
	return api, cloud, keys
}

func writeAPIError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": "Message for " + code + "."}})
}

func TestCloudEncryptsTasksAndReposOnTheWire(t *testing.T) {
	const task, repo = "Fix the secret auth bug", "acme/secret-repo"

	// The fake server echoes the draft's ciphertext back, as the real one stores and returns it
	api, cloud, keys := newFakeAPI(t, func(w http.ResponseWriter, r recordedRequest) {
		switch r.path {
		case "/v1/agents/draft":
			json.NewEncoder(w).Encode(map[string]any{
				"name": "Gandalf", "emoji": "🧙", "copy": 2, "roster": "work", "emojiMode": "on",
				"session": "s1", "task": r.body["task"], "repo": r.body["repo"], "draftedAt": time.Now(),
			})
		default:
			w.Write([]byte(`{}`))
		}
	})

	d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", Roster: "work", Repo: repo, Task: task})
	if err != nil {
		t.Fatal(err)
	}
	if d.Task != task || d.Repo != repo || d.Name() != "Gandalf 2" || d.Display() != "🧙 Gandalf 2" {
		t.Fatalf("decrypted draft: %+v", d)
	}
	if err := cloud.LinkProject(ctx, repo, "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := cloud.SetTask(ctx, AgentKey{Roster: "work", Member: "Gandalf", Copy: 2}, task); err != nil {
		t.Fatal(err)
	}
	err = cloud.Register(ctx, Setup{
		Rosters:  []Roster{{Name: "work", EmojiMode: "on", Members: members("Gandalf")}},
		Settings: Settings{DefaultRoster: "work", AutoDraft: true, Expiry: "7d"},
		Links:    []ProjectLink{{Repo: repo, Roster: "work"}},
		Drafts:   []Draft{{Roster: "work", Member: "Gandalf", Emoji: "🧙", Copy: 1, SessionID: "s1", Repo: repo, Task: task}},
	})
	if err != nil {
		t.Fatal(err)
	}

	fingerprint := keys.Fingerprint(repo)
	for _, r := range api.requests {
		// No plaintext task or repo, and no key material but the auth token, ever leaves
		for _, secret := range []string{task, repo, "secret", crypto.KeyPrefix} {
			if strings.Contains(r.raw, secret) || strings.Contains(r.path, secret) {
				t.Errorf("%s %s sent %q: %s", r.method, r.path, secret, r.raw)
			}
		}
		if r.path == "/v1/register" {
			if r.auth != "" || r.body["authToken"] != keys.AuthToken() {
				t.Errorf("register: auth header %q, body token %v", r.auth, r.body["authToken"])
			}
		} else if r.auth != "Bearer "+keys.AuthToken() {
			t.Errorf("%s: auth header %q", r.path, r.auth)
		}
	}

	// Each request carries the fingerprint and ciphertext that decrypts to the plaintext
	draftReq, linkReq, taskReq, registerReq := api.requests[0], api.requests[1], api.requests[2], api.requests[3]
	decrypt := func(v any) string {
		t.Helper()
		plaintext, err := keys.Decrypt(v.(string))
		if err != nil {
			t.Fatal(err)
		}
		return plaintext
	}
	if draftReq.body["repoHash"] != fingerprint || decrypt(draftReq.body["repo"]) != repo || decrypt(draftReq.body["task"]) != task {
		t.Errorf("draft request: %s", draftReq.raw)
	}
	if linkReq.method != "PUT" || linkReq.path != "/v1/links/"+fingerprint || decrypt(linkReq.body["repo"]) != repo {
		t.Errorf("link request: %s %s %s", linkReq.method, linkReq.path, linkReq.raw)
	}
	if taskReq.body["name"] != "Gandalf 2" || taskReq.body["roster"] != "work" || decrypt(taskReq.body["task"]) != task {
		t.Errorf("task request: %s", taskReq.raw)
	}
	imp := registerReq.body["import"].(map[string]any)
	link := imp["links"].([]any)[0].(map[string]any)
	draft := imp["drafts"].([]any)[0].(map[string]any)
	if link["repoHash"] != fingerprint || decrypt(link["repo"]) != repo || draft["repoHash"] != fingerprint ||
		decrypt(draft["task"]) != task || draft["session"] != "s1" {
		t.Errorf("register request: %s", registerReq.raw)
	}
}

func TestCloudMapsAPIErrors(t *testing.T) {
	var status int
	var code string
	_, cloud, _ := newFakeAPI(t, func(w http.ResponseWriter, r recordedRequest) {
		if code == "" {
			w.WriteHeader(status)
			w.Write([]byte("<html>Bad Gateway</html>"))
			return
		}
		writeAPIError(w, status, code)
	})
	respond := func(s int, c string) { status, code = s, c }

	// Codes that mean "not found", "name taken", or "no rosters" wrap those sentinels
	respond(404, "roster_not_found")
	if _, err := cloud.Roster(ctx, "work"); !errors.Is(err, ErrNotFound) || err.Error() != "Message for roster_not_found." {
		t.Errorf("roster_not_found: %v", err)
	}
	respond(409, "roster_exists")
	if _, err := cloud.CreateRoster(ctx, Roster{Name: "work", EmojiMode: "on", Members: members("A")}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("roster_exists: %v", err)
	}
	respond(404, "no_rosters")
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1"}); !errors.Is(err, ErrNoRosters) || err.Error() != ErrNoRosters.Error() {
		t.Errorf("no_rosters: %v", err)
	}
	respond(404, "not_drafted")
	if _, err := cloud.BenchAgent(ctx, AgentKey{Roster: "work", Member: "A", Copy: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("not_drafted: %v", err)
	}

	// "Nothing there" answers become empty results where the Store says so
	if d, err := cloud.Reclaim(ctx, "s1"); d != nil || err != nil {
		t.Errorf("keep-alive not_drafted: %v, %v", d, err)
	}
	if d, err := cloud.Bench(ctx, "s1"); d != nil || err != nil {
		t.Errorf("release not_drafted: %v, %v", d, err)
	}
	if d, err := cloud.SessionDraft(ctx, "s1"); d != nil || err != nil {
		t.Errorf("session not_drafted: %v, %v", d, err)
	}
	respond(404, "link_not_found")
	if name, err := cloud.ProjectLink(ctx, "acme/api"); name != "" || err != nil {
		t.Errorf("link_not_found: %q, %v", name, err)
	}
	if removed, err := cloud.UnlinkProject(ctx, "acme/api"); removed || err != nil {
		t.Errorf("unlink link_not_found: %v, %v", removed, err)
	}

	// Other failures keep their code, with a message for people
	respond(401, "unauthorized")
	var apiErr *APIError
	if _, err := cloud.Settings(ctx); !errors.As(err, &apiErr) || apiErr.Code != "unauthorized" ||
		!errors.Is(err, ErrUnauthorized) || err.Error() != "the rostr API doesn't recognize your rostr key" {
		t.Errorf("unauthorized: %v", err)
	}
	respond(409, "member_drafted")
	if err := cloud.EditRoster(ctx, "work", "on", members("A")); err == nil || err.Error() != "Message for member_drafted." {
		t.Errorf("member_drafted: %v", err)
	}
	respond(502, "")
	if _, err := cloud.Settings(ctx); err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Errorf("non-JSON error: %v", err)
	}
}

func TestCloudTimesOut(t *testing.T) {
	if CloudTimeout != 3*time.Second {
		t.Fatalf("CloudTimeout is %v; the spec says network calls time out after 3 seconds", CloudTimeout)
	}

	release := make(chan struct{})
	_, cloud, _ := newFakeAPI(t, func(w http.ResponseWriter, r recordedRequest) { <-release })
	defer close(release)
	if cloud.http.Timeout != CloudTimeout {
		t.Fatalf("client timeout %v", cloud.http.Timeout)
	}

	cloud.http.Timeout = 100 * time.Millisecond
	start := time.Now()
	_, err := cloud.Settings(ctx)
	if err == nil || !strings.Contains(err.Error(), "can't reach the rostr API") || time.Since(start) > 2*time.Second {
		t.Fatalf("after %v: %v", time.Since(start), err)
	}
}

func TestSplitAgentName(t *testing.T) {
	for name, want := range map[string]struct {
		member string
		copy   int
	}{
		"Gandalf": {"Gandalf", 1}, "Gandalf 2": {"Gandalf", 2}, "Gandalf 12": {"Gandalf", 12},
		"Gandalf 1": {"Gandalf 1", 1}, "Gandalf 02": {"Gandalf 02", 1}, "Agent 47": {"Agent", 47},
	} {
		if member, copy := splitAgentName(name); member != want.member || copy != want.copy {
			t.Errorf("%q: got %q, %d", name, member, copy)
		}
	}
}

func TestCloudRegisterKeepsRosterOrderWhenTimesTie(t *testing.T) {
	api, cloud, _ := newFakeAPI(t, func(w http.ResponseWriter, r recordedRequest) { w.Write([]byte(`{}`)) })
	second := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	err := cloud.Register(ctx, Setup{Rosters: []Roster{
		{Name: "work", EmojiMode: "on", Members: members("A"), CreatedAt: second},
		{Name: "play", EmojiMode: "on", Members: members("B"), CreatedAt: second},
		{Name: "side", EmojiMode: "on", Members: members("C"), CreatedAt: second.Add(-time.Hour)},
	}})
	if err != nil {
		t.Fatal(err)
	}

	var times []time.Time
	for _, r := range api.requests[0].body["import"].(map[string]any)["rosters"].([]any) {
		parsed, err := time.Parse(time.RFC3339Nano, r.(map[string]any)["createdAt"].(string))
		if err != nil {
			t.Fatal(err)
		}
		times = append(times, parsed)
	}
	if !times[0].Equal(second) || !times[1].After(times[0]) || !times[2].After(times[1]) {
		t.Fatalf("createdAt: %v", times)
	}
}

func TestCloudStatsIsOneCall(t *testing.T) {
	var keys crypto.DerivedKeys
	api, cloud, keys := newFakeAPI(t, func(w http.ResponseWriter, r recordedRequest) {
		task, _ := keys.Encrypt("Fix CI")
		json.NewEncoder(w).Encode(map[string]any{"rosters": []map[string]any{
			{"name": "work", "emojiMode": "on", "isDefault": true, "createdAt": time.Now(),
				"members": []map[string]any{{"name": "Gandalf", "emoji": "🧙"}, {"name": "Frodo", "emoji": "💍"}},
				"drafted": []map[string]any{{"name": "Gandalf 2", "emoji": "🧙", "task": task, "repo": nil,
					"draftedAt": time.Now(), "restingSince": nil}},
				"idle": []any{}},
			{"name": "home", "emojiMode": "only", "isDefault": false, "createdAt": time.Now(),
				"members": []map[string]any{{"name": "Sam", "emoji": "🥔"}}, "drafted": []any{}, "idle": []any{}},
		}})
	})

	stats, err := cloud.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.requests) != 1 || api.requests[0].path != "/v1/stats" {
		t.Fatalf("requests: %+v", api.requests)
	}
	if stats.DefaultRoster != "work" || len(stats.Rosters) != 2 || len(stats.Rosters[0].Members) != 2 ||
		stats.Rosters[1].EmojiMode != "only" || len(stats.Rosters[1].Members) != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	if len(stats.Drafts) != 1 || stats.Drafts[0].Member != "Gandalf" || stats.Drafts[0].Copy != 2 || stats.Drafts[0].Task != "Fix CI" {
		t.Fatalf("drafts: %+v", stats.Drafts)
	}
}
