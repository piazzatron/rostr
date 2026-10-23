package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAuth(t *testing.T) {
	t.Parallel()
	anon := &client{t: t, handler: testHandler}
	callError(anon, "GET", "/v1/stats", nil, http.StatusUnauthorized, "unauthorized")

	malformed := &client{t: t, handler: testHandler, token: "not-a-token"}
	callError(malformed, "GET", "/v1/settings", nil, http.StatusUnauthorized, "unauthorized")

	unregistered := &client{t: t, handler: testHandler, token: newToken()}
	callError(unregistered, "GET", "/v1/settings", nil, http.StatusUnauthorized, "unauthorized")
	callError(unregistered, "POST", "/v1/agents/draft", map[string]any{"session": "s"}, http.StatusUnauthorized, "unauthorized")

	// Registration takes the token in the body, once
	owner := newOwner(t)
	callError(owner, "POST", "/v1/register", map[string]any{"authToken": owner.token}, http.StatusConflict, "already_registered")
	callError(anon, "POST", "/v1/register", map[string]any{"authToken": "short"}, http.StatusBadRequest, "invalid_request")

	// Only a hash of the token is stored
	if n := queryInt(t, `select count(*) from owners where auth_token_hash = $1`, owner.token); n != 0 {
		t.Fatal("auth token stored in plaintext")
	}
	if got := call[settingsResponse](owner, "GET", "/v1/settings", nil, http.StatusOK); got.DraftExpiry != "7d" || !got.AutoDraft || got.DefaultRoster != nil {
		t.Fatalf("new owner settings: %+v", got)
	}
}

func TestOwnersCantSeeOrChangeEachOthersData(t *testing.T) {
	t.Parallel()
	a, b := newOwner(t), newOwner(t)
	createRoster(a, "work", members("Gandalf", "Frodo"), nil)
	repoHash := fakeRepoHash("piazzatron/rostr")
	call[linkResponse](a, "PUT", "/v1/links/"+repoHash, map[string]any{"repo": fakeCiphertext("piazzatron/rostr"), "roster": "work"}, http.StatusOK)
	session := a.session("s1")
	draft(a, map[string]any{"session": session, "task": fakeCiphertext("secret")})

	// B sees none of it
	if l := list(b); len(l.Rosters) != 0 {
		t.Fatalf("B sees rosters: %+v", l)
	}
	callError(b, "GET", "/v1/rosters/work", nil, http.StatusNotFound, "roster_not_found")
	callError(b, "GET", "/v1/links/"+repoHash, nil, http.StatusNotFound, "link_not_found")
	callError(b, "GET", "/v1/sessions/"+session, nil, http.StatusNotFound, "not_drafted")
	if links := call[map[string][]linkResponse](b, "GET", "/v1/links", nil, http.StatusOK); len(links["links"]) != 0 {
		t.Fatalf("B sees links: %v", links)
	}

	// B can't change any of it
	callError(b, "PUT", "/v1/rosters/work", map[string]any{"aTeam": members("Mario")}, http.StatusNotFound, "roster_not_found")
	callError(b, "PUT", "/v1/rosters/work/cast", map[string]any{"aTeam": members("Mario")}, http.StatusNotFound, "roster_not_found")
	callError(b, "DELETE", "/v1/rosters/work", nil, http.StatusNotFound, "roster_not_found")
	callError(b, "DELETE", "/v1/links/"+repoHash, nil, http.StatusNotFound, "link_not_found")
	callError(b, "POST", "/v1/agents/bench", map[string]any{"session": session}, http.StatusNotFound, "not_drafted")
	callError(b, "POST", "/v1/agents/bench", map[string]any{"name": "Gandalf"}, http.StatusNotFound, "not_drafted")
	callError(b, "POST", "/v1/agents/task", map[string]any{"session": session, "task": fakeCiphertext("x")}, http.StatusNotFound, "not_drafted")
	callError(b, "POST", "/v1/agents/keepalive", map[string]any{"session": session}, http.StatusNotFound, "not_drafted")
	callError(b, "PATCH", "/v1/settings", map[string]any{"defaultRoster": "work"}, http.StatusNotFound, "roster_not_found")
	callError(b, "PUT", "/v1/links/"+repoHash, map[string]any{"repo": fakeCiphertext("x"), "roster": "work"}, http.StatusNotFound, "roster_not_found")

	// B's own roster with the same name and the same session ID stay separate
	createRoster(b, "work", members("Mario"), nil)
	if d := draftSessionNamed(b, session); d.Name != "Mario" {
		t.Fatalf("B drafted %s", d.Name)
	}
	call[linkResponse](b, "PUT", "/v1/links/"+repoHash, map[string]any{"repo": fakeCiphertext("b"), "roster": "work"}, http.StatusOK)
	call[map[string]any](b, "DELETE", "/v1/rosters/work", nil, http.StatusOK)

	// A is untouched
	d := call[draftResponse](a, "GET", "/v1/sessions/"+session, nil, http.StatusOK)
	if d.Name != "Gandalf" || *d.Task != fakeCiphertext("secret") {
		t.Fatalf("A's draft changed: %+v", d)
	}
	if link := call[linkResponse](a, "GET", "/v1/links/"+repoHash, nil, http.StatusOK); link.Repo != fakeCiphertext("piazzatron/rostr") {
		t.Fatalf("A's link changed: %+v", link)
	}
}

func importBody(token string) map[string]any {
	return map[string]any{
		"authToken": token,
		"import": map[string]any{
			"rosters": []map[string]any{
				{"name": "work", "emojiMode": "off", "aTeam": members("Gandalf", "Frodo"), "bTeam": members("Boromir"),
					"createdAt": "2026-01-01T00:00:00Z"},
				{"name": "games", "aTeam": members("Mario", "Luigi"), "createdAt": "2026-02-01T00:00:00Z"},
			},
			"settings": map[string]any{"defaultRoster": "games", "draftExpiry": "48h", "autoDraft": false},
			"links": []map[string]any{
				{"repoHash": fakeRepoHash("piazzatron/rostr"), "repo": fakeCiphertext("piazzatron/rostr"), "roster": "work"},
			},
			"drafts": []map[string]any{
				{"roster": "work", "name": "Gandalf", "emoji": "🧙", "session": "import-s1",
					"repoHash": fakeRepoHash("piazzatron/rostr"), "repo": fakeCiphertext("piazzatron/rostr"),
					"task": fakeCiphertext("fix auth"), "draftedAt": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
				{"roster": "games", "name": "Mario", "emoji": "🍄", "copy": 2, "session": "import-s2"},
			},
		},
	}
}

func TestImportUploadsAWholeSetup(t *testing.T) {
	t.Parallel()
	c := &client{t: t, handler: testHandler, token: newToken()}
	body := importBody(c.token)
	imp := body["import"].(map[string]any)
	drafts := imp["drafts"].([]map[string]any)
	drafts[0]["session"], drafts[1]["session"] = c.session("s1"), c.session("s2")

	settings := call[settingsResponse](c, "POST", "/v1/register", body, http.StatusCreated)
	if *settings.DefaultRoster != "games" || settings.DraftExpiry != "2d" || settings.AutoDraft {
		t.Fatalf("settings: %+v", settings)
	}

	l := list(c)
	if len(l.Rosters) != 2 || l.Rosters[0].Name != "work" || l.Rosters[0].EmojiMode != "off" {
		t.Fatalf("rosters: %+v", l.Rosters)
	}
	if names := draftedNames(l, "games"); !slices.Equal(names, []string{"Mario 2"}) {
		t.Fatalf("games drafted: %v", names)
	}
	d := call[draftResponse](c, "GET", "/v1/sessions/"+c.session("s1"), nil, http.StatusOK)
	if d.Name != "Gandalf" || d.Emoji != "🧙" || *d.Task != fakeCiphertext("fix auth") || *d.Repo != fakeCiphertext("piazzatron/rostr") {
		t.Fatalf("imported draft: %+v", d)
	}
	link := call[linkResponse](c, "GET", "/v1/links/"+fakeRepoHash("piazzatron/rostr"), nil, http.StatusOK)
	if link.Roster != "work" {
		t.Fatalf("link: %+v", link)
	}

	// The imported drafts hold their members
	if d := draft(c, map[string]any{"session": c.session("s3"), "roster": "work"}); d.Name != "Frodo" {
		t.Fatalf("drafted %s, want Frodo", d.Name)
	}
}

func TestImportWithoutDatesKeepsListOrder(t *testing.T) {
	t.Parallel()
	c := &client{t: t, handler: testHandler, token: newToken()}
	settings := call[settingsResponse](c, "POST", "/v1/register", map[string]any{"authToken": c.token, "import": map[string]any{
		"rosters": []map[string]any{{"name": "Smash Bros", "aTeam": members("Mario")}, {"name": "Pokémon", "aTeam": members("Pikachu")}},
	}}, http.StatusCreated)
	if settings.DefaultRoster == nil || *settings.DefaultRoster != "Smash Bros" {
		t.Fatalf("default: %v, want the first roster listed", settings.DefaultRoster)
	}
}

func TestImportIsAtomic(t *testing.T) {
	t.Parallel()
	c := &client{t: t, handler: testHandler, token: newToken()}

	// The second draft takes the same member as the first, which fails at the database after everything else is in
	body := importBody(c.token)
	drafts := body["import"].(map[string]any)["drafts"].([]map[string]any)
	drafts[0]["session"], drafts[1]["session"] = c.session("s1"), c.session("s2")
	drafts[1]["roster"], drafts[1]["name"], drafts[1]["copy"] = "work", "Gandalf", 1
	callError(c, "POST", "/v1/register", body, http.StatusConflict, "conflict")

	// A draft for a roster that isn't in the import fails validation midway
	body = importBody(c.token)
	drafts = body["import"].(map[string]any)["drafts"].([]map[string]any)
	drafts[0]["session"], drafts[1]["session"] = c.session("s1"), c.session("s2")
	drafts[1]["roster"] = "nope"
	callError(c, "POST", "/v1/register", body, http.StatusBadRequest, "invalid_request")

	// Nothing was left behind, so the token isn't registered and the same import can be retried
	callError(c, "GET", "/v1/settings", nil, http.StatusUnauthorized, "unauthorized")
	if n := queryInt(t, `select count(*) from drafts where session_id = any($1)`, []string{c.session("s1"), c.session("s2")}); n != 0 {
		t.Fatalf("%d drafts left behind", n)
	}
	body = importBody(c.token)
	drafts = body["import"].(map[string]any)["drafts"].([]map[string]any)
	drafts[0]["session"], drafts[1]["session"] = c.session("s1"), c.session("s2")
	call[settingsResponse](c, "POST", "/v1/register", body, http.StatusCreated)
}

func TestDisbandCascadesAndPromotesOldestRoster(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "first", members("Gandalf"), nil)
	createRoster(c, "second", members("Mario"), nil)
	createRoster(c, "third", members("Pikachu"), nil)
	call[settingsResponse](c, "PATCH", "/v1/settings", map[string]any{"defaultRoster": "second"}, http.StatusOK)
	repoHash := fakeRepoHash("piazzatron/rostr")
	call[linkResponse](c, "PUT", "/v1/links/"+repoHash, map[string]any{"repo": fakeCiphertext("r"), "roster": "second"}, http.StatusOK)
	session := c.session("s1")
	draftSessionNamed(c, session)

	if r := call[rosterResponse](c, "GET", "/v1/rosters/second", nil, http.StatusOK); r.ActiveDrafts != 1 || r.LinkedProjects != 1 || !r.IsDefault {
		t.Fatalf("before disband: %+v", r)
	}

	type disbandResponse struct {
		Name             string  `json:"name"`
		BenchedDrafts    int     `json:"benchedDrafts"`
		UnlinkedProjects int     `json:"unlinkedProjects"`
		NewDefault       *string `json:"newDefault"`
		OwnerDeleted     bool    `json:"ownerDeleted"`
	}
	resp := call[disbandResponse](c, "DELETE", "/v1/rosters/SECOND", nil, http.StatusOK)
	if resp.Name != "second" || resp.BenchedDrafts != 1 || resp.UnlinkedProjects != 1 || resp.NewDefault == nil || *resp.NewDefault != "first" || resp.OwnerDeleted {
		t.Fatalf("disband: %+v", resp)
	}

	// Drafts and links went with it; the session's later calls find nothing
	if n := queryInt(t, `select count(*) from drafts where session_id = $1`, session); n != 0 {
		t.Fatalf("%d drafts left", n)
	}
	callError(c, "GET", "/v1/links/"+repoHash, nil, http.StatusNotFound, "link_not_found")
	callError(c, "POST", "/v1/agents/keepalive", map[string]any{"session": session}, http.StatusNotFound, "not_drafted")
	callError(c, "POST", "/v1/agents/bench", map[string]any{"session": session}, http.StatusNotFound, "not_drafted")

	// Disbanding a roster that isn't the default leaves the default alone
	if resp := call[disbandResponse](c, "DELETE", "/v1/rosters/third", nil, http.StatusOK); resp.NewDefault != nil || resp.OwnerDeleted {
		t.Fatalf("disband non-default: %+v", resp)
	}

	// Disbanding the last roster deletes the owner too: nothing of theirs is left, and the token stops working
	if resp := call[disbandResponse](c, "DELETE", "/v1/rosters/first", nil, http.StatusOK); resp.NewDefault != nil || !resp.OwnerDeleted {
		t.Fatalf("disband last: %+v", resp)
	}
	token, _ := decodeAuthToken(c.token)
	if n := queryInt(t, `select count(*) from owners where auth_token_hash = $1`, hashAuthToken(token)); n != 0 {
		t.Fatalf("%d owner rows left", n)
	}
	callError(c, "GET", "/v1/settings", nil, http.StatusUnauthorized, "unauthorized")
}

func TestReplaceCastKeepsNameLinksDefaultAndDraftEmoji(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", []member{{"Gandalf", "🧙"}, {"Frodo", "💍"}}, nil)
	repoHash := fakeRepoHash("piazzatron/rostr")
	call[linkResponse](c, "PUT", "/v1/links/"+repoHash, map[string]any{"repo": fakeCiphertext("r"), "roster": "work"}, http.StatusOK)
	session := c.session("s1")
	draftSessionNamed(c, session)

	r := call[rosterResponse](c, "PUT", "/v1/rosters/work/cast",
		map[string]any{"emojiMode": "off", "aTeam": []member{{"Mario", "🍄"}, {"Luigi", "🟢"}}}, http.StatusOK)
	if r.Name != "work" || !r.IsDefault || r.LinkedProjects != 1 || r.ActiveDrafts != 1 || r.EmojiMode != "off" {
		t.Fatalf("after replace: %+v", r)
	}
	if link := call[linkResponse](c, "GET", "/v1/links/"+repoHash, nil, http.StatusOK); link.Roster != "work" {
		t.Fatalf("link: %+v", link)
	}

	// The drafted agent keeps its name and stored emoji, with no team in the new cast
	l := list(c)
	drafted := l.Rosters[0].Drafted
	if len(drafted) != 1 || drafted[0].Name != "Gandalf" || drafted[0].Emoji != "🧙" || drafted[0].Team != nil {
		t.Fatalf("drafted after replace: %+v", drafted)
	}
	if idle := l.Rosters[0].Idle; len(idle) != 2 || idle[0].Name != "Mario" {
		t.Fatalf("idle after replace: %+v", idle)
	}
	if d := call[draftResponse](c, "GET", "/v1/sessions/"+session, nil, http.StatusOK); d.Emoji != "🧙" || d.Team != nil {
		t.Fatalf("session after replace: %+v", d)
	}

	// New sessions draft from the new cast; the old draft still releases
	if d := draft(c, map[string]any{"session": c.session("s2"), "repoHash": repoHash, "repo": fakeCiphertext("r")}); d.Name != "Mario" {
		t.Fatalf("drafted %s, want Mario", d.Name)
	}
	call[draftResponse](c, "POST", "/v1/agents/bench", map[string]any{"session": session}, http.StatusOK)
}

func TestEditRefusesToRenameOrRemoveDraftedMembers(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"), nil)
	session := c.session("s1")
	draftSessionNamed(c, session) // Gandalf

	callError(c, "PUT", "/v1/rosters/work", map[string]any{"aTeam": members("Frodo")}, http.StatusConflict, "member_drafted")
	callError(c, "PUT", "/v1/rosters/work", map[string]any{"aTeam": members("Gandalf the White", "Frodo")}, http.StatusConflict, "member_drafted")
	callError(c, "PUT", "/v1/rosters/work", map[string]any{"aTeam": members("gandalf", "Frodo")}, http.StatusConflict, "member_drafted")

	// Changing a drafted member's emoji or team, or anyone else, is fine
	r := call[rosterResponse](c, "PUT", "/v1/rosters/work",
		map[string]any{"emojiMode": "only", "aTeam": members("Sam"), "bTeam": []member{{"Gandalf", "🪄"}}}, http.StatusOK)
	if r.EmojiMode != "only" || len(r.ATeam) != 1 || r.BTeam[0].Emoji != "🪄" {
		t.Fatalf("edit: %+v", r)
	}

	// Once released, Gandalf can go
	call[draftResponse](c, "POST", "/v1/agents/bench", map[string]any{"name": "Gandalf"}, http.StatusOK)
	call[rosterResponse](c, "PUT", "/v1/rosters/work", map[string]any{"aTeam": members("Sam")}, http.StatusOK)
}

func TestPublicListShape(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", []member{{"Gandalf", "🧙"}, {"Frodo", "💍"}}, []member{{"Boromir", "🛡️"}})
	createRoster(c, "empty", members("Nobody"), nil)
	draft(c, map[string]any{"session": c.session("s1"), "task": fakeCiphertext("t"),
		"repoHash": fakeRepoHash("a/b"), "repo": fakeCiphertext("a/b")})

	rec := c.do("GET", "/v1/stats", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var raw struct {
		Rosters []struct {
			Name      string           `json:"name"`
			EmojiMode string           `json:"emojiMode"`
			ATeam     []member         `json:"aTeam"`
			BTeam     []member         `json:"bTeam"`
			IsDefault bool             `json:"isDefault"`
			CreatedAt time.Time        `json:"createdAt"`
			Drafted   []map[string]any `json:"drafted"`
			Idle      []map[string]any `json:"idle"`
		} `json:"rosters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Rosters) != 2 || raw.Rosters[0].Name != "work" || raw.Rosters[1].Name != "empty" {
		t.Fatalf("rosters: %s", rec.Body.String())
	}

	// Each roster has its cast and whether it's the default
	work, empty := raw.Rosters[0], raw.Rosters[1]
	if len(work.ATeam) != 2 || work.ATeam[1].Name != "Frodo" || len(work.BTeam) != 1 || work.BTeam[0].Emoji != "🛡️" ||
		!work.IsDefault || work.CreatedAt.IsZero() || empty.IsDefault || len(empty.ATeam) != 1 || empty.BTeam == nil {
		t.Fatalf("casts: %s", rec.Body.String())
	}

	drafted := raw.Rosters[0].Drafted
	if len(drafted) != 1 || !slices.Equal(sortedKeys(drafted[0]), []string{"draftedAt", "emoji", "name", "repo", "restingSince", "task", "team"}) {
		t.Fatalf("drafted: %v", drafted)
	}
	if drafted[0]["name"] != "Gandalf" || drafted[0]["emoji"] != "🧙" || drafted[0]["team"] != "a" || drafted[0]["restingSince"] != nil ||
		drafted[0]["task"] != fakeCiphertext("t") || drafted[0]["repo"] != fakeCiphertext("a/b") {
		t.Fatalf("drafted: %v", drafted[0])
	}
	if _, err := time.Parse(time.RFC3339, drafted[0]["draftedAt"].(string)); err != nil {
		t.Fatalf("draftedAt: %v", err)
	}

	idle := raw.Rosters[0].Idle
	if len(idle) != 2 || !slices.Equal(sortedKeys(idle[0]), []string{"emoji", "name", "team"}) ||
		idle[0]["name"] != "Frodo" || idle[1]["name"] != "Boromir" || idle[1]["team"] != "b" {
		t.Fatalf("idle: %v", idle)
	}

	// Empty lists are [], not null
	if !strings.Contains(rec.Body.String(), `"drafted":[]`) {
		t.Fatalf("empty roster: %s", rec.Body.String())
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestPublicListCORS(t *testing.T) {
	t.Parallel()
	c := newOwner(t)

	// Preflight
	req := httptest.NewRequest("OPTIONS", "/v1/stats", nil)
	req.Header.Set("Origin", "https://dashboard.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	rec := httptest.NewRecorder()
	testHandler.ServeHTTP(rec, req)
	h := rec.Header()
	if rec.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(h.Get("Access-Control-Allow-Headers"), "Authorization") ||
		!strings.Contains(h.Get("Access-Control-Allow-Methods"), "GET") {
		t.Fatalf("preflight: %d %v", rec.Code, h)
	}

	// The response itself, and its errors, so browsers can read them
	if rec := c.do("GET", "/v1/stats", nil); rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("GET /v1/stats has no CORS header")
	}
	anon := &client{t: t, handler: testHandler}
	if rec := anon.do("GET", "/v1/stats", nil); rec.Code != http.StatusUnauthorized || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("401 has no CORS header: %v", rec.Header())
	}

	// Internal endpoints don't allow browsers
	if rec := c.do("GET", "/v1/settings", nil); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("internal endpoint has a CORS header")
	}
}

func TestRateLimits(t *testing.T) {
	t.Parallel()
	limited := NewHandler(testDB, Config{RegistrationsPerHour: 2, RequestsPerMinute: 3})

	register := func(ip string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"authToken": newToken()})
		req := httptest.NewRequest("POST", "/v1/register", strings.NewReader(string(body)))
		req.Header.Set("X-Real-IP", ip)
		rec := httptest.NewRecorder()
		limited.ServeHTTP(rec, req)
		return rec
	}

	// Registration, per IP
	register("198.51.100.1")
	register("198.51.100.1")
	rec := register("198.51.100.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), `"rate_limited"`) {
		t.Fatalf("third registration: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if rec := register("198.51.100.2"); rec.Code != http.StatusCreated {
		t.Fatalf("other IP: %d", rec.Code)
	}

	// Requests, per auth token
	a := &client{t: t, handler: limited, token: newToken()}
	call[settingsResponse](&client{t: t, handler: testHandler, token: a.token}, "POST", "/v1/register",
		map[string]any{"authToken": a.token}, http.StatusCreated)
	for range 3 {
		call[settingsResponse](a, "GET", "/v1/settings", nil, http.StatusOK)
	}
	callError(a, "GET", "/v1/settings", nil, http.StatusTooManyRequests, "rate_limited")
	b := &client{t: t, handler: limited, token: newToken()}
	callError(b, "GET", "/v1/settings", nil, http.StatusUnauthorized, "unauthorized")
}

func TestRateLimiterResetsEachWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	l := newRateLimiter(1, time.Minute)
	l.now = func() time.Time { return now }

	if ok, _ := l.allow("k"); !ok {
		t.Fatal("first call refused")
	}
	if ok, retry := l.allow("k"); ok || retry != time.Minute {
		t.Fatalf("second call: ok %v, retry %v", ok, retry)
	}
	now = now.Add(time.Minute)
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("call in the next window refused")
	}
}

func TestErrorsAndStatusCodes(t *testing.T) {
	t.Parallel()
	c := newOwner(t)

	// Bad bodies
	callError(c, "POST", "/v1/rosters", "{not json", http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/rosters", map[string]any{"name": "x", "aTeam": members("A"), "extra": 1}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/rosters", `{"name":"`+strings.Repeat("x", maxBodyBytes)+`"}`, http.StatusRequestEntityTooLarge, "too_large")
	callError(c, "POST", "/v1/rosters", map[string]any{"name": "x"}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/rosters", map[string]any{"name": " ", "aTeam": members("A")}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/rosters", map[string]any{"name": "x", "emojiMode": "loud", "aTeam": members("A")}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/rosters", map[string]any{"name": "x", "aTeam": []member{{"A", ""}}}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/rosters", map[string]any{"name": "x", "aTeam": members("Éowyn"), "bTeam": members("eowyn")}, http.StatusBadRequest, "invalid_request")

	// No rosters yet
	callError(c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("s")}, http.StatusNotFound, "no_rosters")

	// Roster names are unique ignoring case and accents, and found the same way
	createRoster(c, "Pokémon", members("Pikachu"), nil)
	callError(c, "POST", "/v1/rosters", map[string]any{"name": "POKEMON", "aTeam": members("A")}, http.StatusConflict, "roster_exists")
	if r := call[rosterResponse](c, "GET", "/v1/rosters/pokemon", nil, http.StatusOK); r.Name != "Pokémon" {
		t.Fatalf("loose lookup: %+v", r)
	}
	callError(c, "GET", "/v1/rosters/nope", nil, http.StatusNotFound, "roster_not_found")
	callError(c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("s"), "roster": "nope"}, http.StatusNotFound, "roster_not_found")

	// Ciphertext, fingerprints, and sessions
	callError(c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("s"), "task": "plaintext!"}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("s"), "task": "c2hvcnQ="}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("s"), "repoHash": fakeRepoHash("a/b")}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("s"), "repoHash": "ABC", "repo": fakeCiphertext("a/b")}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/agents/draft", map[string]any{"session": ""}, http.StatusBadRequest, "invalid_request")
	callError(c, "GET", "/v1/links/not-a-hash", nil, http.StatusBadRequest, "invalid_request")

	// Bench and task targets
	callError(c, "POST", "/v1/agents/bench", map[string]any{}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/agents/bench", map[string]any{"session": "a", "name": "b"}, http.StatusBadRequest, "invalid_request")
	callError(c, "POST", "/v1/agents/task", map[string]any{"session": c.session("s")}, http.StatusBadRequest, "invalid_request")

	// Settings
	callError(c, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "0h"}, http.StatusBadRequest, "invalid_request")
	callError(c, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "7 days"}, http.StatusBadRequest, "invalid_request")
	callError(c, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "53w"}, http.StatusBadRequest, "invalid_request")

	// Unknown routes and methods
	callError(c, "GET", "/v1/nope", nil, http.StatusNotFound, "not_found")
	callError(c, "DELETE", "/v1/settings", nil, http.StatusNotFound, "not_found")

	// Health needs no auth
	anon := &client{t: t, handler: testHandler}
	if got := call[map[string]string](anon, "GET", "/health", nil, http.StatusOK); got["status"] != "ok" {
		t.Fatalf("health: %v", got)
	}
}

func TestReleaseAndTaskByName(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Éowyn", "Gandalf"), nil)
	createRoster(c, "home", members("Gandalf"), nil)
	draftSessionNamed(c, c.session("s1")) // Éowyn
	draftSessionNamed(c, c.session("s2")) // Gandalf
	draftSessionNamed(c, c.session("s3")) // Éowyn 2
	draft(c, map[string]any{"session": c.session("s4"), "roster": "home"})

	// Names match ignoring case and accents, with copy numbers
	if d := call[draftResponse](c, "POST", "/v1/agents/task", map[string]any{"name": "EOWYN 2", "task": fakeCiphertext("t")}, http.StatusOK); d.Copy != 2 || *d.Task != fakeCiphertext("t") {
		t.Fatalf("task by name: %+v", d)
	}
	if d := call[draftResponse](c, "POST", "/v1/agents/bench", map[string]any{"name": "eowyn"}, http.StatusOK); d.Copy != 1 || d.Session != c.session("s1") {
		t.Fatalf("release by name: %+v", d)
	}

	// A name drafted in two rosters needs a roster
	resp := callError(c, "POST", "/v1/agents/bench", map[string]any{"name": "gandalf"}, http.StatusConflict, "ambiguous_name")
	if !slices.Equal(resp.Error.Rosters, []string{"work", "home"}) {
		t.Fatalf("ambiguous rosters: %v", resp.Error.Rosters)
	}
	if d := call[draftResponse](c, "POST", "/v1/agents/bench", map[string]any{"name": "gandalf", "roster": "home"}, http.StatusOK); d.Roster != "home" {
		t.Fatalf("release with roster: %+v", d)
	}
	callError(c, "POST", "/v1/agents/bench", map[string]any{"name": "gandalf", "roster": "home"}, http.StatusNotFound, "not_drafted")
	callError(c, "POST", "/v1/agents/bench", map[string]any{"name": "gandalf", "roster": "nope"}, http.StatusNotFound, "roster_not_found")
}

func TestTaskBySessionBumpsLastSeen(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf"), nil)
	session := c.session("s1")
	draftSessionNamed(c, session)
	execSQL(t, `update drafts set last_seen_at = now() - interval '1 day' where session_id = $1`, session)

	d := call[draftResponse](c, "POST", "/v1/agents/task", map[string]any{"session": session, "task": fakeCiphertext("new")}, http.StatusOK)
	if *d.Task != fakeCiphertext("new") {
		t.Fatalf("task: %v", d.Task)
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1 and last_seen_at > now() - interval '1 minute'`, session); n != 1 {
		t.Fatal("task didn't bump last_seen_at")
	}
}

func TestLinksPickTheDraftRoster(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf"), nil)
	createRoster(c, "games", members("Mario"), nil)
	repoHash := fakeRepoHash("piazzatron/rostr")
	repo := fakeCiphertext("piazzatron/rostr")

	link := call[linkResponse](c, "PUT", "/v1/links/"+repoHash, map[string]any{"repo": repo, "roster": "GAMES"}, http.StatusOK)
	if link.Roster != "games" || link.Repo != repo || link.RepoHash != repoHash {
		t.Fatalf("link: %+v", link)
	}
	if links := call[map[string][]linkResponse](c, "GET", "/v1/links", nil, http.StatusOK); len(links["links"]) != 1 {
		t.Fatalf("links: %v", links)
	}

	// Linked projects draft from their roster; others from the default
	if d := draft(c, map[string]any{"session": c.session("s1"), "repoHash": repoHash, "repo": repo}); d.Roster != "games" || *d.Repo != repo {
		t.Fatalf("linked draft: %+v", d)
	}
	other := fakeRepoHash("other/repo")
	if d := draft(c, map[string]any{"session": c.session("s2"), "repoHash": other, "repo": fakeCiphertext("other/repo")}); d.Roster != "work" {
		t.Fatalf("unlinked draft: %+v", d)
	}
	// An explicit roster beats the link
	if d := draft(c, map[string]any{"session": c.session("s3"), "roster": "work", "repoHash": repoHash, "repo": repo}); d.Roster != "work" {
		t.Fatalf("roster flag: %+v", d)
	}

	// Relinking replaces; unlinking falls back to the default
	call[linkResponse](c, "PUT", "/v1/links/"+repoHash, map[string]any{"repo": repo, "roster": "work"}, http.StatusOK)
	if link := call[linkResponse](c, "GET", "/v1/links/"+repoHash, nil, http.StatusOK); link.Roster != "work" {
		t.Fatalf("relinked: %+v", link)
	}
	if rec := c.do("DELETE", "/v1/links/"+repoHash, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d", rec.Code)
	}
	callError(c, "DELETE", "/v1/links/"+repoHash, nil, http.StatusNotFound, "link_not_found")
	callError(c, "GET", "/v1/links/"+repoHash, nil, http.StatusNotFound, "link_not_found")
}

func TestSettings(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf"), nil)
	createRoster(c, "games", members("Mario"), nil)

	s := call[settingsResponse](c, "PATCH", "/v1/settings", map[string]any{"defaultRoster": "Games", "draftExpiry": "48h", "autoDraft": false}, http.StatusOK)
	if *s.DefaultRoster != "games" || s.DraftExpiry != "2d" || s.AutoDraft {
		t.Fatalf("patched: %+v", s)
	}

	// Fields left out stay as they were
	s = call[settingsResponse](c, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "2w"}, http.StatusOK)
	if *s.DefaultRoster != "games" || s.DraftExpiry != "14d" || s.AutoDraft {
		t.Fatalf("second patch: %+v", s)
	}
	s = call[settingsResponse](c, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "36h"}, http.StatusOK)
	if s.DraftExpiry != "36h" {
		t.Fatalf("hours: %+v", s)
	}
	if d := draftSessionNamed(c, c.session("s1")); d.Roster != "games" {
		t.Fatalf("default roster draft: %+v", d)
	}
}
