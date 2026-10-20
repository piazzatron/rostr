package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/spf13/cobra"
)

func newDisbandCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "disband <roster>",
		Short: "Delete a roster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			if _, err := requireRosters(ctx, st); err != nil {
				return err
			}
			r, err := st.Roster(ctx, args[0])
			if err != nil {
				return err
			}
			usage, err := st.Usage(ctx, r.Name)
			if err != nil {
				return err
			}

			// Ask first; agents must pass --yes
			summary := fmt.Sprintf("%s drafted, %s", plural(usage.Drafted, "agent", "agents"),
				plural(usage.Links, "linked project", "linked projects"))
			if !yes {
				if !isInteractive() {
					return fmt.Errorf("pass --yes to disband the %s roster (%s)", r.Name, summary)
				}
				confirmed, err := promptConfirm(fmt.Sprintf("Disband %s? %s.", r.Name, summary), false)
				if err != nil {
					return err
				}
				if !confirmed {
					return errCancelled
				}
			}

			result, err := st.Disband(ctx, r.Name)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Disbanded the %s roster.\n", r.Name)

			// The server deleted the whole cloud setup with the last roster, so this machine goes back to local mode
			if result.CloudSetupDeleted {
				if err := os.Remove(paths.Credentials()); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				fmt.Fprintln(out, "That was your last roster, so your cloud setup is deleted and this machine is back to local mode.")
				if os.Getenv("ROSTR_KEY") != "" {
					fmt.Fprintln(out, "Unset ROSTR_KEY too: the key no longer works.")
				}
				fmt.Fprintln(out, "Run `rostr assemble` to start again.")
				return nil
			}
			switch {
			case result.WasDefault && result.NewDefault != "":
				fmt.Fprintf(out, "%s is your default roster now.\n", result.NewDefault)
			case result.WasDefault:
				fmt.Fprintln(out, "That was your last roster. Run `rostr assemble` to start again.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "don't ask first")
	return cmd
}
