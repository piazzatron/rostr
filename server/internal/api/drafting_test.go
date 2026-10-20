package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestDraftPicksATeamThenBTeamThenCopies(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "Fellowship", members("Gandalf", "Frodo"), members("Boromir"))

	want := []struct {
		display string
		team    string
	}{
		{"Gandalf", "a"}, {"Frodo", "a"}, {"Boromir", "b"}, {"Gandalf 2", "a"}, {"Frodo 2", "a"}, {"Boromir 2", "b"},
		{"Gandalf 3", "a"},
	}
	for i, w := range want {
		d := draftSessionNamed(c, c.session(fmt.Sprint(i)))
		if displayOf(d) != w.display || d.Team == nil || *d.Team != w.team || d.Roster != "Fellowship" {
			t.Fatalf("draft %d: got %s (team %v, roster %s), want %s (team %s)", i, displayOf(d), d.Team, d.Roster, w.display, w.team)
		}
	}

	// A freed copy-1 slot is drafted before any further copies
	call[draftResponse](c, "POST", "/v1/agents/release", map[string]any{"name": "frodo"}, http.StatusOK)
	if d := draftSessionNamed(c, c.session("next")); displayOf(d) != "Frodo" {
		t.Fatalf("after releasing Frodo, drafted %s", displayOf(d))
	}
}

func TestDraftSameSessionReturnsSameMemberAndBumps(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"), nil)
	session := c.session("s1")

	first := draftSessionNamed(c, session)
	execSQL(t, `update drafts set last_seen_at = now() - interval '3 hours' where session_id = $1`, session)

	again := draft(c, map[string]any{"session": session, "task": fakeCiphertext("new task")})
	if displayOf(again) != displayOf(first) {
		t.Fatalf("re-draft gave %s, want %s", displayOf(again), displayOf(first))
	}
	if again.Task == nil || *again.Task != fakeCiphertext("new task") {
		t.Fatalf("re-draft didn't set the task: %v", again.Task)
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1`, session); n != 1 {
		t.Fatalf("%d draft rows, want 1", n)
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1 and last_seen_at > now() - interval '1 minute'`, session); n != 1 {
		t.Fatal("re-draft didn't bump last_seen_at")
	}
}

func TestDraftFindsSessionAcrossRosters(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf"), nil)
	createRoster(c, "Smash Bros", members("Mario"), nil)
	session := c.session("s1")

	if d := draft(c, map[string]any{"session": session, "roster": "smash bros"}); d.Name != "Mario" {
		t.Fatalf("drafted %s from %s, want Mario", d.Name, d.Roster)
	}

	// Without a roster the default is "work", but the session's draft is found first
	if d := draftSessionNamed(c, session); d.Name != "Mario" || d.Roster != "Smash Bros" {
		t.Fatalf("re-draft gave %s from %s, want Mario from Smash Bros", d.Name, d.Roster)
	}
	if d := call[draftResponse](c, "GET", "/v1/sessions/"+session, nil, http.StatusOK); d.Name != "Mario" {
		t.Fatalf("session lookup gave %s", d.Name)
	}
	callError(c, "GET", "/v1/sessions/"+c.session("unknown"), nil, http.StatusNotFound, "not_drafted")
}

func TestDraftResumesPreviousMemberWhenFree(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo", "Sam"), nil)
	s1, s2, s3 := c.session("s1"), c.session("s2"), c.session("s3")

	draftSessionNamed(c, s1)
	draft(c, map[string]any{"session": s2, "task": fakeCiphertext("fix auth")})
	call[draftResponse](c, "POST", "/v1/agents/release", map[string]any{"session": s1}, http.StatusOK)
	call[draftResponse](c, "POST", "/v1/agents/release", map[string]any{"session": s2}, http.StatusOK)

	// Gandalf is free and first in order, but s2 gets Frodo back, with its task
	resumed := draftSessionNamed(c, s2)
	if resumed.Name != "Frodo" || resumed.Task == nil || *resumed.Task != fakeCiphertext("fix auth") {
		t.Fatalf("resume gave %s with task %v, want Frodo with the old task", resumed.Name, resumed.Task)
	}

	// s1's Gandalf gets taken by s3, so s1 can't resume and gets the next free member
	if d := draftSessionNamed(c, s3); d.Name != "Gandalf" {
		t.Fatalf("s3 drafted %s, want Gandalf", d.Name)
	}
	if d := draftSessionNamed(c, s1); d.Name != "Sam" {
		t.Fatalf("s1 drafted %s, want Sam", d.Name)
	}
}

func TestDraftResumesFromTheSessionsOriginalRoster(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf"), nil)
	createRoster(c, "games", members("Mario"), nil)
	session := c.session("s1")

	draft(c, map[string]any{"session": session, "roster": "games"})
	call[draftResponse](c, "POST", "/v1/agents/release", map[string]any{"session": session}, http.StatusOK)

	if d := draftSessionNamed(c, session); d.Name != "Mario" || d.Roster != "games" {
		t.Fatalf("resume gave %s from %s, want Mario from games", d.Name, d.Roster)
	}
}

func TestDraftExpiresStaleDraftsUsingOwnersExpiry(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo", "Sam"), nil)
	call[settingsResponse](c, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "2h"}, http.StatusOK)
	stale, recent := c.session("stale"), c.session("recent")

	draftSessionNamed(c, stale)  // Gandalf
	draftSessionNamed(c, recent) // Frodo
	execSQL(t, `update drafts set last_seen_at = now() - interval '3 hours' where session_id = $1`, stale)
	execSQL(t, `update drafts set last_seen_at = now() - interval '1 hour' where session_id = $1`, recent)

	// Reads leave out the expired draft before any write releases it
	if names := draftedNames(list(c), "work"); len(names) != 1 || names[0] != "Frodo" {
		t.Fatalf("list shows %v drafted, want [Frodo]", names)
	}

	// The next draft releases the stale one and takes its member
	if d := draftSessionNamed(c, c.session("new")); d.Name != "Gandalf" {
		t.Fatalf("drafted %s, want Gandalf (freed by expiry)", d.Name)
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1 and released_at is not null`, stale); n != 1 {
		t.Fatal("stale draft wasn't released")
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1 and released_at is null`, recent); n != 1 {
		t.Fatal("recent draft was released")
	}
}

func TestDraftPrunesReleasedDraftsAfterSevenDays(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"), nil)
	old, recent := c.session("old"), c.session("recent")

	draftSessionNamed(c, old)
	draftSessionNamed(c, recent)
	call[draftResponse](c, "POST", "/v1/agents/release", map[string]any{"session": old}, http.StatusOK)
	call[draftResponse](c, "POST", "/v1/agents/release", map[string]any{"session": recent}, http.StatusOK)
	execSQL(t, `update drafts set released_at = now() - interval '8 days' where session_id = $1`, old)
	execSQL(t, `update drafts set released_at = now() - interval '6 days' where session_id = $1`, recent)

	draftSessionNamed(c, c.session("trigger"))

	if n := queryInt(t, `select count(*) from drafts where session_id = $1`, old); n != 0 {
		t.Fatal("draft released 8 days ago wasn't deleted")
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1`, recent); n != 1 {
		t.Fatal("draft released 6 days ago was deleted")
	}
}

func TestKeepAliveNeverDraftsADifferentMember(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"), nil)
	call[settingsResponse](c, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "1h"}, http.StatusOK)
	s1, s2 := c.session("s1"), c.session("s2")

	// A session that was never drafted stays unnamed
	callError(c, "POST", "/v1/agents/keepalive", map[string]any{"session": s1}, http.StatusNotFound, "not_drafted")

	// An active draft is bumped
	draftSessionNamed(c, s1)
	execSQL(t, `update drafts set last_seen_at = now() - interval '30 minutes' where session_id = $1`, s1)
	if d := call[draftResponse](c, "POST", "/v1/agents/keepalive", map[string]any{"session": s1}, http.StatusOK); d.Name != "Gandalf" {
		t.Fatalf("keep-alive gave %s", d.Name)
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1 and last_seen_at > now() - interval '1 minute'`, s1); n != 1 {
		t.Fatal("keep-alive didn't bump")
	}

	// An expired draft whose member is free is reclaimed
	execSQL(t, `update drafts set last_seen_at = now() - interval '2 hours' where session_id = $1`, s1)
	if d := call[draftResponse](c, "POST", "/v1/agents/keepalive", map[string]any{"session": s1}, http.StatusOK); d.Name != "Gandalf" {
		t.Fatalf("keep-alive after expiry gave %s, want Gandalf back", d.Name)
	}

	// An expired draft whose member was taken leaves the session unnamed
	execSQL(t, `update drafts set last_seen_at = now() - interval '2 hours' where session_id = $1`, s1)
	if d := draftSessionNamed(c, s2); d.Name != "Gandalf" {
		t.Fatalf("s2 drafted %s, want Gandalf", d.Name)
	}
	callError(c, "POST", "/v1/agents/keepalive", map[string]any{"session": s1}, http.StatusNotFound, "not_drafted")
}

func TestConcurrentDraftsNeverDraftAMemberTwice(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo", "Sam"), members("Boromir", "Merry"))

	const sessions = 40
	results := make([]draftResponse, sessions)
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Go(func() {
			rec := c.do("POST", "/v1/agents/draft", map[string]any{"session": c.session(fmt.Sprint(i))})
			if rec.Code != http.StatusOK {
				t.Errorf("draft %d: status %d: %s", i, rec.Code, rec.Body.String())
				return
			}
			results[i] = decodeDraft(t, rec.Body.Bytes())
		})
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, d := range results {
		if seen[displayOf(d)] {
			t.Fatalf("%s was drafted twice", displayOf(d))
		}
		seen[displayOf(d)] = true
	}
	// 40 drafts over 5 members is exactly 8 copies of each
	if !seen["Merry 8"] || seen["Gandalf 9"] {
		t.Fatalf("copies weren't filled in order: %v", seen)
	}
}

func TestConcurrentDraftsForOneSessionGetOneMember(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo", "Sam"), nil)
	session := c.session("s1")

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if rec := c.do("POST", "/v1/agents/draft", map[string]any{"session": session}); rec.Code != http.StatusOK {
				t.Errorf("status %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
	wg.Wait()

	if n := queryInt(t, `select count(*) from drafts where session_id = $1`, session); n != 1 {
		t.Fatalf("%d drafts for one session, want 1", n)
	}
}

func TestDatabaseAllowsOneActiveDraftPerSessionAcrossAnOwnersRosters(t *testing.T) {
	t.Parallel()
	a, b := newOwner(t), newOwner(t)
	createRoster(a, "work", members("Gandalf"), nil)
	createRoster(a, "games", members("Mario"), nil)
	createRoster(b, "work", members("Frodo"), nil)
	session := a.session("s1")
	draft(a, map[string]any{"session": session, "roster": "work"})

	// A second active row for the session in the owner's other roster is refused by the index, not just the API
	insertIntoGames := func() error {
		token, _ := decodeAuthToken(a.token)
		_, err := testDB.Exec(context.Background(), `
			insert into drafts (owner_id, roster_id, member_name, member_emoji, session_id)
			select r.owner_id, r.id, 'Mario', '🍄', $2 from rosters r join owners o on o.id = r.owner_id
			where o.auth_token_hash = $1 and r.name = 'games'`, hashAuthToken(token), session)
		return err
	}
	var pgErr *pgconn.PgError
	if err := insertIntoGames(); !errors.As(err, &pgErr) || pgErr.ConstraintName != "drafts_active_session" {
		t.Fatalf("second active draft for the session: %v, want a drafts_active_session violation", err)
	}

	// Another owner can use the same session ID
	if d := draftSessionNamed(b, session); d.Name != "Frodo" {
		t.Fatalf("B drafted %s", d.Name)
	}

	// Once the session's draft is released, it can hold a member in another roster
	call[draftResponse](a, "POST", "/v1/agents/release", map[string]any{"session": session}, http.StatusOK)
	if err := insertIntoGames(); err != nil {
		t.Fatalf("draft after release: %v", err)
	}
}

func TestImportRefusesTwoActiveDraftsForOneSession(t *testing.T) {
	t.Parallel()
	c := &client{t: t, handler: testHandler, token: newToken()}
	body := importBody(c.token)
	drafts := body["import"].(map[string]any)["drafts"].([]map[string]any)
	drafts[0]["session"], drafts[1]["session"] = c.session("s1"), c.session("s1")
	callError(c, "POST", "/v1/register", body, http.StatusConflict, "conflict")
	callError(c, "GET", "/v1/settings", nil, http.StatusUnauthorized, "unauthorized")
}
