package pane

import (
	"context"
	"encoding/json"
	"os"
)

// herdrLabel replaces the agent name herdr shows for the pane ("claude" or "codex") with the rostr name. The metadata
// is display-only, so herdr's own agent tracking is untouched, and --agent limits it to while that agent owns the pane.
func herdrLabel(ctx context.Context, paneID string, agent Agent, display string) {
	_, _ = run(ctx, herdrBinary(), "pane", "report-metadata", paneID,
		"--source", "rostr", "--agent", string(agent), "--display-agent", display)
}

// herdrClear gives the pane back herdr's own agent name.
func herdrClear(ctx context.Context, paneID string, agent Agent) {
	_, _ = run(ctx, herdrBinary(), "pane", "report-metadata", paneID,
		"--source", "rostr", "--agent", string(agent), "--clear-display-agent")
}

// herdrFindPane returns the one pane running agent whose terminal title and display name match, or "" if there's no
// such pane or more than one. ok is false if no herdr server answered.
func herdrFindPane(ctx context.Context, agent Agent, match func(title, label string) bool) (id string, ok bool) {
	out, err := run(ctx, herdrBinary(), "pane", "list")
	if err != nil {
		return "", false
	}
	var list struct {
		Result struct {
			Panes []struct {
				PaneID        string `json:"pane_id"`
				Agent         string `json:"agent"`
				TerminalTitle string `json:"terminal_title"`
				DisplayAgent  string `json:"display_agent"`
			} `json:"panes"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return "", false
	}
	for _, p := range list.Result.Panes {
		if p.Agent == string(agent) && match(p.TerminalTitle, p.DisplayAgent) {
			if id != "" {
				return "", true
			}
			id = p.PaneID
		}
	}
	return id, true
}

// herdrBinary is the herdr that runs the pane, which herdr exports as HERDR_BIN_PATH, or else herdr on PATH.
func herdrBinary() string {
	if path := os.Getenv("HERDR_BIN_PATH"); path != "" {
		return path
	}
	return "herdr"
}
