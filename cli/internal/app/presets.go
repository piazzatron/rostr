package app

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/presets"
	"github.com/piazzatron/rostr/cli/internal/roster"
)

// presetListing is a preset in `rostr presets --json`. Members are in draft order: a team of n takes the first n.
type presetListing struct {
	ID      string          `json:"id"`
	Emoji   string          `json:"emoji"`
	Name    string          `json:"name"`
	Source  string          `json:"source"`
	Members []roster.Member `json:"members"`
}

func newPresetsCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "presets",
		Short: "List the pre-built rosters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			refreshPresets(cmd.Context())
			if asJSON {
				listing := []presetListing{}
				for _, p := range presets.All() {
					listing = append(listing, presetListing{ID: p.ID, Emoji: p.Emoji, Name: p.Name, Source: p.Source, Members: p.Members})
				}
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(map[string]any{"presets": listing})
			}
			rows := [][]string{}
			for _, p := range presets.All() {
				rows = append(rows, []string{p.ID, p.Emoji, p.Name, p.Source})
			}
			writeTable(cmd.OutOrStdout(), "", rows, styleAccent, stylePlain, styleBold, styleMuted)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, `print {"presets": [{"id", "emoji", "name", "source", "members"}]}`)
	return cmd
}
