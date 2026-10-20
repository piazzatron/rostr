// Package project works out which project a directory belongs to: its git `origin` as "owner/repo", or the repo's
// main folder name when it has no origin.
package project

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// Identify returns the project for dir. ok is false outside a git repo (or when git isn't installed).
func Identify(dir string) (repo string, ok bool) {
	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return "", false
	}

	if origin, err := git(dir, "remote", "get-url", "origin"); err == nil && origin != "" {
		return NormalizeOrigin(origin), true
	}

	// No origin: use the main checkout's folder name, which every worktree shares through the common git dir
	commonDir, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", false
	}
	return strings.ToLower(filepath.Base(filepath.Dir(commonDir))), true
}

// NormalizeOrigin turns a remote URL into "owner/repo": the last two segments of its path, lowercased, without
// ".git". SSH (scp-style and ssh://), HTTPS, and SSH host aliases all give the same result. A value with one
// segment, like a bare folder name, is returned lowercased.
func NormalizeOrigin(origin string) string {
	s := strings.ToLower(strings.TrimSpace(origin))

	// Drop the scheme and host, keeping only the path
	if _, rest, found := strings.Cut(s, "://"); found {
		_, s, _ = strings.Cut(rest, "/")
	} else if host, path, found := strings.Cut(s, ":"); found && !strings.Contains(host, "/") {
		s = path
	}

	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	var segments []string
	for _, segment := range strings.Split(s, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) > 2 {
		segments = segments[len(segments)-2:]
	}
	return strings.Join(segments, "/")
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}
