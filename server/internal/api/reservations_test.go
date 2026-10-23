package api

import (
	"net/http"
	"testing"
)

func TestMachinesReserveTheirNextPickSoTheyCantCollide(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo", "Sam"), nil)
	a, b := c.onMachine(c.session("mac")), c.onMachine(c.session("laptop"))

	// Each machine's next state reserves a member for it, which the other machine no longer sees as idle
	nextA := call[nextResponse](a, "GET", "/v1/next", nil, http.StatusOK)
	if r := nextA.Rosters[0]; r.Reserved == nil || *r.Reserved != "Gandalf" || len(r.Idle) != 3 {
		t.Fatalf("a's next: %+v", r)
	}
	nextB := call[nextResponse](b, "GET", "/v1/next", nil, http.StatusOK)
	if r := nextB.Rosters[0]; r.Reserved == nil || *r.Reserved != "Frodo" || len(r.Idle) != 2 || r.Idle[0].Name != "Frodo" {
		t.Fatalf("b's next: %+v", r)
	}

	// A draft from a machine takes its reservation; a confirmation of the announced member does too, and the next
	// reservation skips what the other machine holds
	if d := call[draftWithNext](a, "POST", "/v1/agents/draft", map[string]any{"session": c.session("a1")}, http.StatusOK); d.Name != "Gandalf" ||
		d.Next.Rosters[0].Reserved == nil || *d.Next.Rosters[0].Reserved != "Sam" {
		t.Fatalf("a's draft: %s, then reserved %v", d.Name, d.Next.Rosters[0].Reserved)
	}
	if d := call[draftWithNext](b, "POST", "/v1/agents/draft", map[string]any{"session": c.session("b1"), "preferred": "Frodo"}, http.StatusOK); d.Name != "Frodo" ||
		d.Next.Rosters[0].Reserved != nil {
		t.Fatalf("b's draft: %s, then reserved %v", d.Name, d.Next.Rosters[0].Reserved)
	}

	// A machine whose cache went stale announces someone another machine holds: it gets their next copy, as before
	if d := draft(b, map[string]any{"session": c.session("b2"), "preferred": "Sam"}); displayOf(d) != "Sam 2" {
		t.Fatalf("stale announce: %s", displayOf(d))
	}
	// Callers without a machine skip reserved members too, and reserve nothing
	if d := call[draftWithNext](c, "POST", "/v1/agents/draft", map[string]any{"session": c.session("c1")}, http.StatusOK); displayOf(d.draftResponse) != "Gandalf 2" ||
		d.Next.Rosters[0].Reserved != nil {
		t.Fatalf("no machine: %s, reserved %v", displayOf(d.draftResponse), d.Next.Rosters[0].Reserved)
	}
	callError(c.onMachine("bad id!"), "GET", "/v1/next", nil, http.StatusBadRequest, "invalid_request")
}

func TestReservationsExpireAndFollowDrafts(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "work", members("Gandalf", "Frodo"), nil)
	mac, laptop := c.session("mac"), c.session("laptop") // machine IDs unique to this test
	a, b := c.onMachine(mac), c.onMachine(laptop)
	call[nextResponse](a, "GET", "/v1/next", nil, http.StatusOK) // a holds Gandalf

	// Expired, the hold is gone and the other machine can take the member
	execSQL(t, `update reservations set expires_at = now() - interval '1 minute' where machine_id = $1`, mac)
	if next := call[nextResponse](b, "GET", "/v1/next", nil, http.StatusOK); *next.Rosters[0].Reserved != "Gandalf" {
		t.Fatalf("after expiry b reserved %v", next.Rosters[0].Reserved)
	}
	// Every call refreshes a machine's hold, around what the other machine holds
	if next := call[nextResponse](a, "GET", "/v1/next", nil, http.StatusOK); *next.Rosters[0].Reserved != "Frodo" {
		t.Fatalf("a's hold: %v", next.Rosters[0].Reserved)
	}
	// A third machine can't take either held member: it gets a copy. The holder drafting its member drops the hold,
	// and with no free name left to hold, it holds nothing
	if d := draftSessionNamed(c.onMachine(c.session("other")), c.session("o1")); displayOf(d) != "Gandalf 2" {
		t.Fatalf("third machine drafted %s, want Gandalf 2", displayOf(d))
	}
	if d := draftSessionNamed(b, c.session("b1")); d.Name != "Gandalf" {
		t.Fatalf("b drafted %s, want its held Gandalf", d.Name)
	}
	if n := queryInt(t, `select count(*) from reservations where machine_id = $1`, laptop); n != 0 {
		t.Fatalf("b still holds %d reservations with nothing free", n)
	}
	if next := call[nextResponse](a, "GET", "/v1/next", nil, http.StatusOK); *next.Rosters[0].Reserved != "Frodo" {
		t.Fatalf("a's hold after the others drafted: %v", next.Rosters[0].Reserved)
	}
}
