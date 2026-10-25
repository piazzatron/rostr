package presets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

// The site publishes the pre-built rosters at URL, so new ones reach the CLI without a release. Commands that offer
// presets refresh them at most once a day (Refresh) and otherwise use the last list they fetched, falling back to the
// list built into this binary. It's local mode's only network call; hooks never make it.

// URL is where the site publishes the rosters (site/src/pages/rosters.json.ts).
const URL = "https://getrostr.sh/rosters.json"

// listVersion is the newest rosters.json version this CLI reads. It ignores a newer one and keeps what it has.
const listVersion = 1

// refreshEvery is how old the cached list gets before Refresh fetches it again.
const refreshEvery = 24 * time.Hour

// fetchTimeout keeps an offline machine from waiting on the fetch.
const fetchTimeout = 3 * time.Second

// current is the list All and ByID use: the built-in one until Refresh finds a better one.
var current = all

// Builtin returns the presets built into this binary, generated from site/src/data/rosters.ts.
func Builtin() []Preset {
	return all
}

type listFile struct {
	Version int `json:"version"`
	Presets []struct {
		ID      string   `json:"id"`
		Emoji   string   `json:"emoji"`
		Name    string   `json:"name"`
		Source  string   `json:"source"`
		Members []string `json:"members"`
	} `json:"presets"`
}

// Refresh uses the list cached at cachePath, fetching it from url first when the cache is missing or a day old. It
// keeps whatever it had when the fetch or the list fails, so it never stops a command.
func Refresh(ctx context.Context, url, cachePath string) {
	cached, cachedErr := os.ReadFile(cachePath)
	if cachedErr == nil {
		if list, err := parseList(cached); err == nil {
			current = list
		}
	}
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < refreshEvery {
		return
	}

	data, err := fetch(ctx, url)
	if err != nil {
		return
	}
	list, err := parseList(data)
	if err != nil {
		return
	}
	current = list
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return
	}
	tmp := cachePath + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, cachePath)
	}
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// parseList reads a rosters.json, skipping presets this CLI can't use. A list without Free Agents, which assemble
// always offers, is refused whole.
func parseList(data []byte) ([]Preset, error) {
	var file listFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version < 1 || file.Version > listVersion {
		return nil, fmt.Errorf("rosters.json version %d", file.Version)
	}
	var list []Preset
	seen := map[string]bool{}
	for _, p := range file.Presets {
		members := make([]roster.Member, len(p.Members))
		for i, m := range p.Members {
			members[i] = roster.ParseMember(m)
		}
		if p.ID == "" || seen[p.ID] || len(members) < DefaultTeamSize || roster.ValidateCast(members, roster.EmojiOnly) != nil {
			continue
		}
		seen[p.ID] = true
		list = append(list, Preset{ID: p.ID, Emoji: p.Emoji, Name: p.Name, Source: p.Source, Members: members})
	}
	if !seen[FreeAgentsID] {
		return nil, errors.New("rosters.json has no Free Agents")
	}
	return list, nil
}
