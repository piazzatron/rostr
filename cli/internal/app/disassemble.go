package app

import (
	"fmt"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/spf13/cobra"
)

// newDisassembleCommand removes what `assemble` installed into Claude Code. Rosters stay; `disband` removes those.
func newDisassembleCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "disassemble",
		Aliases: []string{"uninstall"},
		Short:   "Remove rostr's hooks, skill, and /draft from Claude Code",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			// Ask first; agents must pass --yes
			if !yes {
				if !isInteractive() {
					return missingInput("pass --yes to remove rostr's hooks, skill, and /draft from Claude Code")
				}
				confirmed, err := promptConfirm("disassemble", "Remove rostr's hooks, skill, and /draft from Claude Code? Your rosters stay.", false)
				if err != nil {
					return err
				}
				if !confirmed {
					return errCancelled
				}
			}

			removed, err := claudecode.Uninstall(paths.ClaudeDir())
			if err != nil {
				return err
			}
			if len(removed) == 0 {
				fmt.Fprintln(out, "Nothing to remove: rostr isn't installed in Claude Code.")
				return nil
			}
			for _, piece := range removed {
				fmt.Fprintf(out, "Removed %s\n", piece)
			}
			fmt.Fprintln(out, "Sessions you start from now on won't be drafted. Your rosters are still there: `rostr assemble` puts the hooks back, and `rostr disband` deletes a roster.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask first")
	return cmd
}
