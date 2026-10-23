package codex

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	//go:embed assets/skill.md
	skillContent string
	//go:embed assets/draft-skill.md
	draftSkillContent string
)

// hookEvents maps each Codex event rostr hooks into to its `rostr hook codex` subcommand. SessionEnd's timeout is
// Codex's most (it defaults to 1 second), and release keeps under it.
var hookEvents = []struct {
	event      string
	subcommand string
	timeout    int
}{
	{"SessionStart", "session-start", 0},
	{"UserPromptSubmit", "prompt", 0},
	{"PostToolUse", "working", 0},
	{"Stop", "turn-end", 0},
	{"SessionEnd", "session-end", 3},
}

// Detected reports whether Codex looks installed: it has a config directory, or `codex` is on PATH.
func Detected(codexDir string) bool {
	if _, err := os.Stat(codexDir); err == nil {
		return true
	}
	_, err := exec.LookPath("codex")
	return err == nil
}

// Fingerprint identifies the hooks this version of rostr installs, so an upgrade that changes them can be noticed and
// the hooks reinstalled.
func Fingerprint() string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%v", hookEvents))
	return hex.EncodeToString(sum[:8])
}

// Installed reports whether hooks.json in codexDir has any of rostr's hooks, current or not.
func Installed(codexDir string) bool {
	data, err := os.ReadFile(filepath.Join(codexDir, "hooks.json"))
	return err == nil && strings.Contains(string(data), "rostr hook codex ")
}

// Install adds rostr's hooks to hooks.json in codexDir, and writes the rostr and rostr-draft skills to skillsDir
// (~/.agents/skills), skipping whatever is already there. It returns a description of each piece it added.
func Install(codexDir, skillsDir string) ([]string, error) {
	var installed []string

	hooksPath := filepath.Join(codexDir, "hooks.json")
	changed, err := installHooks(hooksPath)
	if err != nil {
		return nil, err
	}
	if changed {
		installed = append(installed, "hooks in "+hooksPath)
	}

	for _, f := range skillFiles(skillsDir) {
		if _, err := os.Stat(f.path); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			return nil, err
		}
		installed = append(installed, f.label)
	}
	return installed, nil
}

// Uninstall removes what Install added: rostr's hooks from hooks.json, and the skills if they're still the ones rostr
// wrote. It returns a description of each piece it removed.
func Uninstall(codexDir, skillsDir string) ([]string, error) {
	var removed []string

	hooksPath := filepath.Join(codexDir, "hooks.json")
	changed, err := uninstallHooks(hooksPath)
	if err != nil {
		return nil, err
	}
	if changed {
		removed = append(removed, "hooks in "+hooksPath)
	}

	for _, f := range skillFiles(skillsDir) {
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

		// Each skill lives in its own folder; remove it once it's empty
		_ = os.Remove(filepath.Dir(f.path))
	}
	return removed, nil
}

// HookCommand is the installed command for a subcommand. It does nothing when rostr isn't installed, so hooks copied
// to a machine (or committed to a repo) without rostr stay quiet.
func HookCommand(subcommand string) string {
	return "if command -v rostr >/dev/null; then rostr hook codex " + subcommand + "; fi"
}

type skillFile struct {
	path, content, label string
}

func skillFiles(skillsDir string) []skillFile {
	return []skillFile{
		{filepath.Join(skillsDir, "rostr", "SKILL.md"), skillContent, "the rostr skill"},
		{filepath.Join(skillsDir, "rostr-draft", "SKILL.md"), draftSkillContent, "the $rostr-draft skill"},
	}
}

// installHooks merges rostr's hooks into hooks.json, reporting whether it changed anything. Other hooks and keys are
// kept; an unreadable file is an error rather than being overwritten.
func installHooks(path string) (bool, error) {
	config, err := readJSON(path)
	if err != nil {
		return false, err
	}

	// One entry per event, unless a rostr hook for that subcommand is already there
	changed := false
	hooks, _ := config["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, h := range hookEvents {
		groups, _ := hooks[h.event].([]any)
		if hasRostrHook(groups, h.subcommand) {
			continue
		}
		hook := map[string]any{"type": "command", "command": HookCommand(h.subcommand)}
		if h.timeout > 0 {
			hook["timeout"] = h.timeout
		}
		hooks[h.event] = append(groups, map[string]any{"hooks": []any{hook}})
		changed = true
	}
	if !changed {
		return false, nil
	}
	config["hooks"] = hooks
	return true, writeJSON(path, config)
}

// uninstallHooks removes rostr's hooks from hooks.json, reporting whether it changed anything. Hook groups and events
// left empty are dropped; everything else is kept.
func uninstallHooks(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	config, err := readJSON(path)
	if err != nil {
		return false, err
	}

	changed := false
	hooks, _ := config["hooks"].(map[string]any)
	for event, value := range hooks {
		groups, _ := value.([]any)
		var keptGroups []any
		for _, group := range groups {
			groupMap, _ := group.(map[string]any)
			groupHooks, _ := groupMap["hooks"].([]any)
			var keptHooks []any
			for _, hook := range groupHooks {
				hookMap, _ := hook.(map[string]any)
				command, _ := hookMap["command"].(string)
				if strings.Contains(command, "rostr hook codex ") {
					changed = true
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

	if !changed {
		return false, nil
	}
	return true, writeJSON(path, config)
}

func hasRostrHook(groups []any, subcommand string) bool {
	for _, group := range groups {
		groupMap, _ := group.(map[string]any)
		hooks, _ := groupMap["hooks"].([]any)
		for _, hook := range hooks {
			hookMap, _ := hook.(map[string]any)
			command, _ := hookMap["command"].(string)
			if strings.Contains(command, "rostr hook codex "+subcommand) {
				return true
			}
		}
	}
	return false
}

// readJSON reads a JSON object file; a missing file is empty, and an unreadable one is an error rather than being
// overwritten.
func readJSON(path string) (map[string]any, error) {
	config := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("can't read %s, so rostr didn't change it: %w", path, err)
		}
	}
	return config, nil
}

func writeJSON(path string, config map[string]any) error {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}
