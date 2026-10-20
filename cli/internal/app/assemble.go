package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/presets"
	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

type assembleOptions struct {
	preset      string
	names       string
	emoji       string
	cloud       bool
	noAutoDraft bool
}

// cast is a chosen set of members, from a preset or the user's own names.
type cast struct {
	presetID string
	aTeam    []roster.Member
	bTeam    []roster.Member
}

func newAssembleCommand(use string, hidden bool) *cobra.Command {
	var opts assembleOptions
	cmd := &cobra.Command{
		Use:    use + " [<roster>]",
		Short:  "Set up a roster (interactive, or with flags)",
		Hidden: hidden,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssemble(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.preset, "preset", "", "use a pre-built roster (see `rostr presets`)")
	cmd.Flags().StringVar(&opts.names, "names", "", `comma-separated names, each optionally with an emoji ("🧮 Ada, Grace")`)
	cmd.Flags().StringVar(&opts.emoji, "emoji", "", "the roster's emoji mode: on, off, or only (default on)")
	cmd.Flags().BoolVar(&opts.cloud, "cloud", false, "put the setup in the cloud (first roster only)")
	cmd.Flags().BoolVar(&opts.noAutoDraft, "no-auto-draft", false, "only draft sessions with /draft")
	cmd.MarkFlagsMutuallyExclusive("preset", "names")
	return cmd
}

func runAssemble(cmd *cobra.Command, args []string, opts assembleOptions) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	existing, err := st.Rosters(ctx)
	if err != nil {
		return err
	}
	firstRun := len(existing) == 0
	interactive := isInteractive()

	// --cloud puts a first setup in the cloud; a machine that's already in cloud mode needs nothing more
	alreadyCloud := hasCloudKey()
	if opts.cloud && !firstRun && !alreadyCloud {
		return errors.New("--cloud only works for your first roster. To move your setup to the cloud, run `rostr config cloud on`")
	}
	cloud := opts.cloud && !alreadyCloud

	// Running it again: ask what the person wants. Agents passing flags always make a new roster
	flagsGiven := len(args) > 0 || opts.preset != "" || opts.names != "" || opts.emoji != "" || opts.noAutoDraft
	if !firstRun && interactive && !flagsGiven {
		choice, err := promptSelect("You already have a roster. What do you want to do?",
			[]string{"Make a new roster", "Replace a roster's cast", "Edit a roster"})
		if err != nil {
			return err
		}
		switch choice {
		case 1:
			return replaceCastInteractively(ctx, out, st, existing)
		case 2:
			name, err := pickRoster("Which roster?", existing)
			if err != nil {
				return err
			}
			return editRoster(ctx, out, st, name)
		}
	}

	// Cast
	chosen, err := chooseCast(opts, interactive)
	if err != nil {
		return err
	}

	// Name
	name, err := chooseRosterName(ctx, st, args, chosen, interactive)
	if err != nil {
		return err
	}

	// Emoji mode
	mode := roster.EmojiOn
	switch {
	case opts.emoji != "":
		if mode, err = roster.ParseEmojiMode(opts.emoji); err != nil {
			return err
		}
	case interactive:
		modes := []roster.EmojiMode{roster.EmojiOn, roster.EmojiOff, roster.EmojiOnly}
		picked, err := promptSelect("How should agents show up?", []string{
			roster.Display(chosen.aTeam[0].Emoji, chosen.aTeam[0].Name, 1, roster.EmojiOn) + "  (emoji and name)",
			roster.Display(chosen.aTeam[0].Emoji, chosen.aTeam[0].Name, 1, roster.EmojiOff) + "  (name only)",
			roster.Display(chosen.aTeam[0].Emoji, chosen.aTeam[0].Name, 1, roster.EmojiOnly) + "  (emoji only)",
		})
		if err != nil {
			return err
		}
		mode = modes[picked]
	}

	// Setup-wide questions, asked only on the first run
	autoDraft := !opts.noAutoDraft
	if firstRun && interactive {
		if !cloud && !alreadyCloud {
			if cloud, err = promptWhereRostrLives(); err != nil {
				return err
			}
		}
		if !opts.noAutoDraft {
			if autoDraft, err = promptConfirm("Draft every new session automatically? (Otherwise, use /draft.)", true); err != nil {
				return err
			}
		}
	}
	if !firstRun && opts.noAutoDraft {
		fmt.Fprintln(cmd.ErrOrStderr(), "Note: --no-auto-draft only applies to your first roster. Use `rostr config auto-draft off`.")
	}

	// Save: in the cloud, as a new setup holding just this roster; otherwise in this machine's store
	created := store.Roster{Name: strings.TrimSpace(name), EmojiMode: mode, ATeam: chosen.aTeam, BTeam: chosen.bTeam}
	if cloud {
		if err := roster.ValidateCast(created.ATeam, created.BTeam, created.EmojiMode); err != nil {
			return err
		}
		st.Close()
		setup := store.Setup{Rosters: []store.Roster{created}, Settings: store.Settings{AutoDraft: autoDraft}}
		if err := registerInCloud(ctx, setup); err != nil {
			return err
		}
	} else {
		created, err = st.CreateRoster(ctx, created)
		if errors.Is(err, store.ErrNameTaken) {
			return fmt.Errorf("%w; pick another name", err)
		}
		if err != nil {
			return err
		}
		if firstRun {
			if err := st.SetAutoDraft(ctx, autoDraft); err != nil {
				return err
			}
		}
	}

	installed, err := claudecode.Install(paths.ClaudeDir())
	if err != nil {
		return fmt.Errorf("the %s roster is saved, but installing the Claude Code hooks failed: %w", created.Name, err)
	}

	// Summary
	fmt.Fprintf(out, "Assembled the %s roster.\n", created.Name)
	fmt.Fprintf(out, "  A team: %s\n", displayMembers(created.ATeam, mode))
	if len(created.BTeam) > 0 {
		fmt.Fprintf(out, "  B team: %s\n", displayMembers(created.BTeam, mode))
	}
	if firstRun {
		fmt.Fprintln(out, "It's your default roster. Link it to other projects with `rostr link`.")
		if !autoDraft {
			fmt.Fprintln(out, "Auto-draft is off: use /draft in Claude Code to draft a session.")
		}
		if cloud {
			fmt.Fprintln(out, "Your setup is in the cloud.")
			printKeySaved(out)
		}
	} else {
		fmt.Fprintf(out, "Use it for a project with `rostr link %s`.\n", created.Name)
	}
	if len(installed) > 0 {
		fmt.Fprintf(out, "Installed for Claude Code: %s.\n", strings.Join(installed, ", "))
	}
	return nil
}

// chooseCast takes the cast from --preset or --names, or asks for one.
func chooseCast(opts assembleOptions, interactive bool) (cast, error) {
	switch {
	case opts.preset != "":
		return presetCast(opts.preset)
	case opts.names != "":
		aTeam, bTeam, err := roster.ParseNames(opts.names)
		return cast{aTeam: aTeam, bTeam: bTeam}, err
	case !interactive:
		return cast{}, errors.New("pass --preset <id> (see `rostr presets`) or --names <list>, or run `rostr assemble` in a terminal")
	}

	all := presets.All()
	options := make([]string, 0, len(all)+1)
	for _, p := range all {
		options = append(options, fmt.Sprintf("%s %s  (%s)", p.Emoji, p.Name, p.Source))
	}
	options = append(options, "✏️  Your own names")
	picked, err := promptSelect("Pick a roster", options)
	if err != nil {
		return cast{}, err
	}
	if picked < len(all) {
		return presetCast(all[picked].ID)
	}

	var aTeam, bTeam []roster.Member
	_, err = promptInput(`Names, comma-separated. An emoji in front is optional ("🧮 Ada, Grace"). The first six are the A team.`, "",
		func(s string) error {
			var err error
			aTeam, bTeam, err = roster.ParseNames(s)
			return err
		})
	return cast{aTeam: aTeam, bTeam: bTeam}, err
}

func presetCast(id string) (cast, error) {
	p, ok := presets.ByID(id)
	if !ok {
		return cast{}, fmt.Errorf("no preset with id %q; see `rostr presets`", id)
	}
	return cast{
		presetID: p.ID,
		aTeam:    append([]roster.Member{}, p.ATeam...),
		bTeam:    append([]roster.Member{}, p.BTeam...),
	}, nil
}

// chooseRosterName takes the name argument, asks for one, or defaults to the preset's id when run by an agent.
func chooseRosterName(ctx context.Context, st store.Store, args []string, chosen cast, interactive bool) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if !interactive {
		if chosen.presetID == "" {
			return "", errors.New("name the roster: rostr assemble <roster> --names <list>")
		}
		return chosen.presetID, nil
	}

	initial := chosen.presetID
	if initial == "" {
		initial = "work"
	}
	return promptInput("Name this roster", initial, func(name string) error {
		if strings.TrimSpace(name) == "" {
			return errors.New("a roster needs a name")
		}
		if existing, err := st.Roster(ctx, name); err == nil {
			return fmt.Errorf("you already have a roster named %q", existing.Name)
		}
		return nil
	})
}

// promptWhereRostrLives asks where rostr lives, reporting whether the answer is the cloud.
func promptWhereRostrLives() (bool, error) {
	picked, err := promptSelect("Where should rostr live?", []string{
		"This machine (local)",
		"The cloud (every machine and cloud environment)",
	})
	return picked == 1, err
}

func replaceCastInteractively(ctx context.Context, out io.Writer, st store.Store, existing []store.Roster) error {
	name, err := pickRoster("Whose cast do you want to replace?", existing)
	if err != nil {
		return err
	}
	chosen, err := chooseCast(assembleOptions{}, true)
	if err != nil {
		return err
	}
	if err := st.ReplaceCast(ctx, name, chosen.aTeam, chosen.bTeam); err != nil {
		return err
	}
	fmt.Fprintf(out, "Replaced the %s roster's cast. Agents already drafted keep their names until their sessions end.\n", name)
	return nil
}

func pickRoster(title string, rosters []store.Roster) (string, error) {
	names := make([]string, len(rosters))
	for i, r := range rosters {
		names[i] = r.Name
	}
	picked, err := promptSelect(title, names)
	if err != nil {
		return "", err
	}
	return names[picked], nil
}

func displayMembers(members []roster.Member, mode roster.EmojiMode) string {
	parts := make([]string, len(members))
	for i, m := range members {
		parts[i] = roster.Display(m.Emoji, m.Name, 1, mode)
	}
	return strings.Join(parts, ", ")
}
