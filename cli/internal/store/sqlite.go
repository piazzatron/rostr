package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piazzatron/rostr/cli/internal/roster"

	_ "modernc.org/sqlite"
)

// SQLite is the local-mode Store: one database file, WAL mode, a 3-second busy timeout, and every write in one
// `begin immediate` transaction, so concurrent sessions draft one after the other instead of racing.
type SQLite struct {
	db  *sql.DB
	now func() time.Time
}

const timeLayout = "2006-01-02 15:04:05"

const schema = `
create table settings (
  key   text primary key,
  value text not null
);

create table rosters (
  id         integer primary key,
  name       text not null unique,
  emoji_mode text not null default 'on',
  members    text not null,
  created_at text not null default (datetime('now'))
);

create table project_links (
  repo      text primary key,
  roster_id integer not null references rosters on delete cascade
);

create table drafts (
  id           integer primary key,
  roster_id    integer not null references rosters on delete cascade,
  member_name  text not null,
  member_emoji text not null,
  copy         integer not null default 1,
  session_id   text not null,
  repo         text,
  task         text,
  drafted_at   text not null default (datetime('now')),
  last_seen_at text not null default (datetime('now')),
  benched_at   text,
  turn_started_at text,
  turn_ended_at   text
);

create unique index drafts_active_member on drafts (roster_id, member_name, copy) where benched_at is null;
create unique index drafts_active_session on drafts (session_id) where benched_at is null;
create index drafts_session on drafts (session_id, drafted_at desc);
`

// OpenSQLite opens the database at path, creating it and its schema if needed.
func OpenSQLite(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	s := &SQLite{db: db, now: time.Now}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return s, nil
}

// SetClock replaces the store's clock, for tests.
func (s *SQLite) SetClock(now func() time.Time) {
	s.now = now
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) migrate(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "pragma user_version").Scan(&version); err != nil {
			return err
		}
		switch {
		case version >= 4:
			return nil
		case version == 0:
			if _, err := tx.ExecContext(ctx, schema); err != nil {
				return err
			}
		default:
			if version == 1 {
				if _, err := tx.ExecContext(ctx, "alter table drafts rename column released_at to benched_at"); err != nil {
					return err
				}
			}
			if version <= 2 {
				// When the session's turn last started and ended (see RestAfter)
				if _, err := tx.ExecContext(ctx, `alter table drafts add column turn_started_at text;
					alter table drafts add column turn_ended_at text;`); err != nil {
					return err
				}
			}
			if err := mergeTeams(ctx, tx); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "pragma user_version = 4")
		return err
	})
}

// mergeTeams moves rosters from version 3's A team and B team columns to one members list, the A team first.
func mergeTeams(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "select id, a_team, b_team from rosters")
	if err != nil {
		return err
	}
	merged := map[int64]string{}
	for rows.Next() {
		var id int64
		var aTeam, bTeam string
		if err := rows.Scan(&id, &aTeam, &bTeam); err != nil {
			rows.Close()
			return err
		}
		var a, b []roster.Member
		if err := errors.Join(json.Unmarshal([]byte(aTeam), &a), json.Unmarshal([]byte(bTeam), &b)); err != nil {
			rows.Close()
			return fmt.Errorf("reading roster %d: %w", id, err)
		}
		merged[id] = mustJSON(append(a, b...))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `alter table rosters rename column a_team to members;
		alter table rosters drop column b_team;`); err != nil {
		return err
	}
	for id, members := range merged {
		if _, err := tx.ExecContext(ctx, "update rosters set members = ? where id = ?", members, id); err != nil {
			return err
		}
	}
	return nil
}

// Rosters

func (s *SQLite) Rosters(ctx context.Context) ([]Roster, error) {
	rows, err := allRosters(ctx, s.db)
	if err != nil {
		return nil, err
	}
	out := make([]Roster, len(rows))
	for i, r := range rows {
		out[i] = r.Roster
	}
	return out, nil
}

func (s *SQLite) Roster(ctx context.Context, name string) (Roster, error) {
	r, err := findRoster(ctx, s.db, name)
	return r.Roster, err
}

func (s *SQLite) CreateRoster(ctx context.Context, r Roster) (Roster, error) {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return Roster{}, errors.New("a roster needs a name")
	}
	if err := roster.ValidateCast(r.Members, r.EmojiMode); err != nil {
		return Roster{}, err
	}

	err := s.write(ctx, func(tx *sql.Tx) error {
		if existing, err := findRoster(ctx, tx, r.Name); err == nil {
			return userError{msg: fmt.Sprintf("you already have a roster named %q", existing.Name), kind: ErrNameTaken}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}

		r.CreatedAt = s.now().UTC().Truncate(time.Second)
		if _, err := tx.ExecContext(ctx,
			"insert into rosters (name, emoji_mode, members, created_at) values (?, ?, ?, ?)",
			r.Name, string(r.EmojiMode), mustJSON(r.Members), formatTime(r.CreatedAt)); err != nil {
			return err
		}

		// The first roster becomes the default
		settings, err := readSettings(ctx, tx)
		if err != nil {
			return err
		}
		if settings.DefaultRoster == "" {
			return setSetting(ctx, tx, "default_roster", r.Name)
		}
		return nil
	})
	return r, err
}

func (s *SQLite) ReplaceCast(ctx context.Context, rosterName string, members []roster.Member) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		r, err := findRoster(ctx, tx, rosterName)
		if err != nil {
			return err
		}
		if err := roster.ValidateCast(members, r.EmojiMode); err != nil {
			return err
		}
		return updateRoster(ctx, tx, r.id, r.EmojiMode, members)
	})
}

func (s *SQLite) EditRoster(ctx context.Context, rosterName string, mode roster.EmojiMode, members []roster.Member) error {
	if err := roster.ValidateCast(members, mode); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		r, err := findRoster(ctx, tx, rosterName)
		if err != nil {
			return err
		}

		// Drafted members can't be renamed or removed: their sessions still go by that name
		active, err := queryDrafts(ctx, tx, "where d.roster_id = ? and d.benched_at is null", r.id)
		if err != nil {
			return err
		}
		for _, d := range active {
			if m, ok := memberNamed(members, d.Member); !ok || m.Name != d.Member {
				return fmt.Errorf("%s is drafted, so you can't rename or remove them. Wait for their session to end, "+
					"or release them first: rostr bench %q", d.Name(), d.Name())
			}
		}

		return updateRoster(ctx, tx, r.id, mode, members)
	})
}

func (s *SQLite) SetEmojiMode(ctx context.Context, rosterName string, mode roster.EmojiMode) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		r, err := findRoster(ctx, tx, rosterName)
		if err != nil {
			return err
		}
		if err := roster.ValidateCast(r.Members, mode); err != nil {
			return err
		}
		return updateRoster(ctx, tx, r.id, mode, r.Members)
	})
}

func (s *SQLite) Usage(ctx context.Context, rosterName string) (Usage, error) {
	var usage Usage
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		r, err := findRoster(ctx, tx, rosterName)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "select count(*) from drafts where roster_id = ? and benched_at is null",
			r.id).Scan(&usage.Drafted); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "select count(*) from project_links where roster_id = ?", r.id).
			Scan(&usage.Links)
	})
	return usage, err
}

func (s *SQLite) Disband(ctx context.Context, rosterName string) (DisbandResult, error) {
	var result DisbandResult
	err := s.write(ctx, func(tx *sql.Tx) error {
		r, err := findRoster(ctx, tx, rosterName)
		if err != nil {
			return err
		}
		settings, err := readSettings(ctx, tx)
		if err != nil {
			return err
		}

		// Deleting the roster cascades to its drafts (releasing them) and its project links
		if _, err := tx.ExecContext(ctx, "delete from rosters where id = ?", r.id); err != nil {
			return err
		}
		if settings.DefaultRoster != r.Name {
			return nil
		}

		// Promote the oldest remaining roster, or, with none left, start over as if `assemble` never ran
		result.WasDefault = true
		remaining, err := allRosters(ctx, tx)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			_, err := tx.ExecContext(ctx, "delete from settings")
			return err
		}
		result.NewDefault = remaining[0].Name
		return setSetting(ctx, tx, "default_roster", result.NewDefault)
	})
	return result, err
}

// Project links

func (s *SQLite) ProjectLink(ctx context.Context, repo string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx,
		"select r.name from project_links l join rosters r on r.id = l.roster_id where l.repo = ?", repo).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

func (s *SQLite) LinkProject(ctx context.Context, repo, rosterName string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		r, err := findRoster(ctx, tx, rosterName)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `insert into project_links (repo, roster_id) values (?, ?)
			on conflict (repo) do update set roster_id = excluded.roster_id`, repo, r.id)
		return err
	})
}

func (s *SQLite) UnlinkProject(ctx context.Context, repo string) (bool, error) {
	var removed bool
	err := s.write(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "delete from project_links where repo = ?", repo)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		removed = n > 0
		return err
	})
	return removed, err
}

// Export reads the whole setup, for moving it to the cloud.
func (s *SQLite) Export(ctx context.Context) (Setup, error) {
	var setup Setup
	var err error
	if setup.Rosters, err = s.Rosters(ctx); err != nil {
		return Setup{}, err
	}
	if setup.Settings, err = s.Settings(ctx); err != nil {
		return Setup{}, err
	}
	if setup.Drafts, err = s.ActiveDrafts(ctx); err != nil {
		return Setup{}, err
	}

	rows, err := s.db.QueryContext(ctx,
		"select l.repo, r.name from project_links l join rosters r on r.id = l.roster_id order by r.name, l.repo")
	if err != nil {
		return Setup{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var link ProjectLink
		if err := rows.Scan(&link.Repo, &link.Roster); err != nil {
			return Setup{}, err
		}
		setup.Links = append(setup.Links, link)
	}
	return setup, rows.Err()
}

// Settings

func (s *SQLite) Settings(ctx context.Context) (Settings, error) {
	return readSettings(ctx, s.db)
}

func (s *SQLite) SetDefaultRoster(ctx context.Context, rosterName string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		r, err := findRoster(ctx, tx, rosterName)
		if err != nil {
			return err
		}
		return setSetting(ctx, tx, "default_roster", r.Name)
	})
}

func (s *SQLite) SetAutoDraft(ctx context.Context, on bool) error {
	value := map[bool]string{true: "on", false: "off"}[on]
	return s.write(ctx, func(tx *sql.Tx) error {
		return setSetting(ctx, tx, "auto_draft", value)
	})
}

func (s *SQLite) SetExpiry(ctx context.Context, expiry string) error {
	if _, err := ParseExpiry(expiry); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		return setSetting(ctx, tx, "draft_expiry", strings.ToLower(strings.TrimSpace(expiry)))
	})
}

// Drafts

func (s *SQLite) Draft(ctx context.Context, req DraftRequest) (Draft, error) {
	if req.SessionID == "" {
		return Draft{}, errors.New("drafting needs a session ID")
	}

	var out Draft
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}

		// Same session, same name
		active, err := s.bumpActive(ctx, tx, req.SessionID, req.Task)
		if err != nil || active != nil {
			if active != nil {
				out = active.Draft
			}
			return err
		}
		if req.IfAutoDraft {
			settings, err := readSettings(ctx, tx)
			if err != nil {
				return err
			}
			if !settings.AutoDraft {
				return ErrAutoDraftOff
			}
		}

		// Its old name back, if free
		resumed, err := s.resume(ctx, tx, req.SessionID, req.Repo, req.Task)
		if err != nil || resumed != nil {
			if resumed != nil {
				out = resumed.Draft
			}
			return err
		}

		r, err := draftRoster(ctx, tx, req)
		if err != nil {
			return err
		}

		// The free member drafted least recently; else the longest-resting member's name; else a copy
		member, copy, err := firstFreeMember(ctx, tx, r)
		if err != nil {
			return err
		}
		if copy > 1 {
			rested, ok, err := s.takeRestingMember(ctx, tx, r)
			if err != nil {
				return err
			}
			if ok {
				member, copy = rested, 1
			}
		}
		inserted, err := s.insertDraft(ctx, tx, newDraft{
			rosterID: r.id, member: member, copy: copy, sessionID: req.SessionID, repo: req.Repo, task: req.Task,
		})
		out = inserted.Draft
		return err
	})
	return out, err
}

func (s *SQLite) Reclaim(ctx context.Context, sessionID string) (*Draft, error) {
	var out *Draft
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		d, err := s.bumpActive(ctx, tx, sessionID, "")
		if err == nil && d == nil {
			d, err = s.resume(ctx, tx, sessionID, "", "")
		}
		if d != nil {
			out = &d.Draft
		}
		return err
	})
	return out, err
}

func (s *SQLite) SessionDraft(ctx context.Context, sessionID string) (*Draft, error) {
	var out *Draft
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		active, err := queryDrafts(ctx, tx, "where d.session_id = ? and d.benched_at is null", sessionID)
		if len(active) > 0 {
			out = &active[0].Draft
		}
		return err
	})
	return out, err
}

func (s *SQLite) Stats(ctx context.Context) (Stats, error) {
	rosters, err := s.Rosters(ctx)
	if err != nil {
		return Stats{}, err
	}
	drafts, err := s.ActiveDrafts(ctx)
	if err != nil {
		return Stats{}, err
	}
	settings, err := s.Settings(ctx)
	return Stats{Rosters: rosters, Drafts: drafts, DefaultRoster: settings.DefaultRoster}, err
}

func (s *SQLite) ActiveDrafts(ctx context.Context) ([]Draft, error) {
	var out []Draft
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		active, err := queryDrafts(ctx, tx,
			"where d.benched_at is null order by r.created_at, r.id, d.drafted_at, d.id")
		now := s.now().UTC()
		for _, d := range active {
			d.RestingSince = restingSince(d.turnStarted, d.turnEnded, now)
			out = append(out, d.Draft)
		}
		return err
	})
	return out, err
}

func (s *SQLite) Bench(ctx context.Context, sessionID string) (*Draft, error) {
	var out *Draft
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		active, err := queryDrafts(ctx, tx, "where d.session_id = ? and d.benched_at is null", sessionID)
		if err != nil || len(active) == 0 {
			return err
		}
		out = &active[0].Draft
		_, err = tx.ExecContext(ctx, "update drafts set benched_at = ? where id = ?", s.nowText(), active[0].id)
		return err
	})
	return out, err
}

func (s *SQLite) BenchAgent(ctx context.Context, agent AgentKey) (Draft, error) {
	return s.updateAgent(ctx, agent, "update drafts set benched_at = ? where id = ?", s.nowText())
}

func (s *SQLite) SetTask(ctx context.Context, agent AgentKey, task string) (Draft, error) {
	d, err := s.updateAgent(ctx, agent, "update drafts set task = ?, last_seen_at = ? where id = ?", task, s.nowText())
	d.Task = task
	return d, err
}

func (s *SQLite) RecordTurn(ctx context.Context, sessionID string, ended bool, at time.Time) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		active, err := queryDrafts(ctx, tx, "where d.session_id = ? and d.benched_at is null", sessionID)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return notFound("session %s isn't drafted", sessionID)
		}
		d := active[0]

		now := s.now().UTC().Truncate(time.Second)
		at = at.UTC().Truncate(time.Second)
		if at.After(now) {
			at = now
		}
		column, current := "turn_started_at", d.turnStarted
		if ended {
			column, current = "turn_ended_at", d.turnEnded
		}
		if at.Before(current) {
			at = current
		}
		_, err = tx.ExecContext(ctx, "update drafts set "+column+" = ?, last_seen_at = ? where id = ?",
			formatTime(at), formatTime(now), d.id)
		return err
	})
}

// updateAgent runs an update against one agent's active draft, appending the draft's id to args.
func (s *SQLite) updateAgent(ctx context.Context, agent AgentKey, update string, args ...any) (Draft, error) {
	var out Draft
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := s.expireStaleDrafts(ctx, tx); err != nil {
			return err
		}
		active, err := queryDrafts(ctx, tx,
			"where r.name = ? and d.member_name = ? and d.copy = ? and d.benched_at is null",
			agent.Roster, agent.Member, agent.Copy)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return notFound("%s isn't drafted in the %s roster", roster.AgentName(agent.Member, agent.Copy), agent.Roster)
		}
		out = active[0].Draft
		_, err = tx.ExecContext(ctx, update, append(args, active[0].id)...)
		return err
	})
	return out, err
}

// bumpActive returns the session's active draft, bumped and with its task replaced if task isn't empty, or nil.
func (s *SQLite) bumpActive(ctx context.Context, tx *sql.Tx, sessionID, task string) (*draftRow, error) {
	active, err := queryDrafts(ctx, tx, "where d.session_id = ? and d.benched_at is null", sessionID)
	if err != nil || len(active) == 0 {
		return nil, err
	}
	d := active[0]
	d.LastSeenAt = s.now().UTC().Truncate(time.Second)
	if task != "" {
		d.Task = task
	}
	_, err = tx.ExecContext(ctx, "update drafts set last_seen_at = ?, task = ? where id = ?",
		formatTime(d.LastSeenAt), nullIfEmpty(d.Task), d.id)
	return &d, err
}

// resume redrafts the session's last member if they're still in the cast and free, or returns nil. The new draft keeps
// the last draft's repo and task unless new ones are given.
func (s *SQLite) resume(ctx context.Context, tx *sql.Tx, sessionID, repo, task string) (*draftRow, error) {
	recent, err := queryDrafts(ctx, tx, "where d.session_id = ? order by d.drafted_at desc, d.id desc limit 1", sessionID)
	if err != nil || len(recent) == 0 {
		return nil, err
	}
	last := recent[0]

	r, err := rosterByID(ctx, tx, last.rosterID)
	if err != nil {
		return nil, err
	}
	member, inCast := memberNamed(r.Members, last.Member)
	if !inCast {
		return nil, nil
	}
	busy, err := busySlots(ctx, tx, r.id)
	if err != nil || busy[slot{roster.NormalizeName(member.Name), last.Copy}] {
		return nil, err
	}

	if repo == "" {
		repo = last.Repo
	}
	if task == "" {
		task = last.Task
	}
	d, err := s.insertDraft(ctx, tx, newDraft{
		rosterID: last.rosterID, member: member, copy: last.Copy, sessionID: sessionID, repo: repo, task: task,
	})
	return &d, err
}

// takeRestingMember releases the roster's longest-resting draft of a member's own name (copy 1) whose member is still
// in the cast, and returns that member. ok is false when none is resting.
func (s *SQLite) takeRestingMember(ctx context.Context, tx *sql.Tx, r rosterRow) (roster.Member, bool, error) {
	drafts, err := queryDrafts(ctx, tx, "where d.roster_id = ? and d.copy = 1 and d.benched_at is null", r.id)
	if err != nil {
		return roster.Member{}, false, err
	}
	now := s.now().UTC()
	var longest *draftRow
	for i, d := range drafts {
		since := restingSince(d.turnStarted, d.turnEnded, now)
		if _, inCast := memberNamed(r.Members, d.Member); since.IsZero() || !inCast {
			continue
		}
		if longest == nil || since.Before(longest.turnEnded) {
			longest = &drafts[i]
		}
	}
	if longest == nil {
		return roster.Member{}, false, nil
	}
	member, _ := memberNamed(r.Members, longest.Member)
	_, err = tx.ExecContext(ctx, "update drafts set benched_at = ? where id = ?", s.nowText(), longest.id)
	return member, err == nil, err
}

// expireStaleDrafts releases active drafts not seen within the expiry, and deletes released drafts past retention.
func (s *SQLite) expireStaleDrafts(ctx context.Context, tx *sql.Tx) error {
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	expiry, err := ParseExpiry(settings.Expiry)
	if err != nil {
		expiry, _ = ParseExpiry(DefaultExpiry)
	}

	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx,
		"update drafts set benched_at = ? where benched_at is null and last_seen_at < ?",
		formatTime(now), formatTime(now.Add(-expiry))); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "delete from drafts where benched_at is not null and benched_at < ?",
		formatTime(now.Add(-ReleasedRetention)))
	return err
}

type newDraft struct {
	rosterID  int64
	member    roster.Member
	copy      int
	sessionID string
	repo      string
	task      string
}

func (s *SQLite) insertDraft(ctx context.Context, tx *sql.Tx, d newDraft) (draftRow, error) {
	now := s.nowText()
	result, err := tx.ExecContext(ctx, `insert into drafts
		(roster_id, member_name, member_emoji, copy, session_id, repo, task, drafted_at, last_seen_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.rosterID, d.member.Name, d.member.Emoji, d.copy, d.sessionID, nullIfEmpty(d.repo), nullIfEmpty(d.task),
		now, now)
	if err != nil {
		return draftRow{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return draftRow{}, err
	}
	rows, err := queryDrafts(ctx, tx, "where d.id = ?", id)
	if err != nil {
		return draftRow{}, err
	}
	return rows[0], nil
}

// draftRoster picks the roster for a session that needs a new member: req.Roster, else the project's link, else the
// default.
func draftRoster(ctx context.Context, tx *sql.Tx, req DraftRequest) (rosterRow, error) {
	if req.Roster != "" {
		return findRoster(ctx, tx, req.Roster)
	}

	if req.Repo != "" {
		var rosterID int64
		err := tx.QueryRowContext(ctx, "select roster_id from project_links where repo = ?", req.Repo).Scan(&rosterID)
		if err == nil {
			return rosterByID(ctx, tx, rosterID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return rosterRow{}, err
		}
	}

	settings, err := readSettings(ctx, tx)
	if err != nil {
		return rosterRow{}, err
	}
	if settings.DefaultRoster == "" {
		return rosterRow{}, ErrNoRosters
	}
	return findRoster(ctx, tx, settings.DefaultRoster)
}

func firstFreeMember(ctx context.Context, tx *sql.Tx, r rosterRow) (roster.Member, int, error) {
	members := r.Members
	if len(members) == 0 {
		return roster.Member{}, 0, fmt.Errorf("the %s roster has no members", r.Name)
	}

	busy, err := busySlots(ctx, tx, r.id)
	if err != nil {
		return roster.Member{}, 0, err
	}
	last, err := lastDrafted(ctx, tx, r.id)
	if err != nil {
		return roster.Member{}, 0, err
	}
	m, copy := pickFreeMember(r.Members, func(name string, copy int) bool { return busy[slot{roster.NormalizeName(name), copy}] }, last)
	return m, copy, nil
}

// slot is a member (by normalized name) and copy number.
type slot struct {
	member string
	copy   int
}

// busySlots is the roster's actively drafted slots.
func busySlots(ctx context.Context, tx *sql.Tx, rosterID int64) (map[slot]bool, error) {
	rows, err := tx.QueryContext(ctx, "select member_name, copy from drafts where roster_id = ? and benched_at is null", rosterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	busy := map[slot]bool{}
	for rows.Next() {
		var name string
		var copy int
		if err := rows.Scan(&name, &copy); err != nil {
			return nil, err
		}
		busy[slot{roster.NormalizeName(name), copy}] = true
	}
	return busy, rows.Err()
}

// pickFreeMember picks the free member drafted least recently (never drafted first, then roster order), then numbered
// copies the same way. last is keyed by normalized name. The cast is never empty.
func pickFreeMember(members []roster.Member, busy func(name string, copy int) bool, last map[string]time.Time) (roster.Member, int) {
	for copy := 1; ; copy++ {
		var best roster.Member
		var bestAt time.Time
		found := false
		for _, m := range members {
			if busy(m.Name, copy) {
				continue
			}
			if at := last[roster.NormalizeName(m.Name)]; !found || at.Before(bestAt) {
				best, bestAt, found = m, at, true
			}
		}
		if found {
			return best, copy
		}
	}
}

// lastDrafted is when each of a roster's members (by normalized name) was last drafted, within the retention of
// released drafts.
func lastDrafted(ctx context.Context, tx *sql.Tx, rosterID int64) (map[string]time.Time, error) {
	rows, err := tx.QueryContext(ctx, "select member_name, max(drafted_at) from drafts where roster_id = ? group by member_name", rosterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	last := map[string]time.Time{}
	for rows.Next() {
		var name, at string
		if err := rows.Scan(&name, &at); err != nil {
			return nil, err
		}
		key, t := roster.NormalizeName(name), parseTime(at)
		if t.After(last[key]) {
			last[key] = t
		}
	}
	return last, rows.Err()
}

func memberNamed(members []roster.Member, name string) (roster.Member, bool) {
	for _, m := range members {
		if roster.NamesMatch(m.Name, name) {
			return m, true
		}
	}
	return roster.Member{}, false
}

// Row helpers

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type rosterRow struct {
	id int64
	Roster
}

type draftRow struct {
	id                     int64
	rosterID               int64
	turnStarted, turnEnded time.Time // zero when not reported
	Draft
}

func (s *SQLite) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLite) nowText() string {
	return formatTime(s.now())
}

func allRosters(ctx context.Context, q querier) ([]rosterRow, error) {
	return queryRosters(ctx, q, "order by created_at, id")
}

func rosterByID(ctx context.Context, q querier, id int64) (rosterRow, error) {
	rows, err := queryRosters(ctx, q, "where id = ?", id)
	if err != nil {
		return rosterRow{}, err
	}
	if len(rows) == 0 {
		return rosterRow{}, notFound("roster %d doesn't exist", id)
	}
	return rows[0], nil
}

// findRoster finds a roster by name, matching loosely.
func findRoster(ctx context.Context, q querier, name string) (rosterRow, error) {
	rows, err := allRosters(ctx, q)
	if err != nil {
		return rosterRow{}, err
	}
	for _, r := range rows {
		if roster.NamesMatch(r.Name, name) {
			return r, nil
		}
	}
	return rosterRow{}, notFound("no roster named %q", name)
}

func queryRosters(ctx context.Context, q querier, clause string, args ...any) ([]rosterRow, error) {
	rows, err := q.QueryContext(ctx,
		"select id, name, emoji_mode, members, created_at from rosters "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rosterRow
	for rows.Next() {
		var r rosterRow
		var mode, members, createdAt string
		if err := rows.Scan(&r.id, &r.Name, &mode, &members, &createdAt); err != nil {
			return nil, err
		}
		r.EmojiMode = roster.EmojiMode(mode)
		r.CreatedAt = parseTime(createdAt)
		if err := json.Unmarshal([]byte(members), &r.Members); err != nil {
			return nil, fmt.Errorf("reading roster %s: %w", r.Name, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func queryDrafts(ctx context.Context, q querier, clause string, args ...any) ([]draftRow, error) {
	rows, err := q.QueryContext(ctx, `select d.id, d.roster_id, r.name, r.emoji_mode, d.member_name, d.member_emoji, d.copy, d.session_id, coalesce(d.repo, ''), coalesce(d.task, ''),
		d.drafted_at, d.last_seen_at, coalesce(d.turn_started_at, ''), coalesce(d.turn_ended_at, '')
		from drafts d join rosters r on r.id = d.roster_id `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []draftRow
	for rows.Next() {
		var d draftRow
		var mode, draftedAt, lastSeenAt, turnStarted, turnEnded string
		if err := rows.Scan(&d.id, &d.rosterID, &d.Roster, &mode, &d.Member, &d.Emoji, &d.Copy,
			&d.SessionID, &d.Repo, &d.Task, &draftedAt, &lastSeenAt, &turnStarted, &turnEnded); err != nil {
			return nil, err
		}
		d.EmojiMode = roster.EmojiMode(mode)
		d.DraftedAt = parseTime(draftedAt)
		d.LastSeenAt = parseTime(lastSeenAt)
		d.turnStarted, d.turnEnded = parseTime(turnStarted), parseTime(turnEnded)
		out = append(out, d)
	}
	return out, rows.Err()
}

func updateRoster(ctx context.Context, tx *sql.Tx, id int64, mode roster.EmojiMode, members []roster.Member) error {
	_, err := tx.ExecContext(ctx, "update rosters set emoji_mode = ?, members = ? where id = ?",
		string(mode), mustJSON(members), id)
	return err
}

func readSettings(ctx context.Context, q querier) (Settings, error) {
	settings := Settings{AutoDraft: true, Expiry: DefaultExpiry}
	rows, err := q.QueryContext(ctx, "select key, value from settings")
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, err
		}
		switch key {
		case "default_roster":
			settings.DefaultRoster = value
		case "auto_draft":
			settings.AutoDraft = value != "off"
		case "draft_expiry":
			settings.Expiry = value
		}
	}
	if on, ok := autoDraftOverride(); ok {
		settings.AutoDraft = on
	}
	return settings, rows.Err()
}

func setSetting(ctx context.Context, q querier, key, value string) error {
	_, err := q.ExecContext(ctx,
		"insert into settings (key, value) values (?, ?) on conflict (key) do update set value = excluded.value",
		key, value)
	return err
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeLayout, s)
	return t
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
