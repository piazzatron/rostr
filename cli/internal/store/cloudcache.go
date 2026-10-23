package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/piazzatron/rostr/cli/internal/roster"
)

// Cloud mode's local cache, so hooks don't wait on the API:
//
//   - next.json is the owner's next state, as the API returns it with every draft-changing response: which roster
//     each project uses, who's idle in each roster, and the auto-draft setting. A Fast draft picks a member from it
//     and confirms with the server in the background, sending the pick as preferred; a session announced as Ginny
//     that the server finds taken becomes Ginny 2 (see docs/api.md). Picks not confirmed yet are pending, so the next
//     Fast draft on this machine skips them, and a server draft made meanwhile asks the server to avoid them.
//   - sessions/<session>.json is a session's own draft, which never changes while the draft is active, so later
//     prompts read it instead of calling. Server calls that change the draft update it. A released session keeps
//     the file, marked released, so a resume prefers its old member; the files are pruned after ReleasedRetention.

const (
	// nextTTL is how old the next state may be and still be drafted from.
	nextTTL = 24 * time.Hour
	// pendingWindow is how long a pick stays pending without its confirmation landing.
	pendingWindow = 15 * time.Second
	// confirmRetryAfter is how long SessionDraft waits before retrying a confirmation itself, and between retries.
	confirmRetryAfter = 3 * time.Second
	confirmRetryEvery = time.Minute
)

// Confirmation is a Fast draft to confirm with the server: the request, plus the member the session was told.
type Confirmation struct {
	SessionID string `json:"session"`
	Roster    string `json:"roster"`
	Repo      string `json:"repo"`
	Task      string `json:"task"`
	Preferred string `json:"preferred"`
}

type cloudCache struct {
	dir   string
	spawn func(Confirmation) error
}

type nextState struct {
	FetchedAt     time.Time         `json:"fetchedAt"`
	AutoDraft     bool              `json:"autoDraft"`
	DefaultRoster string            `json:"defaultRoster"`
	Links         map[string]string `json:"links"` // repo fingerprint → roster
	Rosters       []nextRoster      `json:"rosters"`
	Pending       []pendingPick     `json:"pending"`
}

type nextRoster struct {
	Name      string           `json:"name"`
	EmojiMode roster.EmojiMode `json:"emojiMode"`
	Idle      []nextMember     `json:"idle"`
	Reserved  string           `json:"reserved"` // the member the API holds for this machine, "" when none
}

type nextMember struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	Team  string `json:"team"`
}

type pendingPick struct {
	Roster    string    `json:"roster"`
	Member    string    `json:"member"`
	SessionID string    `json:"session"`
	At        time.Time `json:"at"`
}

type cachedSession struct {
	Draft     Draft         `json:"draft"`
	Released  bool          `json:"released,omitempty"`
	Confirmed bool          `json:"confirmed"`
	Confirm   *Confirmation `json:"confirm,omitempty"` // how to confirm, while unconfirmed
	LastTry   time.Time     `json:"lastTry"`
	Announced string        `json:"announced"` // the display name the session was last told
	At        time.Time     `json:"at"`
}

// pick drafts a member from the next state for a Fast draft: the session's last member if they're idle, else the
// member the API reserved for this machine, else the first idle member. It returns no member when the state is
// missing, stale, or has no idle member for the session's roster, and ErrAutoDraftOff when auto-draft is off and
// the request is conditional on it.
func (cc *cloudCache) pick(req DraftRequest, repoHash string, now time.Time) (Draft, Confirmation, error) {
	unlock, err := cc.lock()
	if err != nil {
		return Draft{}, Confirmation{}, err
	}
	defer unlock()
	next, err := cc.readNext()
	if err != nil || next == nil || now.Sub(next.FetchedAt) > nextTTL {
		return Draft{}, Confirmation{}, err
	}
	if req.IfAutoDraft && !next.AutoDraft {
		return Draft{}, Confirmation{}, ErrAutoDraftOff
	}

	// The session's roster: the one asked for, else the project's, else the default
	name := req.Roster
	if name == "" {
		name = next.Links[repoHash]
	}
	if name == "" {
		name = next.DefaultRoster
	}
	var r *nextRoster
	for i := range next.Rosters {
		if roster.NamesMatch(next.Rosters[i].Name, name) {
			r = &next.Rosters[i]
		}
	}
	if r == nil {
		return Draft{}, Confirmation{}, nil
	}

	// A random idle member not picked by a pending draft, A team first
	next.Pending = recentPending(next.Pending, now)
	pending := map[string]bool{}
	for _, p := range next.Pending {
		if roster.NamesMatch(p.Roster, r.Name) {
			pending[roster.NormalizeName(p.Member)] = true
		}
	}
	var free []nextMember
	for _, team := range []string{"A", "B"} {
		for _, m := range r.Idle {
			if m.Team == team && !pending[roster.NormalizeName(m.Name)] {
				free = append(free, m)
			}
		}
		if len(free) > 0 {
			break
		}
	}
	if len(free) == 0 {
		return Draft{}, Confirmation{}, nil
	}
	m := free[0] // the server lists idle members in the order drafts take them
	for _, want := range []string{r.Reserved, cc.lastMember(req.SessionID)} {
		for _, f := range free {
			if want != "" && roster.NamesMatch(f.Name, want) {
				m = f
			}
		}
	}
	cc.pruneReleasedSessions(now)
	next.Pending = append(next.Pending, pendingPick{Roster: r.Name, Member: m.Name, SessionID: req.SessionID, At: now})
	if err := cc.writeNext(*next); err != nil {
		return Draft{}, Confirmation{}, err
	}
	d := Draft{
		Roster: r.Name, EmojiMode: r.EmojiMode, Member: m.Name, Emoji: m.Emoji, Copy: 1, Team: m.Team,
		SessionID: req.SessionID, Repo: req.Repo, Task: req.Task, DraftedAt: now, LastSeenAt: now,
	}
	conf := Confirmation{SessionID: req.SessionID, Roster: r.Name, Repo: req.Repo, Task: req.Task, Preferred: m.Name}
	return d, conf, nil
}

// pendingMembers is who this machine has announced but not confirmed yet: what a server draft should avoid.
func (cc *cloudCache) pendingMembers(now time.Time) []string {
	next, err := cc.readNext()
	if err != nil || next == nil {
		return nil
	}
	var names []string
	for _, p := range recentPending(next.Pending, now) {
		names = append(names, p.Member)
	}
	return names
}

// storeNext replaces the next state with what the server just returned. The server's answer about session resolves
// that session's pending pick; other recent picks stay pending, since their confirmations may not have landed.
func (cc *cloudCache) storeNext(next apiNext, session string, now time.Time) error {
	unlock, err := cc.lock()
	if err != nil {
		return err
	}
	defer unlock()
	state := nextState{FetchedAt: now, AutoDraft: next.AutoDraft, DefaultRoster: deref(next.DefaultRoster), Links: map[string]string{}}
	for _, l := range next.Links {
		state.Links[l.RepoHash] = l.Roster
	}
	for _, r := range next.Rosters {
		nr := nextRoster{Name: r.Name, EmojiMode: roster.EmojiMode(r.EmojiMode), Idle: []nextMember{}, Reserved: deref(r.Reserved)}
		for _, m := range r.Idle {
			nr.Idle = append(nr.Idle, nextMember{Name: m.Name, Emoji: m.Emoji, Team: strings.ToUpper(m.Team)})
		}
		state.Rosters = append(state.Rosters, nr)
	}
	if previous, err := cc.readNext(); err == nil && previous != nil {
		for _, p := range recentPending(previous.Pending, now) {
			if p.SessionID != session {
				state.Pending = append(state.Pending, p)
			}
		}
	}
	return cc.writeNext(state)
}

// autoDraft is the cached auto-draft setting, when the next state is fresh.
func (cc *cloudCache) autoDraft(now time.Time) (on, known bool) {
	next, err := cc.readNext()
	if err != nil || next == nil || now.Sub(next.FetchedAt) > nextTTL {
		return false, false
	}
	return next.AutoDraft, true
}

func (cc *cloudCache) clearNext() {
	_ = os.Remove(cc.nextPath())
}

func (cc *cloudCache) clearAll() {
	_ = os.RemoveAll(cc.dir)
}

func recentPending(pending []pendingPick, now time.Time) []pendingPick {
	var recent []pendingPick
	for _, p := range pending {
		if now.Sub(p.At) < pendingWindow {
			recent = append(recent, p)
		}
	}
	return recent
}

func (cc *cloudCache) readNext() (*nextState, error) {
	data, err := os.ReadFile(cc.nextPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var next nextState
	if err := json.Unmarshal(data, &next); err != nil {
		return nil, nil // a corrupt file is as good as none; the next server response rewrites it
	}
	return &next, nil
}

func (cc *cloudCache) writeNext(next nextState) error {
	return writeFileAtomically(cc.nextPath(), next)
}

func (cc *cloudCache) nextPath() string {
	return filepath.Join(cc.dir, "next.json")
}

// lock takes the machine-wide lock on the next state, so two hooks picking at once take different members.
func (cc *cloudCache) lock() (unlock func(), err error) {
	if err := os.MkdirAll(cc.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(cc.dir, "next.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Sessions

// session is the session's cached draft, or nil when there's none or it's been released.
func (cc *cloudCache) session(sessionID string) *cachedSession {
	cs := cc.readSession(sessionID)
	if cs == nil || cs.Released {
		return nil
	}
	return cs
}

// lastMember is the member the session last had, released or not.
func (cc *cloudCache) lastMember(sessionID string) string {
	if cs := cc.readSession(sessionID); cs != nil {
		return cs.Draft.Member
	}
	return ""
}

func (cc *cloudCache) readSession(sessionID string) *cachedSession {
	data, err := os.ReadFile(cc.sessionPath(sessionID))
	if err != nil {
		return nil
	}
	var cs cachedSession
	if json.Unmarshal(data, &cs) != nil {
		return nil
	}
	return &cs
}

// releaseSession marks the session's draft released, keeping its member for a resume.
func (cc *cloudCache) releaseSession(sessionID string, now time.Time) {
	if cs := cc.readSession(sessionID); cs != nil && !cs.Released {
		cs.Released, cs.Confirmed, cs.Confirm, cs.At = true, true, nil, now
		_ = cc.saveSession(*cs)
	}
}

func (cc *cloudCache) pruneReleasedSessions(now time.Time) {
	entries, _ := os.ReadDir(filepath.Join(cc.dir, "sessions"))
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && now.Sub(info.ModTime()) > ReleasedRetention {
			_ = os.Remove(filepath.Join(cc.dir, "sessions", e.Name()))
		}
	}
}

func (cc *cloudCache) saveSession(cs cachedSession) error {
	return writeFileAtomically(cc.sessionPath(cs.Draft.SessionID), cs)
}

// saveConfirmed records a draft the server confirmed or returned, keeping what the session was last told.
func (cc *cloudCache) saveConfirmed(d Draft, now time.Time) error {
	announced := d.Display()
	if previous := cc.session(d.SessionID); previous != nil && previous.Announced != "" {
		announced = previous.Announced
	}
	return cc.saveSession(cachedSession{Draft: d, Confirmed: true, Announced: announced, At: now})
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func (cc *cloudCache) sessionPath(sessionID string) string {
	return filepath.Join(cc.dir, "sessions", unsafeFileChars.ReplaceAllString(sessionID, "_")+".json")
}

func writeFileAtomically(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
