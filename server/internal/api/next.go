package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// nextResponse is what a machine needs to draft on its own before asking the server: which roster a project uses,
// and who's idle in each roster. Every draft-changing response carries it, and GET /v1/next returns it alone.
type nextResponse struct {
	AutoDraft     bool         `json:"autoDraft"`
	DefaultRoster *string      `json:"defaultRoster"`
	Links         []nextLink   `json:"links"`
	Rosters       []nextRoster `json:"rosters"`
}

type nextLink struct {
	RepoHash string `json:"repoHash"`
	Roster   string `json:"roster"`
}

type nextRoster struct {
	Name      string       `json:"name"`
	EmojiMode string       `json:"emojiMode"`
	Idle      []idleMember `json:"idle"`
	Reserved  *string      `json:"reserved"` // the member this machine holds a reservation on, or null
}

// draftWithNext is a draft response plus the owner's next state after it.
type draftWithNext struct {
	draftResponse
	Next nextResponse `json:"next"`
}

// loadNext is the owner's next state as seen by one machine: idle members leave out the ones other machines hold
// reservations on, and each roster says which member this machine holds.
func loadNext(ctx context.Context, q querier, ownerID, machine string) (nextResponse, error) {
	settings, err := loadSettings(ctx, q, ownerID)
	if err != nil {
		return nextResponse{}, err
	}
	all, err := reservations(ctx, q, ownerID)
	if err != nil {
		return nextResponse{}, err
	}
	rosterIDs := map[string]string{}
	rows, err := q.Query(ctx, `select r.id, r.name from rosters r where r.owner_id = $1`, ownerID)
	if err != nil {
		return nextResponse{}, err
	}
	if _, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct{}, error) {
		var id, name string
		err := row.Scan(&id, &name)
		rosterIDs[name] = id
		return struct{}{}, err
	}); err != nil {
		return nextResponse{}, err
	}
	rows, err = q.Query(ctx, `
		select l.repo_hash, r.name from project_links l join rosters r on r.id = l.roster_id
		where l.owner_id = $1 order by r.name, l.repo_hash`, ownerID)
	if err != nil {
		return nextResponse{}, err
	}
	links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (nextLink, error) {
		var l nextLink
		err := row.Scan(&l.RepoHash, &l.Roster)
		return l, err
	})
	if err != nil {
		return nextResponse{}, err
	}
	listed, err := listRosters(ctx, q, ownerID)
	if err != nil {
		return nextResponse{}, err
	}
	rosters := make([]nextRoster, len(listed))
	for i, r := range listed {
		taken := map[slot]bool{}
		reservedByOthers(all, rosterIDs[r.Name], machine, taken)
		idle := []idleMember{}
		for _, m := range r.Idle {
			if !taken[slot{nameKey(m.Name), 1}] {
				idle = append(idle, m)
			}
		}
		rosters[i] = nextRoster{Name: r.Name, EmojiMode: r.EmojiMode, Idle: idle}
		if held := ownReservation(all, rosterIDs[r.Name], machine); held != "" {
			rosters[i].Reserved = &held
		}
	}
	return nextResponse{AutoDraft: settings.AutoDraft, DefaultRoster: settings.DefaultRoster,
		Links: append([]nextLink{}, links...), Rosters: rosters}, nil
}

// handleGetNext reserves the machine's next picks and returns the next state.
func (s *Server) handleGetNext(w http.ResponseWriter, r *http.Request, ownerID string) error {
	machine, err := machineID(r)
	if err != nil {
		return err
	}
	var next nextResponse
	err = s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		if err := reserveNext(r.Context(), tx, ownerID, machine); err != nil {
			return err
		}
		next, err = loadNext(r.Context(), tx, ownerID, machine)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, next)
	return nil
}
