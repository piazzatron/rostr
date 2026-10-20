package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piazzatron/rostr/cli/internal/crypto"
	"github.com/piazzatron/rostr/cli/internal/roster"
)

// CloudTimeout is how long any one call to the rostr API may take.
const CloudTimeout = 3 * time.Second

// Cloud is the cloud-mode Store: every read and write is a call to the rostr API (docs/api.md). Tasks and repo names
// are encrypted before they're sent and decrypted when read; repos are looked up by their fingerprint.
type Cloud struct {
	baseURL string
	keys    crypto.DerivedKeys
	http    *http.Client
}

// NewCloud returns a Cloud that calls the API at baseURL with the keys derived from key.
func NewCloud(baseURL string, key crypto.Key) (*Cloud, error) {
	keys, err := key.Derive()
	if err != nil {
		return nil, err
	}
	return &Cloud{
		baseURL: strings.TrimRight(baseURL, "/"),
		keys:    keys,
		http:    &http.Client{Timeout: CloudTimeout},
	}, nil
}

// APIError is an error response from the rostr API. It wraps ErrNotFound, ErrNameTaken, or ErrNoRosters for the codes
// that mean those.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	switch e.Code {
	case "unauthorized":
		return "the rostr API doesn't recognize your rostr key"
	case "rate_limited":
		return "the rostr API is rate-limiting this key; try again in a minute"
	case "no_rosters":
		return ErrNoRosters.Error()
	}
	return e.Message
}

func (e *APIError) Unwrap() error {
	return apiErrorKinds[e.Code]
}

var apiErrorKinds = map[string]error{
	"roster_not_found": ErrNotFound,
	"link_not_found":   ErrNotFound,
	"not_drafted":      ErrNotFound,
	"roster_exists":    ErrNameTaken,
	"no_rosters":       ErrNoRosters,
}

func (c *Cloud) Close() error {
	return nil
}

// Register creates the cloud owner for this key, uploading setup in the same request (docs/api.md, POST
// /v1/register). If it fails, nothing is created on the server.
func (c *Cloud) Register(ctx context.Context, setup Setup) error {
	imp := map[string]any{}

	// Local times are whole seconds, so rosters made in the same second are nudged apart to keep their order
	rosters := make([]map[string]any, len(setup.Rosters))
	var previous time.Time
	for i, r := range setup.Rosters {
		rosters[i] = map[string]any{"name": r.Name, "emojiMode": r.EmojiMode, "aTeam": r.ATeam, "bTeam": nonNil(r.BTeam)}
		if r.CreatedAt.IsZero() {
			continue
		}
		createdAt := r.CreatedAt
		if !createdAt.After(previous) {
			createdAt = previous.Add(time.Millisecond)
		}
		rosters[i]["createdAt"], previous = createdAt, createdAt
	}
	imp["rosters"] = rosters

	settings := map[string]any{"autoDraft": setup.Settings.AutoDraft}
	if setup.Settings.DefaultRoster != "" {
		settings["defaultRoster"] = setup.Settings.DefaultRoster
	}
	if setup.Settings.Expiry != "" {
		settings["draftExpiry"] = setup.Settings.Expiry
	}
	imp["settings"] = settings

	// Links and drafts carry the repo as a fingerprint plus ciphertext, and the task as ciphertext
	links := make([]map[string]any, len(setup.Links))
	for i, l := range setup.Links {
		link := map[string]any{"roster": l.Roster}
		if err := c.addRepo(link, l.Repo); err != nil {
			return err
		}
		links[i] = link
	}
	imp["links"] = links

	drafts := make([]map[string]any, len(setup.Drafts))
	for i, d := range setup.Drafts {
		draft := map[string]any{
			"roster": d.Roster, "name": d.Member, "emoji": d.Emoji, "copy": d.Copy, "session": d.SessionID,
			"draftedAt": d.DraftedAt, "lastSeenAt": d.LastSeenAt,
		}
		if err := c.addRepo(draft, d.Repo); err != nil {
			return err
		}
		if err := c.addTask(draft, d.Task); err != nil {
			return err
		}
		drafts[i] = draft
	}
	imp["drafts"] = drafts

	return c.call(ctx, "POST", "/v1/register", map[string]any{"authToken": c.keys.AuthToken(), "import": imp}, nil)
}

// Rosters

func (c *Cloud) Rosters(ctx context.Context) ([]Roster, error) {
	// The listing gives every roster's name, oldest first; each roster's cast comes from its own endpoint
	var listing apiListing
	if err := c.call(ctx, "GET", "/v1/rosters", nil, &listing); err != nil {
		return nil, err
	}
	out := make([]Roster, len(listing.Rosters))
	for i, r := range listing.Rosters {
		full, err := c.Roster(ctx, r.Name)
		if err != nil {
			return nil, err
		}
		out[i] = full
	}
	return out, nil
}

func (c *Cloud) Roster(ctx context.Context, name string) (Roster, error) {
	r, err := c.roster(ctx, name)
	return r.toRoster(), err
}

func (c *Cloud) CreateRoster(ctx context.Context, r Roster) (Roster, error) {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return Roster{}, errors.New("a roster needs a name")
	}
	if err := roster.ValidateCast(r.ATeam, r.BTeam, r.EmojiMode); err != nil {
		return Roster{}, err
	}
	var created apiRoster
	err := c.call(ctx, "POST", "/v1/rosters", map[string]any{
		"name": r.Name, "emojiMode": r.EmojiMode, "aTeam": r.ATeam, "bTeam": nonNil(r.BTeam),
	}, &created)
	return created.toRoster(), err
}

func (c *Cloud) ReplaceCast(ctx context.Context, rosterName string, aTeam, bTeam []roster.Member) error {
	r, err := c.roster(ctx, rosterName)
	if err != nil {
		return err
	}
	if err := roster.ValidateCast(aTeam, bTeam, r.EmojiMode); err != nil {
		return err
	}
	return c.call(ctx, "PUT", "/v1/rosters/"+url.PathEscape(r.Name)+"/cast", castBody(r.EmojiMode, aTeam, bTeam), nil)
}

func (c *Cloud) EditRoster(ctx context.Context, rosterName string, mode roster.EmojiMode, aTeam, bTeam []roster.Member) error {
	if err := roster.ValidateCast(aTeam, bTeam, mode); err != nil {
		return err
	}
	return c.call(ctx, "PUT", "/v1/rosters/"+url.PathEscape(rosterName), castBody(mode, aTeam, bTeam), nil)
}

func (c *Cloud) SetEmojiMode(ctx context.Context, rosterName string, mode roster.EmojiMode) error {
	r, err := c.roster(ctx, rosterName)
	if err != nil {
		return err
	}
	if err := roster.ValidateCast(r.ATeam, r.BTeam, mode); err != nil {
		return err
	}
	return c.call(ctx, "PUT", "/v1/rosters/"+url.PathEscape(r.Name), castBody(mode, r.ATeam, r.BTeam), nil)
}

func (c *Cloud) Usage(ctx context.Context, rosterName string) (Usage, error) {
	r, err := c.roster(ctx, rosterName)
	return Usage{Drafted: r.ActiveDrafts, Links: r.LinkedProjects}, err
}

func (c *Cloud) Disband(ctx context.Context, rosterName string) (DisbandResult, error) {
	// The delete only reports a new default, so whether this was the default comes from the roster first
	r, err := c.roster(ctx, rosterName)
	if err != nil {
		return DisbandResult{}, err
	}
	var resp struct {
		NewDefault   *string `json:"newDefault"`
		OwnerDeleted bool    `json:"ownerDeleted"`
	}
	if err := c.call(ctx, "DELETE", "/v1/rosters/"+url.PathEscape(r.Name), nil, &resp); err != nil {
		return DisbandResult{}, err
	}
	return DisbandResult{WasDefault: r.IsDefault, NewDefault: deref(resp.NewDefault), CloudSetupDeleted: resp.OwnerDeleted}, nil
}

// Project links

func (c *Cloud) ProjectLink(ctx context.Context, repo string) (string, error) {
	var link struct {
		Roster string `json:"roster"`
	}
	err := c.call(ctx, "GET", "/v1/links/"+c.keys.Fingerprint(repo), nil, &link)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return link.Roster, err
}

func (c *Cloud) LinkProject(ctx context.Context, repo, rosterName string) error {
	encrypted, err := c.keys.Encrypt(repo)
	if err != nil {
		return err
	}
	return c.call(ctx, "PUT", "/v1/links/"+c.keys.Fingerprint(repo),
		map[string]any{"repo": encrypted, "roster": rosterName}, nil)
}

func (c *Cloud) UnlinkProject(ctx context.Context, repo string) (bool, error) {
	err := c.call(ctx, "DELETE", "/v1/links/"+c.keys.Fingerprint(repo), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Settings

func (c *Cloud) Settings(ctx context.Context) (Settings, error) {
	var resp apiSettings
	err := c.call(ctx, "GET", "/v1/settings", nil, &resp)
	return resp.toSettings(), err
}

func (c *Cloud) SetDefaultRoster(ctx context.Context, rosterName string) error {
	return c.call(ctx, "PATCH", "/v1/settings", map[string]any{"defaultRoster": rosterName}, nil)
}

func (c *Cloud) SetAutoDraft(ctx context.Context, on bool) error {
	return c.call(ctx, "PATCH", "/v1/settings", map[string]any{"autoDraft": on}, nil)
}

func (c *Cloud) SetExpiry(ctx context.Context, expiry string) error {
	if _, err := ParseExpiry(expiry); err != nil {
		return err
	}
	return c.call(ctx, "PATCH", "/v1/settings",
		map[string]any{"draftExpiry": strings.ToLower(strings.TrimSpace(expiry))}, nil)
}

// Drafts

func (c *Cloud) Draft(ctx context.Context, req DraftRequest) (Draft, error) {
	if req.SessionID == "" {
		return Draft{}, errors.New("drafting needs a session ID")
	}
	body := map[string]any{"session": req.SessionID}
	if req.Roster != "" {
		body["roster"] = req.Roster
	}
	if err := c.addRepo(body, req.Repo); err != nil {
		return Draft{}, err
	}
	if err := c.addTask(body, req.Task); err != nil {
		return Draft{}, err
	}
	return c.draftCall(ctx, "POST", "/v1/agents/draft", body)
}

func (c *Cloud) Reclaim(ctx context.Context, sessionID string) (*Draft, error) {
	return c.optionalDraft(c.draftCall(ctx, "POST", "/v1/agents/keepalive", map[string]any{"session": sessionID}))
}

func (c *Cloud) SessionDraft(ctx context.Context, sessionID string) (*Draft, error) {
	return c.optionalDraft(c.draftCall(ctx, "GET", "/v1/sessions/"+url.PathEscape(sessionID), nil))
}

// ActiveDrafts reads the public listing, which has no session IDs or last-seen times, so those are left empty.
func (c *Cloud) ActiveDrafts(ctx context.Context) ([]Draft, error) {
	var listing apiListing
	if err := c.call(ctx, "GET", "/v1/rosters", nil, &listing); err != nil {
		return nil, err
	}
	var out []Draft
	for _, r := range listing.Rosters {
		for _, d := range r.Drafted {
			member, copy := splitAgentName(d.Name)
			draft := Draft{
				Roster: r.Name, EmojiMode: roster.EmojiMode(r.EmojiMode), Member: member, Emoji: d.Emoji, Copy: copy,
				Team: strings.ToUpper(deref(d.Team)), DraftedAt: d.DraftedAt,
			}
			var err error
			if draft.Task, err = c.decrypt(d.Task); err != nil {
				return nil, err
			}
			if draft.Repo, err = c.decrypt(d.Repo); err != nil {
				return nil, err
			}
			out = append(out, draft)
		}
	}
	return out, nil
}

func (c *Cloud) Release(ctx context.Context, sessionID string) (*Draft, error) {
	return c.optionalDraft(c.draftCall(ctx, "POST", "/v1/agents/release", map[string]any{"session": sessionID}))
}

func (c *Cloud) ReleaseAgent(ctx context.Context, agent AgentKey) (Draft, error) {
	return c.draftCall(ctx, "POST", "/v1/agents/release", agentBody(agent))
}

func (c *Cloud) SetTask(ctx context.Context, agent AgentKey, task string) (Draft, error) {
	body := agentBody(agent)
	if err := c.addTask(body, task); err != nil {
		return Draft{}, err
	}
	return c.draftCall(ctx, "POST", "/v1/agents/task", body)
}

// call sends one request to the API, decoding a successful response into out (if not nil) and an error response into
// an *APIError. Every request but registration carries the auth token.
func (c *Cloud) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if path != "/v1/register" {
		req.Header.Set("Authorization", "Bearer "+c.keys.AuthToken())
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach the rostr API at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&payload) != nil || payload.Error.Code == "" {
			return &APIError{Status: resp.StatusCode, Code: "unknown",
				Message: fmt.Sprintf("the rostr API answered %s", resp.Status)}
		}
		return &APIError{Status: resp.StatusCode, Code: payload.Error.Code, Message: payload.Error.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("reading the rostr API's response: %w", err)
	}
	return nil
}

func (c *Cloud) roster(ctx context.Context, name string) (apiRoster, error) {
	var r apiRoster
	err := c.call(ctx, "GET", "/v1/rosters/"+url.PathEscape(name), nil, &r)
	return r, err
}

func (c *Cloud) draftCall(ctx context.Context, method, path string, body any) (Draft, error) {
	var d apiDraft
	if err := c.call(ctx, method, path, body, &d); err != nil {
		return Draft{}, err
	}
	task, err := c.decrypt(d.Task)
	if err != nil {
		return Draft{}, err
	}
	repo, err := c.decrypt(d.Repo)
	if err != nil {
		return Draft{}, err
	}
	return Draft{
		Roster: d.Roster, EmojiMode: roster.EmojiMode(d.EmojiMode), Member: d.Name, Emoji: d.Emoji, Copy: d.Copy,
		Team: strings.ToUpper(deref(d.Team)), SessionID: d.Session, Repo: repo, Task: task, DraftedAt: d.DraftedAt,
	}, nil
}

// optionalDraft turns "not drafted" into no draft.
func (c *Cloud) optionalDraft(d Draft, err error) (*Draft, error) {
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// addRepo sets a request's repoHash and encrypted repo, unless repo is empty.
func (c *Cloud) addRepo(body map[string]any, repo string) error {
	if repo == "" {
		return nil
	}
	encrypted, err := c.keys.Encrypt(repo)
	if err != nil {
		return err
	}
	body["repoHash"], body["repo"] = c.keys.Fingerprint(repo), encrypted
	return nil
}

// addTask sets a request's encrypted task, unless task is empty.
func (c *Cloud) addTask(body map[string]any, task string) error {
	if task == "" {
		return nil
	}
	encrypted, err := c.keys.Encrypt(task)
	body["task"] = encrypted
	return err
}

func (c *Cloud) decrypt(value *string) (string, error) {
	if value == nil {
		return "", nil
	}
	return c.keys.Decrypt(*value)
}

// Wire shapes, as in docs/api.md

type apiRoster struct {
	Name           string           `json:"name"`
	EmojiMode      roster.EmojiMode `json:"emojiMode"`
	ATeam          []roster.Member  `json:"aTeam"`
	BTeam          []roster.Member  `json:"bTeam"`
	IsDefault      bool             `json:"isDefault"`
	ActiveDrafts   int              `json:"activeDrafts"`
	LinkedProjects int              `json:"linkedProjects"`
	CreatedAt      time.Time        `json:"createdAt"`
}

func (r apiRoster) toRoster() Roster {
	return Roster{Name: r.Name, EmojiMode: r.EmojiMode, ATeam: r.ATeam, BTeam: nonNil(r.BTeam), CreatedAt: r.CreatedAt}
}

type apiDraft struct {
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

type apiSettings struct {
	DefaultRoster *string `json:"defaultRoster"`
	DraftExpiry   string  `json:"draftExpiry"`
	AutoDraft     bool    `json:"autoDraft"`
}

func (s apiSettings) toSettings() Settings {
	return Settings{DefaultRoster: deref(s.DefaultRoster), AutoDraft: s.AutoDraft, Expiry: s.DraftExpiry}
}

// apiListing is the public GET /v1/rosters.
type apiListing struct {
	Rosters []struct {
		Name      string `json:"name"`
		EmojiMode string `json:"emojiMode"`
		Drafted   []struct {
			Name      string    `json:"name"`
			Emoji     string    `json:"emoji"`
			Team      *string   `json:"team"`
			Task      *string   `json:"task"`
			Repo      *string   `json:"repo"`
			DraftedAt time.Time `json:"draftedAt"`
		} `json:"drafted"`
	} `json:"rosters"`
}

func castBody(mode roster.EmojiMode, aTeam, bTeam []roster.Member) map[string]any {
	return map[string]any{"emojiMode": mode, "aTeam": aTeam, "bTeam": nonNil(bTeam)}
}

// agentBody targets one drafted agent by its name with copy number ("Gandalf 2"), in one roster.
func agentBody(agent AgentKey) map[string]any {
	return map[string]any{"name": roster.AgentName(agent.Member, agent.Copy), "roster": agent.Roster}
}

var copySuffix = regexp.MustCompile(`^(.+) ([2-9]|[1-9][0-9]+)$`)

// splitAgentName splits a listed name like "Gandalf 2" into the member and copy number. A member whose own name ends
// in a number ("Agent 47") reads as a copy, but the parts still join back into the same name, which is all that
// release and task by name send.
func splitAgentName(name string) (string, int) {
	m := copySuffix.FindStringSubmatch(name)
	if m == nil {
		return name, 1
	}
	copy, _ := strconv.Atoi(m[2])
	return m[1], copy
}

func nonNil(members []roster.Member) []roster.Member {
	if members == nil {
		return []roster.Member{}
	}
	return members
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
