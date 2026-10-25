package app

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	bubblekey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

// castEditor is a form field for picking a cast: everyone the cast can come from in one list, where you pick who's on
// the team, add your own names, and rename people. The team is the picked members, in the list's order.
type castEditor struct {
	title, description string
	cast               *cast
	// validate, when set, checks the cast before the page is done
	validate func(cast) error

	cursor int
	mode   castEditorMode
	input  textinput.Model
	err    error

	focused       bool
	width, height int
	theme         huh.Theme
	dark          bool
}

type castEditorMode int

const (
	castBrowsing castEditorMode = iota
	castAdding
	castRenaming
)

func newCastEditor() *castEditor {
	input := textinput.New()
	input.Prompt = "> "
	return &castEditor{input: input, dark: true}
}

// reset points the editor at a cast, starting on the first in the list, or ready to add the first one to an empty list.
func (e *castEditor) reset(c *cast) {
	e.cast, e.cursor, e.err = c, 0, nil
	e.mode = castBrowsing
	if e.size() == 0 {
		e.startAdding()
	}
}

// size is how many are in the list, picked or not.
func (e *castEditor) size() int {
	if e.cast == nil {
		return 0
	}
	return len(e.cast.from)
}

// picked is the names on the team.
func (e *castEditor) picked() map[string]bool {
	picked := map[string]bool{}
	for _, m := range e.cast.members {
		picked[m.Name] = true
	}
	return picked
}

// setTeam makes the team everyone in the list whose name is picked, in the list's order.
func (e *castEditor) setTeam(picked map[string]bool) {
	e.cast.members = nil
	for _, m := range e.cast.from {
		if picked[m.Name] {
			e.cast.members = append(e.cast.members, m)
		}
	}
}

// toggle puts the member under the cursor on the team, or takes them off it.
func (e *castEditor) toggle() {
	picked := e.picked()
	name := e.cast.from[e.cursor].Name
	picked[name] = !picked[name]
	e.setTeam(picked)
}

func (e *castEditor) startAdding() {
	e.mode = castAdding
	e.input.SetValue("")
	e.input.Placeholder = `"🧮 Ada", or a few: "Ada, Grace"`
	e.input.Focus()
}

func (e *castEditor) startRenaming() {
	m := e.cast.from[e.cursor]
	e.mode = castRenaming
	e.input.SetValue(roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn))
	e.input.Placeholder = ""
	e.input.CursorEnd()
	e.input.Focus()
}

func (e *castEditor) stopTyping() {
	e.mode = castBrowsing
	e.input.Blur()
	e.err = nil
}

// add puts each comma-separated name typed in on the team. A name already in the list is picked, with its own emoji;
// any other is added to the end of the list, and gets an emoji no one else has if it has none.
func (e *castEditor) add(text string) error {
	picked := e.picked()
	var added []roster.Member
	last := -1
	for _, part := range strings.Split(text, ",") {
		m := roster.ParseMember(part)
		if m.Name == "" {
			continue
		}
		i := slices.IndexFunc(e.cast.from, func(listed roster.Member) bool { return roster.NamesMatch(listed.Name, m.Name) })
		switch {
		case i >= 0 && picked[e.cast.from[i].Name]:
			return fmt.Errorf("%s is already on your team", e.cast.from[i].Name)
		case i >= 0:
			picked[e.cast.from[i].Name], last = true, i
		default:
			if slices.ContainsFunc(added, func(other roster.Member) bool { return roster.NamesMatch(other.Name, m.Name) }) {
				return fmt.Errorf("%s is typed twice", m.Name)
			}
			added = append(added, m)
		}
	}
	everyone, err := roster.AssignMissingEmoji(append(slices.Clone(e.cast.from), added...))
	if err != nil {
		return err
	}
	e.cast.from = everyone
	for _, m := range added {
		picked[m.Name] = true
	}
	e.setTeam(picked)
	if len(added) > 0 {
		last = e.size() - 1
	}
	if last >= 0 {
		e.cursor = last
	}
	return nil
}

// rename changes the member under the cursor to what's typed in, keeping their emoji if none is given, and keeps them
// on the team if they were.
func (e *castEditor) rename(text string) error {
	m := roster.ParseMember(text)
	if m.Name == "" {
		return errors.New("type a name, with an emoji in front if you like")
	}
	for i, other := range e.cast.from {
		if i != e.cursor && roster.NamesMatch(other.Name, m.Name) {
			return fmt.Errorf("%s is already on the list", other.Name)
		}
	}
	old := e.cast.from[e.cursor]
	if m.Emoji == "" {
		m.Emoji = old.Emoji
	}
	picked := e.picked()
	picked[m.Name] = picked[old.Name]
	e.cast.from = slices.Clone(e.cast.from) // it can be a preset's own list
	e.cast.from[e.cursor] = m
	e.setTeam(picked)
	return nil
}

func (e *castEditor) Init() tea.Cmd { return nil }

func (e *castEditor) Update(msg tea.Msg) (huh.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		e.dark = msg.IsDark()
		return e, nil
	case tea.PasteMsg:
		if !e.focused || e.mode == castBrowsing {
			return e, nil
		}
		var cmd tea.Cmd
		e.input, cmd = e.input.Update(msg)
		return e, cmd
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok || !e.focused || e.cast == nil {
		return e, nil
	}
	if e.mode != castBrowsing {
		return e.updateTyping(keyMsg)
	}
	e.err = nil
	switch keyMsg.String() {
	case "up", "k":
		e.cursor = max(0, e.cursor-1)
	case "down", "j":
		e.cursor = min(e.size()-1, e.cursor+1)
	case "space", "x":
		if e.size() > 0 {
			e.toggle()
		}
	case "a", "+":
		e.startAdding()
	case "r", "e":
		if e.size() > 0 {
			e.startRenaming()
		}
	case "shift+tab":
		return e, huh.PrevField
	case "enter", "tab":
		if len(e.cast.members) == 0 {
			e.err = errors.New("pick at least one member")
			return e, nil
		}
		if e.validate != nil {
			if e.err = e.validate(*e.cast); e.err != nil {
				return e, nil
			}
		}
		return e, huh.NextField
	}
	return e, nil
}

func (e *castEditor) updateTyping(msg tea.KeyPressMsg) (huh.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if e.size() > 0 {
			e.stopTyping()
		}
		return e, nil
	case "shift+tab":
		if e.size() > 0 {
			e.stopTyping()
		}
		return e, huh.PrevField
	case "enter":
		text := strings.TrimSpace(e.input.Value())
		if e.mode == castAdding && text == "" {
			if e.size() > 0 {
				e.stopTyping()
			}
			return e, nil
		}
		var err error
		if e.mode == castAdding {
			err = e.add(text)
		} else {
			err = e.rename(text)
		}
		if err != nil {
			e.err = err
			return e, nil
		}
		e.err = nil
		if e.mode == castAdding {
			e.input.SetValue("") // ready for the next one
		} else {
			e.stopTyping()
		}
		return e, nil
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return e, cmd
}

// capturesKeys reports whether the editor is taking typing, so esc stops that instead of leaving the screen.
func (e *castEditor) capturesKeys() bool {
	return e.mode != castBrowsing && e.size() > 0
}

// footerKeys are the keys that work right now.
func (e *castEditor) footerKeys() [][2]string {
	switch e.mode {
	case castAdding:
		if e.size() == 0 {
			return [][2]string{{"enter", "add"}}
		}
		return [][2]string{{"enter", "add"}, {"esc", "done adding"}}
	case castRenaming:
		return [][2]string{{"enter", "save"}, {"esc", "cancel"}}
	}
	return [][2]string{{"↑/↓", "move"}, {"space", "pick"}, {"a", "add your own"}, {"r", "rename"}, {"enter", "done"}}
}

func (e *castEditor) View() string {
	// huh draws a field to measure it before giving it the form's theme, so the fallback isn't kept
	theme := huh.ThemeFunc(promptTheme).Theme(e.dark)
	if e.theme != nil {
		theme = e.theme.Theme(e.dark)
	}
	if e.cast == nil {
		e.cast = &cast{}
	}
	styles := theme.Blurred
	if e.focused {
		styles = theme.Focused
	}
	inner := e.width - styles.Base.GetHorizontalFrameSize()
	wrap := lipgloss.NewStyle().Width(max(1, inner))
	e.input.SetWidth(max(20, inner-lipgloss.Width(e.input.Prompt)-1)) // or the placeholder is cut to one character

	var lines []string
	if e.title != "" {
		lines = append(lines, wrap.Inherit(styles.Title).Render(e.title))
	}
	if e.description != "" {
		lines = append(lines, wrap.Inherit(styles.Description).Render(e.description))
	}
	switch e.mode {
	case castAdding:
		lines = append(lines, styles.Title.Render("Add your own"), e.input.View())
	case castRenaming:
		lines = append(lines, styles.Title.Render("Rename"), e.input.View())
	}
	if e.err != nil {
		lines = append(lines, styles.ErrorMessage.Render("* "+e.err.Error()))
	}

	// The list, scrolled to keep the cursor in view
	rows := e.rows(styles)
	room := max(3, e.height-len(lines)-2)
	if e.height > 0 && len(rows) > room {
		top := min(max(0, 1+e.cursor-room/2), len(rows)-room)
		rows = rows[top : top+room]
	}
	lines = append(lines, "")
	lines = append(lines, rows...)
	return styles.Base.Width(e.width).Render(strings.Join(lines, "\n"))
}

// rows are how many are picked, then everyone in the list, each marked picked or not, the one under the cursor marked.
func (e *castEditor) rows(styles huh.FieldStyles) []string {
	rows := []string{styles.Description.Render(fmt.Sprintf("%d of %d picked", len(e.cast.members), e.size()))}
	if e.size() == 0 {
		rows = append(rows, "  "+styles.TextInput.Placeholder.Render("no one yet"))
	}
	picked := e.picked()
	for i, m := range e.cast.from {
		label := roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn)
		row := styles.UnselectedPrefix.String() + styles.UnselectedOption.Render(label)
		if picked[m.Name] {
			row = styles.SelectedPrefix.String() + styles.SelectedOption.Render(label)
		}
		if i == e.cursor && e.mode == castBrowsing {
			row = styles.MultiSelectSelector.String() + row
		} else {
			row = strings.Repeat(" ", lipgloss.Width(styles.MultiSelectSelector.String())) + row
		}
		rows = append(rows, row)
	}
	return rows
}

func (e *castEditor) Focus() tea.Cmd {
	e.focused = true
	if e.mode != castBrowsing {
		return e.input.Focus()
	}
	return nil
}

func (e *castEditor) Blur() tea.Cmd {
	e.focused = false
	e.input.Blur()
	return nil
}

func (e *castEditor) Error() error { return e.err }

func (e *castEditor) Run() error { return huh.Run(e) }
func (e *castEditor) RunAccessible(w io.Writer, r io.Reader) error {
	return errors.New("the cast editor needs a terminal")
}
func (e *castEditor) Skip() bool                    { return false }
func (e *castEditor) Zoom() bool                    { return false }
func (e *castEditor) KeyBinds() []bubblekey.Binding { return nil }
func (e *castEditor) WithTheme(theme huh.Theme) huh.Field {
	if e.theme == nil {
		e.theme = theme
	}
	return e
}
func (e *castEditor) WithKeyMap(*huh.KeyMap) huh.Field         { return e }
func (e *castEditor) WithWidth(width int) huh.Field            { e.width = width; return e }
func (e *castEditor) WithHeight(height int) huh.Field          { e.height = height; return e }
func (e *castEditor) WithPosition(huh.FieldPosition) huh.Field { return e }
func (e *castEditor) GetKey() string                           { return "" }
func (e *castEditor) GetValue() any                            { return e.cast }
