package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// editedRoster is the JSON `rostr edit` opens in $EDITOR.
type editedRoster struct {
	EmojiMode roster.EmojiMode `json:"emojiMode"`
	ATeam     []roster.Member  `json:"aTeam"`
	BTeam     []roster.Member  `json:"bTeam"`
}

func newEditCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "edit [<roster>]",
		Short: "Edit a roster's members, emoji, and A team in $EDITOR",
		Args:  cobra.MaximumNArgs(1),
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
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				r, _, err := resolveRoster(ctx, st, "", workingDir())
				if err != nil {
					return err
				}
				name = r.Name
			}
			return editRoster(ctx, cmd.OutOrStdout(), st, name)
		},
	}
}

// editRoster opens the roster in $VISUAL or $EDITOR and saves it when the editor closes. An invalid edit reopens the
// editor for a person at a terminal, and is an error otherwise.
func editRoster(ctx context.Context, out io.Writer, st store.Store, name string) error {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		if !isInteractive() {
			return errors.New("rostr edit opens $EDITOR, which needs a terminal. Ask the user to run it")
		}
		editor = "vi"
	}

	r, err := st.Roster(ctx, name)
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(editedRoster{EmojiMode: r.EmojiMode, ATeam: r.ATeam, BTeam: r.BTeam}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "rostr-edit-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(content, '\n')); err != nil {
		return err
	}
	file.Close()

	for {
		saveErr := runEditorAndSave(ctx, st, r.Name, editor, file.Name())
		if saveErr == nil {
			fmt.Fprintf(out, "Saved the %s roster.\n", r.Name)
			return nil
		}
		if !isInteractive() {
			return saveErr
		}
		fmt.Fprintln(out, "Not saved:", saveErr)
		again, err := promptConfirm("edit", "Open the editor again?", true)
		if err != nil {
			return err
		}
		if !again {
			return errCancelled
		}
	}
}

func runEditorAndSave(ctx context.Context, st store.Store, name, editor, path string) error {
	// $EDITOR can carry arguments ("code --wait"), so let the shell split it
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the editor failed: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var edited editedRoster
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edited); err != nil {
		return fmt.Errorf("that isn't valid roster JSON: %w", err)
	}
	mode, err := roster.ParseEmojiMode(string(edited.EmojiMode))
	if err != nil {
		return err
	}
	return st.EditRoster(ctx, name, mode, edited.ATeam, edited.BTeam)
}
