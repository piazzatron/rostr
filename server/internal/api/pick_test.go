package api

import (
	"testing"
	"time"
)

func TestFreeMemberCyclesThroughTheATeamFirst(t *testing.T) {
	roster := rosterRow{
		ATeam: []member{{Name: "Gandalf"}, {Name: "Frodo"}, {Name: "Samwise"}},
		BTeam: []member{{Name: "Boromir"}, {Name: "Merry"}},
	}
	busy := map[slot]bool{{nameKey("Frodo"), 1}: true}
	day := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }

	// Never drafted beats drafted; among the drafted, the longest ago; never a busy one
	last := map[string]time.Time{nameKey("Gandalf"): day(3), nameKey("Samwise"): day(1)}
	if m, copy := freeMember(roster, busy, last); m.Name != "Samwise" || copy != 1 {
		t.Fatalf("least recent: %s copy %d", m.Name, copy)
	}
	if m, _ := freeMember(roster, busy, map[string]time.Time{nameKey("Gandalf"): day(3)}); m.Name != "Samwise" {
		t.Fatalf("never drafted: %s", m.Name)
	}
	if m, _ := freeMember(roster, busy, nil); m.Name != "Gandalf" {
		t.Fatalf("ties keep roster order: %s", m.Name)
	}

	// The B team only once the A team is full, then copies, by the same rule
	busy[slot{nameKey("Gandalf"), 1}], busy[slot{nameKey("Samwise"), 1}] = true, true
	if m, copy := freeMember(roster, busy, map[string]time.Time{nameKey("Boromir"): day(2)}); m.Name != "Merry" || copy != 1 {
		t.Fatalf("B team: %s copy %d", m.Name, copy)
	}
	busy[slot{nameKey("Boromir"), 1}], busy[slot{nameKey("Merry"), 1}] = true, true
	if m, copy := freeMember(roster, busy, last); m.Name != "Frodo" || copy != 2 {
		t.Fatalf("copies: %s copy %d", m.Name, copy)
	}
}
