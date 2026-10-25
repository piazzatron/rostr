package app

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// screen runs an interactive flow full-screen, on the terminal's alternate screen: a "rostr · <command>" header with a
// step counter, the form's current step (with an optional side panel), and a line of keys at the bottom. Leaving it
// puts the terminal back as it was. Its colors follow the terminal's background, which Bubble Tea reports as a message.
type screen struct {
	command string
	panels  []*sidePanel
	// onFocus refreshes a step's dynamic text (titles, options, defaults) from earlier answers as it comes into view
	onFocus func(huh.Field) tea.Cmd

	form          *huh.Form
	steps         []screenStep
	width, height int
	focused       huh.Field
	goingBack     bool
	cancelled     bool
	dark          bool
}

// screenStep is one page of a flow, asked in its own group so shift+tab goes back to it: one question, or with more,
// several asked together. shown, when set, hides the step when earlier answers make it irrelevant.
type screenStep struct {
	field huh.Field
	more  []huh.Field
	shown func() bool
}

func (step screenStep) fields() []huh.Field {
	return append([]huh.Field{step.field}, step.more...)
}

// sidePanel is shown beside one step's field, when the terminal is wide enough.
type sidePanel struct {
	field     huh.Field
	formWidth int
	render    func(st styles, width int) string
}

const (
	screenPadX         = 2
	screenMaxWidth     = 100
	screenMaxFormWidth = 72
	sidePanelGap       = 2
	sidePanelMinWidth  = 24
	sidePanelMaxWidth  = 40
	// screenChromeHeight is every line that isn't the form: top padding, header, rule, blank, blank, footer, bottom padding
	screenChromeHeight = 7
)

func newScreen(command string) *screen {
	return &screen{command: command, dark: true}
}

// run asks the steps, one at a time, in a full-screen form. Cancelling (esc or ctrl+c) returns errCancelled.
func (s *screen) run(steps []screenStep) error {
	s.buildForm(steps)
	if _, err := tea.NewProgram(s, tea.WithOutput(os.Stderr)).Run(); err != nil {
		return err
	}
	if s.cancelled || s.form.State != huh.StateCompleted {
		return errCancelled
	}
	return nil
}

// buildForm puts each step in its own group, hidden whenever the step isn't shown.
func (s *screen) buildForm(steps []screenStep) {
	groups := make([]*huh.Group, len(steps))
	for i, step := range steps {
		groups[i] = huh.NewGroup(step.fields()...)
		if shown := step.shown; shown != nil {
			groups[i].WithHideFunc(func() bool { return !shown() })
		}
	}
	s.steps = steps
	// huh only tells the focused step the terminal's background, so every step takes its shades from the screen
	theme := huh.ThemeFunc(func(bool) *huh.Styles { return promptTheme(s.dark) })
	s.form = huh.NewForm(groups...).WithTheme(theme).WithShowHelp(false)
	s.form.SubmitCmd, s.form.CancelCmd = tea.Quit, tea.Quit
}

// forwardOnly checks answers only when moving on, so shift+tab can always go back past an unfinished one.
func (s *screen) forwardOnly(validate func(string) error) func(string) error {
	return func(value string) error {
		if s.goingBack {
			return nil
		}
		return validate(value)
	}
}

func (s *screen) Init() tea.Cmd {
	return tea.Batch(s.form.Init(), tea.RequestBackgroundColor)
}

func (s *screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		typing, ok := s.focused.(interface{ capturesKeys() bool })
		if key == "ctrl+c" || key == "esc" && (!ok || !typing.capturesKeys()) {
			s.cancelled = true
			return s, tea.Quit
		}
		s.goingBack = key == "shift+tab"
		_, cmd = s.form.Update(msg)
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
		cmd = s.resizeForm()
	case tea.BackgroundColorMsg:
		s.dark = msg.IsDark()
		_, cmd = s.form.Update(msg)
	default:
		_, cmd = s.form.Update(msg)
	}
	if s.form.State != huh.StateNormal {
		return s, cmd
	}

	// A new step came into view: fill in what depends on earlier answers, and size the form for it
	if field := s.form.GetFocusedField(); field != s.focused {
		s.focused = field
		var focusCmd tea.Cmd
		if s.onFocus != nil {
			focusCmd = s.onFocus(field)
		}
		return s, tea.Batch(cmd, focusCmd, s.resizeForm())
	}
	return s, cmd
}

func (s *screen) View() tea.View {
	view := tea.NewView(s.page())
	view.AltScreen = true
	return view
}

// page is everything on the screen: the header, the form's current step, and the keys.
func (s *screen) page() string {
	if s.width == 0 || s.form.State != huh.StateNormal {
		return ""
	}
	width := s.contentWidth()
	st := newStyles(s.dark)

	// Header: where you are, and how far along
	title := st.heading.Render("rostr") + st.accent.Render(" · "+s.command)
	if current, total := s.progress(); total > 1 {
		counter := st.muted.Render(fmt.Sprintf("Step %d of %d", current, total))
		title += strings.Repeat(" ", max(1, width-lipgloss.Width(title)-lipgloss.Width(counter))) + counter
	}
	rule := st.faint.Render(strings.Repeat("─", width))

	// Body: the form, with the side panel beside it when there's room
	body := s.form.View()
	if formWidth, panelWidth := s.layout(); panelWidth > 0 {
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(formWidth+sidePanelGap).Render(body),
			sidePanelStyle(st).Width(panelWidth).MaxHeight(s.formHeight()).Render(s.panel().render(st, panelWidth-sidePanelStyle(st).GetHorizontalFrameSize())))
	}

	// Footer: the keys, pinned to the bottom
	top := lipgloss.JoinVertical(lipgloss.Left, title, rule, "", body)
	footer := s.footer(st)
	if lipgloss.Width(footer) > width {
		footer = strings.ReplaceAll(footer, st.faint.Render("  ·  "), "  ")
	}
	page := top + strings.Repeat("\n", max(2, s.height-2-lipgloss.Height(top))) + lipgloss.NewStyle().MaxWidth(width).Render(footer)
	return lipgloss.NewStyle().Padding(1, screenPadX).Render(page)
}

// progress is the focused step's position among the steps that are shown, given the answers so far.
func (s *screen) progress() (current, total int) {
	for _, step := range s.steps {
		if step.shown != nil && !step.shown() {
			continue
		}
		total++
		if slices.Contains(step.fields(), s.focused) {
			current = total
		}
	}
	return current, total
}

func (s *screen) footer(st styles) string {
	keys := [][2]string{{"↑/↓", "move"}, {"←/→", "choose"}, {"enter", "select"}}
	if field, ok := s.focused.(interface{ footerKeys() [][2]string }); ok {
		keys = field.footerKeys()
	}
	typing, _ := s.focused.(interface{ capturesKeys() bool })
	if typing != nil && typing.capturesKeys() {
		return joinKeys(st, keys)
	}
	if len(s.steps) > 1 {
		keys = append(keys, [2]string{"shift+tab", "back"})
	}
	keys = append(keys, [2]string{"esc", "quit"})
	return joinKeys(st, keys)
}

func joinKeys(st styles, keys [][2]string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = st.muted.Render(k[0]) + " " + st.faint.Render(k[1])
	}
	return strings.Join(parts, st.faint.Render("  ·  "))
}

// resizeForm tells the form how much room the focused step has.
func (s *screen) resizeForm() tea.Cmd {
	if s.width == 0 {
		return nil
	}
	formWidth, _ := s.layout()
	_, cmd := s.form.Update(tea.WindowSizeMsg{Width: formWidth, Height: s.formHeight()})
	// Every step gets the whole height, so one that grows after the form is built (like the cast editor) has room
	s.form.WithHeight(s.formHeight())
	return cmd
}

func (s *screen) formHeight() int {
	return max(3, s.height-screenChromeHeight)
}

// layout splits the content width between the form and the side panel, which is dropped on narrow terminals.
func (s *screen) layout() (formWidth, panelWidth int) {
	width := s.contentWidth()
	panel := s.panel()
	if panel == nil {
		return min(width, screenMaxFormWidth), 0
	}
	room := width - panel.formWidth - sidePanelGap
	if room < sidePanelMinWidth {
		return min(width, screenMaxFormWidth), 0
	}
	return panel.formWidth, min(room, sidePanelMaxWidth)
}

// panel is the side panel for the focused field, if it has one.
func (s *screen) panel() *sidePanel {
	for _, panel := range s.panels {
		if panel.field == s.focused {
			return panel
		}
	}
	return nil
}

func (s *screen) contentWidth() int {
	return max(20, min(s.width-2*screenPadX, screenMaxWidth))
}

func sidePanelStyle(st styles) lipgloss.Style {
	return lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
		BorderForeground(st.faint.GetForeground()).PaddingLeft(2)
}
