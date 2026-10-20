package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// configJSON is what `rostr config --json` prints.
type configJSON struct {
	Storage   string            `json:"storage"`
	Default   string            `json:"default"`
	AutoDraft bool              `json:"autoDraft"`
	Expiry    string            `json:"expiry"`
	Emoji     map[string]string `json:"emoji"` // roster name → emoji mode
}

func newConfigCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			settings, err := st.Settings(ctx)
			if err != nil {
				return err
			}
			rosters, err := st.Rosters(ctx)
			if err != nil {
				return err
			}
			storage, storageDetail := "local", "local ("+paths.Database()+")"
			if hasCloudKey() {
				storage, storageDetail = "cloud", "cloud ("+apiURL()+")"
			}
			config := configJSON{
				Storage: storage, Default: settings.DefaultRoster, AutoDraft: settings.AutoDraft,
				Expiry: settings.Expiry, Emoji: map[string]string{},
			}
			emoji := make([]string, len(rosters))
			for i, r := range rosters {
				config.Emoji[r.Name] = string(r.EmojiMode)
				emoji[i] = r.Name + ": " + string(r.EmojiMode)
			}

			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(config)
			}
			writeTable(cmd.OutOrStdout(), "", [][]string{
				{"Storage", storageDetail},
				{"Default", orDash(config.Default)},
				{"Auto-draft", onOff(config.AutoDraft)},
				{"Expiry", config.Expiry},
				{"Emoji", orDash(strings.Join(emoji, ", "))},
			})
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print settings as JSON")
	cmd.AddCommand(
		newConfigDefaultCommand(),
		newConfigEmojiCommand(),
		newConfigAutoDraftCommand(),
		newConfigExpiryCommand(),
		newConfigCloudCommand(),
		newConfigKeyCommand(),
	)
	return cmd
}

func newConfigDefaultCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "default <roster>",
		Short: "Choose the roster projects use when they don't have a link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(st store.Store) error {
				if _, err := requireRosters(cmd.Context(), st); err != nil {
					return err
				}
				r, err := st.Roster(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if err := st.SetDefaultRoster(cmd.Context(), r.Name); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s is your default roster now.\n", r.Name)
				return nil
			})
		},
	}
}

func newConfigEmojiCommand() *cobra.Command {
	var rosterFlag string
	cmd := &cobra.Command{
		Use:   "emoji on|off|only",
		Short: "Show the current roster's agents with emoji, without, or as just the emoji",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := roster.ParseEmojiMode(args[0])
			if err != nil {
				return err
			}
			return withStore(func(st store.Store) error {
				if _, err := requireRosters(cmd.Context(), st); err != nil {
					return err
				}
				r, _, err := resolveRoster(cmd.Context(), st, rosterFlag, workingDir())
				if err != nil {
					return err
				}
				if err := st.SetEmojiMode(cmd.Context(), r.Name, mode); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "The %s roster's emoji mode is %s now.\n", r.Name, mode)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&rosterFlag, "roster", "", "change this roster instead of the project's")
	return cmd
}

func newConfigAutoDraftCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "auto-draft on|off",
		Short: "Draft every new session automatically, or only with /draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			on, err := parseOnOff(args[0])
			if err != nil {
				return err
			}
			return withStore(func(st store.Store) error {
				if err := st.SetAutoDraft(cmd.Context(), on); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Auto-draft is %s.\n", onOff(on))
				return nil
			})
		},
	}
}

func newConfigExpiryCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "expiry <duration>",
		Short: "How long a draft can go unseen before it's released (48h, 7d, 2w)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(func(st store.Store) error {
				if err := st.SetExpiry(cmd.Context(), args[0]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Drafts unseen for %s are released.\n", strings.ToLower(args[0]))
				return nil
			})
		},
	}
}

func withStore(fn func(st store.Store) error) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	return fn(st)
}

func parseOnOff(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("expected on or off (got %q)", s)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
