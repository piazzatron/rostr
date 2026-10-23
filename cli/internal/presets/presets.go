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

// FreeAgentsID is the preset that mixes everyone else's characters.
const FreeAgentsID = "free-agents"

// freeAgentsMax is how many members Free Agents has in all: the rostr API's limit for a roster.
const freeAgentsMax = 200

// FreeAgents is the Free Agents preset with everyone it has room for: its own A team, then a B team of its own B team
// and every other preset's members, taken a member from each preset in turn so every preset is in it.
func FreeAgents() Preset {
	p, _ := ByID(FreeAgentsID)
	p.ATeam = append([]roster.Member{}, p.ATeam...)
	p.BTeam = combinedBench(p)
	return p
}

func combinedBench(free Preset) []roster.Member {
	seen := map[string]bool{}
	for _, m := range free.ATeam {
		seen[roster.NormalizeName(m.Name)] = true
	}
	var bench []roster.Member
	add := func(m roster.Member) {
		if key := roster.NormalizeName(m.Name); !seen[key] && len(free.ATeam)+len(bench) < freeAgentsMax {
			seen[key] = true
			bench = append(bench, m)
		}
	}
	for _, m := range free.BTeam {
		add(m)
	}
	var casts [][]roster.Member
	longest := 0
	for _, other := range all {
		if other.ID != FreeAgentsID {
			members := append(append([]roster.Member{}, other.ATeam...), other.BTeam...)
			casts = append(casts, members)
			longest = max(longest, len(members))
		}
	}
	for i := range longest {
		for _, members := range casts {
			if i < len(members) {
				add(members[i])
			}
		}
	}
	return bench
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
