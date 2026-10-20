// Package pane labels the terminal pane a Claude Code session runs in with its rostr name, in herdr or tmux. It's
// best-effort: every command has a short timeout, and failures are ignored, so it never breaks or stalls a hook.
// Codex sessions aren't labeled, because Codex runs hooks in a shared daemon whose environment points at the wrong pane.
package pane

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// commandTimeout bounds each tmux or herdr command.
const commandTimeout = time.Second

// Label shows display on the session's pane: through herdr inside herdr, otherwise through tmux inside tmux.
func Label(ctx context.Context, display string) {
	switch multiplexer() {
	case herdr:
		herdrLabel(ctx, display)
	case tmux:
		tmuxLabel(ctx, display)
	}
}

// Clear removes the label Label added, when the session ends.
func Clear(ctx context.Context) {
	switch multiplexer() {
	case herdr:
		herdrClear(ctx)
	case tmux:
		tmuxClear(ctx)
	}
}

type kind int

const (
	none kind = iota
	herdr
	tmux
)

// multiplexer is the one the session's pane belongs to. herdr wins over tmux, since herdr can run inside tmux.
func multiplexer() kind {
	switch {
	case os.Getenv("HERDR_ENV") == "1" && os.Getenv("HERDR_PANE_ID") != "":
		return herdr
	case os.Getenv("HERDR_ENV") == "" && os.Getenv("TMUX") != "" && os.Getenv("TMUX_PANE") != "":
		return tmux
	}
	return none
}

// run runs a command with commandTimeout and returns its trimmed stdout.
func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
