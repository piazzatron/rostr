package app

import (
	"errors"
	"os"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// isInteractive reports whether a person is at a terminal. Agents run commands without one, so every prompt has a
// flag or a clear error for them instead.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

var errCancelled = errors.New("cancelled")

// promptSelect asks the user to pick one option, returning its index.
func promptSelect(title string, options []string) (int, error) {
	choices := make([]huh.Option[int], len(options))
	for i, label := range options {
		choices[i] = huh.NewOption(label, i)
	}
	var picked int
	err := huh.NewSelect[int]().Title(title).Options(choices...).Value(&picked).Run()
	return picked, promptError(err)
}

// promptInput asks for text, starting from initial, until validate accepts it.
func promptInput(title, initial string, validate func(string) error) (string, error) {
	value := initial
	err := huh.NewInput().Title(title).Value(&value).Validate(validate).Run()
	return value, promptError(err)
}

// promptSecret asks for text without showing what's typed, until validate accepts it.
func promptSecret(title string, validate func(string) error) (string, error) {
	var value string
	err := huh.NewInput().Title(title).EchoMode(huh.EchoModeNone).Value(&value).Validate(validate).Run()
	return value, promptError(err)
}

func promptConfirm(title string, initial bool) (bool, error) {
	value := initial
	err := huh.NewConfirm().Title(title).Value(&value).Run()
	return value, promptError(err)
}

func promptError(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return errCancelled
	}
	return err
}
