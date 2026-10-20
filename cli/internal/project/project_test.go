package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:Piazzatron/rostr.git":                "piazzatron/rostr",
		"https://github.com/piazzatron/rostr":                "piazzatron/rostr",
		"https://github.com/piazzatron/rostr/":               "piazzatron/rostr",
		"https://github.com/Piazzatron/Rostr.GIT":            "piazzatron/rostr",
		"git@github-work:piazzatron/rostr.git":               "piazzatron/rostr", // SSH host alias
		"work-gh:piazzatron/rostr":                           "piazzatron/rostr", // alias without a user
		"ssh://git@github.com:22/piazzatron/rostr.git":       "piazzatron/rostr",
		"https://user:token@github.com/piazzatron/rostr.git": "piazzatron/rostr",
		"https://gitlab.com/group/subgroup/rostr.git":        "subgroup/rostr",
		"http://proxy.local:8080/git/piazzatron/rostr":       "piazzatron/rostr",
		"/srv/git/piazzatron/rostr.git":                      "piazzatron/rostr",
		"file:///srv/git/piazzatron/rostr.git":               "piazzatron/rostr",
		"Rostr":                                              "rostr",
		"piazzatron/rostr":                                   "piazzatron/rostr",
	} {
		if got := NormalizeOrigin(in); got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentifyUsesOrigin(t *testing.T) {
	for origin, want := range map[string]string{
		"git@github.com:Piazzatron/Rostr.git":  "piazzatron/rostr",
		"https://github.com/piazzatron/rostr":  "piazzatron/rostr",
		"git@github-personal:piazzatron/rostr": "piazzatron/rostr",
	} {
		dir := gitInit(t, "checkout-folder")
		run(t, dir, "git", "remote", "add", "origin", origin)

		// From a subdirectory too
		sub := filepath.Join(dir, "src", "deep")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if got, ok := Identify(sub); !ok || got != want {
			t.Errorf("origin %q: got %q, %v; want %q", origin, got, ok, want)
		}
	}
}

func TestIdentifyWithoutOriginUsesMainFolderName(t *testing.T) {
	dir := gitInit(t, "My-Project")
	run(t, dir, "git", "remote", "add", "upstream", "git@github.com:someone/else.git")
	if got, ok := Identify(dir); !ok || got != "my-project" {
		t.Fatalf("got %q, %v", got, ok)
	}

	// A worktree in a differently named folder is the same project
	run(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "feature-branch")
	run(t, dir, "git", "worktree", "add", "-b", "feature", worktree)
	if got, ok := Identify(worktree); !ok || got != "my-project" {
		t.Fatalf("worktree: got %q, %v", got, ok)
	}
}

func TestIdentifyOutsideGit(t *testing.T) {
	if got, ok := Identify(t.TempDir()); ok {
		t.Fatalf("got %q outside git", got)
	}
}

func gitInit(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "init", "-q")
	return dir
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// TestMain isolates git from the developer's config, whose url.insteadOf rules would rewrite origins.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "rostr-project-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
