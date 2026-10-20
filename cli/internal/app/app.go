// Package app is the rostr command line: every command, flag, and prompt, on top of the store.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/crypto"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/project"
	"github.com/piazzatron/rostr/cli/internal/store"
)

var (
	errNoRosters = store.ErrNoRosters
	errNoSession = errors.New("no session: pass --session, or run this inside a Claude Code session, where ROSTR_SESSION is set")
)

// defaultAPIURL is the rostr API that cloud mode calls. ROSTR_API_URL overrides it (tests point it at a local server).
const defaultAPIURL = "https://api.getrostr.sh"

// Execute runs rostr and returns its exit code.
func Execute() int {
	root := newRootCommand()
	err := root.Execute()

	var exit exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.code
	default:
		fmt.Fprintln(os.Stderr, "rostr:", err)
		return 1
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "rostr",
		Short:         "Give your agents names and remember who's doing what",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(
		newAssembleCommand("assemble", false),
		newAssembleCommand("create", true),
		newLinkCommand(),
		newUnlinkCommand(),
		newListCommand(),
		newEditCommand(),
		newPresetsCommand(),
		newDisbandCommand(),
		newAgentCommand(),
		newConfigCommand(),
		newHookCommand(),
	)
	return root
}

// exitError ends rostr with a specific exit code and no message, for hooks whose exit code means something.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// openStore opens the storage for this machine's setup: the local database, unless a rostr key puts it in cloud mode.
func openStore() (store.Store, error) {
	if !hasCloudKey() {
		return store.OpenSQLite(paths.Database())
	}
	key, err := loadCloudKey()
	if err != nil {
		return nil, err
	}
	return store.NewCloud(apiURL(), key)
}

// hasCloudKey reports whether this machine has a rostr key, which is what puts it in cloud mode.
func hasCloudKey() bool {
	if os.Getenv("ROSTR_KEY") != "" {
		return true
	}
	_, err := os.Stat(paths.Credentials())
	return err == nil
}

// loadCloudKey reads the rostr key from ROSTR_KEY, or else the credentials file.
func loadCloudKey() (crypto.Key, error) {
	if value := os.Getenv("ROSTR_KEY"); value != "" {
		key, err := crypto.ParseKey(value)
		if err != nil {
			return key, fmt.Errorf("ROSTR_KEY: %w", err)
		}
		return key, nil
	}
	data, err := os.ReadFile(paths.Credentials())
	if err != nil {
		return crypto.Key{}, err
	}
	key, err := crypto.ParseKey(string(data))
	if err != nil {
		return key, fmt.Errorf("%s: %w", paths.Credentials(), err)
	}
	return key, nil
}

func apiURL() string {
	if url := os.Getenv("ROSTR_API_URL"); url != "" {
		return url
	}
	return defaultAPIURL
}

func requireRosters(ctx context.Context, st store.Store) ([]store.Roster, error) {
	rosters, err := st.Rosters(ctx)
	if err == nil && len(rosters) == 0 {
		err = errNoRosters
	}
	return rosters, err
}

// rosterSource says why a roster was picked.
type rosterSource string

const (
	sourceFlag    rosterSource = "--roster"
	sourceLinked  rosterSource = "linked"
	sourceDefault rosterSource = "your default"
)

// resolveRoster picks the roster to act on: the --roster flag, else the project's linked roster, else the default.
func resolveRoster(ctx context.Context, st store.Store, flag, dir string) (store.Roster, rosterSource, error) {
	if flag != "" {
		r, err := st.Roster(ctx, flag)
		return r, sourceFlag, err
	}

	if repo, ok := project.Identify(dir); ok {
		linked, err := st.ProjectLink(ctx, repo)
		if err != nil {
			return store.Roster{}, "", err
		}
		if linked != "" {
			r, err := st.Roster(ctx, linked)
			return r, sourceLinked, err
		}
	}

	settings, err := st.Settings(ctx)
	if err != nil {
		return store.Roster{}, "", err
	}
	if settings.DefaultRoster == "" {
		return store.Roster{}, "", errNoRosters
	}
	r, err := st.Roster(ctx, settings.DefaultRoster)
	return r, sourceDefault, err
}

// targetRepo is the project a command acts on: --repo (normalized) if given, else the current directory's.
func targetRepo(repoFlag string) (string, error) {
	if repoFlag != "" {
		return project.NormalizeOrigin(repoFlag), nil
	}
	if repo, ok := project.Identify(workingDir()); ok {
		return repo, nil
	}
	return "", errors.New("not in a git repo, so there's no project here. Run this inside a repo, or pass --repo owner/repo")
}

// sessionFlag registers --session, which defaults to $ROSTR_SESSION.
func sessionFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "session", os.Getenv("ROSTR_SESSION"),
		"the agent session's ID (defaults to $ROSTR_SESSION)")
}

func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, pluralForm)
}
