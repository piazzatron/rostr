// Package app is the rostr command line: every command, flag, and prompt, on top of the store.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/crypto"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/project"
	"github.com/piazzatron/rostr/cli/internal/store"
)

var (
	errNoRosters = store.ErrNoRosters
	errNoSession = missingInput("no session: pass --session, or run this inside a Claude Code or Codex session, where ROSTR_SESSION or CODEX_THREAD_ID is set")
)

// defaultAPIURL is the rostr API that cloud mode calls. ROSTR_API_URL overrides it (tests point it at a local server).
const defaultAPIURL = "https://api.getrostr.sh"

// Execute runs rostr and returns its exit code.
func Execute() int {
	root := newRootCommand()
	cmd, err := root.ExecuteC()

	var missing errMissingInput
	switch {
	case err == nil:
		return 0
	case errors.As(err, &missing):
		fmt.Fprintf(os.Stderr, "%s %s\n\n%s", styleErrorPrefix.Render("rostr:"), err, cmd.UsageString())
		return 1
	default:
		fmt.Fprintln(os.Stderr, styleErrorPrefix.Render("rostr:")+" "+err.Error())
		return 1
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "rostr",
		Short:         "Give your agents names and remember who's doing what",
		Version:       versionString(),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("rostr {{.Version}}\n")
	// Shell completion still works (the Homebrew formula installs it) but stays out of the command list
	root.CompletionOptions.HiddenDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return missingInput(err.Error()) })
	root.AddCommand(
		newAssembleCommand("assemble", false),
		newAssembleCommand("create", true),
		newLinkCommand(),
		newUnlinkCommand(),
		newListCommand(),
		newEditCommand(),
		newPresetsCommand(),
		newDisbandCommand(),
		newUninstallCommand(),
		newCloudCommand(),
		newDraftCommand(),
		newBenchCommand(),
		newTaskCommand(),
		newConfigCommand(),
		newHookCommand(),
		newVersionCommand(),
	)
	wrapArgsWithUsage(root)
	return root
}

// errMissingInput is a command run without the arguments or flags it needs (wrong argument count, a bad flag, or a
// prompt it can't ask without a terminal). Execute prints the command's usage after it.
type errMissingInput struct{ message string }

func (e errMissingInput) Error() string { return e.message }

func missingInput(message string) error { return errMissingInput{message} }

// wrapArgsWithUsage makes every command's argument-count errors missing-input errors, so they print usage too.
func wrapArgsWithUsage(cmd *cobra.Command) {
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := validate(c, args); err != nil {
				return missingInput(err.Error())
			}
			return nil
		}
	}
	for _, child := range cmd.Commands() {
		wrapArgsWithUsage(child)
	}
}

// openStore opens the storage for this machine's setup: the local database, unless a rostr key puts it in cloud mode.
func openStore() (store.Store, error) {
	if !hasCloudKey() {
		return store.OpenSQLite(paths.Database())
	}
	key, err := loadCloudKey()
	if err != nil {
		return nil, err
	}
	return newCloudStore(key)
}

// newCloudStore is the cloud store for this machine: with its local cache, and its machine ID so the API reserves
// its next picks.
func newCloudStore(key crypto.Key) (*store.Cloud, error) {
	cloud, err := store.NewCloud(apiURL(), key)
	if err != nil {
		return nil, err
	}
	// A cloud session's container is a machine that drafts once and is thrown away, so it gets no reservation, which
	// would only hold a name for a day
	if os.Getenv("CLAUDE_CODE_REMOTE") != "true" {
		cloud.MachineID = machineID()
	}
	cloud.UseCache(paths.CloudCacheDir(), spawnConfirmDraft)
	return cloud, nil
}

// machineID is this machine's random ID, made on first use. Without one (an unwritable config dir) the API just
// reserves nothing for this machine.
func machineID() string {
	if id, err := os.ReadFile(paths.MachineID()); err == nil {
		return strings.TrimSpace(string(id))
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	id := hex.EncodeToString(b)
	if err := os.MkdirAll(paths.ConfigDir(), 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(paths.MachineID(), []byte(id+"\n"), 0o600); err != nil {
		return ""
	}
	return id
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

// sessionFlag registers --session, which defaults to $ROSTR_SESSION (written by the Claude Code hooks), then
// $CODEX_THREAD_ID (which Codex gives every command its agent runs, and which is its hooks' session ID).
func sessionFlag(cmd *cobra.Command, target *string) {
	session := os.Getenv("ROSTR_SESSION")
	if session == "" {
		session = os.Getenv("CODEX_THREAD_ID")
	}
	cmd.Flags().StringVar(target, "session", session,
		"the agent session's ID (defaults to $ROSTR_SESSION, then $CODEX_THREAD_ID)")
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
