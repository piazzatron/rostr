package main

import (
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/piazzatron/rostr/cli/internal/presets"
	"github.com/piazzatron/rostr/cli/internal/roster"
)

const rostersTS = "../../../../site/src/data/rosters.ts"

// TestPresetsMatchSite fails when site/src/data/rosters.ts has changed without `go generate ./...`.
func TestPresetsMatchSite(t *testing.T) {
	source, err := os.ReadFile(rostersTS)
	if err != nil {
		t.Skipf("no site checkout: %v", err)
	}
	parsed, err := ParseRostersTS(string(source))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, presets.All()) {
		t.Fatal("presets_gen.go is out of date with rosters.ts; run `go generate ./...` in cli/")
	}
}

func TestPresetsAreValidRosters(t *testing.T) {
	ids := map[string]bool{}
	for _, p := range presets.All() {
		if ids[p.ID] {
			t.Errorf("duplicate preset id %s", p.ID)
		}
		ids[p.ID] = true
		if err := roster.ValidateCast(p.Members, roster.EmojiOnly); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
		// Every team size assemble offers leaves someone to swap in
		if largest := slices.Max(presets.TeamSizes); len(p.Members) <= largest {
			t.Errorf("%s: %d members, no more than the largest team size (%d)", p.ID, len(p.Members), largest)
		}
	}
	if _, ok := presets.ByID("fellowship"); !ok {
		t.Error("fellowship missing")
	}
}
