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

// fakeRoster is a one-roster server for the cache tests: it drafts the preferred member when free (else their next
// copy), otherwise the first idle member not in avoid, and answers every draft-changing call with the next state.
type fakeRoster struct {
	mu       sync.Mutex
	busy     map[string]int // member → copies drafted
	tasks    map[string]string
	reserved string // what next says this machine holds
}

func (f *fakeRoster) respond(w http.ResponseWriter, r recordedRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	members := []string{"Gandalf", "Frodo"}
	next := func() map[string]any {
		var idle []map[string]any
		for _, m := range members {
			if f.busy[m] == 0 {
				idle = append(idle, map[string]any{"name": m, "emoji": "⭐", "team": "a"})
			}
		}
		if idle == nil {
			idle = []map[string]any{}
		}
		var reserved any
		if f.reserved != "" {
			reserved = f.reserved
		}
		return map[string]any{"autoDraft": true, "defaultRoster": "work", "links": []any{},
			"rosters": []map[string]any{{"name": "work", "emojiMode": "on", "idle": idle, "reserved": reserved}}}
	}
	draft := func(name string, copy int, session string) map[string]any {
		return map[string]any{"name": name, "emoji": "⭐", "copy": copy, "team": "a", "roster": "work", "emojiMode": "on",
			"session": session, "task": nil, "repo": nil, "draftedAt": time.Now(), "next": next()}
	}
	switch r.path {
	case "/v1/settings":
		json.NewEncoder(w).Encode(map[string]any{"defaultRoster": "work", "draftExpiry": "7d", "autoDraft": true})
	case "/v1/next":
		json.NewEncoder(w).Encode(next())
	case "/v1/agents/draft":
		session, _ := r.body["session"].(string)
		avoid := map[string]bool{}
		if list, ok := r.body["avoid"].([]any); ok {
			for _, a := range list {
				avoid[a.(string)] = true
			}
		}
		if preferred, ok := r.body["preferred"].(string); ok {
			f.busy[preferred]++
			json.NewEncoder(w).Encode(draft(preferred, f.busy[preferred], session))
			return
		}
		for _, m := range members {
			if f.busy[m] == 0 && !avoid[m] {
				f.busy[m] = 1
				json.NewEncoder(w).Encode(draft(m, 1, session))
				return
			}
		}
		f.busy["Gandalf"]++
		json.NewEncoder(w).Encode(draft("Gandalf", f.busy["Gandalf"], session))
	case "/v1/agents/release":
		json.NewEncoder(w).Encode(draft("Gandalf", 1, r.body["session"].(string)))
	case "/v1/sessions/s-elsewhere":
		json.NewEncoder(w).Encode(draft("Frodo", 1, "s-elsewhere"))
	default:
		writeAPIError(w, http.StatusNotFound, "not_drafted")
	}
}

// fakesByCloud finds a test's fake roster from its Cloud, for tests that change what the server says.
var fakesByCloud = map[*Cloud]*fakeRoster{}

func cloudFake(c *Cloud) *fakeRoster { return fakesByCloud[c] }

// newCachedCloud is a Cloud with a cache whose confirmations run synchronously, unless spawn is given.
func newCachedCloud(t *testing.T, spawn func(Confirmation) error) (*fakeAPI, *Cloud, string) {
	t.Helper()
	fake := &fakeRoster{busy: map[string]int{}, tasks: map[string]string{}}
	api, cloud, _ := newFakeAPI(t, fake.respond)
	fakesByCloud[cloud] = fake
	dir := filepath.Join(t.TempDir(), "cloud")
	if spawn == nil {
		spawn = func(conf Confirmation) error {
			_, err := cloud.ConfirmDraft(ctx, conf)
			return err
		}
	}
	cloud.MachineID = "test-machine"
	cloud.UseCache(dir, spawn)
	return api, cloud, dir
}

func TestFastDraftTakesTheMemberReservedForThisMachine(t *testing.T) {
	api, cloud, _ := newCachedCloud(t, func(Confirmation) error { return nil })
	// Warm the cache with next saying the server holds Frodo for this machine; every request names the machine
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "warm"}); err != nil {
		t.Fatal(err)
	}
	if api.requests[0].machine != "test-machine" {
		t.Fatalf("machine header: %q", api.requests[0].machine)
	}
	// Both are idle, Gandalf first; the reservation wins (set it, then refresh the cache)
	cloudFake(cloud).reserved = "Frodo"
	cloud.RefreshNext(ctx)
	if d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", Fast: true}); err != nil || d.Name() != "Frodo" {
		t.Fatalf("fast draft: %+v, %v", d, err)
	}
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

func TestFastDraftPicksLocallyAndConfirmsWithTheServer(t *testing.T) {
	var confirmations []Confirmation
	api, cloud, dir := newCachedCloud(t, func(conf Confirmation) error {
		confirmations = append(confirmations, conf)
		return nil
	})

	// Nothing cached yet: the first draft is a server call, whose response warms the cache
	d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", Fast: true, IfAutoDraft: true})
	if err != nil || d.Name() != "Gandalf" || len(confirmations) != 0 || api.count("/v1/agents/draft") != 1 {
		t.Fatalf("cold draft: %+v, %v, confirmations %v", d, err, confirmations)
	}
	if _, err := os.Stat(filepath.Join(dir, "next.json")); err != nil {
		t.Fatal("no next state cached after the first response")
	}

	// The next Fast draft is answered from the cache, with no request, and a confirmation is started
	d, err = cloud.Draft(ctx, DraftRequest{SessionID: "s2", Repo: "acme/api", Task: "Fix it", Fast: true})
	if err != nil || d.Name() != "Frodo" || d.Display() != "⭐ Frodo" || d.Task != "Fix it" || d.Repo != "acme/api" {
		t.Fatalf("fast draft: %+v, %v", d, err)
	}
	if api.count("/v1/agents/draft") != 1 {
		t.Fatal("fast draft called the server")
	}
	want := Confirmation{SessionID: "s2", Roster: "work", Repo: "acme/api", Task: "Fix it", Preferred: "Frodo"}
	if len(confirmations) != 1 || confirmations[0] != want {
		t.Fatalf("confirmations: %+v", confirmations)
	}

	// Meanwhile the session reads its draft from the cache, and a server draft avoids the unconfirmed pick
	if again, err := cloud.SessionDraft(ctx, "s2"); err != nil || again.Name() != "Frodo" || api.count("/v1/sessions/s2") != 0 {
		t.Fatalf("session draft from cache: %+v, %v", again, err)
	}
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s3"}); err != nil {
		t.Fatal(err)
	}
	if last := api.requests[len(api.requests)-1]; last.path != "/v1/agents/draft" || len(last.body["avoid"].([]any)) != 1 ||
		last.body["avoid"].([]any)[0] != "Frodo" {
		t.Fatalf("server draft didn't avoid the pending pick: %s", last.raw)
	}

	// The confirmation sends the pick as preferred, and the server's answer replaces the cached draft
	if _, err := cloud.ConfirmDraft(ctx, confirmations[0]); err != nil {
		t.Fatal(err)
	}
	if last := api.requests[len(api.requests)-1]; last.body["preferred"] != "Frodo" || last.body["session"] != "s2" {
		t.Fatalf("confirmation request: %s", last.raw)
	}
	if again, err := cloud.SessionDraft(ctx, "s2"); err != nil || again.Name() != "Frodo" || again.RenamedFrom != "" {
		t.Fatalf("after confirming: %+v, %v", again, err)
	}
}

func TestFastDraftRenamesWhenTheServerConfirmsACopy(t *testing.T) {
	_, cloud, _ := newCachedCloud(t, nil)
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "warm"}); err != nil {
		t.Fatal(err)
	}

	// Another machine took Frodo first: the confirmation comes back as Frodo 2, and the session is told once
	cloud.cache.spawn = func(conf Confirmation) error { return nil } // confirmed by hand below
	d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", Fast: true})
	if err != nil || d.Name() != "Frodo" {
		t.Fatalf("fast draft: %+v, %v", d, err)
	}
	if _, err := cloud.ConfirmDraft(ctx, Confirmation{SessionID: "elsewhere", Roster: "work", Preferred: "Frodo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cloud.ConfirmDraft(ctx, Confirmation{SessionID: "s1", Roster: "work", Preferred: "Frodo"}); err != nil {
		t.Fatal(err)
	}
	renamed, err := cloud.SessionDraft(ctx, "s1")
	if err != nil || renamed.Name() != "Frodo 2" || renamed.RenamedFrom != "⭐ Frodo" {
		t.Fatalf("renamed: %+v, %v", renamed, err)
	}
	cloud.MarkAnnounced("s1")
	if told, err := cloud.SessionDraft(ctx, "s1"); err != nil || told.RenamedFrom != "" {
		t.Fatalf("after announcing: %+v, %v", told, err)
	}
}

func TestFastDraftFallsBackToTheServer(t *testing.T) {
	api, cloud, dir := newCachedCloud(t, nil)
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "warm"}); err != nil { // takes Gandalf; Frodo idle
		t.Fatal(err)
	}

	// A stale next state isn't drafted from
	stale := filepath.Join(dir, "next.json")
	data, _ := os.ReadFile(stale)
	var next nextState
	json.Unmarshal(data, &next)
	next.FetchedAt = time.Now().Add(-2 * nextTTL)
	writeFileAtomically(stale, next)
	before := api.count("/v1/agents/draft")
	if d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", Fast: true}); err != nil || d.Name() != "Frodo" || api.count("/v1/agents/draft") != before+1 {
		t.Fatalf("stale: %+v, %v", d, err)
	}

	// With everyone busy, so is the server
	before = api.count("/v1/agents/draft")
	if d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s2", Fast: true}); err != nil || d.Name() != "Gandalf 2" || api.count("/v1/agents/draft") != before+1 {
		t.Fatalf("all busy: %+v, %v", d, err)
	}

	// Auto-draft off, known from the cache, needs no request
	data, _ = os.ReadFile(stale)
	json.Unmarshal(data, &next)
	next.AutoDraft = false
	writeFileAtomically(stale, next)
	before = len(api.requests)
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "s3", Fast: true, IfAutoDraft: true}); !errors.Is(err, ErrAutoDraftOff) || len(api.requests) != before {
		t.Fatalf("auto-draft off: %v, %d requests", err, len(api.requests)-before)
	}
}

func TestSessionCacheFollowsTheServer(t *testing.T) {
	api, cloud, _ := newCachedCloud(t, nil)

	// A session drafted elsewhere is fetched once, then cached
	if d, err := cloud.SessionDraft(ctx, "s-elsewhere"); err != nil || d == nil || d.Name() != "Frodo" {
		t.Fatalf("fetch: %+v, %v", d, err)
	}
	if _, err := cloud.SessionDraft(ctx, "s-elsewhere"); err != nil || api.count("/v1/sessions/s-elsewhere") != 1 {
		t.Fatal("second read wasn't cached")
	}

	// Release forgets it, and an unknown session is asked for each time
	if _, err := cloud.Release(ctx, "s-elsewhere"); err != nil {
		t.Fatal(err)
	}
	if d, err := cloud.SessionDraft(ctx, "s-elsewhere"); err != nil || api.count("/v1/sessions/s-elsewhere") != 2 || d == nil {
		t.Fatalf("after release: %+v, %v", d, err)
	}
	if d, err := cloud.SessionDraft(ctx, "nobody"); err != nil || d != nil {
		t.Fatalf("unknown: %+v, %v", d, err)
	}
}

func TestUnconfirmedDraftIsRetriedOnRead(t *testing.T) {
	api, cloud, _ := newCachedCloud(t, func(Confirmation) error { return errors.New("couldn't start") })
	if _, err := cloud.Draft(ctx, DraftRequest{SessionID: "warm"}); err != nil {
		t.Fatal(err)
	}
	d, err := cloud.Draft(ctx, DraftRequest{SessionID: "s1", Fast: true})
	if err != nil || d.Name() != "Frodo" {
		t.Fatalf("fast draft with a failed spawn: %+v, %v", d, err)
	}

	// Too soon to retry, then retried once the draft is old enough and the last try long enough ago
	before := api.count("/v1/agents/draft")
	if _, err := cloud.SessionDraft(ctx, "s1"); err != nil || api.count("/v1/agents/draft") != before {
		t.Fatal("retried immediately")
	}
	cs := cloud.cache.session("s1")
	cs.At, cs.LastTry = time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)
	cloud.cache.saveSession(*cs)
	if _, err := cloud.SessionDraft(ctx, "s1"); err != nil || api.count("/v1/agents/draft") != before+1 {
		t.Fatal("didn't retry")
	}
	if cs := cloud.cache.session("s1"); !cs.Confirmed {
		t.Fatal("retry didn't confirm")
	}
}
