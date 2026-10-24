package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type draftResponse struct {
	Name      string    `json:"name"`
	Emoji     string    `json:"emoji"`
	Copy      int       `json:"copy"`
	Roster    string    `json:"roster"`
	EmojiMode string    `json:"emojiMode"`
	Session   string    `json:"session"`
	Task      *string   `json:"task"`
	Repo      *string   `json:"repo"`
	DraftedAt time.Time `json:"draftedAt"`
}

// draftColumns selects a draft with its roster; queries alias drafts as d and rosters as r.
const draftColumns = `d.id, r.name, r.emoji_mode, d.member_name, d.member_emoji, d.copy,
	d.session_id, d.task, d.repo, d.drafted_at`

func scanDraft(row pgx.Row) (int64, draftResponse, error) {
	var id int64
	var d draftResponse
	var task, repo []byte
	err := row.Scan(&id, &d.Roster, &d.EmojiMode, &d.Name, &d.Emoji, &d.Copy, &d.Session, &task, &repo, &d.DraftedAt)
	d.Task, d.Repo = encodeCiphertext(task), encodeCiphertext(repo)
	return id, d, err
}

func loadDraft(ctx context.Context, q querier, id int64) (draftResponse, error) {
	_, d, err := scanDraft(q.QueryRow(ctx,
		`select `+draftColumns+` from drafts d join rosters r on r.id = d.roster_id where d.id = $1`, id))
	return d, err
}

// logDraft adds the drafted member to the request's log line. The session ID is left out: names and rosters are
// enough to follow a session, and task and repo are ciphertext.
func logDraft(ctx context.Context, d draftResponse) {
	logAttrs(ctx, slog.String("roster", d.Roster), slog.String("member", d.Name), slog.Int("copy", d.Copy))
}

var errNotDrafted = &apiError{http.StatusNotFound, "not_drafted", "No active draft matches.", nil}

var errNoRosters = &apiError{http.StatusNotFound, "no_rosters", "No rosters yet. Run `rostr assemble`.", nil}

// draftInput is what drafting knows about a session. task and repo are ciphertext. idle means the session hasn't had a
// turn yet (it's drafted when it starts, before its first prompt), so it rests like a session whose turn just ended.
type draftInput struct {
	session  string
	roster   *string
	repoHash *string
	repo     []byte
	task     []byte
	idle     bool
}

func (s *Server) handleDraft(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req struct {
		Session  string  `json:"session"`
		Roster   *string `json:"roster"`
		RepoHash *string `json:"repoHash"`
		Repo     *string `json:"repo"`
		Task     *string `json:"task"`
		Idle     bool    `json:"idle"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := validateSession(req.Session); err != nil {
		return err
	}
	repoHash, repo, err := repoInput(req.RepoHash, req.Repo)
	if err != nil {
		return err
	}
	task, err := decodeCiphertext("task", req.Task)
	if err != nil {
		return err
	}

	in := draftInput{req.Session, req.Roster, repoHash, repo, task, req.Idle}
	unlock := s.draftLocks.lock(ownerID)
	resp, err := draftSession(r.Context(), s.db, ownerID, in, true)
	unlock()
	if err != nil {
		return err
	}
	logDraft(r.Context(), resp)
	s.live.notify()
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleKeepAlive bumps the session's draft, or reclaims its last member if the draft expired and they're free. It
// never drafts a different member, so the prompt hook and compaction can't rename a session.
func (s *Server) handleKeepAlive(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req struct {
		Session string `json:"session"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := validateSession(req.Session); err != nil {
		return err
	}
	unlock := s.draftLocks.lock(ownerID)
	resp, err := draftSession(r.Context(), s.db, ownerID, draftInput{session: req.Session}, false)
	unlock()
	if err != nil {
		return err
	}
	logDraft(r.Context(), resp)
	s.live.notify()
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// draftAttempts is how many times draftSession tries before giving up on a race it keeps losing.
const draftAttempts = 10

// draftSession gives the session a member in two round trips: one batch reads everything drafting needs, the member is
// picked here, and one batch writes the draft. It takes no database lock. Callers hold the owner's draft lock, so drafts
// on one instance take turns; across instances, the unique indexes on active drafts (one per name and copy, one per
// session) stop two drafts racing for the same name or session, and the one whose write fails starts over, seeing the
// other's draft. With newMember false it only bumps or resumes the session's own draft (keep-alive).
func draftSession(ctx context.Context, db *pgxpool.Pool, ownerID string, in draftInput, newMember bool) (draftResponse, error) {
	for attempt := 1; ; attempt++ {
		resp, err := tryDraft(ctx, db, ownerID, in, newMember)
		if attempt < draftAttempts && lostRace(err) {
			// A random wait, so two instances drafting in step don't keep picking the same name
			select {
			case <-ctx.Done():
				return draftResponse{}, ctx.Err()
			case <-time.After(time.Duration(rand.IntN(5*attempt)+1) * time.Millisecond):
			}
			continue
		}
		return resp, err
	}
}

// lostRace reports whether a draft's write failed because another write got there first: a name or session taken by
// another draft, or a roster disbanded meanwhile.
func lostRace(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23503") // unique_violation, foreign_key_violation
}

// draftState is what one draft attempt reads.
type draftState struct {
	existing      *draftResponse // the session's active draft, already bumped
	ownerFound    bool
	defaultRoster *string
	linkedRoster  *string
	rosters       []rosterRow
	previous      *previousDraft
	active        []activeDraft
	last          map[string]map[string]time.Time // by roster ID, then member name key
}

// previousDraft is the session's most recent draft, for a resume.
type previousDraft struct {
	rosterID, memberName string
	copy                 int
	repoHash             *string
	repo, task           []byte
}

// activeDraft is one of the owner's active drafts. restingSince is set for a resting one (see restingSince).
type activeDraft struct {
	id               int64
	rosterID, member string
	copy             int
	restingSince     *time.Time
}

// restingSince is when a draft (aliased d) started resting, or null: its session's turn ended, after it last started,
// at least 15 minutes ago.
const restingSince = `case when d.turn_ended_at <= now() - interval '15 minutes'
	and d.turn_ended_at > coalesce(d.turn_started_at, '-infinity') then d.turn_ended_at end`

func tryDraft(ctx context.Context, db *pgxpool.Pool, ownerID string, in draftInput, newMember bool) (draftResponse, error) {
	st, err := readDraftState(ctx, db, ownerID, in)
	if err != nil {
		return draftResponse{}, err
	}
	if st.existing != nil {
		return *st.existing, nil
	}
	if !st.ownerFound {
		return draftResponse{}, errUnauthorized
	}

	// Resume: the session's previous member, if they're still in the cast and free. The resumed draft keeps the
	// session's repo and task unless new ones were sent.
	if p := st.previous; p != nil {
		if roster, ok := st.roster(p.rosterID); ok {
			if m, inCast := roster.findMember(p.memberName); inCast && !st.busy(roster.ID)[slot{nameKey(m.Name), p.copy}] {
				d := newDraft{ownerID, roster, m, p.copy, in.session, p.repoHash, p.repo, p.task, in.idle}
				if in.repoHash != nil {
					d.repoHash, d.repo = in.repoHash, in.repo
				}
				if in.task != nil {
					d.task = in.task
				}
				return writeDraft(ctx, db, d, nil)
			}
		}
	}
	if !newMember {
		return draftResponse{}, errNotDrafted
	}

	roster, err := st.draftRoster(in)
	if err != nil {
		return draftResponse{}, err
	}
	busy := st.busy(roster.ID)
	m, copy := freeMember(roster, busy, st.last[roster.ID])

	// Every name is taken: before handing out a copy, take the name of the member who has rested longest
	var taking *int64
	if copy > 1 {
		if resting, ok := st.longestResting(roster); ok {
			m, copy, taking = resting.member, 1, &resting.id
		}
	}
	return writeDraft(ctx, db, newDraft{ownerID, roster, m, copy, in.session, in.repoHash, in.repo, in.task, in.idle}, taking)
}

// readDraftState reads what a draft needs in one round trip. Its first statement bumps the session's active draft, if
// it has one, which is all a draft of an already-drafted session does.
func readDraftState(ctx context.Context, db *pgxpool.Pool, ownerID string, in draftInput) (draftState, error) {
	st := draftState{last: map[string]map[string]time.Time{}}
	batch := &pgx.Batch{}
	batch.Queue(`
		update drafts d set last_seen_at = now(), task = coalesce($3, d.task) from rosters r
		where r.id = d.roster_id and d.owner_id = $1 and d.session_id = $2 and d.benched_at is null
		returning `+draftColumns, ownerID, in.session, in.task).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			_, d, err := scanDraft(rows)
			if err != nil {
				return err
			}
			st.existing = &d
		}
		return rows.Err()
	})
	batch.Queue(`select default_roster_id from owners where id = $1`, ownerID).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			st.ownerFound = true
			if err := rows.Scan(&st.defaultRoster); err != nil {
				return err
			}
		}
		return rows.Err()
	})
	batch.Queue(`select `+rosterColumns+` from rosters r where r.owner_id = $1 order by r.created_at, r.name`, ownerID).Query(
		func(rows pgx.Rows) error {
			var err error
			st.rosters, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (rosterRow, error) { return scanRoster(row) })
			return err
		})
	if in.repoHash != nil {
		batch.Queue(`select roster_id from project_links where owner_id = $1 and repo_hash = $2`, ownerID, *in.repoHash).Query(
			func(rows pgx.Rows) error {
				for rows.Next() {
					if err := rows.Scan(&st.linkedRoster); err != nil {
						return err
					}
				}
				return rows.Err()
			})
	}
	batch.Queue(`
		select d.roster_id, d.member_name, d.copy, d.repo_hash, d.repo, d.task from drafts d
		where d.owner_id = $1 and d.session_id = $2
		order by d.drafted_at desc, d.id desc limit 1`, ownerID, in.session).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			var p previousDraft
			if err := rows.Scan(&p.rosterID, &p.memberName, &p.copy, &p.repoHash, &p.repo, &p.task); err != nil {
				return err
			}
			st.previous = &p
		}
		return rows.Err()
	})
	batch.Queue(`
		select d.id, d.roster_id, d.member_name, d.copy, `+restingSince+` from drafts d
		where d.owner_id = $1 and d.benched_at is null
		order by d.turn_ended_at, d.id`, ownerID).Query(func(rows pgx.Rows) error {
		var err error
		st.active, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (activeDraft, error) {
			var d activeDraft
			err := row.Scan(&d.id, &d.rosterID, &d.member, &d.copy, &d.restingSince)
			return d, err
		})
		return err
	})
	batch.Queue(`
		select d.roster_id, d.member_name, max(d.drafted_at) from drafts d
		where d.owner_id = $1 group by d.roster_id, d.member_name`, ownerID).Query(func(rows pgx.Rows) error {
		for rows.Next() {
			var rosterID, name string
			var at time.Time
			if err := rows.Scan(&rosterID, &name, &at); err != nil {
				return err
			}
			if st.last[rosterID] == nil {
				st.last[rosterID] = map[string]time.Time{}
			}
			st.last[rosterID][nameKey(name)] = at
		}
		return rows.Err()
	})
	err := db.SendBatch(ctx, batch).Close()
	return st, err
}

func (st draftState) roster(id string) (rosterRow, bool) {
	for _, r := range st.rosters {
		if r.ID == id {
			return r, true
		}
	}
	return rosterRow{}, false
}

// draftRoster picks the roster for a session's first draft: the named roster, else the project's linked roster, else
// the default.
func (st draftState) draftRoster(in draftInput) (rosterRow, error) {
	if in.roster != nil {
		for _, r := range st.rosters {
			if nameKey(r.Name) == nameKey(*in.roster) {
				return r, nil
			}
		}
		return rosterRow{}, rosterNotFound(*in.roster)
	}
	for _, id := range []*string{st.linkedRoster, st.defaultRoster} {
		if id == nil {
			continue
		}
		if r, ok := st.roster(*id); ok {
			return r, nil
		}
	}
	return rosterRow{}, errNoRosters
}

// slot is a member (by name key) and copy number.
type slot struct {
	key  string
	copy int
}

// busy is the roster's names and copies held by active drafts.
func (st draftState) busy(rosterID string) map[slot]bool {
	busy := map[slot]bool{}
	for _, d := range st.active {
		if d.rosterID == rosterID {
			busy[slot{nameKey(d.member), d.copy}] = true
		}
	}
	return busy
}

// restingMember is a resting draft whose name a new draft can take.
type restingMember struct {
	id     int64
	member member
}

// longestResting is the roster's longest-resting draft of a member's own name (copy 1) whose member is still in the
// cast. ok is false when none is resting.
func (st draftState) longestResting(roster rosterRow) (restingMember, bool) {
	for _, d := range st.active { // ordered by when their turns ended
		if d.rosterID != roster.ID || d.copy != 1 || d.restingSince == nil {
			continue
		}
		if m, inCast := roster.findMember(d.member); inCast {
			return restingMember{d.id, m}, true
		}
	}
	return restingMember{}, false
}

// freeMember picks the free copy-1 member drafted least recently (never-drafted first, then roster order), then copy 2
// the same way, and so on, so drafts cycle through the cast. The cast is never empty.
func freeMember(roster rosterRow, busy map[slot]bool, last map[string]time.Time) (member, int) {
	for copy := 1; ; copy++ {
		if m, ok := leastRecentlyDrafted(roster.Members, func(m member) bool { return busy[slot{nameKey(m.Name), copy}] }, last); ok {
			return m, copy
		}
	}
}

// leastRecentlyDrafted is the member not busy whose last draft is oldest; never drafted counts as oldest, and ties
// keep roster order.
func leastRecentlyDrafted(members []member, isBusy func(member) bool, last map[string]time.Time) (member, bool) {
	best, found := member{}, false
	var bestAt time.Time
	for _, m := range members {
		if isBusy(m) {
			continue
		}
		at := last[nameKey(m.Name)]
		if !found || at.Before(bestAt) {
			best, bestAt, found = m, at, true
		}
	}
	return best, found
}

type newDraft struct {
	ownerID  string
	roster   rosterRow
	member   member
	copy     int
	session  string
	repoHash *string
	repo     []byte
	task     []byte
	idle     bool
}

// writeDraft inserts a draft in one round trip, first benching the resting draft whose name it takes, if any. The two
// statements run as one implicit transaction: if the resting draft's session woke meanwhile, the bench changes nothing,
// the insert hits the unique index on its name, and neither is kept.
func writeDraft(ctx context.Context, db *pgxpool.Pool, d newDraft, taking *int64) (draftResponse, error) {
	resp := draftResponse{
		Name: d.member.Name, Emoji: d.member.Emoji, Copy: d.copy, Roster: d.roster.Name,
		EmojiMode: d.roster.EmojiMode, Session: d.session, Task: encodeCiphertext(d.task), Repo: encodeCiphertext(d.repo),
	}
	batch := &pgx.Batch{}
	if taking != nil {
		batch.Queue(`update drafts d set benched_at = now() where d.id = $1 and d.benched_at is null and (`+restingSince+`) is not null`,
			*taking)
	}
	// An idle session's turn "ended" at its draft, so it rests if no prompt comes
	batch.Queue(`
		insert into drafts (owner_id, roster_id, member_name, member_emoji, copy, session_id, repo_hash, repo, task,
		                    turn_ended_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, case when $10 then now() end)
		returning drafted_at`,
		d.ownerID, d.roster.ID, d.member.Name, d.member.Emoji, d.copy, d.session, d.repoHash, d.repo, d.task, d.idle).QueryRow(
		func(row pgx.Row) error { return row.Scan(&resp.DraftedAt) })
	err := db.SendBatch(ctx, batch).Close()
	return resp, err
}

// agentTarget picks an active draft: the session's, or a member by name ("Gandalf 2" for a copy) across the owner's
// rosters, optionally narrowed to one roster.
type agentTarget struct {
	Session *string `json:"session"`
	Name    *string `json:"name"`
	Roster  *string `json:"roster"`
}

func findTargetDraftID(ctx context.Context, tx pgx.Tx, ownerID string, target agentTarget) (int64, error) {
	if (target.Session == nil) == (target.Name == nil) {
		return 0, invalidRequest("Send either session or name.")
	}

	if target.Session != nil {
		var id int64
		err := tx.QueryRow(ctx, `
			select d.id from drafts d join rosters r on r.id = d.roster_id
			where r.owner_id = $1 and d.session_id = $2 and d.benched_at is null`, ownerID, *target.Session).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errNotDrafted
		}
		return id, err
	}

	// By name: match the display name, ignoring case and accents
	var rosterID *string
	if target.Roster != nil {
		roster, err := findRosterByName(ctx, tx, ownerID, *target.Roster)
		if err != nil {
			return 0, err
		}
		rosterID = &roster.ID
	}
	rows, err := tx.Query(ctx, `
		select d.id, r.name, d.member_name, d.copy from drafts d join rosters r on r.id = d.roster_id
		where r.owner_id = $1 and d.benched_at is null and ($2::uuid is null or r.id = $2)
		order by r.created_at, r.name`, ownerID, rosterID)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id      int64
		roster  string
		display string
	}
	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (candidate, error) {
		var c candidate
		var memberName string
		var copy int
		err := row.Scan(&c.id, &c.roster, &memberName, &copy)
		c.display = displayName(memberName, copy)
		return c, err
	})
	if err != nil {
		return 0, err
	}

	var matches []candidate
	for _, c := range candidates {
		if nameKey(c.display) == nameKey(*target.Name) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return 0, errNotDrafted
	case 1:
		return matches[0].id, nil
	default:
		var rosters []string
		for _, m := range matches {
			rosters = append(rosters, m.roster)
		}
		return 0, &apiError{http.StatusConflict, "ambiguous_name",
			fmt.Sprintf("%q is drafted in more than one roster. Pick one with roster.", *target.Name), rosters}
	}
}

// handleBench frees a session's member, or a member by name (the manual fix for a name stuck by a dead session).
func (s *Server) handleBench(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req agentTarget
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}

	var resp draftResponse
	err := s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		id, err := findTargetDraftID(r.Context(), tx, ownerID, req)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `update drafts set benched_at = now() where id = $1`, id); err != nil {
			return err
		}
		resp, err = loadDraft(r.Context(), tx, id)
		return err
	})
	if err != nil {
		return err
	}
	logDraft(r.Context(), resp)
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleSetTask sets an agent's task (ciphertext) and bumps its last-seen time.
func (s *Server) handleSetTask(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req struct {
		agentTarget
		Task *string `json:"task"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Task == nil {
		return invalidRequest("task is required.")
	}
	task, err := decodeCiphertext("task", req.Task)
	if err != nil {
		return err
	}

	var resp draftResponse
	err = s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		id, err := findTargetDraftID(r.Context(), tx, ownerID, req.agentTarget)
		if err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `update drafts set task = $2, last_seen_at = now() where id = $1`, id, task)
		if err != nil {
			return err
		}
		resp, err = loadDraft(r.Context(), tx, id)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleTurn records that the session's turn started or ended, at the client's time but never later than now, and
// bumps the draft's last-seen time. Each time only moves forward, so a report that arrives late can't undo a newer one.
// Every turn reports, so it's one statement, without the owner lock.
func (s *Server) handleTurn(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req struct {
		Session string    `json:"session"`
		Ended   bool      `json:"ended"`
		At      time.Time `json:"at"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if err := validateSession(req.Session); err != nil {
		return err
	}
	if req.At.IsZero() {
		return invalidRequest("at is required.")
	}

	column := "turn_started_at"
	if req.Ended {
		column = "turn_ended_at"
	}
	tag, err := s.db.Exec(r.Context(), `
		update drafts d set `+column+` = greatest(d.`+column+`, least($3::timestamptz, now())), last_seen_at = now()
		where d.owner_id = $1 and d.session_id = $2 and d.benched_at is null`,
		ownerID, req.Session, req.At)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return orUnauthorized(r.Context(), s.db, ownerID, errNotDrafted)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// handleGetSession returns the session's active draft, without bumping it.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request, ownerID string) error {
	_, resp, err := scanDraft(s.db.QueryRow(r.Context(), `
		select `+draftColumns+` from drafts d join rosters r on r.id = d.roster_id
		where d.owner_id = $1 and d.session_id = $2 and d.benched_at is null`, ownerID, r.PathValue("session")))
	if errors.Is(err, pgx.ErrNoRows) {
		return orUnauthorized(r.Context(), s.db, ownerID, errNotDrafted)
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
