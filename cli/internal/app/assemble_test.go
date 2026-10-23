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
			[]assembleStep{stepBuild, stepPreset, stepTeams, stepSettings}},
		{"first run, own names", assembleSetup{}, func(f *assembleFlow) { f.build = buildNames },
			[]assembleStep{stepBuild, stepNames, stepTeams, stepSettings}},
		{"first run, free agents", assembleSetup{}, func(f *assembleFlow) { f.build = buildFreeAgents },
			[]assembleStep{stepBuild, stepTeams, stepSettings}},
		{"first run, surprise", assembleSetup{}, func(f *assembleFlow) { f.build = buildRandom },
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
			[]assembleStep{stepAction, stepPickRoster}},
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
	f.presetID = "hogwarts"
	f.teamsFor = f.castKey()
	f.aTeam = []string{"Dumbledore", "Hermione"}
	chosen := f.chosenCast()
	if names := memberNames(chosen.aTeam); !slices.Equal(names, []string{"Hermione", "Dumbledore"}) {
		t.Fatalf("A team %v", names)
	}
	base := f.baseCast()
	if len(chosen.bTeam) != len(base.aTeam)+len(base.bTeam)-2 {
		t.Fatalf("B team has %d", len(chosen.bTeam))
	}

	// Another cast starts from its own teams
	f.presetID = "kanto"
	if chosen := f.chosenCast(); !slices.Equal(memberNames(chosen.aTeam), memberNames(f.baseCast().aTeam)) {
		t.Fatalf("A team %v", memberNames(chosen.aTeam))
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
	expectView(t, s, "rostr · assemble", "Step 1 of 4", "How do you want to build your roster?", "Free Agents", "Surprise me", "esc quit")

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
	expectView(t, s, "Step 3 of 4", "Who's on the A team?", "called up", "A team · 6", "space toggle")
	drive(s, nil, key(tea.KeySpace))
	expectView(t, s, "A team · 5")

	// The last page confirms everything, with the name from the preset
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 4 of 4", "Ready to assemble?", "A team:", "Roster name", "hogwarts", "Agents show up as",
		"rostr lives on", "Name new sessions", "Assemble")
	for range 5 {
		drive(s, nil, key(tea.KeyEnter))
	}
	if s.cancelled || f.presetID != "hogwarts" || f.name != "hogwarts" || f.cloud || !f.autoDraft || len(f.chosenCast().aTeam) != 5 {
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

func TestAssembleWizardSurprise(t *testing.T) {
	f, err := newAssembleFlow(assembleSetup{})
	if err != nil {
		t.Fatal(err)
	}
	s, steps := f.screen(context.Background(), nil)
	s.buildForm(steps)
	drive(s, s.Init(), tea.WindowSizeMsg{Width: 100, Height: 30}, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 3", "Surprise: it's")
	if f.rolledID == "" || f.rolledID == presets.FreeAgentsID {
		t.Fatalf("rolled %q", f.rolledID)
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
