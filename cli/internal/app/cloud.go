package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/crypto"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/store"
)

func newConfigCloudCommand() *cobra.Command {
	cloud := &cobra.Command{
		Use:   "cloud",
		Short: "Move your setup to the cloud",
	}
	cloud.AddCommand(&cobra.Command{
		Use:   "on",
		Short: "Move your whole setup to the rostr API, or join one with your key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCloudOn(cmd.Context(), cmd.OutOrStdout())
		},
	})
	return cloud
}

func newConfigKeyCommand() *cobra.Command {
	var token bool
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Print your rostr key (terminal only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isInteractive() {
				return errors.New("rostr config key only runs in a terminal, so the key doesn't end up in an agent's transcript")
			}
			if !hasCloudKey() {
				return errors.New("this machine is in local mode, so there's no rostr key. `rostr config cloud on` moves your setup to the cloud")
			}
			key, err := loadCloudKey()
			if err != nil {
				return err
			}
			if !token {
				fmt.Fprintln(cmd.OutOrStdout(), key.String())
				return nil
			}
			keys, err := key.Derive()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), keys.AuthToken())
			return nil
		},
	}
	cmd.Flags().BoolVar(&token, "token", false, "print the derived auth token for the public API instead")
	return cmd
}

// runCloudOn moves this machine's setup to the cloud under a new key, or joins an existing cloud setup with a key
// pasted at a hidden prompt. Without a terminal there's no prompt, so it can only move a local setup that has rosters.
func runCloudOn(ctx context.Context, out io.Writer) error {
	if hasCloudKey() {
		return errors.New("this machine is already in cloud mode")
	}
	local, err := readLocalSetup(ctx)
	if err != nil {
		return err
	}

	if !isInteractive() {
		if len(local.Rosters) == 0 {
			return errors.New("there are no local rosters to move. To use an existing cloud setup, run `rostr config cloud on` " +
				"in a terminal or set ROSTR_KEY; for a new one, run `rostr assemble --cloud`")
		}
		return moveSetupToCloud(ctx, out, local)
	}

	// Ask for a new setup or an existing key, then for the key, on one screen. A machine with local rosters can't join
	var join bool
	var key crypto.Key
	s := newScreen("config cloud on")
	err = s.run([]screenStep{
		{field: huh.NewSelect[bool]().Title("New setup, or do you have a key?").Options(
			huh.NewOption("New setup (moves this machine's rosters to the cloud)", false),
			huh.NewOption("I have a key (from another machine)", true),
		).Value(&join)},
		{
			field: huh.NewInput().Title("Paste your rostr key (it won't be shown)").EchoMode(huh.EchoModeNone).
				Validate(s.forwardOnly(func(value string) error {
					var err error
					key, err = crypto.ParseKey(value)
					return err
				})),
			shown: func() bool { return join && len(local.Rosters) == 0 },
		},
	})
	if err != nil {
		return err
	}
	if !join {
		return moveSetupToCloud(ctx, out, local)
	}
	if len(local.Rosters) > 0 {
		return fmt.Errorf("this machine already has local rosters (%s), so it can't join another setup. "+
			"Choose a new setup to move them to the cloud, or disband them first", rosterNames(local.Rosters))
	}
	return joinCloudWithKey(ctx, out, key)
}

// moveSetupToCloud generates a key and uploads the setup with it in one request. Only once that succeeds does it save
// the key and delete the local database, so a failed upload leaves the local setup as it was.
func moveSetupToCloud(ctx context.Context, out io.Writer, setup store.Setup) error {
	if err := registerInCloud(ctx, setup); err != nil {
		return err
	}
	if len(setup.Rosters) == 0 {
		fmt.Fprintln(out, "Your setup is in the cloud now. Run `rostr assemble` to make your first roster.")
	} else {
		fmt.Fprintf(out, "Moved your setup to the cloud: %s, %s, and %s.\n",
			plural(len(setup.Rosters), "roster", "rosters"),
			plural(len(setup.Links), "linked project", "linked projects"),
			plural(len(setup.Drafts), "drafted agent", "drafted agents"))
	}
	printKeySaved(out)
	return nil
}

// registerInCloud creates a cloud owner under a new key with setup uploaded in the same request, then saves the key
// and deletes the local database.
func registerInCloud(ctx context.Context, setup store.Setup) error {
	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	cloud, err := store.NewCloud(apiURL(), key)
	if err != nil {
		return err
	}
	if err := cloud.Register(ctx, setup); err != nil {
		return fmt.Errorf("uploading to the rostr API failed, so your local setup is untouched: %w", err)
	}

	if err := saveCredentials(key); err != nil {
		return fmt.Errorf("your setup is in the cloud, but saving its key failed (your local setup is untouched): %w", err)
	}
	if err := removeLocalDatabase(); err != nil {
		return fmt.Errorf("your setup is in the cloud and its key is saved, but deleting the local database failed: %w", err)
	}
	return nil
}

// joinCloudWithKey checks an existing key with the API, and saves it.
func joinCloudWithKey(ctx context.Context, out io.Writer, key crypto.Key) error {
	cloud, err := store.NewCloud(apiURL(), key)
	if err != nil {
		return err
	}
	settings, err := cloud.Settings(ctx)
	var apiErr *store.APIError
	if errors.As(err, &apiErr) && apiErr.Code == "unauthorized" {
		return errors.New("that key isn't registered with the rostr API. Check you copied all of it from `rostr config key`")
	}
	if err != nil {
		return err
	}

	if err := saveCredentials(key); err != nil {
		return err
	}
	if err := removeLocalDatabase(); err != nil {
		return err
	}
	if settings.DefaultRoster == "" {
		fmt.Fprintln(out, "This machine is in cloud mode now. Run `rostr assemble` to make your first roster.")
	} else {
		fmt.Fprintf(out, "This machine is in cloud mode now, with your default roster %s.\n", settings.DefaultRoster)
	}
	printKeySaved(out)
	return nil
}

// readLocalSetup reads the local database's whole setup, without creating the database if there isn't one.
func readLocalSetup(ctx context.Context) (store.Setup, error) {
	if _, err := os.Stat(paths.Database()); errors.Is(err, os.ErrNotExist) {
		return store.Setup{}, nil
	}
	st, err := store.OpenSQLite(paths.Database())
	if err != nil {
		return store.Setup{}, err
	}
	defer st.Close()
	return st.Export(ctx)
}

// saveCredentials writes the rostr key where only this user can read it.
func saveCredentials(key crypto.Key) error {
	if err := os.MkdirAll(paths.ConfigDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(paths.Credentials(), []byte(key.String()+"\n"), 0o600)
}

// removeLocalDatabase deletes the local database and SQLite's files beside it.
func removeLocalDatabase() error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(paths.Database() + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func printKeySaved(out io.Writer) {
	fmt.Fprintf(out, "Your rostr key is saved in %s. To use rostr on another machine or in a cloud environment, "+
		"see it with `rostr config key`.\n", paths.Credentials())
}

func rosterNames(rosters []store.Roster) string {
	names := make([]string, len(rosters))
	for i, r := range rosters {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}
