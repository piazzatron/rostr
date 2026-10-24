package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/store"
)

func newEditCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "edit [<roster>]",
		Short: "Edit a roster's members and emoji (terminal only)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			existing, err := requireRosters(ctx, st)
			if err != nil {
				return err
			}
			if !isInteractive() {
				return errors.New("rostr edit opens a full-screen editor, which needs a terminal. Ask the user to run it")
			}
			var r store.Roster
			if len(args) == 1 {
				r, err = st.Roster(ctx, args[0])
			} else {
				r, _, err = resolveRoster(ctx, st, "", workingDir())
			}
			if err != nil {
				return err
			}

			flow, err := newAssembleFlow(assembleSetup{existing: existing, edit: r.Name})
			if err != nil {
				return err
			}
			if err := flow.ask(ctx, st); err != nil {
				return err
			}
			return saveEdit(ctx, cmd.OutOrStdout(), st, flow)
		},
	}
}

// saveEdit saves the roster as changed on the cast page and the last page. It refuses to drop or rename a member
// who's drafted.
func saveEdit(ctx context.Context, out io.Writer, st store.Store, flow *assembleFlow) error {
	edited := flow.chosenCast()
	if err := st.EditRoster(ctx, flow.roster, flow.emoji, edited.members); err != nil {
		return fmt.Errorf("not saved: %w", err)
	}
	fmt.Fprintf(out, "Saved the %s roster.\n", flow.roster)
	return nil
}
