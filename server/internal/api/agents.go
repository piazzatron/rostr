package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

type draftResponse struct {
	Name      string    `json:"name"`
	Emoji     string    `json:"emoji"`
	Copy      int       `json:"copy"`
	Team      *string   `json:"team"`
	Roster    string    `json:"roster"`
	EmojiMode string    `json:"emojiMode"`
	Session   string    `json:"session"`
	Task      *string   `json:"task"`
	Repo      *string   `json:"repo"`
	DraftedAt time.Time `json:"draftedAt"`
}

// draftColumns selects a draft with its roster; queries alias drafts as d and rosters as r.
const draftColumns = `d.id, r.name, r.emoji_mode, r.a_team, r.b_team, d.member_name, d.member_emoji, d.copy,
	d.session_id, d.task, d.repo, d.drafted_at`

func scanDraft(row pgx.Row) (int64, draftResponse, error) {
	var id int64
	var d draftResponse
	var roster rosterRow
	var task, repo []byte
	err := row.Scan(&id, &d.Roster, &d.EmojiMode, &roster.ATeam, &roster.BTeam, &d.Name, &d.Emoji, &d.Copy,
		&d.Session, &task, &repo, &d.DraftedAt)
	d.Team = roster.team(d.Name)
	d.Task, d.Repo = encodeCiphertext(task), encodeCiphertext(repo)
	return id, d, err
}

func loadDraft(ctx context.Context, q querier, id int64) (draftResponse, error) {
	_, d, err := scanDraft(q.QueryRow(ctx,
		`select `+draftColumns+` from drafts d join rosters r on r.id = d.roster_id where d.id = $1`, id))
	return d, err
}

var errNotDrafted = &apiError{http.StatusNotFound, "not_drafted", "No active draft matches.", nil}

var errNoRosters = &apiError{http.StatusNotFound, "no_rosters", "No rosters yet. Run `rostr assemble`.", nil}

// draftInput is what drafting knows about a session. task and repo are ciphertext. preferred is a member the
// machine already announced to the session, drafted on its own from its copy of the idle list; avoid is the members
// it has announced to other sessions but not confirmed yet.
type draftInput struct {
	session   string
	roster    *string
	repoHash  *string
	repo      []byte
	task      []byte
	preferred *string
	avoid     []string
	machine   string
}

func (s *Server) handleDraft(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req struct {
		Session   string   `json:"session"`
		Roster    *string  `json:"roster"`
		RepoHash  *string  `json:"repoHash"`
		Repo      *string  `json:"repo"`
		Task      *string  `json:"task"`
		Preferred *string  `json:"preferred"`
		Avoid     []string `json:"avoid"`
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
	if req.Preferred != nil {
		if _, err := validateName("preferred", *req.Preferred); err != nil {
			return err
		}
	}
	if len(req.Avoid) > maxMembers {
		return invalidRequest("avoid has too many names.")
	}

	machine, err := machineID(r)
	if err != nil {
		return err
	}
	in := draftInput{req.Session, req.Roster, repoHash, repo, task, req.Preferred, req.Avoid, machine}
	resp, err := s.draftTx(r.Context(), ownerID, machine, func(tx pgx.Tx) (draftResponse, error) {
		return draftSession(r.Context(), tx, ownerID, in)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// draftTx runs a draft-changing write in the owner's transaction, reserves the machine's next picks, and returns the
// draft with the owner's next state.
func (s *Server) draftTx(ctx context.Context, ownerID, machine string, fn func(tx pgx.Tx) (draftResponse, error)) (draftWithNext, error) {
	var resp draftWithNext
	err := s.withOwnerTx(ctx, ownerID, func(tx pgx.Tx) error {
		var err error
		if resp.draftResponse, err = fn(tx); err != nil {
			return err
		}
		if err := reserveNext(ctx, tx, ownerID, machine); err != nil {
			return err
		}
		resp.Next, err = loadNext(ctx, tx, ownerID, machine)
		return err
	})
	return resp, err
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

	machine, err := machineID(r)
	if err != nil {
		return err
	}
	resp, err := s.draftTx(r.Context(), ownerID, machine, func(tx pgx.Tx) (draftResponse, error) {
		resp, found, err := reclaimSession(r.Context(), tx, ownerID, draftInput{session: req.Session})
		if err == nil && !found {
			return resp, errNotDrafted
		}
		return resp, err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// draftSession gives the session a member. It runs inside withOwnerTx, which has already expired stale drafts:
//  1. same session, same name, via reclaimSession;
//  2. resume, also via reclaimSession, when the preferred member (if any) is who the session had;
//  3. the preferred member if they're in the cast, or the member the machine holds a reservation on: their name if
//     it's free, else their lowest free copy, so a session announced as Ginny becomes Ginny 2 rather than someone
//     else;
//  4. otherwise the free member drafted least recently, A team first, then numbered copies, skipping members other
//     machines hold reservations on and the ones this machine has announced to other sessions (avoid), unless only
//     copies are left.
func draftSession(ctx context.Context, tx pgx.Tx, ownerID string, in draftInput) (draftResponse, error) {
	resp, found, err := reclaimSession(ctx, tx, ownerID, in)
	if err != nil || found {
		return resp, err
	}

	roster, err := resolveDraftRoster(ctx, tx, ownerID, in)
	if err != nil {
		return draftResponse{}, err
	}
	busy, err := busySlots(ctx, tx, roster.ID)
	if err != nil {
		return draftResponse{}, err
	}
	last, err := lastDrafted(ctx, tx, roster.ID)
	if err != nil {
		return draftResponse{}, err
	}
	all, err := reservations(ctx, tx, ownerID)
	if err != nil {
		return draftResponse{}, err
	}
	reservedByOthers(all, roster.ID, in.machine, busy)
	preferred := ownReservation(all, roster.ID, in.machine)
	for _, name := range in.avoid {
		// The machine announced its reserved member to another session, whose confirmation is on its way
		if nameKey(name) == nameKey(preferred) {
			preferred = ""
		}
	}
	if in.preferred != nil {
		preferred = *in.preferred
	}
	if preferred != "" {
		if m, _, inCast := roster.findMember(preferred); inCast {
			copy := 1
			for busy[slot{nameKey(m.Name), copy}] {
				copy++
			}
			return insertReservedDraft(ctx, tx, newDraft{ownerID, roster.ID, m, copy, in.session, in.repoHash, in.repo, in.task})
		}
	}
	for _, name := range in.avoid {
		busy[slot{nameKey(name), 1}] = true
	}
	m, copy := freeMember(roster, busy, last)
	return insertReservedDraft(ctx, tx, newDraft{ownerID, roster.ID, m, copy, in.session, in.repoHash, in.repo, in.task})
}

// insertReservedDraft inserts a draft and drops the reservations on its member's name, now taken.
func insertReservedDraft(ctx context.Context, tx pgx.Tx, d newDraft) (draftResponse, error) {
	if d.copy == 1 {
		if err := releaseReservations(ctx, tx, d.rosterID, d.member.Name); err != nil {
			return draftResponse{}, err
		}
	}
	return insertDraft(ctx, tx, d)
}

// reclaimSession returns the session's active draft in any of the owner's rosters, bumping its last-seen time (and
// setting its task, if given). Failing that, it re-drafts the session's previous member from the same roster if they're
// still in the cast and free. found is false when neither applies.
func reclaimSession(ctx context.Context, tx pgx.Tx, ownerID string, in draftInput) (draftResponse, bool, error) {
	// Same session, same name
	var id int64
	err := tx.QueryRow(ctx, `
		update drafts d set last_seen_at = now(), task = coalesce($3, d.task)
		from rosters r
		where r.id = d.roster_id and r.owner_id = $1 and d.session_id = $2 and d.released_at is null
		returning d.id`, ownerID, in.session, in.task).Scan(&id)
	if err == nil {
		resp, err := loadDraft(ctx, tx, id)
		return resp, err == nil, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return draftResponse{}, false, err
	}
	// Resume: the session's most recent member, from the same roster
	var prev struct {
		rosterID, memberName string
		copy                 int
		repoHash             *string
		repo, task           []byte
	}
	err = tx.QueryRow(ctx, `
		select d.roster_id, d.member_name, d.copy, d.repo_hash, d.repo, d.task
		from drafts d join rosters r on r.id = d.roster_id
		where r.owner_id = $1 and d.session_id = $2
		order by d.drafted_at desc, d.id desc
		limit 1`, ownerID, in.session).Scan(&prev.rosterID, &prev.memberName, &prev.copy, &prev.repoHash, &prev.repo, &prev.task)
	if errors.Is(err, pgx.ErrNoRows) {
		return draftResponse{}, false, nil
	}
	if err != nil {
		return draftResponse{}, false, err
	}
	// A session told a preferred member resumes only if that's who it had; anyone else is drafted fresh
	if in.preferred != nil && nameKey(*in.preferred) != nameKey(prev.memberName) {
		return draftResponse{}, false, nil
	}

	roster, err := scanRoster(tx.QueryRow(ctx, `select `+rosterColumns+` from rosters r where r.id = $1`, prev.rosterID))
	if err != nil {
		return draftResponse{}, false, err
	}
	m, _, inCast := roster.findMember(prev.memberName)
	if !inCast {
		return draftResponse{}, false, nil
	}
	busy, err := busySlots(ctx, tx, roster.ID)
	if err != nil || busy[slot{nameKey(m.Name), prev.copy}] {
		return draftResponse{}, false, err
	}

	// The resumed draft keeps the session's repo and task unless new ones were sent
	repoHash, repo, task := prev.repoHash, prev.repo, prev.task
	if in.repoHash != nil {
		repoHash, repo = in.repoHash, in.repo
	}
	if in.task != nil {
		task = in.task
	}
	resp, err := insertDraft(ctx, tx, newDraft{ownerID, roster.ID, m, prev.copy, in.session, repoHash, repo, task})
	return resp, err == nil, err
}

// resolveDraftRoster picks the roster for a session's first draft: the named roster, else the project's linked
// roster, else the default.
func resolveDraftRoster(ctx context.Context, tx pgx.Tx, ownerID string, in draftInput) (rosterRow, error) {
	if in.roster != nil {
		return findRosterByName(ctx, tx, ownerID, *in.roster)
	}

	if in.repoHash != nil {
		roster, err := scanRoster(tx.QueryRow(ctx, `
			select `+rosterColumns+` from rosters r join project_links l on l.roster_id = r.id
			where l.owner_id = $1 and l.repo_hash = $2`, ownerID, *in.repoHash))
		if !errors.Is(err, pgx.ErrNoRows) {
			return roster, err
		}
	}

	roster, err := scanRoster(tx.QueryRow(ctx, `
		select `+rosterColumns+` from rosters r join owners o on o.default_roster_id = r.id
		where o.id = $1`, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return rosterRow{}, errNoRosters
	}
	return roster, err
}

// slot is a member (by name key) and copy number.
type slot struct {
	key  string
	copy int
}

func busySlots(ctx context.Context, tx pgx.Tx, rosterID string) (map[slot]bool, error) {
	rows, err := tx.Query(ctx, `select member_name, copy from drafts where roster_id = $1 and released_at is null`, rosterID)
	if err != nil {
		return nil, err
	}
	slots, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (slot, error) {
		var name string
		var copy int
		err := row.Scan(&name, &copy)
		return slot{nameKey(name), copy}, err
	})
	busy := map[slot]bool{}
	for _, s := range slots {
		busy[s] = true
	}
	return busy, err
}

// freeMember picks the free copy-1 member drafted least recently (never-drafted first, then roster order), A team
// first, then the B team, then copy 2 the same way, and so on, so drafts cycle through the cast. The cast is never
// empty.
func freeMember(roster rosterRow, busy map[slot]bool, last map[string]time.Time) (member, int) {
	for copy := 1; ; copy++ {
		for _, team := range [][]member{roster.ATeam, roster.BTeam} {
			if m, ok := leastRecentlyDrafted(team, func(m member) bool { return busy[slot{nameKey(m.Name), copy}] }, last); ok {
				return m, copy
			}
		}
	}
}

// leastRecentlyDrafted is the member not busy whose last draft is oldest; never drafted counts as oldest, and ties
// keep roster order.
func leastRecentlyDrafted(team []member, isBusy func(member) bool, last map[string]time.Time) (member, bool) {
	best, found := member{}, false
	var bestAt time.Time
	for _, m := range team {
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

// lastDrafted is when each of a roster's members was last drafted, by name key, for members drafted within the
// 7-day retention of released drafts.
func lastDrafted(ctx context.Context, q querier, rosterID string) (map[string]time.Time, error) {
	rows, err := q.Query(ctx, `select member_name, max(drafted_at) from drafts where roster_id = $1 group by member_name`, rosterID)
	if err != nil {
		return nil, err
	}
	last := map[string]time.Time{}
	_, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct{}, error) {
		var name string
		var at time.Time
		err := row.Scan(&name, &at)
		last[nameKey(name)] = at
		return struct{}{}, err
	})
	return last, err
}

type newDraft struct {
	ownerID  string
	rosterID string
	member   member
	copy     int
	session  string
	repoHash *string
	repo     []byte
	task     []byte
}

func insertDraft(ctx context.Context, tx pgx.Tx, d newDraft) (draftResponse, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		insert into drafts (owner_id, roster_id, member_name, member_emoji, copy, session_id, repo_hash, repo, task)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		returning id`,
		d.ownerID, d.rosterID, d.member.Name, d.member.Emoji, d.copy, d.session, d.repoHash, d.repo, d.task).Scan(&id)
	if err != nil {
		return draftResponse{}, err
	}
	return loadDraft(ctx, tx, id)
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
			where r.owner_id = $1 and d.session_id = $2 and d.released_at is null`, ownerID, *target.Session).Scan(&id)
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
		where r.owner_id = $1 and d.released_at is null and ($2::uuid is null or r.id = $2)
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

// handleRelease frees a session's member, or a member by name (the manual fix for a name stuck by a dead session).
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req agentTarget
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}

	machine, err := machineID(r)
	if err != nil {
		return err
	}
	resp, err := s.draftTx(r.Context(), ownerID, machine, func(tx pgx.Tx) (draftResponse, error) {
		id, err := findTargetDraftID(r.Context(), tx, ownerID, req)
		if err != nil {
			return draftResponse{}, err
		}
		if _, err := tx.Exec(r.Context(), `update drafts set released_at = now() where id = $1`, id); err != nil {
			return draftResponse{}, err
		}
		return loadDraft(r.Context(), tx, id)
	})
	if err != nil {
		return err
	}
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

	machine, err := machineID(r)
	if err != nil {
		return err
	}
	resp, err := s.draftTx(r.Context(), ownerID, machine, func(tx pgx.Tx) (draftResponse, error) {
		id, err := findTargetDraftID(r.Context(), tx, ownerID, req.agentTarget)
		if err != nil {
			return draftResponse{}, err
		}
		_, err = tx.Exec(r.Context(), `update drafts set task = $2, last_seen_at = now() where id = $1`, id, task)
		if err != nil {
			return draftResponse{}, err
		}
		return loadDraft(r.Context(), tx, id)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleGetSession returns the session's active draft, without bumping it.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request, ownerID string) error {
	_, resp, err := scanDraft(s.db.QueryRow(r.Context(), `
		select `+draftColumns+`
		from drafts d join rosters r on r.id = d.roster_id join owners o on o.id = r.owner_id
		where r.owner_id = $1 and d.session_id = $2 and d.released_at is null
		  and d.last_seen_at >= now() - o.draft_expiry`, ownerID, r.PathValue("session")))
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotDrafted
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
