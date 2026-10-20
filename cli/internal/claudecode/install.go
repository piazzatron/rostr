package claudecode

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	//go:embed assets/skill.md
	skillContent string
	//go:embed assets/draft.md
	draftCommandContent string
)

// hookEvents maps each Claude Code event rostr hooks into to its `rostr hook claude-code` subcommand.
var hookEvents = []struct {
	event       string
	subcommand  string
	asyncRewake bool
}{
	{"SessionStart", "session-start", false},
	{"UserPromptSubmit", "prompt", true},
	{"SessionEnd", "session-end", false},
}

// permissions are pre-approved so the draft step's instructions don't stop on a permission prompt.
var permissions = []string{
	"mcp__claude-code-remote__set_session_title",
	"Bash(rostr agent task:*)",
}

// Install adds rostr's hooks and permissions to settings.json, and writes the skill and /draft command, skipping
// whatever is already there. It returns a description of each piece it added.
func Install(claudeDir string) ([]string, error) {
	var installed []string

	settingsChanged, err := installSettings(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		return nil, err
	}
	if settingsChanged {
		installed = append(installed, "hooks in "+filepath.Join(claudeDir, "settings.json"))
	}

	files := []struct {
		path, content, label string
	}{
		{filepath.Join(claudeDir, "skills", "rostr", "SKILL.md"), skillContent, "the rostr skill"},
		{filepath.Join(claudeDir, "commands", "draft.md"), draftCommandContent, "the /draft command"},
	}
	for _, f := range files {
		wrote, err := writeIfMissing(f.path, f.content)
		if err != nil {
			return nil, err
		}
		if wrote {
			installed = append(installed, f.label)
		}
	}
	return installed, nil
}

// HookCommand is the installed command for a subcommand. It does nothing when rostr isn't installed (hooks committed
// to a repo also run for teammates without it), and otherwise keeps rostr's exit code, which asyncRewake needs.
func HookCommand(subcommand string) string {
	return "if command -v rostr >/dev/null; then rostr hook claude-code " + subcommand + "; fi"
}

// installSettings merges the hooks and permissions into settings.json, reporting whether it changed anything. Other
// settings are kept; an unreadable file is an error rather than being overwritten.
func installSettings(path string) (bool, error) {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return false, err
	default:
		if err := json.Unmarshal(data, &settings); err != nil {
			return false, fmt.Errorf("can't read %s, so rostr didn't change it: %w", path, err)
		}
	}

	changed := false

	// Hooks: one entry per event, unless a rostr hook for that subcommand is already there
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, h := range hookEvents {
		groups, _ := hooks[h.event].([]any)
		if hasRostrHook(groups, h.subcommand) {
			continue
		}
		hook := map[string]any{"type": "command", "command": HookCommand(h.subcommand)}
		if h.asyncRewake {
			hook["asyncRewake"] = true
		}
		hooks[h.event] = append(groups, map[string]any{"hooks": []any{hook}})
		changed = true
	}
	settings["hooks"] = hooks

	// Permissions for the tools the draft step asks the agent to use
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	for _, p := range permissions {
		if !containsString(allow, p) {
			allow = append(allow, p)
			changed = true
		}
	}
	perms["allow"] = allow
	settings["permissions"] = perms

	if !changed {
		return false, nil
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, out.Bytes(), 0o644)
}

func hasRostrHook(groups []any, subcommand string) bool {
	for _, group := range groups {
		groupMap, _ := group.(map[string]any)
		hooks, _ := groupMap["hooks"].([]any)
		for _, hook := range hooks {
			hookMap, _ := hook.(map[string]any)
			command, _ := hookMap["command"].(string)
			if strings.Contains(command, "rostr hook claude-code "+subcommand) {
				return true
			}
		}
	}
	return false
}

func containsString(list []any, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func writeIfMissing(path, content string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}
