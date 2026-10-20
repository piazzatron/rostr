package main

import (
	"os"
	"reflect"
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
		if err := roster.ValidateCast(p.ATeam, p.BTeam, roster.EmojiOnly); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
		if len(p.ATeam) != roster.ATeamSize {
			t.Errorf("%s: A team of %d", p.ID, len(p.ATeam))
		}
	}
	if _, ok := presets.ByID("fellowship"); !ok {
		t.Error("fellowship missing")
	}
}
