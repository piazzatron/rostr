// Package presets holds the pre-built rosters that ship with the CLI, generated from site/src/data/rosters.ts.
package presets

import "github.com/piazzatron/rostr/cli/internal/roster"

//go:generate go run ./gen ../../../site/src/data/rosters.ts presets_gen.go

// Preset is a pre-built roster.
type Preset struct {
	ID     string
	Name   string
	Source string
	Emoji  string
	ATeam  []roster.Member
	BTeam  []roster.Member
}

// All returns every preset, in the site's order.
func All() []Preset {
	return all
}

// ByID finds a preset by its id.
func ByID(id string) (Preset, bool) {
	for _, p := range all {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
