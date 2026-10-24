package presets

import (
	"slices"
	"testing"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

func TestFreeAgentsIsARandomMix(t *testing.T) {
	site, _ := ByID(FreeAgentsID)
	free := FreeAgents()
	if len(free.Members) != len(site.Members) {
		t.Fatalf("%d members, want %d", len(free.Members), len(site.Members))
	}
	if err := roster.ValidateCast(free.Members, roster.EmojiOnly); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if !slices.Equal(FreeAgents().Members, free.Members) {
			return
		}
	}
	t.Fatal("the same cast every time")
}
