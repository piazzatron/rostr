package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// runConfigScreen lets a person pick one setting, see its current value, and change it, on a full-screen page.
func runConfigScreen(ctx context.Context, out io.Writer, st store.Store, settings store.Settings, rosters []store.Roster) error {
	cloud := hasCloudKey()

	// First, which setting, each shown with its current value
	var choice string
	options := []huh.Option[string]{
		huh.NewOption("Default roster · "+orDash(settings.DefaultRoster), "default"),
		huh.NewOption("Auto-draft · "+onOff(settings.AutoDraft), "auto-draft"),
		huh.NewOption("Draft expiry · "+settings.Expiry, "expiry"),
	}
	for _, r := range rosters {
		options = append(options, huh.NewOption("Emoji for "+r.Name+" · "+string(r.EmojiMode), "emoji:"+r.Name))
	}
	if !cloud {
		options = append(options, huh.NewOption("Storage · this machine (move to the cloud)", "cloud"))
	}
	pick := huh.NewSelect[string]().Title("Which setting do you want to change?").Options(options...).Value(&choice)

	// Then its new value
	names := make([]string, len(rosters))
	for i, r := range rosters {
		names[i] = r.Name
	}
	defaultRoster := settings.DefaultRoster
	pickDefault := huh.NewSelect[string]().Title("Which roster should projects without a link use?").
		Options(huh.NewOptions(names...)...).Value(&defaultRoster)
	autoDraft := settings.AutoDraft
	pickAutoDraft := huh.NewConfirm().Title("Draft names automatically in new sessions, or manually assign with /draft?").
		Affirmative("Automatically").Negative("Manually with /draft").Value(&autoDraft)
	expiry := settings.Expiry
	pickExpiry := huh.NewInput().Title("Release a name once its session has gone unseen for how long?").
		Description("Hours, days, or weeks: 48h, 7d, 2w.").Value(&expiry).
		Validate(func(s string) error { _, err := store.ParseExpiry(s); return err })
	var mode roster.EmojiMode
	pickEmoji := huh.NewSelect[roster.EmojiMode]().Title("Emojis for this roster's agents").Value(&mode)
	moveToCloud := true
	confirmCloud := huh.NewConfirm().Title("Move your whole setup to the cloud?").
		Description("Your rosters and settings move to the rostr API, so other machines and cloud environments can share " +
			"them. Tasks and repo names are end-to-end encrypted. There's no moving back in v1.").Value(&moveToCloud)

	s := newScreen("config")
	steps := []screenStep{
		{field: pick},
		{field: pickDefault, shown: func() bool { return choice == "default" }},
		{field: pickAutoDraft, shown: func() bool { return choice == "auto-draft" }},
		{field: pickExpiry, shown: func() bool { return choice == "expiry" }},
		{field: pickEmoji, shown: func() bool { return strings.HasPrefix(choice, "emoji:") }},
		{field: confirmCloud, shown: func() bool { return choice == "cloud" }},
	}
	emojiRoster := func() store.Roster {
		for _, r := range rosters {
			if "emoji:"+r.Name == choice {
				return r
			}
		}
		return store.Roster{}
	}
	s.onFocus = func(field huh.Field) tea.Cmd {
		if field == pickEmoji {
			r := emojiRoster()
			mode = r.EmojiMode
			first := roster.Member{Emoji: "🧙", Name: "Gandalf"}
			if len(r.Members) > 0 {
				first = r.Members[0]
			}
			pickEmoji.Options(emojiModeOptions(first)...)
		}
		return nil
	}
	if err := s.run(steps); err != nil {
		return err
	}

	// Save it
	switch {
	case choice == "default":
		if err := st.SetDefaultRoster(ctx, defaultRoster); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s is your default roster now.\n", defaultRoster)
	case choice == "auto-draft":
		if err := st.SetAutoDraft(ctx, autoDraft); err != nil {
			return err
		}
		fmt.Fprintf(out, "Auto-draft is %s.\n", onOff(autoDraft))
	case choice == "expiry":
		if err := st.SetExpiry(ctx, expiry); err != nil {
			return err
		}
		fmt.Fprintf(out, "Drafts unseen for %s are released.\n", strings.ToLower(expiry))
	case strings.HasPrefix(choice, "emoji:"):
		r := emojiRoster()
		if err := st.SetEmojiMode(ctx, r.Name, mode); err != nil {
			return err
		}
		fmt.Fprintf(out, "The %s roster's emoji mode is %s now.\n", r.Name, mode)
	case choice == "cloud" && moveToCloud:
		st.Close()
		return runCloudOn(ctx, out)
	}
	return nil
}

// emojiModeOptions previews each emoji mode with one member.
func emojiModeOptions(m roster.Member) []huh.Option[roster.EmojiMode] {
	return []huh.Option[roster.EmojiMode]{
		huh.NewOption("always  ("+roster.Display(m.Emoji, m.Name, 1, roster.EmojiOn)+")", roster.EmojiOn),
		huh.NewOption("never  ("+roster.Display(m.Emoji, m.Name, 1, roster.EmojiOff)+")", roster.EmojiOff),
		huh.NewOption("only  ("+roster.Display(m.Emoji, m.Name, 1, roster.EmojiOnly)+")", roster.EmojiOnly),
	}
}
