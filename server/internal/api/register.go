package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

type registerRequest struct {
	AuthToken string         `json:"authToken"`
	Import    *importRequest `json:"import"`
}

// importRequest is a whole local setup, uploaded by `rostr config cloud on`.
type importRequest struct {
	Rosters  []importRoster `json:"rosters"`
	Settings settingsPatch  `json:"settings"`
	Links    []importLink   `json:"links"`
	Drafts   []importDraft  `json:"drafts"`
}

type importRoster struct {
	Name string `json:"name"`
	cast
	CreatedAt *time.Time `json:"createdAt"`
}

type importLink struct {
	RepoHash string  `json:"repoHash"`
	Repo     *string `json:"repo"`
	Roster   string  `json:"roster"`
}

type importDraft struct {
	Roster     string     `json:"roster"`
	Name       string     `json:"name"`
	Emoji      string     `json:"emoji"`
	Copy       int        `json:"copy"`
	Session    string     `json:"session"`
	RepoHash   *string    `json:"repoHash"`
	Repo       *string    `json:"repo"`
	Task       *string    `json:"task"`
	DraftedAt  *time.Time `json:"draftedAt"`
	LastSeenAt *time.Time `json:"lastSeenAt"`
}

// handleRegister creates an owner for a new auth token and, with an import, uploads their whole setup in the same
// transaction, so a failed import leaves nothing behind. Rate-limited per client IP.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if allowed, retryAfter := s.registrationLimiter.allow(clientIP(r)); !allowed {
		writeRateLimited(w, retryAfter)
		return
	}

	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	token, ok := decodeAuthToken(req.AuthToken)
	if !ok {
		writeError(w, invalidRequest("authToken must be 32 bytes, base64url without padding."))
		return
	}

	var resp settingsResponse
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		var ownerID string
		err := tx.QueryRow(r.Context(), `
			insert into owners (auth_token_hash) values ($1)
			on conflict (auth_token_hash) do nothing
			returning id`, hashAuthToken(token)).Scan(&ownerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &apiError{http.StatusConflict, "already_registered", "This auth token is already registered.", nil}
		}
		if err != nil {
			return err
		}

		if req.Import != nil {
			if err := importSetup(r.Context(), tx, ownerID, *req.Import); err != nil {
				return err
			}
		}
		resp, err = loadSettings(r.Context(), tx, ownerID)
		return err
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func importSetup(ctx context.Context, tx pgx.Tx, ownerID string, imp importRequest) error {
	// Rosters, then the settings that can name one as the default
	rosterIDs := map[string]string{}
	for _, ir := range imp.Rosters {
		name, err := validateName("Roster name", ir.Name)
		if err != nil {
			return err
		}
		c, err := validateCast(ir.cast)
		if err != nil {
			return err
		}
		roster, err := insertRoster(ctx, tx, ownerID, name, c, ir.CreatedAt)
		if err != nil {
			return err
		}
		rosterIDs[nameKey(name)] = roster.ID
	}
	if err := applySettings(ctx, tx, ownerID, imp.Settings); err != nil {
		return err
	}
	if err := promoteOldestRosterToDefault(ctx, tx, ownerID); err != nil {
		return err
	}

	rosterID := func(name string) (string, error) {
		id, ok := rosterIDs[nameKey(name)]
		if !ok {
			return "", invalidRequest(fmt.Sprintf("Roster %q isn't in the import.", name))
		}
		return id, nil
	}

	// Project links
	for _, link := range imp.Links {
		if err := validateRepoHash(link.RepoHash); err != nil {
			return err
		}
		if link.Repo == nil {
			return invalidRequest("Each link needs a repo.")
		}
		repo, err := decodeCiphertext("repo", link.Repo)
		if err != nil {
			return err
		}
		id, err := rosterID(link.Roster)
		if err != nil {
			return err
		}
		if err := upsertLink(ctx, tx, ownerID, link.RepoHash, repo, id); err != nil {
			return err
		}
	}

	// Active drafts. Their members needn't be in the current cast, since a replaced cast keeps its drafts.
	for _, d := range imp.Drafts {
		id, err := rosterID(d.Roster)
		if err != nil {
			return err
		}
		name, err := validateName("Member name", d.Name)
		if err != nil {
			return err
		}
		if d.Emoji == "" || len(d.Emoji) > maxEmojiBytes {
			return invalidRequest(fmt.Sprintf("Draft %q needs an emoji.", name))
		}
		if d.Copy == 0 {
			d.Copy = 1
		}
		if d.Copy < 1 {
			return invalidRequest("copy must be 1 or more.")
		}
		if err := validateSession(d.Session); err != nil {
			return err
		}
		repoHash, repo, err := repoInput(d.RepoHash, d.Repo)
		if err != nil {
			return err
		}
		task, err := decodeCiphertext("task", d.Task)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			insert into drafts (owner_id, roster_id, member_name, member_emoji, copy, session_id, repo_hash, repo, task,
			                    drafted_at, last_seen_at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, coalesce($10, now()), coalesce($11, now()))`,
			ownerID, id, name, d.Emoji, d.Copy, d.Session, repoHash, repo, task, d.DraftedAt, d.LastSeenAt)
		if err != nil {
			return err
		}
	}
	return nil
}
