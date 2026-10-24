package app

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/piazzatron/rostr/cli/internal/presets"
	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

func TestAssembleWizardSteps(t *testing.T) {
	existing := []store.Roster{{Name: "work"}}
	tests := []struct {
		name   string
		setup  assembleSetup
		answer func(*assembleFlow)
		want   []assembleStep
	}{
		{"first run", assembleSetup{}, nil,
			[]assembleStep{stepBuild, stepPreset, stepSize, stepCast, stepSettings}},
		{"first run, own names", assembleSetup{}, func(f *assembleFlow) { f.build = buildNames },
			[]assembleStep{stepBuild, stepCast, stepSettings}},
		{"first run, free agents", assembleSetup{}, func(f *assembleFlow) { f.build = buildFreeAgents },
			[]assembleStep{stepBuild, stepSize, stepCast, stepSettings}},
		{"first run, every flag", assembleSetup{args: []string{"work"}, opts: assembleOptions{preset: "kanto", emoji: "off", noAutoDraft: true}, inCloud: true}, nil,
			nil},
		{"first run, a preset and nothing else", assembleSetup{opts: assembleOptions{preset: "kanto"}}, nil,
			[]assembleStep{stepSettings}},
		{"later run, new roster", assembleSetup{existing: existing}, nil,
			[]assembleStep{stepAction, stepBuild, stepPreset, stepSize, stepCast, stepSettings}},
		{"later run, replace a cast", assembleSetup{existing: existing}, func(f *assembleFlow) { f.action = actionReplace },
			[]assembleStep{stepAction, stepPickRoster, stepBuild, stepPreset, stepSize, stepCast}},
		{"later run, edit", assembleSetup{existing: existing}, func(f *assembleFlow) { f.action = actionEdit },
			[]assembleStep{stepAction, stepPickRoster, stepCast, stepEditReview}},
		{"rostr edit", assembleSetup{existing: existing, edit: "work"}, nil,
			[]assembleStep{stepCast, stepEditReview}},
		{"later run with a name", assembleSetup{existing: existing, args: []string{"play"}}, nil,
			[]assembleStep{stepBuild, stepPreset, stepSize, stepCast, stepSettings}},
		{"later run with a name and emoji mode", assembleSetup{existing: existing, args: []string{"play"}, opts: assembleOptions{emoji: "on"}}, nil,
			[]assembleStep{stepBuild, stepPreset, stepSize, stepCast}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := newAssembleFlow(tt.setup)
			if err != nil {
				t.Fatal(err)
			}
			if tt.answer != nil {
				tt.answer(f)
			}
			if got := f.visibleSteps(); !slices.Equal(got, tt.want) {
				t.Fatalf("steps %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAssembleWizardSettings(t *testing.T) {
	existing := []store.Roster{{Name: "work"}}
	tests := []struct {
		name  string
		setup assembleSetup
		want  []assembleSetting
	}{
		{"first run", assembleSetup{}, []assembleSetting{settingName, settingEmoji, settingWhere, settingAutoDraft}},
		{"first run in the cloud, no auto-draft", assembleSetup{inCloud: true, opts: assembleOptions{noAutoDraft: true}},
			[]assembleSetting{settingName, settingEmoji}},
		{"first run in a repo: the default needs no link", assembleSetup{repo: "acme/api"},
			[]assembleSetting{settingName, settingEmoji, settingWhere, settingAutoDraft}},
		{"later run", assembleSetup{existing: existing}, []assembleSetting{settingName, settingEmoji}},
		{"later run in a repo asks to link it", assembleSetup{existing: existing, repo: "acme/api"},
			[]assembleSetting{settingName, settingEmoji, settingLink}},
		{"later run in a repo with --link", assembleSetup{existing: existing, repo: "acme/api", opts: assembleOptions{link: true}},
			[]assembleSetting{settingName, settingEmoji}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := newAssembleFlow(tt.setup)
			if err != nil {
				t.Fatal(err)
			}
			var got []assembleSetting
			for _, setting := range assembleSettings {
				if f.asksSetting(setting) {
					got = append(got, setting)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("settings %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAssembleWizardCast(t *testing.T) {
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	page := newCastEditor()
	f.presetID = "hogwarts"
	f.prepareCast(page)
	if names := memberNames(f.chosenCast().members); !slices.Equal(names, []string{"Harry", "Hermione", "Ron", "Dumbledore", "Snape", "Hagrid"}) {
		t.Fatalf("a team of the default size: %v", names)
	}
	f.edited.members = f.edited.members[:2]
	if names := memberNames(f.chosenCast().members); !slices.Equal(names, []string{"Harry", "Hermione"}) {
		t.Fatalf("cast %v", names)
	}

	// Another size or preset starts from its own team, and the preset itself is untouched
	f.size = 4
	if chosen := f.chosenCast(); len(chosen.members) != 4 {
		t.Fatalf("cast %v", memberNames(chosen.members))
	}
	f.presetID, f.size = "kanto", 0
	if chosen := f.chosenCast(); !slices.Equal(chosen.members, f.baseCast().members) || len(chosen.members) != 14 {
		t.Fatalf("cast %v", memberNames(chosen.members))
	}
	if p, _ := presets.ByID("hogwarts"); len(p.Members) != 14 || p.Members[2].Name != "Ron" {
		t.Fatal("editing changed the preset")
	}
}

func TestTeamSize(t *testing.T) {
	hogwarts, _ := presets.ByID("hogwarts")
	for flag, want := range map[string]int{"": 6, "4": 4, " 10 ": 10, "all": 14, "ALL": 14, "99": 14} {
		size, err := parseTeamSize(flag)
		if err != nil {
			t.Fatalf("%q: %v", flag, err)
		}
		if c := presetTeam(hogwarts, size); len(c.members) != want || len(c.from) != 14 || c.members[0].Name != "Harry" {
			t.Errorf("%q: a team of %d, want %d", flag, len(c.members), want)
		}
	}
	for _, flag := range []string{"0", "-2", "six", "6.5"} {
		if _, err := parseTeamSize(flag); err == nil {
			t.Errorf("%q: accepted", flag)
		}
	}
}

func TestCastEditor(t *testing.T) {
	c := cast{}
	e := newCastEditor()
	e.WithTheme(promptTheme())
	e.Focus()
	e.reset(&c)
	if e.mode != castAdding {
		t.Fatal("an empty cast doesn't start adding")
	}
	typeIn := func(text string) {
		for _, r := range text {
			e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		e.Update(key(tea.KeyEnter))
	}

	// Adding, a few at once, keeps their order; a name taken already is refused
	typeIn("🧮 Ada, Grace, Linus, Margaret, Alan, Barbara, Ken")
	if len(c.members) != 7 || c.members[0].Emoji != "🧮" || c.members[1].Emoji == "" || c.members[6].Name != "Ken" {
		t.Fatalf("cast %+v", c)
	}
	typeIn("grace")
	if e.err == nil || len(c.members) != 7 {
		t.Fatal("added Grace twice")
	}
	e.Update(key(tea.KeyEsc))
	if e.mode != castBrowsing {
		t.Fatal("esc didn't stop adding")
	}

	// With no preset there's no one to swap in; r renames, x removes
	e.cursor = 0
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if e.mode != castBrowsing {
		t.Fatal("swapping with no one to swap in")
	}
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	e.input.SetValue("🐚 Grace Hopper")
	e.Update(key(tea.KeyEnter))
	if c.members[0] != (roster.Member{Name: "Grace Hopper", Emoji: "🐚"}) {
		t.Fatalf("renamed to %+v", c.members[0])
	}
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if len(c.members) != 6 || c.members[0].Name != "Grace" {
		t.Fatalf("cast %+v", c)
	}

	// Done needs someone on the roster
	c.members = nil
	if _, cmd := e.Update(key(tea.KeyEnter)); cmd != nil || e.err == nil {
		t.Fatal("finished with no one on the roster")
	}
}

func TestCastEditorSwapsInFromThePreset(t *testing.T) {
	fellowship, _ := presets.ByID("fellowship")
	c := presetTeam(fellowship, 4)
	e := newCastEditor()
	e.WithTheme(promptTheme())
	e.Focus()
	e.reset(&c)
	if got := memberNames(e.swappable()); len(got) != 16 || got[0] != "Legolas" {
		t.Fatalf("swappable %v", got)
	}

	// s on Frodo, then down to Gimli, swaps Gimli in where Frodo was
	e.Update(key(tea.KeyDown))
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if e.mode != castSwapping || !e.capturesKeys() {
		t.Fatal("s didn't start swapping")
	}
	e.Update(key(tea.KeyDown))
	e.Update(key(tea.KeyEnter))
	if names := memberNames(c.members); !slices.Equal(names, []string{"Gandalf", "Gimli", "Samwise", "Aragorn"}) {
		t.Fatalf("cast %v", names)
	}
	// Frodo is back among those who can be swapped in, in the preset's order; esc cancels a swap
	if got := memberNames(e.swappable()); got[0] != "Frodo" || slices.Contains(got, "Gimli") {
		t.Fatalf("swappable %v", got)
	}
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	e.Update(key(tea.KeyEsc))
	if e.mode != castBrowsing || c.members[1].Name != "Gimli" {
		t.Fatalf("esc swapped: %v", memberNames(c.members))
	}

	// Typing a name from the preset, without an emoji, adds them with theirs
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	for _, r := range "eowyn" {
		e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	e.Update(key(tea.KeyEnter))
	if last := c.members[len(c.members)-1]; last != (roster.Member{Name: "Éowyn", Emoji: "⚔️"}) {
		t.Fatalf("added %+v", last)
	}
	if fellowship.Members[1].Name != "Frodo" {
		t.Fatal("swapping changed the preset")
	}
}

func TestAssembleWizardOwnNames(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "rostr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(ctx, st)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 34}, key(tea.KeyDown), key(tea.KeyDown))
	drive(s, nil, key(tea.KeyEnter))
	drive(s, nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("🧮 Ada, Grace")})
	drive(s, nil, key(tea.KeyEnter))
	drive(s, nil, key(tea.KeyEsc))
	expectView(t, s, "Your team · 2", "Ada", "Grace")
	if strings.Contains(s.View(), "swap") {
		t.Fatal("offers swaps for your own names")
	}
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Ready to assemble?", "Team of 2: 🧮 Ada")

	// It saves, and comes back to edit
	chosen := f.chosenCast()
	if len(chosen.members) != 2 {
		t.Fatalf("cast %+v", chosen)
	}
	r, err := st.CreateRoster(ctx, store.Roster{Name: "work", EmojiMode: roster.EmojiOn, Members: chosen.members})
	if err != nil {
		t.Fatal(err)
	}
	edit, err := newAssembleFlow(assembleSetup{existing: []store.Roster{r}, edit: "work"})
	if err != nil {
		t.Fatal(err)
	}
	s, steps = edit.screen(ctx, st)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 34})
	expectView(t, s, "Edit the work roster", "Your team · 2")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Team of 2", "Save")
	var out strings.Builder
	if err := saveEdit(ctx, &out, st, edit); err != nil {
		t.Fatal(err)
	}
	if saved, _ := st.Roster(ctx, "work"); len(saved.Members) != 2 {
		t.Fatalf("saved %+v", saved)
	}
}

func TestAssembleWizardLeavesAnEmptyCast(t *testing.T) {
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(context.Background(), nil)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 34}, key(tea.KeyDown), key(tea.KeyDown))
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Who's on your roster?", "shift+tab back", "esc quit")

	// Shift+tab goes back with no one added, and esc quits
	drive(s, nil, key(tea.KeyShiftTab))
	expectView(t, s, "How do you want to build your roster?")
	drive(s, nil, key(tea.KeyEnter))
	drive(s, nil, key(tea.KeyEsc))
	if !s.cancelled {
		t.Fatal("esc didn't quit an empty cast")
	}
}

func TestAssembleWizardNewRosterDoesntTakeAnEditedEmojiMode(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "rostr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, _ := presets.ByID("hogwarts")
	r := store.Roster{Name: "wizards", EmojiMode: roster.EmojiOff, Members: p.Members}
	f, err := newAssembleFlow(assembleSetup{existing: []store.Roster{r}})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(context.Background(), st)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 40})
	for _, k := range []tea.KeyType{tea.KeyDown, tea.KeyDown, tea.KeyEnter, tea.KeyEnter, tea.KeyEnter} {
		drive(s, nil, key(k))
	}
	expectView(t, s, "Save the wizards roster?")
	if f.emoji != roster.EmojiOff {
		t.Fatalf("editing starts from %s", f.emoji)
	}
	for _, k := range []tea.KeyType{tea.KeyShiftTab, tea.KeyShiftTab, tea.KeyShiftTab, tea.KeyUp, tea.KeyUp, tea.KeyEnter, tea.KeyEnter, tea.KeyEnter, tea.KeyEnter, tea.KeyEnter} {
		drive(s, nil, key(k))
	}
	expectView(t, s, "Ready to assemble?")
	if f.emoji != roster.EmojiOn {
		t.Fatalf("a new roster starts from %s", f.emoji)
	}
}

func TestAssembleWizardChecksAReplacedCast(t *testing.T) {
	r := store.Roster{Name: "work", EmojiMode: roster.EmojiOnly, Members: []roster.Member{{Name: "Ada", Emoji: "🧮"}}}
	f, err := newAssembleFlow(assembleSetup{existing: []store.Roster{r}})
	if err != nil {
		t.Fatal(err)
	}
	f.action, f.roster, f.build = actionReplace, "work", buildNames
	page := newCastEditor()
	f.prepareCast(page)
	page.Focus()
	f.edited = cast{members: []roster.Member{{Name: "Ada", Emoji: "🧮"}, {Name: "Grace", Emoji: "🧮"}}}
	page.cast, page.mode = &f.edited, castBrowsing
	if _, cmd := page.Update(key(tea.KeyEnter)); cmd != nil || page.err == nil {
		t.Fatal("finished a cast its roster's emoji mode can't save")
	}
}

func TestAssembleWizardEdits(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "rostr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	p, _ := presets.ByID("hogwarts")
	r, err := st.CreateRoster(ctx, store.Roster{Name: "wizards", EmojiMode: roster.EmojiOff, Members: p.Members})
	if err != nil {
		t.Fatal(err)
	}
	f, err := newAssembleFlow(assembleSetup{existing: []store.Roster{r}, edit: "wizards"})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(ctx, st)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 40})
	expectView(t, s, "rostr · edit", "Step 1 of 2", "Edit the wizards roster", "Harry", "Neville", "x remove")
	if strings.Contains(s.View(), "swap") {
		t.Fatal("offers swaps with no preset to swap from")
	}

	// Remove Harry, then save, keeping the roster's emoji mode
	drive(s, nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 2", "Save the wizards roster?", "Save")
	drive(s, nil, key(tea.KeyEnter))
	drive(s, nil, key(tea.KeyEnter))
	var out strings.Builder
	if err := saveEdit(ctx, &out, st, f); err != nil {
		t.Fatal(err)
	}
	saved, _ := st.Roster(ctx, "wizards")
	if saved.EmojiMode != roster.EmojiOff || len(saved.Members) != 13 || saved.Members[0].Name != "Hermione" {
		t.Fatalf("saved %+v", saved)
	}
}

func memberNames(members []roster.Member) []string {
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.Name
	}
	return names
}

func TestAssembleWizardRejectsBadFlags(t *testing.T) {
	if _, err := newAssembleFlow(assembleSetup{opts: assembleOptions{preset: "nope"}}); err == nil {
		t.Fatal("no error for an unknown preset")
	}
	if _, err := newAssembleFlow(assembleSetup{opts: assembleOptions{emoji: "sometimes"}}); err == nil {
		t.Fatal("no error for an unknown emoji mode")
	}
}

func TestAssembleWizardScreen(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "rostr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(context.Background(), st)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 30})
	expectView(t, s, "rostr · assemble", "Step 1 of 5", "How do you want to build your roster?", "Free Agents", "esc quit")

	// The preset picker previews the highlighted roster, and searching moves it
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 5", "Pick a roster: type to search", "The Lord of the Rings", "Gandalf", "Wormtongue", "20 members")
	if strings.Contains(s.View(), "Feeling chaotic") {
		t.Fatal("the preset picker lists Free Agents")
	}
	drive(s, nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hogwarts")})
	expectView(t, s, "Hermione", "Dumbledore")

	// Shift+tab goes back; coming forward again searches again
	drive(s, nil, key(tea.KeyShiftTab))
	expectView(t, s, "Step 1 of 5", "How do you want to build your roster?")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 5", "/hogwarts", "Hermione")

	// The size picker starts at six, and previews the team each size makes
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 3 of 5", "How big is your team?", "first few of Hogwarts", "All 14", "Hagrid", "8 more to swap in")
	drive(s, nil, key(tea.KeyUp))
	if strings.Contains(s.View(), "Hagrid") {
		t.Fatal("the preview of a team of four shows Hagrid")
	}

	// The cast page starts from that team, and swaps someone in from the rest of Hogwarts
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 4 of 5", "Who's on the roster?", "Your team · 4", "10 more to swap in", "s swap")
	drive(s, nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	expectView(t, s, "Swap out ⚡ Harry for", "Snape", "↑/↓ move", "esc cancel")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Your team · 4", "Snape")

	// The last page confirms everything, with the name from the preset
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 5 of 5", "Ready to assemble?", "Team of 4: 🧪 Snape", "Roster name", "hogwarts", "Agents show up as",
		"rostr lives on", "Name new sessions", "Assemble")
	for range 5 {
		drive(s, nil, key(tea.KeyEnter))
	}
	chosen := memberNames(f.chosenCast().members)
	if s.cancelled || f.presetID != "hogwarts" || f.name != "hogwarts" || !f.cloud || !f.autoDraft ||
		!slices.Equal(chosen, []string{"Snape", "Hermione", "Ron", "Dumbledore"}) {
		t.Fatalf("answers: %+v, cast %v", f, chosen)
	}

	// A narrow terminal drops the preview
	narrow := newScreen("assemble")
	narrow.panels = s.panels
	narrow.focused = s.panels[0].field
	narrow.width, narrow.height = 60, 24
	if _, panelWidth := narrow.layout(); panelWidth != 0 {
		t.Fatalf("panel %d wide at 60 columns", panelWidth)
	}
}

func TestAssembleWizardFreeAgents(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "rostr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(context.Background(), st)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 30}, key(tea.KeyDown), key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 4", "How big is your team?", "this random", "All 14")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 3 of 4", "Who's on the roster?", "Your team · 6", "8 more to swap in")
	drawn := f.chosenCast()

	// The cast stays put on the way to the last page and back to the size, and going back to the start draws a new one
	drive(s, nil, key(tea.KeyEnter))
	if !slices.Equal(f.chosenCast().members, drawn.members) {
		t.Fatal("the cast changed on the last page")
	}
	drive(s, nil, key(tea.KeyShiftTab), key(tea.KeyShiftTab))
	if f.draws != 1 || !slices.Equal(f.chosenCast().members, drawn.members) {
		t.Fatal("going back to the size drew a new cast")
	}
	for _, k := range []tea.KeyType{tea.KeyShiftTab, tea.KeyEnter} {
		drive(s, nil, key(k))
	}
	if f.draws != 2 {
		t.Fatalf("%d draws", f.draws)
	}
}

func TestScreenCancels(t *testing.T) {
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(context.Background(), nil)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 80, Height: 24}, key(tea.KeyEsc))
	if !s.cancelled {
		t.Fatal("esc didn't cancel")
	}
}

func key(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

func expectView(t *testing.T, s *screen, want ...string) {
	t.Helper()
	view := s.View()
	for _, w := range want {
		if !strings.Contains(view, w) {
			t.Fatalf("view doesn't show %q:\n%s", w, view)
		}
	}
}

// drive runs cmd and sends msgs to the screen, running the commands they return until there are none left. Commands
// that wait (like the cursor's blink) are dropped.
func drive(s *screen, cmd tea.Cmd, msgs ...tea.Msg) {
	queue := []tea.Cmd{cmd}
	for _, msg := range msgs {
		queue = append(queue, func() tea.Msg { return msg })
	}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		result := make(chan tea.Msg, 1)
		go func() { result <- next() }()
		var msg tea.Msg
		select {
		case msg = <-result:
		case <-time.After(50 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case nil, tea.QuitMsg:
			continue
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		}
		// tea.Sequence's message is an unexported []tea.Cmd
		if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
			for i := range v.Len() {
				queue = append(queue, v.Index(i).Interface().(tea.Cmd))
			}
			continue
		}
		_, cmd := s.Update(msg)
		queue = append(queue, cmd)
	}
}
