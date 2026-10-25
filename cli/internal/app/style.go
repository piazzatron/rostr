package app

import (
	"image/color"
	"os"
	"sync"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// palette is the site's red-orange accent (--accent in site/src/layouts/Layout.astro) and shades of grey, in the
// shades for a dark or light terminal background.
type palette struct {
	accent, text, muted, faint, error color.Color
}

func newPalette(dark bool) palette {
	shade := lipgloss.LightDark(dark)
	return palette{
		accent: shade(lipgloss.Color("#D9480F"), lipgloss.Color("#FF7A3D")),
		text:   shade(lipgloss.Color("236"), lipgloss.Color("252")),
		muted:  shade(lipgloss.Color("244"), lipgloss.Color("245")),
		faint:  shade(lipgloss.Color("249"), lipgloss.Color("240")),
		error:  shade(lipgloss.Color("#B91C1C"), lipgloss.Color("#E06C75")),
	}
}

// styles are the text styles on the palette. Colors only show at a terminal: command output goes through a
// colorprofile writer (see Execute), so piped output, agents, and NO_COLOR get plain text.
type styles struct {
	heading, accent, bold, plain, muted, faint, errorPrefix lipgloss.Style
}

func newStyles(dark bool) styles {
	p := newPalette(dark)
	return styles{
		heading:     lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		accent:      lipgloss.NewStyle().Foreground(p.accent),
		bold:        lipgloss.NewStyle().Bold(true),
		plain:       lipgloss.NewStyle(),
		muted:       lipgloss.NewStyle().Foreground(p.muted),
		faint:       lipgloss.NewStyle().Foreground(p.faint),
		errorPrefix: lipgloss.NewStyle().Foreground(p.error).Bold(true),
	}
}

// plainStyles are the styles for output printed outside a screen. They ask the terminal for its background once, when
// first used, and only when stdin and stdout are a terminal; screens get theirs from Bubble Tea instead.
var plainStyles = sync.OnceValue(func() styles {
	return newStyles(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
})

// promptTheme is huh's Charm theme recolored to the palette: orange for what's focused, greys for everything else.
func promptTheme(dark bool) *huh.Styles {
	p := newPalette(dark)
	t := huh.ThemeCharm(dark)
	cream := lipgloss.Color("#FFFDF5")

	t.Focused.Base = t.Focused.Base.BorderForeground(p.faint)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(p.accent).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(p.muted)
	t.Focused.NoteTitle = t.Focused.Title
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(p.error)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(p.error)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(p.accent)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(p.accent)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(p.accent)
	t.Focused.Option = t.Focused.Option.Foreground(p.text)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(p.accent)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(p.text)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(cream).Background(p.accent)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(p.muted).
		Background(lipgloss.LightDark(dark)(lipgloss.Color("254"), lipgloss.Color("236")))
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(p.accent)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(p.faint)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(p.accent)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = t.Blurred.Title.Foreground(p.muted).Bold(false)
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}
