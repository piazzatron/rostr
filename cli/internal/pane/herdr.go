package pane

import (
	"context"
	"os"
)

// herdrLabel replaces the agent name herdr shows for the pane ("claude" or "codex") with the rostr name. The metadata
// is display-only, so herdr's own agent tracking is untouched, and --agent limits it to while that agent owns the pane.
func herdrLabel(ctx context.Context, agent Agent, display string) {
	_, _ = run(ctx, herdrBinary(), "pane", "report-metadata", os.Getenv("HERDR_PANE_ID"),
		"--source", "rostr", "--agent", string(agent), "--display-agent", display)
}

// herdrClear gives the pane back herdr's own agent name.
func herdrClear(ctx context.Context, agent Agent) {
	_, _ = run(ctx, herdrBinary(), "pane", "report-metadata", os.Getenv("HERDR_PANE_ID"),
		"--source", "rostr", "--agent", string(agent), "--clear-display-agent")
}

// herdrBinary is the herdr that runs the pane, which herdr exports as HERDR_BIN_PATH, or else herdr on PATH.
func herdrBinary() string {
	if path := os.Getenv("HERDR_BIN_PATH"); path != "" {
		return path
	}
	return "herdr"
}
