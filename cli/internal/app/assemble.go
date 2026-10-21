package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/codex"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/presets"
	"github.com/piazzatron/rostr/cli/internal/project"
	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

type assembleOptions struct {
	preset      string
	names       string
	emoji       string
	cloud       bool
	noAutoDraft bool
	link        bool
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
	cmd.Flags().BoolVar(&opts.link, "link", false, "use the new roster for the current repo")
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
		return errors.New("--cloud only works for your first roster. To move your setup to the cloud, run `rostr cloud on`")
	}
	cloud := opts.cloud && !alreadyCloud

	// The repo you're in, if any: a new roster can be linked to it straight away. The first roster is the default for
	// every project, so it never needs linking
	repo, inRepo := project.Identify(workingDir())
	if opts.link && !inRepo {
		return missingInput("--link needs a git repo: run `rostr assemble` inside the repo to link")
	}
	link := opts.link && !firstRun

	// Without a terminal, flags say everything; at a terminal, one full-screen wizard asks whatever they don't
	var chosen cast
	var name string
	mode := roster.EmojiOn
	autoDraft := !opts.noAutoDraft
	if interactive {
		flow, err := newAssembleFlow(assembleSetup{existing: existing, opts: opts, args: args, inCloud: opts.cloud || alreadyCloud, repo: repo})
		if err != nil {
			return err
		}
		if err := flow.ask(ctx, st); err != nil {
			return err
		}
		switch flow.action {
		case actionEdit:
			return editRoster(ctx, out, st, flow.roster)
		case actionReplace:
			replacement := flow.chosenCast()
			if err := st.ReplaceCast(ctx, flow.roster, replacement.aTeam, replacement.bTeam); err != nil {
				return err
			}
			fmt.Fprintf(out, "Replaced the %s roster's cast. Agents already drafted keep their names until their sessions end.\n", flow.roster)
			return nil
		}
		chosen, name, mode = flow.chosenCast(), flow.rosterName(), flow.emoji
		if flow.asks(stepWhere) {
			cloud = flow.cloud
		}
		if flow.asks(stepAutoDraft) {
			autoDraft = flow.autoDraft
		}
		if flow.asks(stepLink) {
			link = flow.link
		}
	} else {
		if chosen, err = chooseCast(opts); err != nil {
			return err
		}
		if name, err = chooseRosterName(args, chosen); err != nil {
			return err
		}
		if opts.emoji != "" {
			if mode, err = roster.ParseEmojiMode(opts.emoji); err != nil {
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
		if link {
			if err := st.LinkProject(ctx, repo, created.Name); err != nil {
				return err
			}
		}
	}

	installed, err := claudecode.Install(paths.ClaudeDir())
	if err != nil {
		return fmt.Errorf("the %s roster is saved, but installing the Claude Code hooks failed: %w", created.Name, err)
	}

	// Codex only where it looks installed, so assemble doesn't create a ~/.codex for people who don't use it
	var installedCodex []string
	if codex.Detected(paths.CodexDir()) {
		if installedCodex, err = codex.Install(paths.CodexDir(), paths.AgentsSkillsDir()); err != nil {
			return fmt.Errorf("the %s roster is saved, but installing the Codex hooks failed: %w", created.Name, err)
		}
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
			fmt.Fprintln(out, "Auto-draft is off: use /draft in Claude Code, or $rostr-draft in Codex, to draft a session.")
		}
		if cloud {
			fmt.Fprintln(out, "Your setup is in the cloud.")
			printKeySaved(out)
		}
	} else if link {
		fmt.Fprintf(out, "Linked %s to it.\n", repo)
	} else {
		fmt.Fprintf(out, "Use it for a project with `rostr link %s`.\n", created.Name)
	}
	if len(installed) > 0 {
		fmt.Fprintf(out, "Installed for Claude Code: %s.\n", strings.Join(installed, ", "))
	}
	if len(installedCodex) > 0 {
		fmt.Fprintf(out, "Installed for Codex: %s.\n", strings.Join(installedCodex, ", "))
		fmt.Fprintln(out, "Codex only runs hooks you've approved: open /hooks in Codex and trust rostr's.")
	}
	return nil
}

// chooseCast takes the cast from --preset or --names.
func chooseCast(opts assembleOptions) (cast, error) {
	switch {
	case opts.preset != "":
		return presetCast(opts.preset)
	case opts.names != "":
		aTeam, bTeam, err := roster.ParseNames(opts.names)
		return cast{aTeam: aTeam, bTeam: bTeam}, err
	}
	return cast{}, missingInput("pass --preset <id> (see `rostr presets`) or --names <list>, or run `rostr assemble` in a terminal")
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

// chooseRosterName takes the name argument, or defaults to the preset's id.
func chooseRosterName(args []string, chosen cast) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if chosen.presetID == "" {
		return "", missingInput("name the roster: rostr assemble <roster> --names <list>")
	}
	return chosen.presetID, nil
}

func displayMembers(members []roster.Member, mode roster.EmojiMode) string {
	parts := make([]string, len(members))
	for i, m := range members {
		parts[i] = roster.Display(m.Emoji, m.Name, 1, mode)
	}
	return strings.Join(parts, ", ")
}

// assembleAction is what a run of `rostr assemble` does once you already have a roster.
type assembleAction int

const (
	actionNew assembleAction = iota
	actionReplace
	actionEdit
)

// assembleStep is one question of the assemble wizard, in the order they're asked.
type assembleStep int

const (
	stepAction assembleStep = iota
	stepPickRoster
	stepBuild
	stepPreset
	stepNames
	stepName
	stepEmoji
	stepLink
	stepWhere
	stepAutoDraft
	stepConfirm
)

var assembleSteps = []assembleStep{
	stepAction, stepPickRoster, stepBuild, stepPreset, stepNames, stepName, stepEmoji, stepLink, stepWhere, stepAutoDraft,
	stepConfirm,
}

// assembleSetup is what the wizard starts from: the rosters there are, and the flags and argument given.
type assembleSetup struct {
	existing []store.Roster
	opts     assembleOptions
	args     []string
	// inCloud is whether the setup is going, or already is, in the cloud
	inCloud bool
	// repo is the project you're in ("owner/repo"), or "" outside a git repo
	repo string
}

// assembleFlow is the assemble wizard's questions and answers. The setup and flags decide which questions it can ask;
// earlier answers decide which of those it does.
type assembleFlow struct {
	assembleSetup
	flagCast cast

	action      assembleAction
	roster      string
	usePreset   bool
	presetID    string
	names       string
	namesCast   cast
	name        string
	nameDefault string
	emoji       roster.EmojiMode
	cloud       bool
	autoDraft   bool
	link        bool
	confirmed   bool
}

func newAssembleFlow(setup assembleSetup) (*assembleFlow, error) {
	f := &assembleFlow{
		assembleSetup: setup,
		usePreset:     true,
		presetID:      presets.All()[0].ID,
		emoji:         roster.EmojiOn,
		autoDraft:     true,
		link:          true,
		confirmed:     true,
	}
	var err error
	if !f.asks(stepBuild) {
		if f.flagCast, err = chooseCast(setup.opts); err != nil {
			return nil, err
		}
	}
	if !f.asks(stepEmoji) {
		if f.emoji, err = roster.ParseEmojiMode(setup.opts.emoji); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// asks reports whether the wizard can ask a step at all, given the setup and flags. Running it again with no flags
// starts by asking what to do; the setup-wide questions are only for the first roster.
func (f *assembleFlow) asks(step assembleStep) bool {
	firstRun := len(f.existing) == 0
	opts := f.opts
	switch step {
	case stepAction, stepPickRoster:
		flagsGiven := len(f.args) > 0 || opts.preset != "" || opts.names != "" || opts.emoji != "" || opts.noAutoDraft
		return !firstRun && !flagsGiven
	case stepBuild, stepPreset, stepNames:
		return opts.preset == "" && opts.names == ""
	case stepName:
		return len(f.args) == 0
	case stepEmoji:
		return opts.emoji == ""
	case stepWhere:
		return firstRun && !f.inCloud
	case stepAutoDraft:
		return firstRun && !opts.noAutoDraft
	case stepLink:
		return !firstRun && f.repo != "" && !opts.link
	}
	return true
}

// shows reports whether a step is asked, given the answers so far.
func (f *assembleFlow) shows(step assembleStep) bool {
	if !f.asks(step) {
		return false
	}
	switch step {
	case stepPickRoster:
		return f.action != actionNew
	case stepBuild, stepConfirm:
		return f.action != actionEdit
	case stepPreset:
		return f.action != actionEdit && f.usePreset
	case stepNames:
		return f.action != actionEdit && !f.usePreset
	case stepName, stepEmoji, stepLink:
		return f.action == actionNew
	}
	return true
}

// visibleSteps is every step the wizard asks, given the answers so far.
func (f *assembleFlow) visibleSteps() []assembleStep {
	var steps []assembleStep
	for _, step := range assembleSteps {
		if f.shows(step) {
			steps = append(steps, step)
		}
	}
	return steps
}

// ask runs the wizard, if the flags leave anything to ask. Nothing is saved until its last step is confirmed.
func (f *assembleFlow) ask(ctx context.Context, st store.Store) error {
	if !slices.ContainsFunc(assembleSteps, func(step assembleStep) bool { return step != stepConfirm && f.asks(step) }) {
		return nil
	}
	s, steps := f.screen(ctx, st)
	if err := s.run(steps); err != nil {
		return err
	}
	if !f.confirmed {
		return errCancelled
	}
	return nil
}

// screen builds the wizard: a field for each step it can ask, the preset preview beside the preset picker, and the
// text that depends on earlier answers, filled in as each step comes into view.
func (f *assembleFlow) screen(ctx context.Context, st store.Store) (*screen, []screenStep) {
	s := newScreen("assemble")

	all := presets.All()
	presetOptions := make([]huh.Option[string], len(all))
	widest := 0
	for i, p := range all {
		label := fmt.Sprintf("%s %s · %s", p.Emoji, p.Name, p.Source)
		presetOptions[i] = huh.NewOption(label, p.ID)
		widest = max(widest, lipgloss.Width(label))
	}

	action := huh.NewSelect[assembleAction]().Title("You already have a roster. What do you want to do?").Options(
		huh.NewOption("Make a new roster", actionNew),
		huh.NewOption("Replace a roster's cast", actionReplace),
		huh.NewOption("Edit a roster", actionEdit),
	).Value(&f.action)
	rosterNames := make([]string, len(f.existing))
	for i, r := range f.existing {
		rosterNames[i] = r.Name
	}
	pickRoster := huh.NewSelect[string]().Title("Which roster?").Options(huh.NewOptions(rosterNames...)...).Value(&f.roster)
	build := huh.NewConfirm().Title("How do you want to build your roster?").
		Affirmative("🎭 Pick a pre-built roster").Negative("✏️  Use your own names").Value(&f.usePreset)
	preset := huh.NewSelect[string]().Title("Pick a roster").
		Description("Pick a roster: type to search, ↑/↓ to move, enter to pick.").
		Options(presetOptions...).Filtering(true).Value(&f.presetID)
	names := huh.NewInput().
		Title(`Names, comma-separated. An emoji in front is optional ("🧮 Ada, Grace"). The first six are the A team.`).
		Value(&f.names).Validate(s.forwardOnly(func(list string) error {
		aTeam, bTeam, err := roster.ParseNames(list)
		f.namesCast = cast{aTeam: aTeam, bTeam: bTeam}
		return err
	}))
	name := huh.NewInput().Title("Name this roster").Value(&f.name).Validate(s.forwardOnly(func(name string) error {
		if strings.TrimSpace(name) == "" {
			return errors.New("a roster needs a name")
		}
		if existing, err := st.Roster(ctx, name); err == nil {
			return fmt.Errorf("you already have a roster named %q", existing.Name)
		}
		return nil
	}))
	emoji := huh.NewSelect[roster.EmojiMode]().Title("How should agents show up?").Options(f.emojiOptions()...).Value(&f.emoji)
	where := huh.NewSelect[bool]().Title("Where should rostr live?").Options(
		huh.NewOption("This machine (local)", false),
		huh.NewOption("The cloud (every machine and cloud environment)", true),
	).Value(&f.cloud)
	autoDraft := huh.NewConfirm().Title("Draft names automatically in new sessions, or manually assign with /draft?").
		Affirmative("Automatically").Negative("Manually with /draft").Value(&f.autoDraft)
	link := huh.NewConfirm().Affirmative("Yes, link it").Negative("No").Value(&f.link)
	confirm := huh.NewConfirm().Value(&f.confirmed)

	fields := map[assembleStep]huh.Field{
		stepAction: action, stepPickRoster: pickRoster, stepBuild: build, stepPreset: preset, stepNames: names,
		stepName: name, stepEmoji: emoji, stepLink: link, stepWhere: where, stepAutoDraft: autoDraft, stepConfirm: confirm,
	}
	var steps []screenStep
	for _, step := range assembleSteps {
		if f.asks(step) {
			steps = append(steps, screenStep{field: fields[step], shown: func() bool { return f.shows(step) }})
		}
	}

	// The preset picker shows the highlighted roster beside it
	s.panel = &sidePanel{
		field:     preset,
		formWidth: widest + 5,
		render: func(width int) string {
			id, ok := preset.Hovered()
			if !ok {
				return ""
			}
			p, _ := presets.ByID(id)
			return presetPreview(p, width)
		},
	}

	s.onFocus = func(field huh.Field) tea.Cmd {
		switch field {
		case pickRoster:
			pickRoster.Title(map[assembleAction]string{
				actionReplace: "Whose cast do you want to replace?",
				actionEdit:    "Which roster do you want to edit?",
			}[f.action])
		case preset:
			// Coming back to the picker searches again
			if !preset.GetFiltering() {
				return func() tea.Msg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}} }
			}
		case name:
			// Default to the chosen preset's id, unless the name's been changed
			if f.name == f.nameDefault {
				f.nameDefault = f.chosenCast().presetID
				if f.nameDefault == "" {
					f.nameDefault = "work"
				}
				f.name = f.nameDefault
				name.Value(&f.name)
			}
		case emoji:
			emoji.Options(f.emojiOptions()...)
		case link:
			link.Title(fmt.Sprintf("Use the %s roster for %s?", strings.TrimSpace(f.rosterName()), f.repo)).
				Description("You're in this repo, so its sessions can draft from the new roster instead of your default.")
		case confirm:
			title, description := f.confirmation()
			confirm.Title(title).Description(description)
		}
		return nil
	}
	return s, steps
}

// chosenCast is the cast from the flags, the picked preset, or the names typed in.
func (f *assembleFlow) chosenCast() cast {
	switch {
	case !f.asks(stepBuild):
		return f.flagCast
	case f.usePreset:
		chosen, _ := presetCast(f.presetID)
		return chosen
	}
	return f.namesCast
}

// rosterName is the name typed in, or else the name argument.
func (f *assembleFlow) rosterName() string {
	if f.asks(stepName) {
		return f.name
	}
	return f.args[0]
}

// emojiOptions previews each emoji mode with the cast's first member.
func (f *assembleFlow) emojiOptions() []huh.Option[roster.EmojiMode] {
	first := roster.Member{Emoji: "🧙", Name: "Gandalf"}
	if chosen := f.chosenCast(); len(chosen.aTeam) > 0 {
		first = chosen.aTeam[0]
	}
	return emojiModeOptions(first)
}

// confirmation is the last step's question and its summary of the answers.
func (f *assembleFlow) confirmation() (title, description string) {
	castLabel := f.castLabel()
	if f.action == actionReplace {
		return fmt.Sprintf("Replace the %s roster's cast with %s?", f.roster, castLabel),
			"Drafted agents keep their names until their sessions end."
	}

	parts := []string{castLabel, "emoji " + string(f.emoji)}
	if len(f.existing) == 0 {
		where := "this machine"
		if f.inCloud || (f.asks(stepWhere) && f.cloud) {
			where = "the cloud"
		}
		autoDraft := !f.opts.noAutoDraft
		if f.asks(stepAutoDraft) {
			autoDraft = f.autoDraft
		}
		parts = append(parts, where, "auto-draft "+onOff(autoDraft))
	}
	if f.repo != "" && ((f.asks(stepLink) && f.link) || (f.opts.link && len(f.existing) > 0)) {
		parts = append(parts, "linked to "+f.repo)
	}
	return fmt.Sprintf("Assemble the %s roster?", strings.TrimSpace(f.rosterName())), strings.Join(parts, " · ")
}

// castLabel names the chosen cast: the preset, or the first few names.
func (f *assembleFlow) castLabel() string {
	chosen := f.chosenCast()
	if p, ok := presets.ByID(chosen.presetID); ok {
		return p.Emoji + " " + p.Name
	}
	members := append(append([]roster.Member{}, chosen.aTeam...), chosen.bTeam...)
	if len(members) <= 3 {
		return displayMembers(members, roster.EmojiOn)
	}
	return fmt.Sprintf("%s and %d more", displayMembers(members[:3], roster.EmojiOn), len(members)-3)
}

// presetPreview shows a preset's name and source, its A team, and its B team in grey.
func presetPreview(p presets.Preset, width int) string {
	wrap := lipgloss.NewStyle().Width(width)
	aTeam := make([]string, len(p.ATeam))
	for i, m := range p.ATeam {
		aTeam[i] = roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		wrap.Inherit(styleHeading).Render(p.Emoji+" "+p.Name),
		wrap.Inherit(styleMuted).Render(p.Source),
		"",
		styleMuted.Render("A team"),
		wrap.Render(strings.Join(aTeam, "\n")),
		"",
		styleMuted.Render("B team"),
		wrap.Inherit(styleFaint).Render(displayMembers(p.BTeam, roster.EmojiOn)),
	)
}
