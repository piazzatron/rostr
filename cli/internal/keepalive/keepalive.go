// Package keepalive throttles the prompt hook's keep-alive bumps to at most one an hour per session. The last bump is
// the modification time of a file named after the session, so it costs no storage call (or API call, in cloud mode).
package keepalive

import (
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

// Forget removes the session's file, once the session has ended.
func Forget(dir, sessionID string) {
	_ = os.Remove(file(dir, sessionID))
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func file(dir, sessionID string) string {
	return filepath.Join(dir, unsafeChars.ReplaceAllString(sessionID, "_"))
}
