package api

import (
	"testing"
	"time"
)

func TestFreeMemberCyclesThroughTheCast(t *testing.T) {
	roster := rosterRow{
		Members: []member{{Name: "Gandalf"}, {Name: "Frodo"}, {Name: "Samwise"}, {Name: "Boromir"}, {Name: "Merry"}},
	}
	busy := map[slot]bool{{nameKey("Frodo"), 1}: true}
	day := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }

	// Among the drafted, the longest ago; never a busy one
	last := map[string]time.Time{nameKey("Gandalf"): day(3), nameKey("Samwise"): day(1), nameKey("Boromir"): day(4),
		nameKey("Merry"): day(2)}
	if m, copy := freeMember(roster, busy, last); m.Name != "Samwise" || copy != 1 {
		t.Fatalf("least recent: %s copy %d", m.Name, copy)
	}
	// Never drafted beats drafted, wherever they are in the list
	if m, _ := freeMember(roster, busy, map[string]time.Time{nameKey("Gandalf"): day(3), nameKey("Samwise"): day(1),
		nameKey("Boromir"): day(4)}); m.Name != "Merry" {
		t.Fatalf("never drafted: %s", m.Name)
	}
	if m, _ := freeMember(roster, busy, nil); m.Name != "Gandalf" {
		t.Fatalf("ties keep roster order: %s", m.Name)
	}

	// The last free name before any copy, then copies, by the same rule
	busy[slot{nameKey("Gandalf"), 1}], busy[slot{nameKey("Samwise"), 1}], busy[slot{nameKey("Boromir"), 1}] = true, true, true
	if m, copy := freeMember(roster, busy, last); m.Name != "Merry" || copy != 1 {
		t.Fatalf("last free: %s copy %d", m.Name, copy)
	}
	busy[slot{nameKey("Merry"), 1}] = true
	if m, copy := freeMember(roster, busy, last); m.Name != "Frodo" || copy != 2 {
		t.Fatalf("copies: %s copy %d", m.Name, copy)
	}
}
