package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

type settingsResponse struct {
	DefaultRoster *string `json:"defaultRoster"`
	DraftExpiry   string  `json:"draftExpiry"`
	AutoDraft     bool    `json:"autoDraft"`
}

// settingsPatch changes the fields that are set and leaves the rest alone.
type settingsPatch struct {
	DefaultRoster *string `json:"defaultRoster"`
	DraftExpiry   *string `json:"draftExpiry"`
	AutoDraft     *bool   `json:"autoDraft"`
}

func loadSettings(ctx context.Context, q querier, ownerID string) (settingsResponse, error) {
	var resp settingsResponse
	var expiryHours int
	err := q.QueryRow(ctx, `
		select r.name, (extract(epoch from o.draft_expiry) / 3600)::int, o.auto_draft
		from owners o left join rosters r on r.id = o.default_roster_id
		where o.id = $1`, ownerID).Scan(&resp.DefaultRoster, &expiryHours, &resp.AutoDraft)
	resp.DraftExpiry = formatExpiryHours(expiryHours)
	return resp, err
}

func applySettings(ctx context.Context, tx pgx.Tx, ownerID string, patch settingsPatch) error {
	if patch.DefaultRoster != nil {
		roster, err := findRosterByName(ctx, tx, ownerID, *patch.DefaultRoster)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update owners set default_roster_id = $2 where id = $1`, ownerID, roster.ID); err != nil {
			return err
		}
	}

	if patch.DraftExpiry != nil {
		hours, err := parseExpiryHours(*patch.DraftExpiry)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update owners set draft_expiry = make_interval(hours => $2) where id = $1`, ownerID, hours)
		if err != nil {
			return err
		}
	}

	if patch.AutoDraft != nil {
		if _, err := tx.Exec(ctx, `update owners set auto_draft = $2 where id = $1`, ownerID, *patch.AutoDraft); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, ownerID string) error {
	resp, err := loadSettings(r.Context(), s.db, ownerID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request, ownerID string) error {
	var patch settingsPatch
	if err := decodeJSON(w, r, &patch); err != nil {
		return err
	}

	var resp settingsResponse
	err := s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		if err := applySettings(r.Context(), tx, ownerID, patch); err != nil {
			return err
		}
		var err error
		resp, err = loadSettings(r.Context(), tx, ownerID)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

type linkResponse struct {
	RepoHash string `json:"repoHash"`
	Repo     string `json:"repo"`
	Roster   string `json:"roster"`
}

const linkColumns = `l.repo_hash, l.repo, r.name`

func scanLink(row pgx.Row) (linkResponse, error) {
	var link linkResponse
	var repo []byte
	err := row.Scan(&link.RepoHash, &repo, &link.Roster)
	if repo != nil {
		link.Repo = *encodeCiphertext(repo)
	}
	return link, err
}

var errLinkNotFound = &apiError{http.StatusNotFound, "link_not_found", "That project isn't linked to a roster.", nil}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request, ownerID string) error {
	rows, err := s.db.Query(r.Context(), `
		select `+linkColumns+` from project_links l join rosters r on r.id = l.roster_id
		where l.owner_id = $1 order by r.name, l.repo_hash`, ownerID)
	if err != nil {
		return err
	}
	links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (linkResponse, error) { return scanLink(row) })
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": append([]linkResponse{}, links...)})
	return nil
}

func (s *Server) handleGetLink(w http.ResponseWriter, r *http.Request, ownerID string) error {
	repoHash := r.PathValue("repoHash")
	if err := validateRepoHash(repoHash); err != nil {
		return err
	}
	link, err := scanLink(s.db.QueryRow(r.Context(), `
		select `+linkColumns+` from project_links l join rosters r on r.id = l.roster_id
		where l.owner_id = $1 and l.repo_hash = $2`, ownerID, repoHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return errLinkNotFound
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, link)
	return nil
}

// handlePutLink links a project (by repo fingerprint) to a roster, replacing any existing link.
func (s *Server) handlePutLink(w http.ResponseWriter, r *http.Request, ownerID string) error {
	repoHash := r.PathValue("repoHash")
	if err := validateRepoHash(repoHash); err != nil {
		return err
	}
	var req struct {
		Repo   *string `json:"repo"`
		Roster string  `json:"roster"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.Repo == nil {
		return invalidRequest("repo is required.")
	}
	repo, err := decodeCiphertext("repo", req.Repo)
	if err != nil {
		return err
	}

	resp := linkResponse{RepoHash: repoHash, Repo: *req.Repo}
	err = s.withOwnerTx(r.Context(), ownerID, func(tx pgx.Tx) error {
		roster, err := findRosterByName(r.Context(), tx, ownerID, req.Roster)
		if err != nil {
			return err
		}
		resp.Roster = roster.Name
		return upsertLink(r.Context(), tx, ownerID, repoHash, repo, roster.ID)
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func upsertLink(ctx context.Context, tx pgx.Tx, ownerID, repoHash string, repo []byte, rosterID string) error {
	_, err := tx.Exec(ctx, `
		insert into project_links (owner_id, repo_hash, repo, roster_id) values ($1, $2, $3, $4)
		on conflict (owner_id, repo_hash) do update set repo = excluded.repo, roster_id = excluded.roster_id`,
		ownerID, repoHash, repo, rosterID)
	return err
}

func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request, ownerID string) error {
	repoHash := r.PathValue("repoHash")
	if err := validateRepoHash(repoHash); err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `delete from project_links where owner_id = $1 and repo_hash = $2`, ownerID, repoHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errLinkNotFound
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
