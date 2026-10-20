package pane

import (
	"context"
	"os"
)

// tmuxLabel tags the pane with a @rostr user option, and names the window after the session when the window is the
// session's alone: one pane, and a name tmux picks automatically (or one rostr gave it), so a name the user chose is
// kept. @rostr_renamed records that rostr renamed it, so tmuxClear can hand naming back to tmux. Every command targets
// $TMUX_PANE, since without -t tmux acts on whichever pane is active.
func tmuxLabel(ctx context.Context, display string) {
	target := os.Getenv("TMUX_PANE")
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
func tmuxClear(ctx context.Context) {
	target := os.Getenv("TMUX_PANE")
	_, _ = run(ctx, "tmux",
		"set-option", "-p", "-u", "-t", target, "@rostr", ";",
		"if-shell", "-F", "-t", target, "#{@rostr_renamed}",
		"set-option -w -t "+target+" automatic-rename on ; set-option -w -u -t "+target+" @rostr_renamed")
}
