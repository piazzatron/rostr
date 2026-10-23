package presets

import (
	"testing"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

func TestFreeAgentsHasEveryPreset(t *testing.T) {
	free := FreeAgents()
	if n := len(free.ATeam) + len(free.BTeam); n != freeAgentsMax {
		t.Fatalf("%d members", n)
	}
	if err := roster.ValidateCast(free.ATeam, free.BTeam, roster.EmojiOn); err != nil {
		t.Fatal(err)
	}
	members := map[string]bool{}
	for _, m := range append(free.ATeam, free.BTeam...) {
		members[m.Name] = true
	}
	for _, p := range All() {
		if !members[p.ATeam[0].Name] {
			t.Errorf("no one from %s", p.ID)
		}
	}
	if site, _ := ByID(FreeAgentsID); len(site.BTeam) == len(free.BTeam) {
		t.Fatal("changed the site's preset")
	}
}
