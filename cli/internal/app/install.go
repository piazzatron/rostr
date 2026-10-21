package app

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/paths"
)

// newInstallCommand is the hidden `rostr install`, which installs the Claude Code hooks without assembling a roster.
// The install script runs it in Claude Code cloud sessions, whose containers start without the hooks; people set up
// rostr with `assemble`, so it stays out of the command list.
func newInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "install",
		Short:  "Install the Claude Code hooks",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			installed, err := claudecode.Install(paths.ClaudeDir())
			if err != nil {
				return err
			}
			if len(installed) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Installed for Claude Code: %s.\n", strings.Join(installed, ", "))
			}
			return nil
		},
	}
}
