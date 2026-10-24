package pane

import (
	"context"
	"strings"
)

// tmuxLabel tags the pane with a @rostr user option, and names the window after the session when the window is the
// session's alone: one pane, and a name tmux picks automatically (or one rostr gave it), so a name the user chose is
// kept. @rostr_renamed records that rostr renamed it, so tmuxClear can hand naming back to tmux. Every command targets
// the pane, since without -t tmux acts on whichever pane is active.
func tmuxLabel(ctx context.Context, target, display string) {
	window, err := run(ctx, "tmux",
		"set-option", "-p", "-t", target, "@rostr", display, ";",
		"display-message", "-p", "-t", target, "#{window_panes} #{||:#{automatic-rename},#{@rostr_renamed}}")
	if err != nil || window != "1 1" {
		return
	}
	_, _ = run(ctx, "tmux",
		"rename-window", "-t", target, display, ";",
		"set-option", "-w", "-t", target, "@rostr_renamed", "1")
}

// tmuxClear removes the pane's @rostr tag and, if rostr renamed the window, turns automatic-rename back on.
func tmuxClear(ctx context.Context, target string) {
	_, _ = run(ctx, "tmux",
		"set-option", "-p", "-u", "-t", target, "@rostr", ";",
		"if-shell", "-F", "-t", target, "#{@rostr_renamed}",
		"set-option -w -t "+target+" automatic-rename on ; set-option -w -u -t "+target+" @rostr_renamed")
}

// tmuxFindPane returns the one pane, across the tmux server's sessions, whose title (Codex's terminal title) and
// @rostr tag match, or "" if there's no such pane or more than one. ok is false if no tmux server answered.
func tmuxFindPane(ctx context.Context, match func(title, label string) bool) (id string, ok bool) {
	out, err := run(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{@rostr}\t#{pane_title}")
	if err != nil {
		return "", false
	}
	for line := range strings.Lines(out) {
		pane, rest, _ := strings.Cut(strings.TrimRight(line, "\n"), "\t")
		label, title, _ := strings.Cut(rest, "\t")
		if match(title, label) {
			if id != "" {
				return "", true
			}
			id = pane
		}
	}
	return id, true
}
