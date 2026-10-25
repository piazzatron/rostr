// Package paths resolves where rostr, Claude Code, and Codex keep their files. Everything hangs off $HOME, with the XDG,
// CLAUDE_CONFIG_DIR, and CODEX_HOME overrides honored, so tests can point it all at a temp dir.
package paths

import (
	"os"
	"path/filepath"
)

// ConfigDir is ~/.config/rostr (or $XDG_CONFIG_HOME/rostr).
func ConfigDir() string {
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "rostr")
}

// Database is the local-mode SQLite database.
func Database() string {
	return filepath.Join(ConfigDir(), "rostr.db")
}

// AutoDraft holds this machine's auto-draft setting in cloud mode ("on" or "off"). Local mode keeps it in Database.
func AutoDraft() string {
	return filepath.Join(ConfigDir(), "auto-draft")
}

// Credentials holds the rostr key in cloud mode.
func Credentials() string {
	return filepath.Join(ConfigDir(), "credentials")
}

// KeepAliveDir holds one file per session, whose modification time is the session's last keep-alive bump.
func KeepAliveDir() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "rostr", "keepalive")
}

// HooksStamp records the fingerprint of the hooks rostr last installed for an agent ("claude-code" or "codex").
func HooksStamp(agent string) string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "rostr", "hooks", agent)
}

// TurnDir holds one file per session whose last turn ended: when, and the name the session had then.
func TurnDir() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "rostr", "turns")
}

// CloudCacheDir holds cloud mode's local cache of each session's own draft.
func CloudCacheDir() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "rostr", "cloud")
}

// PresetsCache is the last list of pre-built rosters fetched from the site.
func PresetsCache() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "rostr", "presets.json")
}

// BackgroundLog collects what rostr's background work (drafts ahead of a first prompt, prompt summaries) writes to stderr.
func BackgroundLog() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "rostr", "background.log")
}

// ClaudeDir is Claude Code's config directory: ~/.claude, or $CLAUDE_CONFIG_DIR.
func ClaudeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".claude")
}

// CodexDir is Codex's config directory: ~/.codex, or $CODEX_HOME.
func CodexDir() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".codex")
}

// AgentsSkillsDir is ~/.agents/skills, where Codex finds user skills.
func AgentsSkillsDir() string {
	return filepath.Join(home(), ".agents", "skills")
}

func xdgDir(envVar, fallback string) string {
	if dir := os.Getenv(envVar); dir != "" {
		return dir
	}
	return filepath.Join(home(), fallback)
}

func home() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return dir
}
