package store

import (
	"testing"
	"time"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

func TestPickFreeMemberCyclesThroughTheATeamFirst(t *testing.T) {
	aTeam := []roster.Member{{Name: "Gandalf"}, {Name: "Frodo"}, {Name: "Samwise"}}
	bTeam := []roster.Member{{Name: "Boromir"}, {Name: "Merry"}}
	busy := map[string]bool{"Frodo": true}
	isBusy := func(name string, copy int) bool { return copy == 1 && busy[name] }
	day := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }

	// Never drafted beats drafted; among the drafted, the longest ago; never a busy one; ties keep roster order
	if m, copy := pickFreeMember(aTeam, bTeam, isBusy, map[string]time.Time{"Gandalf": day(3), "Samwise": day(1)}); m.Name != "Samwise" || copy != 1 {
		t.Fatalf("least recent: %s copy %d", m.Name, copy)
	}
	if m, _ := pickFreeMember(aTeam, bTeam, isBusy, map[string]time.Time{"Gandalf": day(3)}); m.Name != "Samwise" {
		t.Fatalf("never drafted: %s", m.Name)
	}
	if m, _ := pickFreeMember(aTeam, bTeam, isBusy, nil); m.Name != "Gandalf" {
		t.Fatalf("ties: %s", m.Name)
	}

	// The B team only once the A team is full, then copies
	busy["Gandalf"], busy["Samwise"] = true, true
	if m, copy := pickFreeMember(aTeam, bTeam, isBusy, map[string]time.Time{"Boromir": day(2)}); m.Name != "Merry" || copy != 1 {
		t.Fatalf("B team: %s copy %d", m.Name, copy)
	}
	busy["Boromir"], busy["Merry"] = true, true
	if m, copy := pickFreeMember(aTeam, bTeam, isBusy, map[string]time.Time{"Gandalf": day(3)}); m.Name != "Frodo" || copy != 2 {
		t.Fatalf("copies: %s copy %d", m.Name, copy)
	}
}
