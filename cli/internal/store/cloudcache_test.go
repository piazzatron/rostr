package store

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeRoster is a one-roster server for the cache tests: it drafts the first free member, else the next copy of
// Gandalf, and remembers each session's draft.
type fakeRoster struct {
	mu       sync.Mutex
	busy     map[string]int    // member → copies drafted
	sessions map[string]string // session → member
}

func (f *fakeRoster) respond(w http.ResponseWriter, r recordedRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	draft := func(name string, copy int, session string) map[string]any {
		return map[string]any{"name": name, "emoji": "⭐", "copy": copy, "roster": "work", "emojiMode": "on",
			"session": session, "task": nil, "repo": nil, "draftedAt": time.Now()}
	}
	switch r.path {
	case "/v1/settings":
		json.NewEncoder(w).Encode(map[string]any{"defaultRoster": "work", "draftExpiry": "7d"})
	case "/v1/agents/draft":
		session, _ := r.body["session"].(string)
		if m, ok := f.sessions[session]; ok {
			json.NewEncoder(w).Encode(draft(m, 1, session))
			return
		}
		for _, m := range []string{"Gandalf", "Frodo"} {
			if f.busy[m] == 0 {
				f.busy[m], f.sessions[session] = 1, m
				json.NewEncoder(w).Encode(draft(m, 1, session))
				return
			}
		}
		f.busy["Gandalf"]++
		json.NewEncoder(w).Encode(draft("Gandalf", f.busy["Gandalf"], session))
	case "/v1/agents/bench":
		json.NewEncoder(w).Encode(draft("Gandalf", 1, r.body["session"].(string)))
	case "/v1/agents/task":
		for session, m := range f.sessions {
			if m == r.body["name"] {
				json.NewEncoder(w).Encode(draft(m, 1, session))
				return
			}
		}
		writeAPIError(w, http.StatusNotFound, "not_drafted")
	case "/v1/sessions/s-elsewhere":
		json.NewEncoder(w).Encode(draft("Frodo", 1, "s-elsewhere"))
	default:
		writeAPIError(w, http.StatusNotFound, "not_drafted")
	}
}

// newCachedCloud is a Cloud with a session cache and an auto-draft file, against a fakeRoster.
func newCachedCloud(t *testing.T) (*fakeAPI, *Cloud) {
	t.Helper()
	fake := &fakeRoster{busy: map[string]int{}, sessions: map[string]string{}}
	api, cloud, _ := newFakeAPI(t, fake.respond)
	dir := t.TempDir()
	cloud.UseCache(filepath.Join(dir, "cloud"))
	cloud.AutoDraftFile = filepath.Join(dir, "auto-draft")
	return api, cloud
}

func (api *fakeAPI) count(path string) int {
	api.mu.Lock()
	defer api.mu.Unlock()
	n := 0
	for _, r := range api.requests {
		if r.path == path {
			n++
		}
	}
	return n
}

func TestADraftIsOneCallThenCached(t *testing.T) {
	api, cloud := newCachedCloud(t)
	d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", IfAutoDraft: true, Idle: true})
	if err != nil || d.Name() != "Gandalf" {
		t.Fatalf("draft: %+v, %v", d, err)
	}
	if len(api.requests) != 1 || api.requests[0].path != "/v1/agents/draft" || api.requests[0].body["idle"] != true {
		t.Fatalf("requests: %+v", api.requests)
	}
	for _, field := range []string{"preferred", "avoid"} {
		if _, ok := api.requests[0].body[field]; ok {
			t.Fatalf("sent %s", field)
		}
	}
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1"}); err != nil || len(api.requests) != 1 {
		t.Fatalf("a cached session's draft called the server: %v", err)
	}
}

func TestAutoDraftIsPerMachine(t *testing.T) {
	api, cloud := newCachedCloud(t)

	// On until this machine turns it off, and checked without a call
	if s, err := cloud.Settings(ctx); err != nil || !s.AutoDraft || s.DefaultRoster != "work" {
		t.Fatalf("settings: %+v, %v", s, err)
	}
	if err := cloud.SetAutoDraft(ctx, false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(cloud.AutoDraftFile); string(data) != "off\n" {
		t.Fatalf("auto-draft file: %q", data)
	}
	before := len(api.requests)
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", IfAutoDraft: true}); !errors.Is(err, ErrAutoDraftOff) {
		t.Fatalf("draft with auto-draft off: %v", err)
	}
	if api.count("/v1/settings") != 1 || api.count("/v1/agents/draft") != 0 || len(api.requests) != before+1 {
		t.Fatalf("requests: %+v", api.requests[before:]) // only the check for an existing draft
	}
	if s, _ := cloud.Settings(ctx); s.AutoDraft {
		t.Fatal("settings say on")
	}

	// The environment overrides the file, as in a cloud container
	t.Setenv(AutoDraftEnv, "on")
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", IfAutoDraft: true}); err != nil {
		t.Fatalf("draft with ROSTR_AUTO_DRAFT=on: %v", err)
	}
}

func TestCachedDraftsTrackWhetherTheAgentWasTold(t *testing.T) {
	_, cloud := newCachedCloud(t)
	if d, told := cloud.CachedSessionDraft("s1"); d != nil || told {
		t.Fatal("an unknown session is cached")
	}

	// Drafted when the session started, the agent hasn't been told yet
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", Idle: true}); err != nil {
		t.Fatal(err)
	}
	if d, told := cloud.CachedSessionDraft("s1"); d == nil || d.Name() != "Gandalf" || told {
		t.Fatalf("after drafting: %+v, told %v", d, told)
	}
	cloud.MarkAnnounced("s1")
	if _, told := cloud.CachedSessionDraft("s1"); !told {
		t.Fatal("not told after MarkAnnounced")
	}

	// A later answer from the server for the same draft keeps that
	if _, err := cloud.SetTask(ctx, AgentKey{Roster: "work", Member: "Gandalf", Copy: 1}, "x"); err != nil {
		t.Fatal(err)
	}
	if _, told := cloud.CachedSessionDraft("s1"); !told {
		t.Fatal("forgot the agent was told")
	}
}

func TestSessionCacheFollowsTheServer(t *testing.T) {
	api, cloud := newCachedCloud(t)

	// A session drafted elsewhere is fetched once, then cached
	if d, err := cloud.SessionDraft(ctx, "s-elsewhere"); err != nil || d == nil || d.Name() != "Frodo" {
		t.Fatalf("fetch: %+v, %v", d, err)
	}
	if _, err := cloud.SessionDraft(ctx, "s-elsewhere"); err != nil || api.count("/v1/sessions/s-elsewhere") != 1 {
		t.Fatal("second read wasn't cached")
	}

	// Bench forgets it, and an unknown session is asked for each time
	if _, err := cloud.Bench(ctx, "s-elsewhere"); err != nil {
		t.Fatal(err)
	}
	if d, err := cloud.SessionDraft(ctx, "s-elsewhere"); err != nil || api.count("/v1/sessions/s-elsewhere") != 2 || d == nil {
		t.Fatalf("after release: %+v, %v", d, err)
	}
	if d, err := cloud.SessionDraft(ctx, "nobody"); err != nil || d != nil {
		t.Fatalf("unknown: %+v, %v", d, err)
	}
}

func TestAStaleCachedDraftIsCheckedWithTheServer(t *testing.T) {
	api, cloud := newCachedCloud(t)
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}

	// Older than the keep-alive interval: the server is asked
	cs := cloud.cache.session("s1")
	cs.At = time.Now().Add(-2 * cachedDraftTrust)
	cloud.cache.saveSession(*cs)
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1"}); err != nil || api.count("/v1/agents/draft") != 2 {
		t.Fatalf("stale cached draft wasn't checked: %v", err)
	}
}

func TestATurnReportForAGoneDraftForgetsIt(t *testing.T) {
	_, cloud := newCachedCloud(t)
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}

	// The server doesn't have the draft: its name was taken while it rested
	if err := cloud.RecordTurn(ctx, "s1", false, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("turn: %v", err)
	}
	if cloud.cache.session("s1") != nil {
		t.Fatal("kept a draft the server doesn't have")
	}
}
