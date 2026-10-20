package api

import "testing"

func TestFreeMemberIsRandomWithinTheATeamFirst(t *testing.T) {
	roster := rosterRow{
		ATeam: []member{{Name: "Gandalf"}, {Name: "Frodo"}, {Name: "Samwise"}},
		BTeam: []member{{Name: "Boromir"}, {Name: "Merry"}},
	}
	busy := map[slot]bool{{nameKey("Frodo"), 1}: true}

	// Any free A-teamer can come up; a busy one never does
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		m, copy := freeMember(roster, busy, func(n int) int { return i % n })
		seen[m.Name] = true
		if copy != 1 || m.Name == "Frodo" {
			t.Fatalf("picked %s copy %d", m.Name, copy)
		}
	}
	if !seen["Gandalf"] || !seen["Samwise"] {
		t.Fatalf("picked only %v", seen)
	}

	// The B team only once the A team is full, then copies
	busy[slot{nameKey("Gandalf"), 1}], busy[slot{nameKey("Samwise"), 1}] = true, true
	if m, copy := freeMember(roster, busy, func(n int) int { return n - 1 }); m.Name != "Merry" || copy != 1 {
		t.Fatalf("B team: %s copy %d", m.Name, copy)
	}
	busy[slot{nameKey("Boromir"), 1}], busy[slot{nameKey("Merry"), 1}] = true, true
	if m, copy := freeMember(roster, busy, func(int) int { return 0 }); m.Name != "Gandalf" || copy != 2 {
		t.Fatalf("copies: %s copy %d", m.Name, copy)
	}
}
