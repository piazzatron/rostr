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
	f.presetID = "fellowship"
	f.prepareCast(page)
	if names := memberNames(f.chosenCast().members); len(names) != 16 || names[0] != "Gandalf" || names[15] != "Saruman" {
		t.Fatalf("a team of the default size: %v", names)
	}
	f.edited.members = f.edited.members[:2]
	if names := memberNames(f.chosenCast().members); !slices.Equal(names, []string{"Gandalf", "Frodo"}) {
		t.Fatalf("cast %v", names)
	}

	// Another size or preset starts from its own team, and the preset itself is untouched
	f.size = 4
	if chosen := f.chosenCast(); len(chosen.members) != 4 {
		t.Fatalf("cast %v", memberNames(chosen.members))
	}
	f.presetID, f.size = "kanto", 0
	if chosen := f.chosenCast(); !slices.Equal(chosen.members, f.baseCast().members) || len(chosen.members) != 16 {
		t.Fatalf("cast %v", memberNames(chosen.members))
	}
	if p, _ := presets.ByID("fellowship"); len(p.Members) != 20 || p.Members[1].Name != "Frodo" {
		t.Fatal("editing changed the preset")
	}
}

func TestTeamSize(t *testing.T) {
	fellowship, _ := presets.ByID("fellowship")
	for flag, want := range map[string]int{"": 16, "4": 4, " 8 ": 8, "all": 20, "ALL": 20, "99": 20} {
		size, err := parseTeamSize(flag)
		if err != nil {
			t.Fatalf("%q: %v", flag, err)
		}
		if c := presetTeam(fellowship, size); len(c.members) != want || len(c.from) != 20 || c.members[0].Name != "Gandalf" {
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

	// Adding, a few at once, puts them on the team in order; a name taken already is refused
	typeIn("🧮 Ada, Grace, Linus, Margaret, Alan, Barbara, Ken")
	if len(c.members) != 7 || len(c.from) != 7 || c.members[0].Emoji != "🧮" || c.members[1].Emoji == "" || c.members[6].Name != "Ken" {
		t.Fatalf("cast %+v", c)
	}
	typeIn("grace")
	if e.err == nil || len(c.from) != 7 {
		t.Fatal("added Grace twice")
	}
	e.Update(key(tea.KeyEsc))
	if e.mode != castBrowsing {
		t.Fatal("esc didn't stop adding")
	}

	// r renames, space takes someone off the team and puts them back, in their place
	e.cursor = 0
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	e.input.SetValue("🐚 Grace Hopper")
	e.Update(key(tea.KeyEnter))
	if c.members[0] != (roster.Member{Name: "Grace Hopper", Emoji: "🐚"}) {
		t.Fatalf("renamed to %+v", c.members[0])
	}
	e.Update(key(tea.KeySpace))
	if len(c.members) != 6 || c.members[0].Name != "Grace" || len(c.from) != 7 {
		t.Fatalf("cast %+v", c)
	}
	e.Update(key(tea.KeySpace))
	if len(c.members) != 7 || c.members[0].Name != "Grace Hopper" {
		t.Fatalf("cast %+v", c)
	}

	// Done needs someone on the team
	c.members = nil
	if _, cmd := e.Update(key(tea.KeyEnter)); cmd != nil || e.err == nil {
		t.Fatal("finished with no one on the team")
	}
}

func TestCastEditorPicksFromThePreset(t *testing.T) {
	fellowship, _ := presets.ByID("fellowship")
	c := presetTeam(fellowship, 4)
	c.from = slices.Clone(c.from)
	e := newCastEditor()
	e.WithTheme(promptTheme())
	e.Focus()
	e.reset(&c)

	// Space on Frodo takes him off; down to Galadriel and space puts her on, in the roster's order
	e.Update(key(tea.KeyDown))
	e.Update(key(tea.KeySpace))
	for range 9 {
		e.Update(key(tea.KeyDown))
	}
	e.Update(key(tea.KeySpace))
	if names := memberNames(c.members); !slices.Equal(names, []string{"Gandalf", "Samwise", "Aragorn", "Galadriel"}) {
		t.Fatalf("cast %v", names)
	}

	// Typing a name from the roster picks them, with their emoji; a new name joins the end of the list
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	for _, r := range "eowyn, 🧮 Ada" {
		e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	e.Update(key(tea.KeyEnter))
	if names := memberNames(c.members); !slices.Equal(names, []string{"Gandalf", "Samwise", "Aragorn", "Galadriel", "Éowyn", "Ada"}) {
		t.Fatalf("cast %v", names)
	}
	if c.members[4].Emoji != "⚔️" || len(c.from) != 21 || c.from[20].Name != "Ada" {
		t.Fatalf("cast %+v", c)
	}
	if len(fellowship.Members) != 20 || fellowship.Members[1].Name != "Frodo" {
		t.Fatal("picking changed the preset")
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
	expectView(t, s, "Ada", "Grace")
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 3, 3)

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
	expectStep(t, s, 1, 2)
	expectView(t, s, "Ada", "Grace")
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 2, 2)
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
	expectStep(t, s, 2, 3)

	// Shift+tab goes back with no one added, and esc quits
	drive(s, nil, key(tea.KeyShiftTab))
	expectStep(t, s, 1, 3)
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
	expectStep(t, s, 4, 4)
	if f.emoji != roster.EmojiOff {
		t.Fatalf("editing starts from %s", f.emoji)
	}
	for _, k := range []tea.KeyType{tea.KeyShiftTab, tea.KeyShiftTab, tea.KeyShiftTab, tea.KeyUp, tea.KeyUp, tea.KeyEnter, tea.KeyEnter, tea.KeyEnter, tea.KeyEnter, tea.KeyEnter} {
		drive(s, nil, key(k))
	}
	expectStep(t, s, 6, 6)
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
	expectStep(t, s, 1, 2)
	expectView(t, s, "Harry", "Neville")

	// Take Harry off, then save, keeping the roster's emoji mode
	drive(s, nil, key(tea.KeySpace))
	if n := len(f.chosenCast().members); n != 15 {
		t.Fatalf("%d picked after taking Harry off", n)
	}
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 2, 2)
	drive(s, nil, key(tea.KeyEnter))
	drive(s, nil, key(tea.KeyEnter))
	var out strings.Builder
	if err := saveEdit(ctx, &out, st, f); err != nil {
		t.Fatal(err)
	}
	saved, _ := st.Roster(ctx, "wizards")
	if saved.EmojiMode != roster.EmojiOff || len(saved.Members) != 15 || saved.Members[0].Name != "Hermione" {
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
	expectStep(t, s, 1, 5)

	// The preset picker previews the highlighted roster, and searching moves it
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 2, 5)
	expectView(t, s, "Gandalf", "Wormtongue")
	drive(s, nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hogwarts")})
	expectView(t, s, "Hermione", "Dumbledore")

	// Shift+tab goes back; coming forward again searches again
	drive(s, nil, key(tea.KeyShiftTab))
	expectStep(t, s, 1, 5)
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 2, 5)
	expectView(t, s, "Hermione")

	// The size picker starts at everyone (Hogwarts has 16), and previews the team each size makes
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 3, 5)
	expectView(t, s, "Draco")
	drive(s, nil, key(tea.KeyUp), key(tea.KeyUp))
	if strings.Contains(s.View(), "Snape") {
		t.Fatal("the preview of a team of four shows Snape")
	}

	// The cast page lists all of Hogwarts with that team picked, and shows the team beside it
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 4, 5)
	expectView(t, s, "Draco")
	drive(s, nil, key(tea.KeySpace))
	for range 4 {
		drive(s, nil, key(tea.KeyDown))
	}
	drive(s, nil, key(tea.KeySpace))
	expectView(t, s, "Snape")

	// The last page confirms everything, with the name from the preset
	drive(s, nil, key(tea.KeyEnter))
	expectStep(t, s, 5, 5)
	expectView(t, s, "Hermione", "hogwarts")
	for range 5 {
		drive(s, nil, key(tea.KeyEnter))
	}
	chosen := memberNames(f.chosenCast().members)
	if s.cancelled || f.presetID != "hogwarts" || f.name != "hogwarts" || !f.cloud || !f.autoDraft ||
		!slices.Equal(chosen, []string{"Hermione", "Ron", "Dumbledore", "Snape"}) {
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
	expectStep(t, s, 2, 4)
	drive(s, nil, key(tea.KeyUp), key(tea.KeyEnter))
	expectStep(t, s, 3, 4)
	drawn := f.chosenCast()
	if len(drawn.members) != 8 {
		t.Fatalf("drew %d", len(drawn.members))
	}

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

// expectStep checks which page of the flow is showing, by its position rather than its wording.
func expectStep(t *testing.T, s *screen, current, total int) {
	t.Helper()
	if c, n := s.progress(); c != current || n != total {
		t.Fatalf("on step %d of %d, want %d of %d:\n%s", c, n, current, total, s.View())
	}
}

// expectView checks the screen shows some data, like member and roster names. Don't check its wording.
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
