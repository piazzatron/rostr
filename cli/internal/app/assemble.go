package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
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
	size        string
	emoji       string
	cloud       bool
	noAutoDraft bool
	link        bool
}

// cast is a chosen set of members, from a preset or the user's own names.
type cast struct {
	presetID string
	members  []roster.Member
	// from is everyone the cast can be picked from: a preset, or the cast itself plus names added to it
	from []roster.Member
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
	cmd.Flags().StringVar(&opts.size, "size", "", fmt.Sprintf("how many of the preset's members to take, or all (default %d)", presets.DefaultTeamSize))
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
	if opts.size != "" && opts.preset == "" {
		return errors.New("--size picks how many of a preset's members to take, so it needs --preset")
	}
	if opts.names == "" {
		refreshPresets(ctx)
	}

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
			return saveEdit(ctx, out, st, flow)
		case actionReplace:
			replacement := flow.chosenCast()
			if err := st.ReplaceCast(ctx, flow.roster, replacement.members); err != nil {
				return err
			}
			fmt.Fprintf(out, "Replaced the %s roster's cast. Agents already drafted keep their names until their sessions end.\n", flow.roster)
			return nil
		}
		chosen, name, mode = flow.chosenCast(), flow.rosterName(), flow.emoji
		if flow.asksSetting(settingWhere) {
			cloud = flow.cloud
		}
		if flow.asksSetting(settingAutoDraft) {
			autoDraft = flow.autoDraft
		}
		if flow.asksSetting(settingLink) {
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
	created := store.Roster{Name: strings.TrimSpace(name), EmojiMode: mode, Members: chosen.members}
	if cloud {
		if err := roster.ValidateCast(created.Members, created.EmojiMode); err != nil {
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

	installed, installedCodex, err := installAgentHooks()
	if err != nil {
		return fmt.Errorf("the %s roster is saved, but %w", created.Name, err)
	}

	// Summary
	fmt.Fprintf(out, "Assembled the %s roster: %s\n", created.Name, displayMembers(created.Members, mode))
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
	printInstalledHooks(out, installed, installedCodex)
	return nil
}

// installAgentHooks installs rostr's hooks, skills, and commands into Claude Code, and into Codex where it looks
// installed, so nothing creates a ~/.codex for people who don't use it.
func installAgentHooks() (installed, installedCodex []string, err error) {
	if installed, err = claudecode.Install(paths.ClaudeDir()); err != nil {
		return nil, nil, fmt.Errorf("installing the Claude Code hooks failed: %w", err)
	}
	if codex.Detected(paths.CodexDir()) {
		if installedCodex, err = codex.Install(paths.CodexDir(), paths.AgentsSkillsDir()); err != nil {
			return nil, nil, fmt.Errorf("installing the Codex hooks failed: %w", err)
		}
	}
	return installed, installedCodex, nil
}

// printInstalledHooks says which agents got new hooks. Only new hooks are worth a line; refreshed skill text isn't.
func printInstalledHooks(out io.Writer, installed, installedCodex []string) {
	var tools []string
	if changedHooks(installed) {
		tools = append(tools, "Claude Code")
	}
	if changedHooks(installedCodex) {
		tools = append(tools, "Codex")
	}
	if len(tools) > 0 {
		fmt.Fprintf(out, "Hooks installed for %s.\n", strings.Join(tools, " and "))
	}
	if changedHooks(installedCodex) {
		fmt.Fprintln(out, "Codex only runs hooks you've approved: open /hooks in Codex and trust rostr's.")
	}
}

// chooseCast takes the cast from --preset and --size, or --names.
func chooseCast(opts assembleOptions) (cast, error) {
	switch {
	case opts.preset != "":
		p, err := findPreset(opts.preset)
		if err != nil {
			return cast{}, err
		}
		size, err := parseTeamSize(opts.size)
		if err != nil {
			return cast{}, err
		}
		return presetTeam(p, size), nil
	case opts.names != "":
		members, err := roster.ParseNames(opts.names)
		return cast{members: members}, err
	}
	return cast{}, missingInput("pass --preset <id> (see `rostr presets`) or --names <list>, or run `rostr assemble` in a terminal")
}

// findPreset finds a preset by id, drawing a new Free Agents cast for that one.
func findPreset(id string) (presets.Preset, error) {
	p, ok := presets.ByID(id)
	if !ok {
		return presets.Preset{}, fmt.Errorf("no preset with id %q; see `rostr presets`", id)
	}
	if id == presets.FreeAgentsID {
		p = presets.FreeAgents()
	}
	return p, nil
}

// parseTeamSize reads --size: a number of members, or all of them (0).
func parseTeamSize(flag string) (int, error) {
	flag = strings.TrimSpace(flag)
	switch {
	case flag == "":
		return presets.DefaultTeamSize, nil
	case strings.EqualFold(flag, "all"):
		return 0, nil
	}
	n, err := strconv.Atoi(flag)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("--size must be a number of members, or all (got %q)", flag)
	}
	return n, nil
}

// presetTeam is a preset's first size members, or all of them for 0 or more than it has, with everyone on it to swap
// in.
func presetTeam(p presets.Preset, size int) cast {
	if size <= 0 || size > len(p.Members) {
		size = len(p.Members)
	}
	return cast{presetID: p.ID, members: slices.Clone(p.Members[:size]), from: p.Members}
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

// buildChoice is where a new cast comes from.
type buildChoice int

const (
	buildPreset buildChoice = iota
	buildFreeAgents
	buildNames
)

// assembleStep is one page of the assemble wizard, in the order they're asked.
type assembleStep int

const (
	stepAction assembleStep = iota
	stepPickRoster
	stepBuild
	stepPreset
	stepSize
	stepCast
	stepSettings
	stepEditReview
)

var assembleSteps = []assembleStep{
	stepAction, stepPickRoster, stepBuild, stepPreset, stepSize, stepCast, stepSettings, stepEditReview,
}

// assembleSetting is one question on the wizard's last page, where a new roster's settings are confirmed together.
type assembleSetting int

const (
	settingName assembleSetting = iota
	settingEmoji
	settingLink
	settingWhere
	settingAutoDraft
)

var assembleSettings = []assembleSetting{settingName, settingEmoji, settingLink, settingWhere, settingAutoDraft}

// assembleSetup is what the wizard starts from: the rosters there are, and the flags and argument given.
type assembleSetup struct {
	existing []store.Roster
	opts     assembleOptions
	args     []string
	// inCloud is whether the setup is going, or already is, in the cloud
	inCloud bool
	// repo is the project you're in ("owner/repo"), or "" outside a git repo
	repo string
	// edit is the roster `rostr edit` opens straight away, skipping what to do and which roster
	edit string
}

// assembleFlow is the assemble wizard's questions and answers. The setup and flags decide which questions it can ask;
// earlier answers decide which of those it does.
type assembleFlow struct {
	assembleSetup
	flagCast cast

	action   assembleAction
	roster   string
	build    buildChoice
	presetID string
	// freeAgents is the Free Agents cast drawn for this visit, and draws counts them
	freeAgents presets.Preset
	draws      int
	// size is how many of the preset's members to take, or 0 for all of them
	size int
	// edited is the cast as changed on the cast page, starting from the cast castFor names
	edited  cast
	castFor string
	// emojiFor is the roster whose emoji mode f.emoji was set from, so making a new one goes back to defaultEmoji
	emojiFor     string
	defaultEmoji roster.EmojiMode
	name         string
	nameDefault  string
	emoji        roster.EmojiMode
	cloud        bool
	autoDraft    bool
	link         bool
}

func newAssembleFlow(setup assembleSetup) (*assembleFlow, error) {
	f := &assembleFlow{
		assembleSetup: setup,
		presetID:      presets.All()[0].ID,
		size:          presets.DefaultTeamSize,
		emoji:         roster.EmojiOn,
		cloud:         true,
		autoDraft:     true,
		link:          true,
	}
	if setup.edit != "" {
		f.action, f.roster = actionEdit, setup.edit
	}
	var err error
	if !f.asks(stepBuild) && setup.edit == "" {
		if f.flagCast, err = chooseCast(setup.opts); err != nil {
			return nil, err
		}
	}
	if !f.asksSetting(settingEmoji) {
		if f.emoji, err = roster.ParseEmojiMode(setup.opts.emoji); err != nil {
			return nil, err
		}
	}
	f.defaultEmoji = f.emoji
	return f, nil
}

// asks reports whether the wizard can ask a step at all, given the setup and flags. Running it again with no flags
// starts by asking what to do.
func (f *assembleFlow) asks(step assembleStep) bool {
	firstRun := len(f.existing) == 0
	opts := f.opts
	switch step {
	case stepAction, stepPickRoster:
		flagsGiven := len(f.args) > 0 || opts.preset != "" || opts.names != "" || opts.emoji != "" || opts.noAutoDraft
		return !firstRun && !flagsGiven && f.edit == ""
	case stepBuild, stepPreset, stepSize:
		return opts.preset == "" && opts.names == "" && f.edit == ""
	case stepCast:
		return opts.preset == "" && opts.names == "" || f.edit != ""
	case stepSettings:
		return f.edit == "" && slices.ContainsFunc(assembleSettings, f.asksSetting)
	case stepEditReview:
		return f.edit != "" || f.asks(stepAction)
	}
	return true
}

// asksSetting reports whether the last page asks a setting, given the setup and flags. The setup-wide settings are
// only for the first roster.
func (f *assembleFlow) asksSetting(setting assembleSetting) bool {
	firstRun := len(f.existing) == 0
	opts := f.opts
	switch setting {
	case settingName:
		return len(f.args) == 0
	case settingEmoji:
		return opts.emoji == ""
	case settingWhere:
		return firstRun && !f.inCloud
	case settingAutoDraft:
		return firstRun && !opts.noAutoDraft
	case settingLink:
		return !firstRun && f.repo != "" && !opts.link
	}
	return false
}

// shows reports whether a step is asked, given the answers so far.
func (f *assembleFlow) shows(step assembleStep) bool {
	if !f.asks(step) {
		return false
	}
	switch step {
	case stepPickRoster:
		return f.action != actionNew
	case stepBuild:
		return f.action != actionEdit
	case stepPreset:
		return f.action != actionEdit && f.build == buildPreset
	case stepSize:
		return f.action != actionEdit && f.build != buildNames
	case stepSettings:
		return f.action == actionNew
	case stepEditReview:
		return f.action == actionEdit
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

// ask runs the wizard, if the flags leave anything to ask. Nothing is saved until its last step is answered.
func (f *assembleFlow) ask(ctx context.Context, st store.Store) error {
	if !slices.ContainsFunc(assembleSteps, f.asks) {
		return nil
	}
	s, steps := f.screen(ctx, st)
	return s.run(steps)
}

// screen builds the wizard: a page for each step it can ask, the preset preview beside the preset picker, and the
// text that depends on earlier answers, filled in as each step comes into view.
func (f *assembleFlow) screen(ctx context.Context, st store.Store) (*screen, []screenStep) {
	s := newScreen("assemble")
	if f.edit != "" {
		s = newScreen("edit")
	}

	var presetOptions []huh.Option[string]
	widest := 0
	for _, p := range presets.All() {
		if p.ID == presets.FreeAgentsID {
			continue // it's its own choice, on the first page
		}
		label := fmt.Sprintf("%s %s · %s", p.Emoji, p.Name, p.Source)
		presetOptions = append(presetOptions, huh.NewOption(label, p.ID))
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
	build := huh.NewSelect[buildChoice]().Title("How do you want to build your roster?").Options(
		huh.NewOption("🎭 Pick a pre-built roster", buildPreset),
		huh.NewOption("🎲 Free Agents · a random mix from every roster", buildFreeAgents),
		huh.NewOption("✏️ Use your own names", buildNames),
	).Value(&f.build)
	preset := huh.NewSelect[string]().Title("Pick a roster").
		Description("Pick a roster: type to search, ↑/↓ to move, enter to pick.").
		Options(presetOptions...).Filtering(true).Value(&f.presetID)
	size := huh.NewSelect[int]().Title("How big is your team?").Value(&f.size)
	castPage := newCastEditor()

	// The new roster's last page: the cast, then each setting with its default, then a button to make it
	summary := huh.NewNote().Title("Ready to assemble?")
	name := huh.NewInput().Title("Roster name").Value(&f.name).Validate(s.forwardOnly(func(name string) error {
		if strings.TrimSpace(name) == "" {
			return errors.New("a roster needs a name")
		}
		if existing, err := st.Roster(ctx, name); err == nil {
			return fmt.Errorf("you already have a roster named %q", existing.Name)
		}
		return nil
	}))
	validateEmoji := func(mode roster.EmojiMode) error {
		if s.goingBack {
			return nil // so shift+tab can go back to the cast to fix it
		}
		return roster.ValidateCast(f.chosenCast().members, mode)
	}
	emoji := huh.NewSelect[roster.EmojiMode]().Title("Emojis").Inline(true).Value(&f.emoji).Validate(validateEmoji)
	link := huh.NewSelect[bool]().Inline(true).TitleFunc(func() string {
		return fmt.Sprintf("Use it for %s", f.repo)
	}, nil).Options(
		huh.NewOption("yes, link it", true),
		huh.NewOption("no", false),
	).Value(&f.link)
	where := huh.NewSelect[bool]().Title("rostr lives on").Inline(true).Options(
		huh.NewOption("the cloud (every machine and cloud environment)", true),
		huh.NewOption("this machine", false),
	).Value(&f.cloud)
	autoDraft := huh.NewSelect[bool]().Title("Name new sessions").Inline(true).Options(
		huh.NewOption("automatically", true),
		huh.NewOption("only with /draft", false),
	).Value(&f.autoDraft)
	confirmed := true
	assemble := huh.NewConfirm().Affirmative("Assemble").Negative("").Value(&confirmed)

	settingFields := map[assembleSetting]huh.Field{
		settingName: name, settingEmoji: emoji, settingLink: link, settingWhere: where, settingAutoDraft: autoDraft,
	}
	settings := screenStep{field: summary, shown: func() bool { return f.shows(stepSettings) }}
	for _, setting := range assembleSettings {
		if f.asksSetting(setting) {
			settings.more = append(settings.more, settingFields[setting])
		}
	}
	settings.more = append(settings.more, assemble)

	// An edited roster's last page: the cast, its emoji mode, and a button to save it
	editSummary := huh.NewNote()
	editEmoji := huh.NewSelect[roster.EmojiMode]().Title("Emojis").Inline(true).Value(&f.emoji).Validate(validateEmoji)
	save := huh.NewConfirm().Affirmative("Save").Negative("").Value(&confirmed)
	editReview := screenStep{field: editSummary, more: []huh.Field{editEmoji, save}, shown: func() bool { return f.shows(stepEditReview) }}

	fields := map[assembleStep]huh.Field{
		stepAction: action, stepPickRoster: pickRoster, stepBuild: build, stepPreset: preset, stepSize: size, stepCast: castPage,
	}
	var steps []screenStep
	for _, step := range assembleSteps {
		switch {
		case !f.asks(step):
		case step == stepSettings:
			steps = append(steps, settings)
		case step == stepEditReview:
			steps = append(steps, editReview)
		default:
			steps = append(steps, screenStep{field: fields[step], shown: func() bool { return f.shows(step) }})
		}
	}

	// The preset picker shows the highlighted roster beside it, and the size picker the team that size makes
	s.panels = []*sidePanel{{
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
	}, {
		field:     size,
		formWidth: 44,
		render: func(width int) string {
			n, ok := size.Hovered()
			if !ok {
				return ""
			}
			return teamPreview(presetTeam(f.pickedPreset(), n), width)
		},
	}, {
		field:     castPage,
		formWidth: 46,
		render: func(width int) string {
			if castPage.cast == nil {
				return ""
			}
			return teamPreview(*castPage.cast, width)
		},
	}}

	inSettings, inEditReview := false, false
	s.onFocus = func(field huh.Field) tea.Cmd {
		wasInSettings, wasInEditReview := inSettings, inEditReview
		inSettings = slices.Contains(settings.fields(), field)
		inEditReview = slices.Contains(editReview.fields(), field)
		switch {
		case field == pickRoster:
			pickRoster.Title(map[assembleAction]string{
				actionReplace: "Whose cast do you want to replace?",
				actionEdit:    "Which roster do you want to edit?",
			}[f.action])
		case field == build:
			// Coming back here draws new free agents
			f.freeAgents = presets.Preset{}
		case field == preset:
			// Coming back to the picker searches again
			if !preset.GetFiltering() {
				return func() tea.Msg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}} }
			}
		case field == size:
			f.prepareSize(size)
		case field == castPage:
			f.prepareCast(castPage)
		case inSettings && !wasInSettings:
			f.prepareSettings(summary, name, emoji)
		case inEditReview && !wasInEditReview:
			f.prepareEditReview(editSummary, editEmoji)
		}
		return nil
	}
	return s, steps
}

// prepareSize fills in the size page for the picked preset: the usual sizes it has room for, then everyone on it.
func (f *assembleFlow) prepareSize(size *huh.Select[int]) {
	p := f.pickedPreset()
	var options []huh.Option[int]
	for _, n := range presets.TeamSizes {
		if n < len(p.Members) {
			options = append(options, huh.NewOption(strconv.Itoa(n), n))
		}
	}
	options = append(options, huh.NewOption(fmt.Sprintf("All %d", len(p.Members)), 0))
	// A size of everyone on it is the "All" option
	inOptions := func(n int) bool {
		return slices.ContainsFunc(options, func(o huh.Option[int]) bool { return o.Value == n })
	}
	if f.size >= len(p.Members) {
		f.size = 0
	}
	if !inOptions(f.size) {
		f.size = presets.DefaultTeamSize
		if !inOptions(f.size) {
			f.size = 0
		}
	}
	from := p.Name
	if p.ID == presets.FreeAgentsID {
		from = "this random mix"
	}
	size.Description(fmt.Sprintf("Your team is the first few of %s. You can pick anyone else from it next.", from))

	// huh scrolls a list to its selected option, hiding the ones above it, so this selects the first option and moves
	// down to the size wanted
	want := f.size
	f.size = options[0].Value
	size.Options(options...)
	for range options {
		if f.size == want {
			break
		}
		size.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
}

// prepareCast fills in the cast page for the cast chosen so far, starting over from that cast when it's one the page
// hasn't shown before.
func (f *assembleFlow) prepareCast(page *castEditor) {
	base := f.baseCast() // first, since it can draw the Free Agents the key names
	if key := f.castKey(); key != f.castFor || page.cast == nil {
		f.castFor = key
		from := base.from
		if len(from) == 0 {
			from = base.members
		}
		f.edited = cast{presetID: base.presetID, members: slices.Clone(base.members), from: slices.Clone(from)}
		page.reset(&f.edited)
	}
	// Replacing a cast, or a new roster whose emoji mode came from --emoji, saves without a page to check it on
	page.validate = nil
	if r, ok := f.editedRoster(); ok && f.action == actionReplace {
		page.validate = func(c cast) error { return roster.ValidateCast(c.members, r.EmojiMode) }
	} else if f.action == actionNew && !f.asksSetting(settingEmoji) {
		page.validate = func(c cast) error { return roster.ValidateCast(c.members, f.emoji) }
	}
	page.description = "Space picks who's on your team, and a adds your own."
	switch {
	case f.action == actionEdit:
		page.title = fmt.Sprintf("Edit the %s roster", f.roster)
		page.description = "Space takes someone off the roster or puts them back, and a adds someone."
	case f.build == buildNames && f.action != actionReplace:
		page.title = "Who's on your roster?"
	default:
		page.title = "Who's on the roster?"
	}
}

// prepareSettings fills in the last page as it comes into view: the cast it's about to make, the name (from the
// preset, unless it's been changed), and the emoji modes previewed with the cast's first member.
func (f *assembleFlow) prepareSettings(summary *huh.Note, name *huh.Input, emoji *huh.Select[roster.EmojiMode]) {
	chosen := f.chosenCast()
	summary.Description(noteText(castSummary(chosen)))
	if f.emojiFor != "" {
		f.emojiFor, f.emoji = "", f.defaultEmoji // the mode came from the roster being edited
	}
	if f.name == f.nameDefault {
		f.nameDefault = chosen.presetID
		if f.nameDefault == "" {
			f.nameDefault = "work"
		}
		f.name = f.nameDefault
		name.Value(&f.name)
	}
	emoji.Options(f.emojiOptions()...)
}

// prepareEditReview fills in an edited roster's last page, starting from its own emoji mode.
func (f *assembleFlow) prepareEditReview(summary *huh.Note, emoji *huh.Select[roster.EmojiMode]) {
	summary.Title(fmt.Sprintf("Save the %s roster?", f.roster)).Description(noteText(castSummary(f.chosenCast())))
	if r, ok := f.editedRoster(); ok && f.emojiFor != r.Name {
		f.emojiFor, f.emoji = r.Name, r.EmojiMode
	}
	emoji.Options(f.emojiOptions()...)
}

// editedRoster is the roster being edited, or replaced.
func (f *assembleFlow) editedRoster() (store.Roster, bool) {
	for _, r := range f.existing {
		if r.Name == f.roster {
			return r, true
		}
	}
	return store.Roster{}, false
}

// baseCast is the cast from the flags, the roster being edited, the picked preset, Free Agents, or, for your own
// names, no one yet, before any change on the teams page.
func (f *assembleFlow) baseCast() cast {
	if f.action == actionEdit {
		r, _ := f.editedRoster()
		return cast{members: r.Members}
	}
	if !f.asks(stepBuild) {
		return f.flagCast
	}
	if f.build == buildNames {
		return cast{}
	}
	return presetTeam(f.pickedPreset(), f.size)
}

// pickedPreset is the preset picked, or the Free Agents drawn for this visit (drawing them if there are none yet).
func (f *assembleFlow) pickedPreset() presets.Preset {
	if f.build == buildFreeAgents {
		if len(f.freeAgents.Members) == 0 {
			f.freeAgents = presets.FreeAgents()
			f.draws++
		}
		return f.freeAgents
	}
	p, _ := presets.ByID(f.presetID)
	return p
}

// castKey names the base cast, so the cast page knows when it's a different one.
func (f *assembleFlow) castKey() string {
	switch {
	case f.action == actionEdit:
		return "roster:" + f.roster
	case f.build == buildNames:
		return "names"
	case f.build == buildFreeAgents:
		return fmt.Sprintf("free-agents:%d:%d", f.draws, f.size)
	}
	return fmt.Sprintf("preset:%s:%d", f.presetID, f.size)
}

// chosenCast is the cast as changed on the cast page, or else the base cast.
func (f *assembleFlow) chosenCast() cast {
	if !f.asks(stepCast) || f.castFor != f.castKey() {
		return f.baseCast()
	}
	return f.edited
}

// rosterName is the name typed in, or else the name argument.
func (f *assembleFlow) rosterName() string {
	if f.asksSetting(settingName) {
		return f.name
	}
	return f.args[0]
}

// emojiOptions previews each emoji mode with the cast's first member.
func (f *assembleFlow) emojiOptions() []huh.Option[roster.EmojiMode] {
	first := roster.Member{Emoji: "🧙", Name: "Gandalf"}
	if chosen := f.chosenCast(); len(chosen.members) > 0 {
		first = chosen.members[0]
	}
	return emojiModeOptions(first)
}

// castSummary is the cast on the last page: where it's from, and who's on it.
func castSummary(c cast) string {
	var lines []string
	if p, ok := presets.ByID(c.presetID); ok {
		lines = append(lines, p.Emoji+" "+p.Name)
	}
	lines = append(lines, fmt.Sprintf("Team of %d: %s", len(c.members), displayMembers(c.members, roster.EmojiOn)))
	return strings.Join(lines, "\n")
}

// noteText escapes the characters a huh note reads as formatting, so names show as typed.
func noteText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "_", `\_`, "*", `\*`, "`", "\\`").Replace(s)
}

// presetPreview shows a preset's name and source, and everyone on it.
func presetPreview(p presets.Preset, width int) string {
	wrap := lipgloss.NewStyle().Width(width)
	return lipgloss.JoinVertical(lipgloss.Left,
		wrap.Inherit(styleHeading).Render(p.Emoji+" "+p.Name),
		wrap.Inherit(styleMuted).Render(p.Source),
		"",
		styleMuted.Render(fmt.Sprintf("%d members", len(p.Members))),
		wrap.Render(displayMembers(p.Members, roster.EmojiOn)),
	)
}

// teamPreview shows who's on a team, one per line.
func teamPreview(c cast, width int) string {
	wrap := lipgloss.NewStyle().Width(width)
	members := make([]string, len(c.members))
	for i, m := range c.members {
		members[i] = roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn)
	}
	if len(members) == 0 {
		members = []string{styleFaint.Render("no one yet")}
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		styleMuted.Render(fmt.Sprintf("Your team · %d", len(c.members))), wrap.Render(strings.Join(members, "\n")))
}

// changedHooks reports whether an install's list of what it wrote includes hooks.
func changedHooks(installed []string) bool {
	for _, piece := range installed {
		if strings.HasPrefix(piece, "hooks in ") {
			return true
		}
	}
	return false
}
