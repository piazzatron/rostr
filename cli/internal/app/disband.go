package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

func newDisbandCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "disband [<roster>]",
		Short: "Delete a roster",
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

			// Without a name, a person picks one (and confirms it on the same screen); agents must name it
			var name string
			confirmed := yes
			switch {
			case len(args) == 1:
				name = args[0]
			case isInteractive():
				if name, err = pickRosterToDisband(ctx, st, rosters, yes); err != nil {
					return err
				}
				confirmed = true
			default:
				return missingInput("name the roster: rostr disband <roster> --yes")
			}
			r, err := st.Roster(ctx, name)
			if err != nil {
				return err
			}
			usage, err := st.Usage(ctx, r.Name)
			if err != nil {
				return err
			}

			// Ask first; agents must pass --yes
			if !confirmed {
				if !isInteractive() {
					return missingInput(fmt.Sprintf("pass --yes to disband the %s roster (%s)", r.Name, usageSummary(usage)))
				}
				confirmed, err := promptConfirm("disband", disbandQuestion(r.Name, usage), false)
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
			// The roster's first A-teamer says goodbye
			farewell := ""
			if len(r.ATeam) > 0 {
				farewell = " " + roster.Display(r.ATeam[0].Emoji, r.ATeam[0].Name, 1, r.EmojiMode) + " says bye."
			}
			fmt.Fprintf(out, "Disbanded the %s roster.%s\n", r.Name, farewell)

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

// pickRosterToDisband asks which roster to disband and, unless --yes was passed, confirms it, on one screen.
func pickRosterToDisband(ctx context.Context, st store.Store, rosters []store.Roster, yes bool) (string, error) {
	questions := map[string]string{}
	names := make([]string, len(rosters))
	for i, r := range rosters {
		usage, err := st.Usage(ctx, r.Name)
		if err != nil {
			return "", err
		}
		names[i] = r.Name
		questions[r.Name] = disbandQuestion(r.Name, usage)
	}

	var name string
	confirmed := false
	pick := huh.NewSelect[string]().Title("Which roster do you want to disband?").Options(huh.NewOptions(names...)...).Value(&name)
	confirm := huh.NewConfirm().Value(&confirmed)
	s := newScreen("disband")
	s.onFocus = func(field huh.Field) tea.Cmd {
		if field == confirm {
			confirm.Title(questions[name])
		}
		return nil
	}
	steps := []screenStep{{field: pick}}
	if !yes {
		steps = append(steps, screenStep{field: confirm})
	}
	if err := s.run(steps); err != nil {
		return "", err
	}
	if !yes && !confirmed {
		return "", errCancelled
	}
	return name, nil
}

// disbandQuestion asks to disband a roster, saying what it would affect: "Disband work? 3 agents drafted, 1 linked
// project."
func disbandQuestion(name string, usage store.Usage) string {
	return fmt.Sprintf("Disband %s? %s.", name, usageSummary(usage))
}

func usageSummary(usage store.Usage) string {
	return fmt.Sprintf("%s drafted, %s", plural(usage.Drafted, "agent", "agents"),
		plural(usage.Links, "linked project", "linked projects"))
}
