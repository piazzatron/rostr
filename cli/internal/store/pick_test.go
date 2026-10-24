package store

import (
	"testing"
	"time"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

func TestPickFreeMemberCyclesThroughTheRoster(t *testing.T) {
	members := []roster.Member{{Name: "Gandalf"}, {Name: "Frodo"}, {Name: "Samwise"}, {Name: "Boromir"}, {Name: "Merry"}}
	busy := map[string]bool{"Frodo": true}
	isBusy := func(name string, copy int) bool { return copy == 1 && busy[name] }
	day := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }

	// Never drafted beats drafted; among the drafted, the longest ago; never a busy one; ties keep roster order
	all := map[string]time.Time{"gandalf": day(3), "samwise": day(1), "boromir": day(4), "merry": day(5)}
	if m, copy := pickFreeMember(members, isBusy, all); m.Name != "Samwise" || copy != 1 {
		t.Fatalf("least recent: %s copy %d", m.Name, copy)
	}
	if m, _ := pickFreeMember(members, isBusy, map[string]time.Time{"gandalf": day(3)}); m.Name != "Samwise" {
		t.Fatalf("never drafted: %s", m.Name)
	}
	if m, _ := pickFreeMember(members, isBusy, nil); m.Name != "Gandalf" {
		t.Fatalf("ties: %s", m.Name)
	}

	// Copies only once everyone is busy, by the same rule
	busy["Gandalf"], busy["Samwise"], busy["Boromir"], busy["Merry"] = true, true, true, true
	if m, copy := pickFreeMember(members, isBusy, map[string]time.Time{"gandalf": day(3)}); m.Name != "Frodo" || copy != 2 {
		t.Fatalf("copies: %s copy %d", m.Name, copy)
	}
}
