// Package presets holds the pre-built rosters that ship with the CLI, generated from site/src/data/rosters.ts.
package presets

import (
	"math/rand/v2"
	"strings"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

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

// FreeAgentsID is the preset that mixes everyone else's characters.
const FreeAgentsID = "free-agents"

// FreeAgents is the Free Agents preset with a new random cast: as many members as the site's Free Agents, drawn from
// every other preset, no two sharing a name or an emoji, so every emoji mode works.
func FreeAgents() Preset {
	p, _ := ByID(FreeAgentsID)
	var pool []roster.Member
	for _, other := range all {
		if other.ID != FreeAgentsID {
			pool = append(append(pool, other.ATeam...), other.BTeam...)
		}
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })

	size := len(p.ATeam) + len(p.BTeam)
	names, emoji := map[string]bool{}, map[string]bool{}
	var cast []roster.Member
	for _, m := range pool {
		if len(cast) == size {
			break
		}
		name, mark := roster.NormalizeName(m.Name), strings.ReplaceAll(m.Emoji, "\ufe0f", "")
		if names[name] || emoji[mark] {
			continue
		}
		names[name], emoji[mark] = true, true
		cast = append(cast, m)
	}
	split := min(len(p.ATeam), len(cast))
	p.ATeam, p.BTeam = cast[:split], cast[split:]
	return p
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
