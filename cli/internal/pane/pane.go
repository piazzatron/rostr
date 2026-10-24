// Package pane labels the terminal pane a session runs in with its rostr name, in herdr or tmux. It's best-effort:
// every command has a short timeout, and failures are ignored, so it never breaks or stalls a hook.
package pane

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/piazzatron/rostr/cli/internal/proc"
)

// commandTimeout bounds each tmux or herdr command.
const commandTimeout = time.Second

// Agent is the coding agent whose hook is labeling its pane, named as herdr names it.
type Agent string

const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

// titleWait is how long a Codex hook outside the TUI looks for its pane, while Codex puts the thread's new name in the
// terminal title.
const titleWait = time.Second

// Label shows display on the session's pane: through herdr inside herdr, otherwise through tmux inside tmux. A Codex
// hook that its shared daemon ran can't tell its pane from its environment, so it labels the one Codex pane whose
// terminal title shows display, which is there because rostr names the Codex thread after the session. It skips panes
// already labeled display: a resting thread keeps its name in its title and label after the name is drafted again.
func Label(ctx context.Context, agent Agent, display string) {
	if paneEnvTrusted(agent) {
		switch multiplexer() {
		case herdr:
			herdrLabel(ctx, os.Getenv("HERDR_PANE_ID"), agent, display)
		case tmux:
			tmuxLabel(ctx, os.Getenv("TMUX_PANE"), display)
		}
		return
	}
	titled := func(title, label string) bool { return label != display && showsName(title, display) }
	switch where, id := findPane(ctx, agent, titled, titleWait); where {
	case herdr:
		herdrLabel(ctx, id, agent, display)
	case tmux:
		tmuxLabel(ctx, id, display)
	}
}

// Clear removes the label Label added, when the session ends. A Codex hook that its shared daemon ran looks for the
// pane labeled display instead.
func Clear(ctx context.Context, agent Agent, display string) {
	if paneEnvTrusted(agent) {
		switch multiplexer() {
		case herdr:
			herdrClear(ctx, os.Getenv("HERDR_PANE_ID"), agent)
		case tmux:
			tmuxClear(ctx, os.Getenv("TMUX_PANE"))
		}
		return
	}
	if display == "" {
		return
	}
	labeled := func(_, label string) bool { return label == display }
	switch where, id := findPane(ctx, agent, labeled, 0); where {
	case herdr:
		herdrClear(ctx, id, agent)
	case tmux:
		tmuxClear(ctx, id)
	}
}

// findPane looks for the one pane that match picks by its terminal title and rostr label: in herdr's panes running
// agent, else in tmux's. While herdr or tmux answers without a match, it tries again for up to wait.
func findPane(ctx context.Context, agent Agent, match func(title, label string) bool, wait time.Duration) (kind, string) {
	deadline := time.Now().Add(wait)
	for {
		id, herdrOK := herdrFindPane(ctx, agent, match)
		if id != "" {
			return herdr, id
		}
		id, tmuxOK := tmuxFindPane(ctx, match)
		if id != "" {
			return tmux, id
		}
		if !herdrOK && !tmuxOK || time.Now().After(deadline) {
			return none, ""
		}
		select {
		case <-ctx.Done():
			return none, ""
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// showsName reports whether a terminal title shows display as a whole name: "🧙 Gandalf" in
// "🧙 Gandalf · Fix it | proj", but "🌱 Sam" not in "🌱 Samwise · Fix it".
func showsName(title, display string) bool {
	for i := 0; display != ""; {
		j := strings.Index(title[i:], display)
		if j < 0 {
			return false
		}
		end := i + j + len(display)
		if end == len(title) || title[end] == ' ' {
			return true
		}
		i += j + 1
	}
	return false
}

// paneEnvTrusted reports whether this hook's environment names the session's own pane. Claude Code runs hooks as its
// own children, so it always does. Codex runs them in a shared app-server daemon by default, whose environment
// belongs to whichever terminal started it (openai/codex#48500), so a Codex hook only trusts it when the Codex process
// that ran the hook is the TUI itself (`codex --no-daemon`, or `daemon_auto_start = false`).
func paneEnvTrusted(agent Agent) bool {
	return agent != Codex || ranByCodexTUI(os.Getppid(), proc.Info)
}

// ranByCodexTUI walks up from pid to the nearest Codex process, past the shell and sandbox wrappers Codex runs hooks
// and commands in, and reports whether it's a TUI rather than an app-server (the shared daemon, or the app's). info
// returns a process's parent and arguments.
func ranByCodexTUI(pid int, info func(int) (int, []string)) bool {
	for range 8 {
		ppid, args := info(pid)
		if len(args) == 0 {
			return false
		}
		if filepath.Base(args[0]) == "codex" {
			return !slices.Contains(args, "app-server")
		}
		pid = ppid
	}
	return false
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
