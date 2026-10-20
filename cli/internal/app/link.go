package app

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLinkCommand() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "link [<roster>]",
		Short: "Use a roster for the current project (bare: show and change which one it uses)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			rosters, err := requireRosters(ctx, st)
			if err != nil {
				return err
			}
			repo, err := targetRepo(repoFlag)
			if err != nil {
				return err
			}

			// Link the named roster
			if len(args) == 1 {
				r, err := st.Roster(ctx, args[0])
				if err != nil {
					return err
				}
				if err := st.LinkProject(ctx, repo, r.Name); err != nil {
					return err
				}
				fmt.Fprintf(out, "Linked %s to the %s roster.\n", repo, r.Name)
				return nil
			}

			// Bare link: say which roster the project uses and why, then let a person pick another
			linked, err := st.ProjectLink(ctx, repo)
			if err != nil {
				return err
			}
			settings, err := st.Settings(ctx)
			if err != nil {
				return err
			}
			current := fmt.Sprintf("%s uses the %s roster (your default).", repo, settings.DefaultRoster)
			if linked != "" {
				current = fmt.Sprintf("%s uses the %s roster (linked).", repo, linked)
			}
			fmt.Fprintln(out, current)
			if !isInteractive() {
				return nil
			}

			options := []string{"Keep it"}
			for _, r := range rosters {
				options = append(options, "Link "+r.Name)
			}
			if linked != "" {
				options = append(options, fmt.Sprintf("Unlink (use your default, %s)", settings.DefaultRoster))
			}
			picked, err := promptSelect("link", "Change it?", current, options)
			if err != nil {
				return err
			}
			switch {
			case picked == 0:
				return nil
			case picked <= len(rosters):
				if err := st.LinkProject(ctx, repo, rosters[picked-1].Name); err != nil {
					return err
				}
				fmt.Fprintf(out, "Linked %s to the %s roster.\n", repo, rosters[picked-1].Name)
			default:
				if _, err := st.UnlinkProject(ctx, repo); err != nil {
					return err
				}
				fmt.Fprintf(out, "Unlinked %s. It uses your default roster (%s).\n", repo, settings.DefaultRoster)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "the project to link, as owner/repo (defaults to the current repo)")
	return cmd
}

func newUnlinkCommand() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "unlink",
		Short: "Go back to the default roster for the current project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			if _, err := requireRosters(ctx, st); err != nil {
				return err
			}
			repo, err := targetRepo(repoFlag)
			if err != nil {
				return err
			}
			removed, err := st.UnlinkProject(ctx, repo)
			if err != nil {
				return err
			}
			settings, err := st.Settings(ctx)
			if err != nil {
				return err
			}
			if removed {
				fmt.Fprintf(cmd.OutOrStdout(), "Unlinked %s. It uses your default roster (%s).\n", repo, settings.DefaultRoster)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s isn't linked. It uses your default roster (%s).\n", repo, settings.DefaultRoster)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "the project to unlink, as owner/repo (defaults to the current repo)")
	return cmd
}
