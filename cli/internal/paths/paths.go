// Package paths resolves where rostr and Claude Code keep their files. Everything hangs off $HOME, with the XDG and
// CLAUDE_CONFIG_DIR overrides honored, so tests can point it all at a temp dir.
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

// Credentials holds the rostr key in cloud mode.
func Credentials() string {
	return filepath.Join(ConfigDir(), "credentials")
}

// KeepAliveDir holds one file per session, whose modification time is the session's last keep-alive bump.
func KeepAliveDir() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "rostr", "keepalive")
}

// ClaudeDir is Claude Code's config directory: ~/.claude, or $CLAUDE_CONFIG_DIR.
func ClaudeDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".claude")
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
