package app

import (
	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/presets"
)

func newPresetsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "presets",
		Short: "List the pre-built rosters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rows := [][]string{}
			for _, p := range presets.All() {
				rows = append(rows, []string{p.ID, p.Emoji, p.Name, p.Source})
			}
			writeTable(cmd.OutOrStdout(), "", rows)
			return nil
		},
	}
}
