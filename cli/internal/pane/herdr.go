package pane

import (
	"context"
	"os"
)

// herdrLabel replaces the agent name herdr shows for the pane ("claude") with the rostr name. The metadata is
// display-only, so herdr's own agent tracking is untouched, and --agent limits it to while Claude owns the pane.
func herdrLabel(ctx context.Context, display string) {
	_, _ = run(ctx, herdrBinary(), "pane", "report-metadata", os.Getenv("HERDR_PANE_ID"),
		"--source", "rostr", "--agent", "claude", "--display-agent", display)
}

// herdrClear gives the pane back herdr's own agent name.
func herdrClear(ctx context.Context) {
	_, _ = run(ctx, herdrBinary(), "pane", "report-metadata", os.Getenv("HERDR_PANE_ID"),
		"--source", "rostr", "--agent", "claude", "--clear-display-agent")
}

// herdrBinary is the herdr that runs the pane, which herdr exports as HERDR_BIN_PATH, or else herdr on PATH.
func herdrBinary() string {
	if path := os.Getenv("HERDR_BIN_PATH"); path != "" {
		return path
	}
	return "herdr"
}
