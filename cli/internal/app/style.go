package app

import (
	"os"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// The palette: the site's red-orange accent (--accent in site/src/layouts/Layout.astro) and shades of grey. Colors only show at a terminal, so piped
// output, agents, and NO_COLOR get plain text.
var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#D9480F", Dark: "#FF7A3D"}
	colorText   = lipgloss.AdaptiveColor{Light: "236", Dark: "252"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "244", Dark: "245"}
	colorFaint  = lipgloss.AdaptiveColor{Light: "249", Dark: "240"}
	colorError  = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#E06C75"}

	styleHeading = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleAccent  = lipgloss.NewStyle().Foreground(colorAccent)
	styleBold    = lipgloss.NewStyle().Bold(true)
	stylePlain   = lipgloss.NewStyle()
	styleMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	styleFaint   = lipgloss.NewStyle().Foreground(colorFaint)

	// Errors go to stderr, so they're styled for stderr's terminal, not stdout's
	styleErrorPrefix = lipgloss.NewRenderer(os.Stderr).NewStyle().Foreground(colorError).Bold(true)
)

// promptTheme is huh's Charm theme recolored to the palette: orange for what's focused, greys for everything else.
func promptTheme() *huh.Theme {
	t := huh.ThemeCharm()
	cream := lipgloss.AdaptiveColor{Light: "#FFFDF5", Dark: "#FFFDF5"}

	t.Focused.Base = t.Focused.Base.BorderForeground(colorFaint)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(colorAccent).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(colorMuted)
	t.Focused.NoteTitle = t.Focused.Title
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(colorError)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(colorError)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(colorAccent)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(colorAccent)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(colorAccent)
	t.Focused.Option = t.Focused.Option.Foreground(colorText)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(colorAccent)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(colorText)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(cream).Background(colorAccent)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(colorMuted).Background(lipgloss.AdaptiveColor{Light: "254", Dark: "236"})
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colorAccent)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(colorFaint)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colorAccent)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = t.Blurred.Title.Foreground(colorMuted).Bold(false)
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}
