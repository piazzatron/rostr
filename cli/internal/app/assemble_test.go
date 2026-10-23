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
			[]assembleStep{stepBuild, stepPreset, stepTeams, stepSettings}},
		{"first run, own names", assembleSetup{}, func(f *assembleFlow) { f.build = buildNames },
			[]assembleStep{stepBuild, stepTeams, stepSettings}},
		{"first run, free agents", assembleSetup{}, func(f *assembleFlow) { f.build = buildFreeAgents },
			[]assembleStep{stepBuild, stepTeams, stepSettings}},
		{"first run, every flag", assembleSetup{args: []string{"work"}, opts: assembleOptions{preset: "kanto", emoji: "off", noAutoDraft: true}, inCloud: true}, nil,
			nil},
		{"first run, a preset and nothing else", assembleSetup{opts: assembleOptions{preset: "kanto"}}, nil,
			[]assembleStep{stepSettings}},
		{"later run, new roster", assembleSetup{existing: existing}, nil,
			[]assembleStep{stepAction, stepBuild, stepPreset, stepTeams, stepSettings}},
		{"later run, replace a cast", assembleSetup{existing: existing}, func(f *assembleFlow) { f.action = actionReplace },
			[]assembleStep{stepAction, stepPickRoster, stepBuild, stepPreset, stepTeams}},
		{"later run, edit", assembleSetup{existing: existing}, func(f *assembleFlow) { f.action = actionEdit },
			[]assembleStep{stepAction, stepPickRoster, stepTeams, stepEditReview}},
		{"rostr edit", assembleSetup{existing: existing, edit: "work"}, nil,
			[]assembleStep{stepTeams, stepEditReview}},
		{"later run with a name", assembleSetup{existing: existing, args: []string{"play"}}, nil,
			[]assembleStep{stepBuild, stepPreset, stepTeams, stepSettings}},
		{"later run with a name and emoji mode", assembleSetup{existing: existing, args: []string{"play"}, opts: assembleOptions{emoji: "on"}}, nil,
			[]assembleStep{stepBuild, stepPreset, stepTeams}},
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

func TestAssembleWizardTeams(t *testing.T) {
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	teams := newCastEditor()
	f.presetID = "hogwarts"
	f.prepareTeams(teams)
	f.edited.aTeam = f.edited.aTeam[:2]
	if names := memberNames(f.chosenCast().aTeam); !slices.Equal(names, []string{"Harry", "Hermione"}) {
		t.Fatalf("A team %v", names)
	}

	// Another cast starts from its own teams, and the preset itself is untouched
	f.presetID = "kanto"
	if chosen := f.chosenCast(); !slices.Equal(memberNames(chosen.aTeam), memberNames(f.baseCast().aTeam)) {
		t.Fatalf("A team %v", memberNames(chosen.aTeam))
	}
	if p, _ := presetCast("hogwarts"); len(p.aTeam) != 6 {
		t.Fatal("editing changed the preset")
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

	// Adding, a few at once, fills the A team to six first; a name taken already is refused
	typeIn("🧮 Ada, Grace, Linus, Margaret, Alan, Barbara, Ken")
	if len(c.aTeam) != 6 || len(c.bTeam) != 1 || c.aTeam[0].Emoji != "🧮" || c.aTeam[1].Emoji == "" {
		t.Fatalf("cast %+v", c)
	}
	typeIn("grace")
	if e.err == nil || len(c.aTeam)+len(c.bTeam) != 7 {
		t.Fatal("added Grace twice")
	}
	e.Update(key(tea.KeyEsc))
	if e.mode != castBrowsing {
		t.Fatal("esc didn't stop adding")
	}

	// Space moves the one under the cursor to the other team, r renames, x removes
	e.cursor = 0
	e.Update(key(tea.KeySpace))
	if c.bTeam[len(c.bTeam)-1].Name != "Ada" || len(c.aTeam) != 5 {
		t.Fatalf("cast %+v", c)
	}
	e.cursor = 0
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	e.input.SetValue("🐚 Grace Hopper")
	e.Update(key(tea.KeyEnter))
	if c.aTeam[0] != (roster.Member{Name: "Grace Hopper", Emoji: "🐚"}) {
		t.Fatalf("renamed to %+v", c.aTeam[0])
	}
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if len(c.aTeam) != 4 {
		t.Fatalf("cast %+v", c)
	}

	// Done needs someone on the A team
	c.bTeam = append(c.bTeam, c.aTeam...)
	c.aTeam = nil
	if _, cmd := e.Update(key(tea.KeyEnter)); cmd != nil || e.err == nil {
		t.Fatal("finished with no A team")
	}
}

func TestAssembleWizardEmptyBTeam(t *testing.T) {
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
	expectView(t, s, "A team · 2", "B team · 0", "no one yet")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Ready to assemble?", "B team: no one")

	// It saves, and comes back to edit, with no one on the B team
	chosen := f.chosenCast()
	if len(chosen.aTeam) != 2 || len(chosen.bTeam) != 0 {
		t.Fatalf("cast %+v", chosen)
	}
	r, err := st.CreateRoster(ctx, store.Roster{Name: "work", EmojiMode: roster.EmojiOn, ATeam: chosen.aTeam, BTeam: chosen.bTeam})
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
	expectView(t, s, "Edit the work roster", "B team · 0")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "B team: no one", "Save")
	var out strings.Builder
	if err := saveEdit(ctx, &out, st, edit); err != nil {
		t.Fatal(err)
	}
	if saved, _ := st.Roster(ctx, "work"); len(saved.ATeam) != 2 || len(saved.BTeam) != 0 {
		t.Fatalf("saved %+v", saved)
	}
}

func TestAssembleWizardEdits(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "rostr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	p, _ := presetCast("hogwarts")
	r, err := st.CreateRoster(ctx, store.Roster{Name: "wizards", EmojiMode: roster.EmojiOff, ATeam: p.aTeam, BTeam: p.bTeam})
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
	expectView(t, s, "rostr · edit", "Step 1 of 2", "Edit the wizards roster", "Harry", "Neville", "switch team")

	// Move Harry to the B team, then save, keeping the roster's emoji mode
	drive(s, nil, key(tea.KeySpace))
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 2", "Save the wizards roster?", "Save")
	drive(s, nil, key(tea.KeyEnter))
	drive(s, nil, key(tea.KeyEnter))
	var out strings.Builder
	if err := saveEdit(ctx, &out, st, f); err != nil {
		t.Fatal(err)
	}
	saved, _ := st.Roster(ctx, "wizards")
	if saved.EmojiMode != roster.EmojiOff || len(saved.ATeam) != 5 || saved.BTeam[len(saved.BTeam)-1].Name != "Harry" {
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
	expectView(t, s, "rostr · assemble", "Step 1 of 4", "How do you want to build your roster?", "Free Agents", "esc quit")

	// The preset picker previews the highlighted roster, and searching moves it
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 4", "Pick a roster: type to search", "The Lord of the Rings", "Gandalf", "B team")
	if strings.Contains(s.View(), "Feeling chaotic") {
		t.Fatal("the preset picker lists Free Agents")
	}
	drive(s, nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hogwarts")})
	expectView(t, s, "Hermione", "Dumbledore")

	// Shift+tab goes back; coming forward again searches again
	drive(s, nil, key(tea.KeyShiftTab))
	expectView(t, s, "Step 1 of 4", "How do you want to build your roster?")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 4", "/hogwarts", "Hermione")

	// The team picker starts from the preset's A team, and space moves someone to the B team
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 3 of 4", "Who's on the roster?", "called up", "A team · 6", "space switch team")
	drive(s, nil, key(tea.KeySpace))
	expectView(t, s, "A team · 5")

	// The last page confirms everything, with the name from the preset
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 4 of 4", "Ready to assemble?", "A team:", "Roster name", "hogwarts", "Agents show up as",
		"rostr lives on", "Name new sessions", "Assemble")
	for range 5 {
		drive(s, nil, key(tea.KeyEnter))
	}
	if s.cancelled || f.presetID != "hogwarts" || f.name != "hogwarts" || !f.cloud || !f.autoDraft || len(f.chosenCast().aTeam) != 5 {
		t.Fatalf("answers: %+v", f)
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
	expectView(t, s, "Step 2 of 3", "Who's on the roster?", "A team · 6", "B team · 8")
	drawn := f.chosenCast()

	// The cast stays put on the way to the last page, and going back to the start draws a new one
	drive(s, nil, key(tea.KeyEnter))
	if !slices.Equal(f.chosenCast().aTeam, drawn.aTeam) {
		t.Fatal("the cast changed on the last page")
	}
	for _, k := range []tea.KeyType{tea.KeyShiftTab, tea.KeyShiftTab, tea.KeyEnter} {
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
