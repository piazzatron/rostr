package app

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	bubblekey "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

// castEditor is a form field for building or changing a cast: one list, where you add people, rename them, change
// their emoji, remove them, and swap them for someone else from the preset the cast was picked from.
type castEditor struct {
	title, description string
	cast               *cast
	// validate, when set, checks the cast before the page is done
	validate func(cast) error

	cursor int
	// swapCursor is the highlighted member among those who can be swapped in
	swapCursor int
	mode       castEditorMode
	input      textinput.Model
	err        error

	focused       bool
	width, height int
	theme         *huh.Theme
}

type castEditorMode int

const (
	castBrowsing castEditorMode = iota
	castAdding
	castRenaming
	castSwapping
)

func newCastEditor() *castEditor {
	input := textinput.New()
	input.Prompt = "> "
	return &castEditor{input: input}
}

// reset points the editor at a cast, starting on its first member, or ready to add the first one to an empty cast.
func (e *castEditor) reset(c *cast) {
	e.cast, e.cursor, e.err = c, 0, nil
	e.mode = castBrowsing
	if e.size() == 0 {
		e.startAdding()
	}
}

func (e *castEditor) size() int {
	if e.cast == nil {
		return 0
	}
	return len(e.cast.members)
}

// swappable is everyone the cast was picked from who isn't on it, in the preset's order.
func (e *castEditor) swappable() []roster.Member {
	var out []roster.Member
	for _, m := range e.cast.from {
		if !slices.ContainsFunc(e.cast.members, func(on roster.Member) bool { return roster.NamesMatch(on.Name, m.Name) }) {
			out = append(out, m)
		}
	}
	return out
}

func (e *castEditor) startAdding() {
	e.mode = castAdding
	e.input.SetValue("")
	e.input.Placeholder = `"🧮 Ada", or a few at once: "Ada, Grace, Linus"`
	e.input.Focus()
}

func (e *castEditor) startRenaming() {
	m := e.cast.members[e.cursor]
	e.mode = castRenaming
	e.input.SetValue(roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn))
	e.input.Placeholder = ""
	e.input.CursorEnd()
	e.input.Focus()
}

func (e *castEditor) startSwapping() {
	e.mode, e.swapCursor = castSwapping, 0
}

func (e *castEditor) stopTyping() {
	e.mode = castBrowsing
	e.input.Blur()
	e.err = nil
}

// add adds each comma-separated name typed in to the end of the cast. A name from the preset without an emoji gets
// its emoji there; any other name without one gets one no one else has.
func (e *castEditor) add(text string) error {
	var added []roster.Member
	for _, part := range strings.Split(text, ",") {
		m := roster.ParseMember(part)
		if m.Name == "" {
			continue
		}
		if i := slices.IndexFunc(e.cast.from, func(p roster.Member) bool { return roster.NamesMatch(p.Name, m.Name) }); i >= 0 && m.Emoji == "" {
			m = e.cast.from[i]
		}
		added = append(added, m)
	}
	if err := e.checkNames(added, -1); err != nil {
		return err
	}
	everyone, err := roster.AssignMissingEmoji(append(slices.Clone(e.cast.members), added...))
	if err != nil {
		return err
	}
	e.cast.members = everyone
	e.cursor = e.size() - 1
	return nil
}

// rename changes the member under the cursor to what's typed in, keeping their emoji if none is given.
func (e *castEditor) rename(text string) error {
	m := roster.ParseMember(text)
	if m.Name == "" {
		return errors.New("type a name, with an emoji in front if you like")
	}
	if err := e.checkNames([]roster.Member{m}, e.cursor); err != nil {
		return err
	}
	if m.Emoji == "" {
		m.Emoji = e.cast.members[e.cursor].Emoji
	}
	e.cast.members[e.cursor] = m
	return nil
}

// checkNames refuses names already in the cast, other than the member at skip, or typed twice.
func (e *castEditor) checkNames(names []roster.Member, skip int) error {
	taken := map[string]string{}
	for i, m := range e.cast.members {
		if i != skip {
			taken[roster.NormalizeName(m.Name)] = m.Name
		}
	}
	for _, m := range names {
		if other, ok := taken[roster.NormalizeName(m.Name)]; ok {
			return fmt.Errorf("%s is already on the roster", other)
		}
		taken[roster.NormalizeName(m.Name)] = m.Name
	}
	return nil
}

// swap puts the highlighted swappable member in place of the member under the cursor.
func (e *castEditor) swap() {
	if in := e.swappable(); e.swapCursor < len(in) {
		e.cast.members[e.cursor] = in[e.swapCursor]
	}
}

func (e *castEditor) remove() {
	e.cast.members = slices.Delete(e.cast.members, e.cursor, e.cursor+1)
	e.cursor = max(0, min(e.cursor, e.size()-1))
}

func (e *castEditor) Init() tea.Cmd { return nil }

func (e *castEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok || !e.focused || e.cast == nil {
		return e, nil
	}
	switch e.mode {
	case castSwapping:
		return e.updateSwapping(keyMsg)
	case castAdding, castRenaming:
		return e.updateTyping(keyMsg)
	}
	e.err = nil
	switch keyMsg.String() {
	case "up", "k":
		e.cursor = max(0, e.cursor-1)
	case "down", "j":
		e.cursor = min(e.size()-1, e.cursor+1)
	case "a", "+":
		e.startAdding()
	case "s", " ":
		if e.size() > 0 && len(e.swappable()) > 0 {
			e.startSwapping()
		}
	case "r", "e":
		if e.size() > 0 {
			e.startRenaming()
		}
	case "x", "d", "delete", "backspace":
		if e.size() > 0 {
			e.remove()
		}
	case "shift+tab":
		return e, huh.PrevField
	case "enter", "tab":
		if e.size() == 0 {
			e.err = errors.New("a roster needs at least one member")
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

func (e *castEditor) updateSwapping(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		e.swapCursor = max(0, e.swapCursor-1)
	case "down", "j":
		e.swapCursor = min(len(e.swappable())-1, e.swapCursor+1)
	case "enter", " ", "s":
		e.swap()
		e.mode = castBrowsing
	case "esc":
		e.mode = castBrowsing
	case "shift+tab":
		e.mode = castBrowsing
		return e, huh.PrevField
	}
	return e, nil
}

func (e *castEditor) updateTyping(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

// capturesKeys reports whether the editor is taking typing or a choice, so esc stops that instead of leaving the
// screen.
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
	case castSwapping:
		return [][2]string{{"↑/↓", "move"}, {"enter", "swap in"}, {"esc", "cancel"}}
	}
	keys := [][2]string{{"a", "add"}, {"r", "rename"}, {"x", "remove"}, {"enter", "done"}}
	if len(e.swappable()) > 0 {
		keys = append([][2]string{{"s", "swap"}}, keys...)
	}
	return keys
}

func (e *castEditor) View() string {
	if e.theme == nil {
		e.theme = promptTheme()
	}
	if e.cast == nil {
		e.cast = &cast{}
	}
	styles := e.theme.Blurred
	if e.focused {
		styles = e.theme.Focused
	}
	inner := e.width - styles.Base.GetHorizontalFrameSize()
	wrap := lipgloss.NewStyle().Width(max(1, inner))
	e.input.Width = max(20, inner-lipgloss.Width(e.input.Prompt)-1) // or the placeholder is cut to one character

	var lines []string
	if e.title != "" {
		lines = append(lines, wrap.Inherit(styles.Title).Render(e.title))
	}
	if e.description != "" {
		lines = append(lines, wrap.Inherit(styles.Description).Render(e.description))
	}
	switch e.mode {
	case castAdding:
		lines = append(lines, styles.Title.Render("Add someone"), e.input.View())
	case castRenaming:
		lines = append(lines, styles.Title.Render("Rename"), e.input.View())
	case castSwapping:
		out := e.cast.members[e.cursor]
		lines = append(lines, styles.Title.Render("Swap out "+roster.Display(out.Emoji, out.Name, 1, roster.EmojiOn)+" for"))
	}
	if e.err != nil {
		lines = append(lines, styles.ErrorMessage.Render("* "+e.err.Error()))
	}

	// The cast, or who can be swapped in, scrolled to keep the cursor in view
	rows, cursorRow := e.rows(styles)
	room := max(3, e.height-len(lines)-2)
	if e.height > 0 && len(rows) > room {
		top := min(max(0, cursorRow-room/2), len(rows)-room)
		rows = rows[top : top+room]
	}
	lines = append(lines, "")
	lines = append(lines, rows...)
	return styles.Base.Width(e.width).Render(strings.Join(lines, "\n"))
}

// rows are the cast under its heading, the member under the cursor marked, and how many more can be swapped in; or,
// while swapping, everyone who can be. cursorRow is the marked row.
func (e *castEditor) rows(styles huh.FieldStyles) (rows []string, cursorRow int) {
	member := func(m roster.Member, marked bool) string {
		label := roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn)
		if marked {
			return styles.SelectSelector.String() + styles.SelectedOption.Render(label)
		}
		return "  " + styles.Option.Render(label)
	}
	if e.mode == castSwapping {
		for i, m := range e.swappable() {
			rows = append(rows, member(m, i == e.swapCursor))
		}
		return rows, e.swapCursor
	}

	rows = append(rows, styleMuted.Render(fmt.Sprintf("Your team · %d", e.size())))
	if e.size() == 0 {
		rows = append(rows, "  "+styleFaint.Render("no one yet"))
	}
	for i, m := range e.cast.members {
		rows = append(rows, member(m, i == e.cursor && e.mode == castBrowsing))
	}
	if more := len(e.swappable()); more > 0 {
		rows = append(rows, "", styleFaint.Render(fmt.Sprintf("%d more to swap in", more)))
	}
	return rows, 1 + e.cursor
}

func (e *castEditor) Focus() tea.Cmd {
	e.focused = true
	if e.mode == castAdding || e.mode == castRenaming {
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
func (e *castEditor) WithTheme(theme *huh.Theme) huh.Field {
	if e.theme == nil {
		e.theme = theme
	}
	return e
}
func (e *castEditor) WithAccessible(bool) huh.Field            { return e }
func (e *castEditor) WithKeyMap(*huh.KeyMap) huh.Field         { return e }
func (e *castEditor) WithWidth(width int) huh.Field            { e.width = width; return e }
func (e *castEditor) WithHeight(height int) huh.Field          { e.height = height; return e }
func (e *castEditor) WithPosition(huh.FieldPosition) huh.Field { return e }
func (e *castEditor) GetKey() string                           { return "" }
func (e *castEditor) GetValue() any                            { return e.cast }
