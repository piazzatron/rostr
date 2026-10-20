package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/codex"
	"github.com/piazzatron/rostr/cli/internal/paths"
)

// newUninstallCommand undoes `assemble` completely: it deletes every roster (in the cloud, that deletes the whole
// cloud setup) and removes what was installed into Claude Code and Codex.
func newUninstallCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove rostr from Claude Code and Codex and delete all your rosters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			// Ask first; agents must pass --yes
			if !yes {
				if !isInteractive() {
					return missingInput("pass --yes to remove rostr from Claude Code and Codex and delete all your rosters")
				}
				confirmed, err := promptConfirm("uninstall",
					"Remove rostr from Claude Code and Codex and delete all your rosters? This can't be undone.", false)
				if err != nil {
					return err
				}
				if !confirmed {
					return errCancelled
				}
			}

			// Delete the rosters first, so if that fails (say the API is unreachable) nothing is half-removed. In the
			// cloud, disbanding the last roster deletes the whole setup
			st, err := openStore()
			if err != nil {
				return err
			}
			rosters, err := st.Rosters(ctx)
			if err == nil {
				for _, r := range rosters {
					if _, err = st.Disband(ctx, r.Name); err != nil {
						break
					}
				}
			}
			st.Close()
			if err != nil {
				return err
			}

			// Then the files this machine keeps: the key or the local database, and the keep-alive marks
			cloud := hasCloudKey()
			files := []string{paths.Credentials(), paths.Database(), paths.Database() + "-wal", paths.Database() + "-shm"}
			for _, path := range files {
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			if err := os.RemoveAll(paths.KeepAliveDir()); err != nil {
				return err
			}

			// And what assemble installed into Claude Code and Codex
			removed, err := claudecode.Uninstall(paths.ClaudeDir())
			if err != nil {
				return err
			}
			removedCodex, err := codex.Uninstall(paths.CodexDir(), paths.AgentsSkillsDir())
			if err != nil {
				return err
			}

			if len(rosters) > 0 {
				fmt.Fprintf(out, "Deleted %s.\n", plural(len(rosters), "roster", "rosters"))
			}
			for _, piece := range removed {
				fmt.Fprintf(out, "Removed %s\n", piece)
			}
			for _, piece := range removedCodex {
				fmt.Fprintf(out, "Removed %s (Codex)\n", piece)
			}
			if len(rosters) == 0 && len(removed) == 0 && len(removedCodex) == 0 {
				fmt.Fprintln(out, "Nothing to remove: rostr isn't set up here.")
				return nil
			}
			if cloud && os.Getenv("ROSTR_KEY") != "" {
				fmt.Fprintln(out, "Your cloud setup is deleted. Unset ROSTR_KEY too: the key no longer works.")
			}
			fmt.Fprintln(out, "rostr is uninstalled. Run `rostr assemble` to start again.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask first")
	return cmd
}
