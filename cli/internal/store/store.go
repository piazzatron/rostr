// Package store defines the storage rostr runs against: rosters, project links, settings, and drafts. Local mode
// implements it with SQLite (SQLite); cloud mode implements it with the rostr API (Cloud). Drafting itself, including
// picking the roster, lives behind this interface, because in cloud mode the server
// drafts.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

// DefaultExpiry is how long a draft can go unseen before it's released, until `rostr config expiry` changes it.
const DefaultExpiry = "1d"

// ReleasedRetention is how long released drafts are kept, so a resumed session can ask for its old name back.
const ReleasedRetention = 7 * 24 * time.Hour

// RestAfter is how long after its turn ends a session's draft starts resting: a draft that would otherwise get a
// numbered copy takes the longest-resting member's name instead.
const RestAfter = 15 * time.Minute

var (
	ErrNotFound     = errors.New("not found")
	ErrNameTaken    = errors.New("name taken")
	ErrNoRosters    = errors.New("No rosters yet. Run `rostr assemble`.")
	ErrAutoDraftOff = errors.New("auto-draft is off")
	// ErrUnauthorized is the API not recognizing the key, which is what happens once its cloud setup is deleted
	ErrUnauthorized = errors.New("unauthorized")
)

// Store is everything the CLI reads and writes.
type Store interface {
	Close() error

	// Rosters returns every roster, oldest first.
	Rosters(ctx context.Context) ([]Roster, error)
	// Roster finds a roster by name, matching loosely. The error wraps ErrNotFound.
	Roster(ctx context.Context, name string) (Roster, error)
	// CreateRoster adds a roster, which becomes the default if there's none yet. The error wraps ErrNameTaken when
	// another roster's name matches loosely.
	CreateRoster(ctx context.Context, r Roster) (Roster, error)
	// ReplaceCast swaps in a whole new cast. Drafted agents keep their names until their sessions end.
	ReplaceCast(ctx context.Context, rosterName string, members []roster.Member) error
	// EditRoster saves an edited emoji mode and cast, refusing to rename or remove a drafted member.
	EditRoster(ctx context.Context, rosterName string, mode roster.EmojiMode, members []roster.Member) error
	SetEmojiMode(ctx context.Context, rosterName string, mode roster.EmojiMode) error
	// Usage counts a roster's active drafts and linked projects.
	Usage(ctx context.Context, rosterName string) (Usage, error)
	// Disband deletes a roster with its drafts and links, promoting the oldest remaining roster if it was the default.
	Disband(ctx context.Context, rosterName string) (DisbandResult, error)

	// ProjectLink returns the name of the roster linked to repo, or "" if there's none.
	ProjectLink(ctx context.Context, repo string) (string, error)
	LinkProject(ctx context.Context, repo, rosterName string) error
	// UnlinkProject removes repo's link, reporting whether there was one.
	UnlinkProject(ctx context.Context, repo string) (bool, error)

	Settings(ctx context.Context) (Settings, error)
	SetDefaultRoster(ctx context.Context, rosterName string) error
	SetAutoDraft(ctx context.Context, on bool) error
	SetExpiry(ctx context.Context, expiry string) error

	// Draft gives a session its member: the same one if it has an active draft, its last one if that's free,
	// otherwise the free member drafted least recently (then the longest-resting member's name, then numbered copies)
	// of req.Roster, else the linked roster of req.Repo, else the default; with none, the error is ErrNoRosters.
	Draft(ctx context.Context, req DraftRequest) (Draft, error)
	// Reclaim re-tells a session's name without drafting a new member: its active draft (bumped), else its last
	// member if still free, else nil.
	Reclaim(ctx context.Context, sessionID string) (*Draft, error)
	// SessionDraft returns the session's active draft, or nil.
	SessionDraft(ctx context.Context, sessionID string) (*Draft, error)
	// ActiveDrafts returns every active draft across all rosters.
	ActiveDrafts(ctx context.Context) ([]Draft, error)
	// Stats returns what `rostr stats` shows: every roster, every active draft, and the default roster.
	Stats(ctx context.Context) (Stats, error)
	// Bench frees the session's member, returning the released draft or nil if it had none.
	Bench(ctx context.Context, sessionID string) (*Draft, error)
	// BenchAgent frees one drafted agent. The error wraps ErrNotFound if it isn't drafted.
	BenchAgent(ctx context.Context, agent AgentKey) (Draft, error)
	// SetTask sets a drafted agent's task and bumps its last-seen time.
	SetTask(ctx context.Context, agent AgentKey, task string) (Draft, error)
	// RecordTurn records that the session's turn started or ended at at (never later than now), and bumps its draft's
	// last-seen time. Each time only moves forward, so a late report can't undo a newer one. The error wraps
	// ErrNotFound when the session has no active draft, as when another session took its name while it rested.
	RecordTurn(ctx context.Context, sessionID string, ended bool, at time.Time) error
}

// Stats is every roster, oldest first, with every active draft and the default roster's name ("" if none).
type Stats struct {
	Rosters       []Roster
	Drafts        []Draft
	DefaultRoster string
}

// Roster is a named cast of members, in draft order.
type Roster struct {
	Name      string
	EmojiMode roster.EmojiMode
	Members   []roster.Member
	CreatedAt time.Time
}

// Settings are setup-wide.
type Settings struct {
	DefaultRoster string
	AutoDraft     bool
	Expiry        string
}

// Usage is what disbanding a roster would affect.
type Usage struct {
	Drafted int
	Links   int
}

// DisbandResult says what happened to the default roster, and whether the cloud setup itself was deleted.
type DisbandResult struct {
	WasDefault bool
	NewDefault string // "" when no roster is left
	// CloudSetupDeleted is set when the last cloud roster was disbanded: the server deleted the whole setup, so the
	// rostr key no longer works.
	CloudSetupDeleted bool
}

// DraftRequest asks for a member for a session.
type DraftRequest struct {
	SessionID string
	Roster    string // the --roster flag, used only when the session needs a new member
	Repo      string // the project the session is in, "" outside git
	Task      string // optional; replaces the task of an existing draft
	// IfAutoDraft makes a new draft conditional on the auto-draft setting: with it off, the error is ErrAutoDraftOff.
	// A session that already has a draft gets it back either way.
	IfAutoDraft bool
	// Idle means the session hasn't had a turn yet: it's drafted when it starts, ahead of its first prompt. In cloud
	// mode such a draft rests if no prompt comes, so a session opened and left doesn't hold a name.
	Idle bool
}

// ProjectLink is a project linked to a roster.
type ProjectLink struct {
	Repo   string
	Roster string
}

// Setup is a whole setup, as `rostr cloud on` uploads it: rosters (oldest first), settings, project links, and
// active drafts.
type Setup struct {
	Rosters  []Roster
	Settings Settings
	Links    []ProjectLink
	Drafts   []Draft
}

// AgentKey identifies an active draft: a member (and copy) of a roster.
type AgentKey struct {
	Roster string
	Member string
	Copy   int
}

// Draft is a member drafted by a session.
type Draft struct {
	Roster     string
	EmojiMode  roster.EmojiMode
	Member     string
	Emoji      string
	Copy       int
	SessionID  string
	Repo       string
	Task       string
	DraftedAt  time.Time
	LastSeenAt time.Time
	// RestingSince is when the session finished its last turn, once it has been resting (see RestAfter); zero while
	// it's working or was recently.
	RestingSince time.Time
}

// Name is the agent's name, with its copy number ("Gandalf 2").
func (d Draft) Name() string {
	return roster.AgentName(d.Member, d.Copy)
}

// Display is the agent's name per the roster's emoji mode.
func (d Draft) Display() string {
	return roster.Display(d.Emoji, d.Member, d.Copy, d.EmojiMode)
}

// Key identifies this draft's agent.
func (d Draft) Key() AgentKey {
	return AgentKey{Roster: d.Roster, Member: d.Member, Copy: d.Copy}
}

// restingSince is when a session whose turn last started and ended at these times started resting, or zero.
func restingSince(started, ended, now time.Time) time.Time {
	// Times can be equal (SQLite keeps seconds): a start in the same second as the end counts as after it
	if ended.IsZero() || !ended.After(started) || now.Sub(ended) < RestAfter {
		return time.Time{}
	}
	return ended
}

// ParseExpiry parses a draft expiry in hours, days, or weeks ("48h", "7d", "2w").
func ParseExpiry(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	units := map[string]time.Duration{"h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}
	if len(s) >= 2 {
		if unit, ok := units[s[len(s)-1:]]; ok {
			if n, err := strconv.Atoi(s[:len(s)-1]); err == nil && n > 0 {
				return time.Duration(n) * unit, nil
			}
		}
	}
	return 0, fmt.Errorf("expiry must be a whole number of hours, days, or weeks, like 48h, 7d, or 2w (got %q)", s)
}

// userError is an error whose message is meant for the user, wrapping a sentinel callers can test with errors.Is.
type userError struct {
	msg  string
	kind error
}

func (e userError) Error() string { return e.msg }
func (e userError) Unwrap() error { return e.kind }

func notFound(format string, args ...any) error {
	return userError{msg: fmt.Sprintf(format, args...), kind: ErrNotFound}
}

// AutoDraftEnv overrides the auto-draft setting on this machine: "on" or "off". Cloud containers, which start without
// rostr's local files, set it in their environment.
const AutoDraftEnv = "ROSTR_AUTO_DRAFT"

// autoDraftOverride is the auto-draft setting AutoDraftEnv gives, if it gives one.
func autoDraftOverride() (on, ok bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AutoDraftEnv))) {
	case "on":
		return true, true
	case "off":
		return false, true
	}
	return false, false
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
