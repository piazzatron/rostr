package api

import (
	"net/http"
	"testing"
	"time"
)

func turn(c *client, session string, ended bool, at time.Time) {
	c.t.Helper()
	if rec := c.do("POST", "/v1/agents/turn", map[string]any{"session": session, "ended": ended, "at": at}); rec.Code != http.StatusNoContent {
		c.t.Fatalf("turn: %d %s", rec.Code, rec.Body)
	}
}

func restingSinceOf(c *client, name string) *time.Time {
	c.t.Helper()
	for _, r := range list(c).Rosters {
		for _, d := range r.Drafted {
			if d.Name == name {
				return d.RestingSince
			}
		}
	}
	c.t.Fatalf("%s isn't drafted", name)
	return nil
}

func TestTurnsMarkADraftResting(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf"))
	s := c.session("s")
	draftSessionNamed(c, s)
	now := time.Now()

	if restingSinceOf(c, "Gandalf") != nil {
		t.Fatal("a new draft is resting")
	}

	// Resting 15 minutes after its turn ended, not before
	turn(c, s, true, now.Add(-5*time.Minute))
	if restingSinceOf(c, "Gandalf") != nil {
		t.Fatal("resting 5 minutes after its turn ended")
	}
	turn(c, s, true, now.Add(-20*time.Minute)) // a late report of an earlier turn end doesn't move it back
	if restingSinceOf(c, "Gandalf") != nil {
		t.Fatal("an older turn end replaced a newer one")
	}
	execSQL(t, `update drafts set turn_ended_at = now() - interval '20 minutes' where session_id = $1`, s)
	if since := restingSinceOf(c, "Gandalf"); since == nil || time.Since(*since) < 19*time.Minute {
		t.Fatalf("resting since %v, want 20 minutes ago", since)
	}

	// A turn that started before the end doesn't wake it; one that started after does
	turn(c, s, false, now.Add(-30*time.Minute))
	if restingSinceOf(c, "Gandalf") == nil {
		t.Fatal("an earlier turn start woke it")
	}
	turn(c, s, false, now)
	if restingSinceOf(c, "Gandalf") != nil {
		t.Fatal("still resting after a new turn started")
	}

	callError(c, "POST", "/v1/agents/turn", map[string]any{"session": c.session("other"), "ended": true, "at": now},
		http.StatusNotFound, "not_drafted")
}

func TestDraftNeverTakesARestingName(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"))
	gandalf, frodo := c.session("gandalf"), c.session("frodo")
	draftSessionNamed(c, gandalf)
	draftSessionNamed(c, frodo)
	execSQL(t, `update drafts set turn_ended_at = now() - interval '2 hours' where session_id in ($1, $2)`, gandalf, frodo)

	if d := draftSessionNamed(c, c.session("a")); displayOf(d) != "Gandalf 2" {
		t.Fatalf("drafted %s, want Gandalf 2 (resting sessions keep their names)", displayOf(d))
	}
	for _, session := range []string{gandalf, frodo} {
		if n := queryInt(t, `select count(*) from drafts where session_id = $1 and benched_at is null`, session); n != 1 {
			t.Fatal("a resting session lost its name")
		}
		turn(c, session, false, time.Now())
	}
}

func TestDraftPrefersAFreeNameToAResting(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"))
	gandalf := c.session("gandalf")
	draftSessionNamed(c, gandalf)
	execSQL(t, `update drafts set turn_ended_at = now() - interval '1 hour' where session_id = $1`, gandalf)

	if d := draftSessionNamed(c, c.session("new")); displayOf(d) != "Frodo" {
		t.Fatalf("drafted %s, want Frodo (free)", displayOf(d))
	}
	if n := queryInt(t, `select count(*) from drafts where session_id = $1 and benched_at is null`, gandalf); n != 1 {
		t.Fatal("the resting draft was released while a name was free")
	}
}

// A session drafted when it starts, before any prompt, rests like one whose turn ended at the draft, and its first turn
// makes it working.
func TestIdleDraftsRestUntilTheirFirstTurn(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"))
	opened, prompted := c.session("opened"), c.session("prompted")
	draft(c, map[string]any{"session": opened, "idle": true})   // Gandalf
	draft(c, map[string]any{"session": prompted, "idle": true}) // Frodo
	if restingSinceOf(c, "Gandalf") != nil {
		t.Fatal("an idle draft rests right away")
	}
	execSQL(t, `update drafts set turn_ended_at = turn_ended_at - interval '20 minutes' where session_id = any($1)`,
		[]string{opened, prompted})
	turn(c, prompted, false, time.Now())

	if restingSinceOf(c, "Gandalf") == nil || restingSinceOf(c, "Frodo") != nil {
		t.Fatal("want the never-prompted session resting and the prompted one working")
	}
}
