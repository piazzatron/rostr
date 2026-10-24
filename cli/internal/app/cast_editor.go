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

// castEditor is a form field for building or changing a cast: the A team and the B team as one list, where you add
// people, rename them, change their emoji, remove them, and move them between teams.
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
	theme         *huh.Theme
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

func (e *castEditor) members() []roster.Member {
	return append(append([]roster.Member{}, e.cast.aTeam...), e.cast.bTeam...)
}

func (e *castEditor) size() int {
	if e.cast == nil {
		return 0
	}
	return len(e.cast.aTeam) + len(e.cast.bTeam)
}

// at finds a member by position in the list: the A team, then the B team.
func (e *castEditor) at(i int) (team *[]roster.Member, index int) {
	if i < len(e.cast.aTeam) {
		return &e.cast.aTeam, i
	}
	return &e.cast.bTeam, i - len(e.cast.aTeam)
}

func (e *castEditor) startAdding() {
	e.mode = castAdding
	e.input.SetValue("")
	e.input.Placeholder = `"🧮 Ada", or a few at once: "Ada, Grace, Linus"`
	e.input.Focus()
}

func (e *castEditor) startRenaming() {
	team, i := e.at(e.cursor)
	m := (*team)[i]
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

// add adds each comma-separated name typed in, filling the A team to six before the B team. Names without an emoji get
// one no one else has.
func (e *castEditor) add(text string) error {
	var added []roster.Member
	for _, part := range strings.Split(text, ",") {
		if m := roster.ParseMember(part); m.Name != "" {
			added = append(added, m)
		}
	}
	if err := e.checkNames(added, -1); err != nil {
		return err
	}
	everyone, err := roster.AssignMissingEmoji(append(e.members(), added...))
	if err != nil {
		return err
	}
	for _, m := range everyone[e.size():] {
		if len(e.cast.aTeam) < roster.ATeamSize {
			e.cast.aTeam = append(e.cast.aTeam, m)
		} else {
			e.cast.bTeam = append(e.cast.bTeam, m)
		}
	}
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
	team, i := e.at(e.cursor)
	if m.Emoji == "" {
		m.Emoji = (*team)[i].Emoji
	}
	(*team)[i] = m
	return nil
}

// checkNames refuses names already in the cast, other than the member at skip, or typed twice.
func (e *castEditor) checkNames(names []roster.Member, skip int) error {
	taken := map[string]string{}
	for i, m := range e.members() {
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

// move puts the member under the cursor at the end of the other team, and keeps the cursor on them.
func (e *castEditor) move() {
	team, i := e.at(e.cursor)
	m := (*team)[i]
	*team = slices.Delete(*team, i, i+1)
	if team == &e.cast.aTeam {
		e.cast.bTeam = append(e.cast.bTeam, m)
		e.cursor = e.size() - 1
	} else {
		e.cast.aTeam = append(e.cast.aTeam, m)
		e.cursor = len(e.cast.aTeam) - 1
	}
}

func (e *castEditor) remove() {
	team, i := e.at(e.cursor)
	*team = slices.Delete(*team, i, i+1)
	e.cursor = max(0, min(e.cursor, e.size()-1))
}

func (e *castEditor) Init() tea.Cmd { return nil }

func (e *castEditor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
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
	case "a", "+":
		e.startAdding()
	case " ", "left", "right":
		if e.size() > 0 {
			e.move()
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
		if len(e.cast.aTeam) == 0 {
			e.err = errors.New("the A team needs at least one member")
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

// capturesKeys reports whether the editor is taking typing, so esc stops typing instead of leaving the screen.
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
	return [][2]string{{"space", "switch team"}, {"a", "add"}, {"r", "rename"}, {"x", "remove"}, {"enter", "done"}}
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
	}
	if e.err != nil {
		lines = append(lines, styles.ErrorMessage.Render("* "+e.err.Error()))
	}

	// The teams, scrolled to keep the cursor in view
	rows := e.rows(styles)
	room := max(3, e.height-len(lines)-2)
	if e.height > 0 && len(rows) > room {
		top := min(max(0, e.cursorRow()-room/2), len(rows)-room)
		rows = rows[top : top+room]
	}
	lines = append(lines, "")
	lines = append(lines, rows...)
	return styles.Base.Width(e.width).Render(strings.Join(lines, "\n"))
}

// rows are the two team headings and their members, the one under the cursor marked.
func (e *castEditor) rows(styles huh.FieldStyles) []string {
	var rows []string
	member := func(m roster.Member, i int) string {
		label := roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn)
		if i == e.cursor && e.mode == castBrowsing {
			return styles.SelectSelector.String() + styles.SelectedOption.Render(label)
		}
		return "  " + styles.Option.Render(label)
	}
	team := func(heading string, members []roster.Member, offset int) {
		rows = append(rows, styleMuted.Render(fmt.Sprintf("%s · %d", heading, len(members))))
		if len(members) == 0 {
			rows = append(rows, "  "+styleFaint.Render("no one yet"))
		}
		for i, m := range members {
			rows = append(rows, member(m, offset+i))
		}
	}
	team("A team", e.cast.aTeam, 0)
	rows = append(rows, "")
	team("B team", e.cast.bTeam, len(e.cast.aTeam))
	return rows
}

// cursorRow is the cursor's line among the rows.
func (e *castEditor) cursorRow() int {
	if e.cursor < len(e.cast.aTeam) {
		return 1 + e.cursor
	}
	return 1 + max(1, len(e.cast.aTeam)) + 2 + (e.cursor - len(e.cast.aTeam))
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
