// Package keepalive keeps the per-session state the hooks need between prompts in files named after the session, so
// reading it costs no storage call (or API call, in cloud mode): the throttle that bumps a draft at most once an hour,
// and when the session's last turn ended.
package keepalive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Interval is the least time between two bumps for one session.
const Interval = time.Hour

// Due reports whether the session hasn't been bumped within Interval of now.
func Due(dir, sessionID string, now time.Time) bool {
	info, err := os.Stat(file(dir, sessionID))
	if err != nil {
		return true
	}
	return now.Sub(info.ModTime()) >= Interval
}

// Mark records a bump at now.
func Mark(dir, sessionID string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := file(dir, sessionID)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return err
	}
	return os.Chtimes(path, now, now)
}

// Forget removes the session's file, once the session has ended. It works for both kinds of file.
func Forget(dir, sessionID string) {
	_ = os.Remove(file(dir, sessionID))
}

// TurnEnd is what the Stop hook remembers about a session's last turn: when it ended, and the session's name and task
// then, so the next prompt knows whether the session rested and can tell it when its name was taken meanwhile.
type TurnEnd struct {
	At      time.Time `json:"at"`
	Display string    `json:"display"`
	Task    string    `json:"task"`
}

// SaveTurnEnd records the session's last turn end.
func SaveTurnEnd(dir, sessionID string, end TurnEnd) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(end)
	if err != nil {
		return err
	}
	return os.WriteFile(file(dir, sessionID), data, 0o600)
}

// LoadTurnEnd returns the session's last turn end, if one was recorded.
func LoadTurnEnd(dir, sessionID string) (TurnEnd, bool) {
	var end TurnEnd
	data, err := os.ReadFile(file(dir, sessionID))
	if err != nil || json.Unmarshal(data, &end) != nil {
		return TurnEnd{}, false
	}
	return end, true
}

// ForgetTurnEnd removes the session's record of its last turn end, if it's still the one that ended at at, so a report
// that finishes late can't remove a newer turn's end.
func ForgetTurnEnd(dir, sessionID string, at time.Time) {
	if end, ok := LoadTurnEnd(dir, sessionID); ok && end.At.Equal(at) {
		Forget(dir, sessionID)
	}
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func file(dir, sessionID string) string {
	return filepath.Join(dir, unsafeChars.ReplaceAllString(sessionID, "_"))
}
