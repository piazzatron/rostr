package store

import (
	"testing"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

func TestPickFreeMemberIsRandomWithinTheATeamFirst(t *testing.T) {
	aTeam := []roster.Member{{Name: "Gandalf"}, {Name: "Frodo"}, {Name: "Samwise"}}
	bTeam := []roster.Member{{Name: "Boromir"}, {Name: "Merry"}}
	busy := map[string]bool{"Frodo/1": true}
	isBusy := func(name string, copy int) bool { return busy[name+"/"+string(rune('0'+copy))] }

	// Any free A-teamer can come up; a busy one never does
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		m, copy := pickFreeMember(aTeam, bTeam, isBusy, func(n int) int { return i % n })
		seen[m.Name] = true
		if copy != 1 || m.Name == "Frodo" {
			t.Fatalf("picked %s copy %d", m.Name, copy)
		}
	}
	if !seen["Gandalf"] || !seen["Samwise"] {
		t.Fatalf("picked only %v", seen)
	}

	// The B team only once the A team is full, then copies
	busy["Gandalf/1"], busy["Samwise/1"] = true, true
	if m, copy := pickFreeMember(aTeam, bTeam, isBusy, func(n int) int { return n - 1 }); m.Name != "Merry" || copy != 1 {
		t.Fatalf("B team: %s copy %d", m.Name, copy)
	}
	busy["Boromir/1"], busy["Merry/1"] = true, true
	if m, copy := pickFreeMember(aTeam, bTeam, isBusy, func(int) int { return 0 }); m.Name != "Gandalf" || copy != 2 {
		t.Fatalf("copies: %s copy %d", m.Name, copy)
	}
}
