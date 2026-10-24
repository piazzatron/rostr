package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is satisfied by both the pool and a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

type rosterRow struct {
	ID        string
	Name      string
	EmojiMode string
	Members   []member // in draft order
	CreatedAt time.Time
}

// rosterColumns selects a rosterRow; queries alias rosters as r.
const rosterColumns = `r.id, r.name, r.emoji_mode, r.members, r.created_at`

func scanRoster(row pgx.Row) (rosterRow, error) {
	var r rosterRow
	err := row.Scan(&r.ID, &r.Name, &r.EmojiMode, &r.Members, &r.CreatedAt)
	return r, err
}

// findMember finds a member by name, ignoring case and accents.
func (r rosterRow) findMember(name string) (member, bool) {
	key := nameKey(name)
	for _, m := range r.Members {
		if nameKey(m.Name) == key {
			return m, true
		}
	}
	return member{}, false
}

func rosterNotFound(name string) *apiError {
	return &apiError{http.StatusNotFound, "roster_not_found", fmt.Sprintf("No roster named %q.", name), nil}
}

func findRosterByName(ctx context.Context, q querier, ownerID, name string) (rosterRow, error) {
	roster, err := scanRoster(q.QueryRow(ctx,
		`select `+rosterColumns+` from rosters r where r.owner_id = $1 and r.name_key = $2`, ownerID, nameKey(name)))
	if errors.Is(err, pgx.ErrNoRows) {
		return rosterRow{}, rosterNotFound(name)
	}
	return roster, err
}

func insertRoster(ctx context.Context, tx pgx.Tx, ownerID, name string, c cast, createdAt *time.Time) (rosterRow, error) {
	roster, err := scanRoster(tx.QueryRow(ctx, `
		insert into rosters as r (owner_id, name, name_key, emoji_mode, members, created_at)
		values ($1, $2, $3, $4, $5, coalesce($6, clock_timestamp()))
		returning `+rosterColumns,
		ownerID, name, nameKey(name), c.EmojiMode, c.Members, createdAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "rosters_owner_name" {
		return rosterRow{}, &apiError{http.StatusConflict, "roster_exists",
			fmt.Sprintf("A roster named %q already exists (names ignore case and accents).", name), nil}
	}
	return roster, err
}

// promoteOldestRosterToDefault makes the owner's oldest roster the default when there isn't one.
func promoteOldestRosterToDefault(ctx context.Context, tx pgx.Tx, ownerID string) error {
	_, err := tx.Exec(ctx, `
		update owners set default_roster_id =
		  (select id from rosters where owner_id = $1 order by created_at, name limit 1)
		where id = $1 and default_roster_id is null`, ownerID)
	return err
}

type rosterResponse struct {
	Name           string    `json:"name"`
	EmojiMode      string    `json:"emojiMode"`
	Members        []member  `json:"members"`
	IsDefault      bool      `json:"isDefault"`
	ActiveDrafts   int       `json:"activeDrafts"`
	LinkedProjects int       `json:"linkedProjects"`
	CreatedAt      time.Time `json:"createdAt"`
}

func rosterDetails(ctx context.Context, q querier, ownerID string, r rosterRow) (rosterResponse, error) {
	resp := rosterResponse{Name: r.Name, EmojiMode: r.EmojiMode, Members: r.Members, CreatedAt: r.CreatedAt}
	err := q.QueryRow(ctx, `
		select
		  coalesce((select default_roster_id = $2 from owners where id = $1), false),
		  (select count(*) from drafts d where d.roster_id = $2 and d.benched_at is null),
		  (select count(*) from project_links where roster_id = $2)`,
		ownerID, r.ID).Scan(&resp.IsDefault, &resp.ActiveDrafts, &resp.LinkedProjects)
	return resp, err
}

func (s *Server) handleCreateRoster(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var req struct {
		Name string `json:"name"`
		cast
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	name, err := validateName("name", req.Name)
	if err != nil {
		return err
	}
	c, err := validateCast(req.cast)
	if err != nil {
		return err
	}

	var resp rosterResponse
	err = s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		roster, err := insertRoster(r.Context(), tx, ownerID, name, c, nil)
		if err != nil {
			return err
		}
		if err := promoteOldestRosterToDefault(r.Context(), tx, ownerID); err != nil {
			return err
		}
		resp, err = rosterDetails(r.Context(), tx, ownerID, roster)
		return err
	})
	if err != nil {
		return err
	}
	logAttrs(r.Context(), slog.String("roster", name), slog.Int("members", len(c.Members)))
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

func (s *Server) handleGetRoster(w http.ResponseWriter, r *http.Request, ownerID string) error {
	roster, err := findRosterByName(r.Context(), s.db, ownerID, r.PathValue("name"))
	var notFound *apiError
	if errors.As(err, &notFound) && notFound.code == "roster_not_found" {
		return orUnauthorized(r.Context(), s.db, ownerID, err)
	}
	if err != nil {
		return err
	}
	resp, err := rosterDetails(r.Context(), s.db, ownerID, roster)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleEditRoster saves `rostr edit`, refusing to rename or remove a drafted member.
func (s *Server) handleEditRoster(w http.ResponseWriter, r *http.Request, ownerID string) error {
	return s.updateCast(w, r, ownerID, true)
}

// handleReplaceCast swaps in a new cast from `rostr assemble`. The roster keeps its name, links, and default status,
// and drafted agents keep their names (each draft stores its emoji) until their sessions end.
func (s *Server) handleReplaceCast(w http.ResponseWriter, r *http.Request, ownerID string) error {
	return s.updateCast(w, r, ownerID, false)
}

func (s *Server) updateCast(w http.ResponseWriter, r *http.Request, ownerID string, keepDraftedMembers bool) error {
	var req cast
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	c, err := validateCast(req)
	if err != nil {
		return err
	}

	var resp rosterResponse
	err = s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		roster, err := findRosterByName(r.Context(), tx, ownerID, r.PathValue("name"))
		if err != nil {
			return err
		}

		// An edit must keep every drafted member, under the same name
		if keepDraftedMembers {
			rows, err := tx.Query(r.Context(),
				`select distinct member_name from drafts where roster_id = $1 and benched_at is null order by 1`, roster.ID)
			if err != nil {
				return err
			}
			drafted, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			updated := rosterRow{Members: c.Members}
			var missing []string
			for _, name := range drafted {
				if m, ok := updated.findMember(name); !ok || m.Name != name {
					missing = append(missing, name)
				}
			}
			if len(missing) > 0 {
				return &apiError{http.StatusConflict, "member_drafted", fmt.Sprintf(
					"Can't rename or remove drafted members: %s. Wait for their sessions to end, or release them first.",
					strings.Join(missing, ", ")), nil}
			}
		}

		roster, err = scanRoster(tx.QueryRow(r.Context(), `
			update rosters r set emoji_mode = $2, members = $3
			where r.id = $1
			returning `+rosterColumns, roster.ID, c.EmojiMode, c.Members))
		if err != nil {
			return err
		}
		resp, err = rosterDetails(r.Context(), tx, ownerID, roster)
		return err
	})
	if err != nil {
		return err
	}
	logAttrs(r.Context(), slog.String("roster", r.PathValue("name")), slog.Int("members", len(c.Members)))
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleDisbandRoster deletes a roster with its drafts and project links. If it was the default, the oldest
// remaining roster becomes the default.
func (s *Server) handleDisbandRoster(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var resp struct {
		Name             string  `json:"name"`
		BenchedDrafts    int     `json:"benchedDrafts"`
		UnlinkedProjects int     `json:"unlinkedProjects"`
		NewDefault       *string `json:"newDefault"`
		OwnerDeleted     bool    `json:"ownerDeleted"`
	}
	err := s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		roster, err := findRosterByName(r.Context(), tx, ownerID, r.PathValue("name"))
		if err != nil {
			return err
		}
		details, err := rosterDetails(r.Context(), tx, ownerID, roster)
		if err != nil {
			return err
		}
		resp.Name, resp.BenchedDrafts, resp.UnlinkedProjects = roster.Name, details.ActiveDrafts, details.LinkedProjects

		// Deleting cascades to drafts and links, and clears the owner's default if it was this roster
		if _, err := tx.Exec(r.Context(), `delete from rosters where id = $1`, roster.ID); err != nil {
			return err
		}

		// Disbanding the last roster deletes the owner too, so nothing of theirs is left on the server
		var remaining int
		if err := tx.QueryRow(r.Context(), `select count(*) from rosters where owner_id = $1`, ownerID).Scan(&remaining); err != nil {
			return err
		}
		if remaining == 0 {
			resp.OwnerDeleted = true
			_, err := tx.Exec(r.Context(), `delete from owners where id = $1`, ownerID)
			return err
		}
		if !details.IsDefault {
			return nil
		}

		// Promote the oldest remaining roster to default
		if err := promoteOldestRosterToDefault(r.Context(), tx, ownerID); err != nil {
			return err
		}
		settings, err := loadSettings(r.Context(), tx, ownerID)
		resp.NewDefault = settings.DefaultRoster
		return err
	})
	if err != nil {
		return err
	}
	logAttrs(r.Context(), slog.String("roster", resp.Name), slog.Int("benched", resp.BenchedDrafts),
		slog.Bool("ownerDeleted", resp.OwnerDeleted))
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type listedDraft struct {
	Name      string    `json:"name"`
	Emoji     string    `json:"emoji"`
	Task      *string   `json:"task"`
	Repo      *string   `json:"repo"`
	DraftedAt time.Time `json:"draftedAt"`
	// RestingSince is when the agent's session finished its last turn, once it has been resting (15 minutes); null
	// while it's working or was recently
	RestingSince *time.Time `json:"restingSince"`
}

type listedRoster struct {
	Name      string        `json:"name"`
	EmojiMode string        `json:"emojiMode"`
	Members   []member      `json:"members"`
	IsDefault bool          `json:"isDefault"`
	CreatedAt time.Time     `json:"createdAt"`
	Drafted   []listedDraft `json:"drafted"`
	Idle      []member      `json:"idle"`
}

// handleStats is the public GET /v1/stats: every roster with its cast, drafted agents, and idle members, in the
// shape of `rostr stats --json`. Tasks and repos are ciphertext.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request, ownerID string) error {
	listed, err := listRosters(r.Context(), s.db, ownerID)
	if err != nil {
		return err
	}
	if len(listed) == 0 {
		if missing, err := ownerMissing(r.Context(), s.db, ownerID); err != nil {
			return err
		} else if missing {
			return errUnauthorized
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rosters": listed})
	return nil
}

// listRosters is every roster with its drafted agents and idle members, oldest roster first, in one round trip.
func listRosters(ctx context.Context, q querier, ownerID string) ([]listedRoster, error) {
	type listingRoster struct {
		rosterRow
		isDefault bool
	}
	type activeDraft struct {
		rosterID, memberName, memberEmoji string
		copy                              int
		task, repo                        []byte
		draftedAt                         time.Time
		restingSince                      *time.Time
	}
	batch := &pgx.Batch{}
	var rosters []listingRoster
	var drafts []activeDraft
	last := map[string]map[string]time.Time{} // by roster ID, then member name key

	batch.Queue(`
		select `+rosterColumns+`, coalesce(o.default_roster_id = r.id, false)
		from rosters r join owners o on o.id = r.owner_id
		where r.owner_id = $1 order by r.created_at, r.name`, ownerID).Query(func(rows pgx.Rows) error {
		var err error
		rosters, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (listingRoster, error) {
			var r listingRoster
			err := row.Scan(&r.ID, &r.Name, &r.EmojiMode, &r.Members, &r.CreatedAt, &r.isDefault)
			return r, err
		})
		return err
	})

	// Active drafts
	batch.Queue(`
		select d.roster_id, d.member_name, d.member_emoji, d.copy, d.task, d.repo, d.drafted_at, `+restingSince+`
		from drafts d where d.owner_id = $1 and d.benched_at is null
		order by d.drafted_at, d.id`, ownerID).Query(func(rows pgx.Rows) error {
		var err error
		drafts, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (activeDraft, error) {
			var d activeDraft
			err := row.Scan(&d.rosterID, &d.memberName, &d.memberEmoji, &d.copy, &d.task, &d.repo, &d.draftedAt, &d.restingSince)
			return d, err
		})
		return err
	})

	// When each member was last drafted, as lastDrafted gives for one roster
	batch.Queue(`
		select d.roster_id, d.member_name, max(d.drafted_at) from drafts d
		where d.owner_id = $1 group by d.roster_id, d.member_name`, ownerID).Query(func(rows pgx.Rows) error {
		_, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct{}, error) {
			var rosterID, name string
			var at time.Time
			if err := row.Scan(&rosterID, &name, &at); err != nil {
				return struct{}{}, err
			}
			if last[rosterID] == nil {
				last[rosterID] = map[string]time.Time{}
			}
			last[rosterID][nameKey(name)] = at
			return struct{}{}, nil
		})
		return err
	})

	if err := q.SendBatch(ctx, batch).Close(); err != nil {
		return nil, err
	}

	// A member is idle when their name (copy 1) is free, since that's who the next draft can pick; they're listed
	// in the order drafts would take them (see freeMember)
	listed := make([]listedRoster, 0, len(rosters))
	for _, roster := range rosters {
		entry := listedRoster{Name: roster.Name, EmojiMode: roster.EmojiMode, Members: roster.Members,
			IsDefault: roster.isDefault, CreatedAt: roster.CreatedAt, Drafted: []listedDraft{}, Idle: []member{}}
		busy := map[string]bool{}
		for _, d := range drafts {
			if d.rosterID != roster.ID {
				continue
			}
			if d.copy == 1 {
				busy[nameKey(d.memberName)] = true
			}
			entry.Drafted = append(entry.Drafted, listedDraft{
				Name:         displayName(d.memberName, d.copy),
				Emoji:        d.memberEmoji,
				Task:         encodeCiphertext(d.task),
				Repo:         encodeCiphertext(d.repo),
				DraftedAt:    d.draftedAt,
				RestingSince: d.restingSince,
			})
		}
		taken := map[string]bool{}
		for {
			m, ok := leastRecentlyDrafted(roster.Members, func(m member) bool { return busy[nameKey(m.Name)] || taken[nameKey(m.Name)] }, last[roster.ID])
			if !ok {
				break
			}
			taken[nameKey(m.Name)] = true
			entry.Idle = append(entry.Idle, m)
		}
		listed = append(listed, entry)
	}
	return listed, nil
}
