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
	event      string
	subcommand string
}{
	{"SessionStart", "session-start"},
	{"UserPromptSubmit", "prompt"},
	{"Stop", "title"},
	{"SessionEnd", "session-end"},
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

// Uninstall removes what Install added: rostr's hooks and permissions from settings.json, and the skill and /draft
// command if they're still the ones rostr wrote (a /draft the user wrote themselves is left alone). It returns a
// description of each piece it removed.
func Uninstall(claudeDir string) ([]string, error) {
	var removed []string

	settingsPath := filepath.Join(claudeDir, "settings.json")
	settingsChanged, err := uninstallSettings(settingsPath)
	if err != nil {
		return nil, err
	}
	if settingsChanged {
		removed = append(removed, "hooks in "+settingsPath)
	}

	files := []struct {
		path, content, label string
	}{
		{filepath.Join(claudeDir, "skills", "rostr", "SKILL.md"), skillContent, "the rostr skill"},
		{filepath.Join(claudeDir, "commands", "draft.md"), draftCommandContent, "the /draft command"},
	}
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && string(data) != f.content) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := os.Remove(f.path); err != nil {
			return nil, err
		}
		removed = append(removed, f.label)
	}

	// The skill lives in its own folder; remove it once it's empty
	_ = os.Remove(filepath.Join(claudeDir, "skills", "rostr"))
	return removed, nil
}

// HookCommand is the installed command for a subcommand. It does nothing when rostr isn't installed (hooks committed
// to a repo also run for teammates without it), and otherwise keeps rostr's exit code, which the Stop hook needs.
func HookCommand(subcommand string) string {
	return "if command -v rostr >/dev/null; then rostr hook claude-code " + subcommand + "; fi"
}

// installSettings merges the hooks and permissions into settings.json, reporting whether it changed anything. Other
// settings are kept; an unreadable file is an error rather than being overwritten.
func installSettings(path string) (bool, error) {
	settings, err := readSettings(path)
	if err != nil {
		return false, err
	}

	changed := false

	// Hooks: drop rostr hooks that don't match the current set (older installs ran the title step, and before that the
	// prompt hook, in the background on UserPromptSubmit), then add one entry per event that's missing its rostr hook
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	if removeHooks(hooks, func(event string, hook map[string]any) bool {
		return isRostrHook(hook) && !isCurrentRostrHook(event, hook)
	}) {
		changed = true
	}
	for _, h := range hookEvents {
		groups, _ := hooks[h.event].([]any)
		if rostrHook(groups, h.subcommand) != nil {
			continue
		}
		hook := map[string]any{"type": "command", "command": HookCommand(h.subcommand)}
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
	return true, writeSettings(path, settings)
}

// uninstallSettings removes rostr's hooks and permissions from settings.json, reporting whether it changed anything.
// Hook groups and events left empty are dropped; everything else is kept.
func uninstallSettings(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	settings, err := readSettings(path)
	if err != nil {
		return false, err
	}

	changed := false

	// Hooks: drop every rostr hook
	hooks, _ := settings["hooks"].(map[string]any)
	if removeHooks(hooks, func(_ string, hook map[string]any) bool { return isRostrHook(hook) }) {
		changed = true
	}

	// Permissions rostr added
	perms, _ := settings["permissions"].(map[string]any)
	allow, _ := perms["allow"].([]any)
	var keptAllow []any
	for _, p := range allow {
		if s, ok := p.(string); ok && containsString(toAny(permissions), s) {
			changed = true
			continue
		}
		keptAllow = append(keptAllow, p)
	}
	if perms != nil && allow != nil {
		perms["allow"] = keptAllow
		if len(keptAllow) == 0 {
			delete(perms, "allow")
		}
	}

	if !changed {
		return false, nil
	}
	return true, writeSettings(path, settings)
}

// readSettings reads settings.json; a missing file is empty, and an unreadable one is an error rather than being
// overwritten.
func readSettings(path string) (map[string]any, error) {
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("can't read %s, so rostr didn't change it: %w", path, err)
		}
	}
	return settings, nil
}

func writeSettings(path string, settings map[string]any) error {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func toAny(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// removeHooks drops every hook that remove matches, then any groups and events that leaves empty, reporting whether
// it removed anything.
func removeHooks(hooks map[string]any, remove func(event string, hook map[string]any) bool) bool {
	removed := false
	for event, value := range hooks {
		groups, _ := value.([]any)
		var keptGroups []any
		for _, group := range groups {
			groupMap, _ := group.(map[string]any)
			groupHooks, _ := groupMap["hooks"].([]any)
			var keptHooks []any
			for _, hook := range groupHooks {
				hookMap, _ := hook.(map[string]any)
				if remove(event, hookMap) {
					removed = true
					continue
				}
				keptHooks = append(keptHooks, hook)
			}
			if len(keptHooks) > 0 {
				groupMap["hooks"] = keptHooks
				keptGroups = append(keptGroups, groupMap)
			}
		}
		if len(keptGroups) > 0 {
			hooks[event] = keptGroups
		} else {
			delete(hooks, event)
		}
	}
	return removed
}

// isRostrHook reports whether a hook runs one of rostr's Claude Code hooks.
func isRostrHook(hook map[string]any) bool {
	command, _ := hook["command"].(string)
	return strings.Contains(command, "rostr hook claude-code ")
}

// isCurrentRostrHook reports whether a hook is one rostr installs today, on the event and with the options it uses.
func isCurrentRostrHook(event string, hook map[string]any) bool {
	command, _ := hook["command"].(string)
	_, async := hook["asyncRewake"]
	for _, h := range hookEvents {
		if h.event == event && command == HookCommand(h.subcommand) && !async {
			return true
		}
	}
	return false
}

// rostrHook finds rostr's installed hook for a subcommand, or nil.
func rostrHook(groups []any, subcommand string) map[string]any {
	for _, group := range groups {
		groupMap, _ := group.(map[string]any)
		hooks, _ := groupMap["hooks"].([]any)
		for _, hook := range hooks {
			hookMap, _ := hook.(map[string]any)
			command, _ := hookMap["command"].(string)
			if strings.HasSuffix(strings.TrimSuffix(command, "; fi"), "rostr hook claude-code "+subcommand) {
				return hookMap
			}
		}
	}
	return nil
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
