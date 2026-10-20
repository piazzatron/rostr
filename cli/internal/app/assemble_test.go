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
			[]assembleStep{stepBuild, stepPreset, stepName, stepEmoji, stepWhere, stepAutoDraft, stepConfirm}},
		{"first run, own names", assembleSetup{}, func(f *assembleFlow) { f.usePreset = false },
			[]assembleStep{stepBuild, stepNames, stepName, stepEmoji, stepWhere, stepAutoDraft, stepConfirm}},
		{"first run, in the cloud, no auto-draft", assembleSetup{inCloud: true, opts: assembleOptions{noAutoDraft: true}}, nil,
			[]assembleStep{stepBuild, stepPreset, stepName, stepEmoji, stepConfirm}},
		{"first run, every flag", assembleSetup{args: []string{"work"}, opts: assembleOptions{preset: "kanto", emoji: "off", noAutoDraft: true}, inCloud: true}, nil,
			[]assembleStep{stepConfirm}},
		{"later run, new roster", assembleSetup{existing: existing}, nil,
			[]assembleStep{stepAction, stepBuild, stepPreset, stepName, stepEmoji, stepConfirm}},
		{"later run, replace a cast", assembleSetup{existing: existing}, func(f *assembleFlow) { f.action = actionReplace },
			[]assembleStep{stepAction, stepPickRoster, stepBuild, stepPreset, stepConfirm}},
		{"later run, edit", assembleSetup{existing: existing}, func(f *assembleFlow) { f.action = actionEdit },
			[]assembleStep{stepAction, stepPickRoster}},
		{"later run with a name", assembleSetup{existing: existing, args: []string{"play"}}, nil,
			[]assembleStep{stepBuild, stepPreset, stepEmoji, stepConfirm}},
		{"later run in a repo asks to link it", assembleSetup{existing: existing, repo: "acme/api"}, nil,
			[]assembleStep{stepAction, stepBuild, stepPreset, stepName, stepEmoji, stepLink, stepConfirm}},
		{"later run in a repo with --link", assembleSetup{existing: existing, repo: "acme/api", opts: assembleOptions{link: true}}, nil,
			[]assembleStep{stepAction, stepBuild, stepPreset, stepName, stepEmoji, stepConfirm}},
		{"replacing a cast in a repo doesn't link", assembleSetup{existing: existing, repo: "acme/api"}, func(f *assembleFlow) { f.action = actionReplace },
			[]assembleStep{stepAction, stepPickRoster, stepBuild, stepPreset, stepConfirm}},
		{"first run in a repo: the default needs no link", assembleSetup{repo: "acme/api"}, nil,
			[]assembleStep{stepBuild, stepPreset, stepName, stepEmoji, stepWhere, stepAutoDraft, stepConfirm}},
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
	expectView(t, s, "rostr · assemble", "Step 1 of 7", "How do you want to build your roster?", "esc quit")

	// The preset picker previews the highlighted roster, and searching moves it
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 7", "Pick a roster: type to search", "The Lord of the Rings", "Gandalf", "B team")
	drive(s, nil, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hogwarts")})
	expectView(t, s, "Hermione", "Dumbledore")

	// Shift+tab goes back; coming forward again searches again
	drive(s, nil, key(tea.KeyShiftTab))
	expectView(t, s, "Step 1 of 7", "How do you want to build your roster?")
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Step 2 of 7", "/hogwarts", "Hermione")

	// The name defaults to the preset's id, and the last step sums it all up
	drive(s, nil, key(tea.KeyEnter))
	expectView(t, s, "Name this roster", "hogwarts")
	for range 4 {
		drive(s, nil, key(tea.KeyEnter))
	}
	expectView(t, s, "Step 7 of 7", "Assemble the hogwarts roster?", "⚡ Hogwarts · emoji on · this machine · auto-draft on")
	drive(s, nil, key(tea.KeyEnter))
	if s.cancelled || f.presetID != "hogwarts" || f.name != "hogwarts" || !f.confirmed || f.cloud || !f.autoDraft {
		t.Fatalf("answers: %+v", f)
	}

	// A narrow terminal drops the preview
	narrow := newScreen("assemble")
	narrow.panel = s.panel
	narrow.focused = s.panel.field
	narrow.width, narrow.height = 60, 24
	if _, panelWidth := narrow.layout(); panelWidth != 0 {
		t.Fatalf("panel %d wide at 60 columns", panelWidth)
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
