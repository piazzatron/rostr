package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

type listJSON struct {
	Rosters []rosterJSON `json:"rosters"`
}

type rosterJSON struct {
	Name      string             `json:"name"`
	EmojiMode roster.EmojiMode   `json:"emojiMode"`
	Drafted   []draftedAgentJSON `json:"drafted"`
	Idle      []idleMemberJSON   `json:"idle"`
}

type draftedAgentJSON struct {
	Name       string    `json:"name"`
	Emoji      string    `json:"emoji"`
	Display    string    `json:"display"`
	Team       string    `json:"team"`
	Task       string    `json:"task"`
	Repo       string    `json:"repo"`
	Session    string    `json:"session,omitempty"` // not in cloud mode, whose listing leaves sessions out
	DraftedAt  time.Time `json:"draftedAt"`
	LastSeenAt time.Time `json:"lastSeenAt,omitzero"` // likewise
}

type idleMemberJSON struct {
	Name    string `json:"name"`
	Emoji   string `json:"emoji"`
	Display string `json:"display"`
	Team    string `json:"team"`
}

func newListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list [<roster>...]",
		Short: "Show who's drafted and what they're working on, and who's idle",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			rosters, err := requireRosters(ctx, st)
			if err != nil {
				return err
			}
			if len(args) > 0 {
				rosters = nil
				for _, name := range args {
					r, err := st.Roster(ctx, name)
					if err != nil {
						return err
					}
					rosters = append(rosters, r)
				}
			}
			drafts, err := st.ActiveDrafts(ctx)
			if err != nil {
				return err
			}

			listing := buildListing(rosters, drafts)
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(listing)
			}
			settings, err := st.Settings(ctx)
			if err != nil {
				return err
			}
			printListing(cmd.OutOrStdout(), listing, settings.DefaultRoster, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, `print {"rosters": [{"name", "emojiMode", "drafted", "idle"}]}`)
	return cmd
}

func buildListing(rosters []store.Roster, drafts []store.Draft) listJSON {
	listing := listJSON{Rosters: []rosterJSON{}}
	for _, r := range rosters {
		entry := rosterJSON{Name: r.Name, EmojiMode: r.EmojiMode, Drafted: []draftedAgentJSON{}, Idle: []idleMemberJSON{}}
		busy := map[string]bool{}
		for _, d := range drafts {
			if d.Roster != r.Name {
				continue
			}
			busy[d.Member] = true
			entry.Drafted = append(entry.Drafted, draftedAgentJSON{
				Name: d.Name(), Emoji: d.Emoji, Display: d.Display(), Team: d.Team, Task: d.Task, Repo: d.Repo,
				Session: d.SessionID, DraftedAt: d.DraftedAt, LastSeenAt: d.LastSeenAt,
			})
		}
		for _, m := range r.Members() {
			if !busy[m.Name] {
				entry.Idle = append(entry.Idle, idleMemberJSON{
					Name: m.Name, Emoji: m.Emoji, Display: roster.Display(m.Emoji, m.Name, 1, r.EmojiMode), Team: r.Team(m.Name),
				})
			}
		}
		listing.Rosters = append(listing.Rosters, entry)
	}
	return listing
}

func printListing(w io.Writer, listing listJSON, defaultRoster string, now time.Time) {
	for i, r := range listing.Rosters {
		if i > 0 {
			fmt.Fprintln(w)
		}
		heading := r.Name + " roster"
		if r.Name == defaultRoster {
			heading += " (default)"
		}
		fmt.Fprintln(w, heading)

		rows := make([][]string, len(r.Drafted))
		for j, d := range r.Drafted {
			rows[j] = []string{d.Display, orDash(d.Task), orDash(d.Repo), since(now.Sub(d.DraftedAt))}
		}
		writeTable(w, "  ", rows)

		idle := make([]string, len(r.Idle))
		for j, m := range r.Idle {
			idle[j] = m.Display
		}
		if len(idle) == 0 {
			idle = []string{"none"}
		}
		fmt.Fprintf(w, "  Idle: %s\n", strings.Join(idle, ", "))
	}
}

// writeTable prints rows with columns padded to line up, measuring emoji by display width.
func writeTable(w io.Writer, indent string, rows [][]string) {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], runewidth.StringWidth(cell))
		}
	}
	for _, row := range rows {
		var line strings.Builder
		line.WriteString(indent)
		for i, cell := range row {
			line.WriteString(cell)
			if i < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-runewidth.StringWidth(cell)+3))
			}
		}
		fmt.Fprintln(w, line.String())
	}
}

// since formats a duration the way `rostr list` shows time since drafting: "just now", "5m", "2h", "3d".
func since(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
