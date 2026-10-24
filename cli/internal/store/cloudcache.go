package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Cloud mode's local cache is sessions/<session>.json: a session's own draft, which never changes while the draft is
// active, so later prompts read it instead of calling. Server calls that change the draft update it. A released session's
// file is marked released, and the files are pruned after ReleasedRetention.

// cachedDraftTrust is how long a cached draft is answered from the cache: the keep-alive interval, after which the
// server is asked again.
const cachedDraftTrust = time.Hour

type cloudCache struct {
	dir string
}

type cachedSession struct {
	Draft    Draft `json:"draft"`
	Released bool  `json:"released,omitempty"`
	// Announced is the display name the agent was told, or "" while it hasn't been told: a session drafted when it
	// starts learns its name on its first prompt
	Announced string    `json:"announced"`
	At        time.Time `json:"at"`
}

func (cc *cloudCache) clearAll() {
	_ = os.RemoveAll(cc.dir)
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
		cs.Released, cs.At = true, now
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

// saveDraft records a draft the server returned, keeping what the agent was last told, and prunes old released
// sessions.
func (cc *cloudCache) saveDraft(d Draft, now time.Time) error {
	announced := ""
	if previous := cc.session(d.SessionID); previous != nil {
		announced = previous.Announced
	}
	cc.pruneReleasedSessions(now)
	return cc.saveSession(cachedSession{Draft: d, Announced: announced, At: now})
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
	// A temp file of its own, so two processes writing the same file can't interleave
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
