package api

import (
	"context"
	"net/http"
	"testing"
)

// The stats cover the whole database, so the count tests aren't parallel: they compare before and after with no other
// test in this package writing in between.

func TestStatsCountEveryOwner(t *testing.T) {
	before := stats(t)
	a, b := newOwner(t), newOwner(t)
	createRoster(a, "work", members("Gandalf", "Frodo", "Sam"), members("Boromir"))
	createRoster(a, "games", members("Mario"), nil)
	createRoster(b, "work", members("Pikachu", "Eevee"), nil)

	// Gandalf, Mario 2, and Pikachu are working. Mario is benched: copy 1 was released, and Mario 2 doesn't hold the name.
	draftSessionNamed(a, a.session("s1"))
	draft(a, map[string]any{"session": a.session("s2"), "roster": "games"})
	draft(a, map[string]any{"session": a.session("s3"), "roster": "games"})
	call[draftResponse](a, "POST", "/v1/agents/release", map[string]any{"name": "Mario", "roster": "games"}, http.StatusOK)
	draftSessionNamed(b, b.session("s1"))
	draftSessionNamed(b, b.session("s2"))
	call[draftResponse](b, "POST", "/v1/agents/release", map[string]any{"session": b.session("s2")}, http.StatusOK)

	after := stats(t)
	if working, benched := after.Working-before.Working, after.Benched-before.Benched; working != 3 || benched != 5 {
		t.Fatalf("working +%d, benched +%d; want +3, +5", working, benched)
	}
}

func TestStatsLeaveOutExpiredDrafts(t *testing.T) {
	before := stats(t)
	a, b := newOwner(t), newOwner(t)
	createRoster(a, "work", members("Gandalf", "Frodo"), nil)
	createRoster(b, "work", members("Mario"), nil)
	call[settingsResponse](a, "PATCH", "/v1/settings", map[string]any{"draftExpiry": "1h"}, http.StatusOK)
	stale, recent, other := a.session("s1"), a.session("s2"), b.session("s1")
	draftSessionNamed(a, stale)
	draftSessionNamed(a, recent)
	draftSessionNamed(b, other)

	// Each owner's own expiry applies: A's 1h drops Gandalf, B's 7d keeps Mario
	execSQL(t, `update drafts set last_seen_at = now() - interval '2 hours' where session_id = any($1)`, []string{stale, other})

	after := stats(t)
	if working, benched := after.Working-before.Working, after.Benched-before.Benched; working != 2 || benched != 1 {
		t.Fatalf("working +%d, benched +%d; want +2, +1", working, benched)
	}
}

// stats counts the whole database, as a live push would.
func stats(t *testing.T) statsResponse {
	t.Helper()
	stats, err := queryStats(context.Background(), testDB)
	if err != nil {
		t.Fatal(err)
	}
	return stats
}
