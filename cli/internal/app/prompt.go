package app

import (
	"errors"
	"os"

	"charm.land/huh/v2"
	"golang.org/x/term"
)

// isInteractive reports whether a person is at a terminal. Agents run commands without one, so every prompt has a
// flag or a clear error for them instead.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

var errCancelled = errors.New("cancelled")

// promptSelect asks the user to pick one option on a full screen headed by command, returning its index.
func promptSelect(command, title, description string, options []string) (int, error) {
	choices := make([]huh.Option[int], len(options))
	for i, label := range options {
		choices[i] = huh.NewOption(label, i)
	}
	var picked int
	field := huh.NewSelect[int]().Title(title).Description(description).Options(choices...).Value(&picked)
	err := newScreen(command).run([]screenStep{{field: field}})
	return picked, err
}

// promptConfirm asks a yes-or-no question on a full screen headed by command.
func promptConfirm(command, title string, initial bool) (bool, error) {
	value := initial
	err := newScreen(command).run([]screenStep{{field: huh.NewConfirm().Title(title).Value(&value)}})
	return value, err
}
