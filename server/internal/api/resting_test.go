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
	createRoster(c, "work", members("Gandalf"), nil)
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

func TestDraftTakesTheLongestRestingNameBeforeCopies(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo", "Samwise"), nil)
	gandalf, frodo, samwise := c.session("gandalf"), c.session("frodo"), c.session("samwise")
	draftSessionNamed(c, gandalf)
	draftSessionNamed(c, frodo)
	draftSessionNamed(c, samwise)
	execSQL(t, `update drafts set turn_ended_at = now() - interval '20 minutes' where session_id = $1`, gandalf)
	execSQL(t, `update drafts set turn_ended_at = now() - interval '40 minutes' where session_id = $1`, frodo)
	// Samwise's turn ended long ago, but a new one has started since: working, not resting
	execSQL(t, `update drafts set turn_ended_at = now() - interval '50 minutes', turn_started_at = now() - interval '1 minute'
		where session_id = $1`, samwise)

	if d := draftSessionNamed(c, c.session("a")); displayOf(d) != "Frodo" {
		t.Fatalf("drafted %s, want Frodo (resting longest)", displayOf(d))
	}
	if d := draftSessionNamed(c, c.session("b")); displayOf(d) != "Gandalf" {
		t.Fatalf("drafted %s, want Gandalf (resting next)", displayOf(d))
	}
	// Nobody else is resting, so copies, drafted least recently first
	if d := draftSessionNamed(c, c.session("c")); displayOf(d) != "Samwise 2" {
		t.Fatalf("drafted %s, want Samwise 2", displayOf(d))
	}

	// Frodo's session lost the name; coming back, it doesn't get it, and its keep-alive says it isn't drafted
	callError(c, "POST", "/v1/agents/turn", map[string]any{"session": frodo, "ended": false, "at": time.Now()},
		http.StatusNotFound, "not_drafted")
	callError(c, "POST", "/v1/agents/keepalive", map[string]any{"session": frodo}, http.StatusNotFound, "not_drafted")
	if d := draftSessionNamed(c, frodo); displayOf(d) != "Frodo 2" {
		t.Fatalf("Frodo's old session drafted %s, want Frodo 2", displayOf(d))
	}
}

func TestDraftPrefersAFreeNameToAResting(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"), nil)
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
