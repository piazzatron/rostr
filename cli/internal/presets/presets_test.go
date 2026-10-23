package presets

import (
	"slices"
	"testing"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

func TestFreeAgentsIsARandomMix(t *testing.T) {
	site, _ := ByID(FreeAgentsID)
	free := FreeAgents()
	if len(free.ATeam) != len(site.ATeam) || len(free.BTeam) != len(site.BTeam) {
		t.Fatalf("teams of %d and %d", len(free.ATeam), len(free.BTeam))
	}
	if err := roster.ValidateCast(free.ATeam, free.BTeam, roster.EmojiOnly); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if !slices.Equal(FreeAgents().ATeam, free.ATeam) {
			return
		}
	}
	t.Fatal("the same A team every time")
}
