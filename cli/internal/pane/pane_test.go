package pane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTmuxLabelsThePaneAndRenamesALoneWindow(t *testing.T) {
	server := newTmuxServer(t)
	first := server.attach(t)
	ctx := context.Background()

	// A window with one pane and an automatic name is renamed, and the pane tagged
	Label(ctx, Claude, "🧙 Gandalf")
	server.expect(t, first, "#{@rostr}|#{window_name}|#{automatic-rename}|#{@rostr_renamed}", "🧙 Gandalf|🧙 Gandalf|0|1")

	// Labelling again (resume with a new name) renames the window rostr already renamed
	Label(ctx, Claude, "💍 Frodo")
	server.expect(t, first, "#{@rostr}|#{window_name}", "💍 Frodo|💍 Frodo")

	// Session end clears the tag and hands the name back to tmux
	Clear(ctx, Claude)
	server.expect(t, first, "#{@rostr}|#{automatic-rename}|#{@rostr_renamed}", "|1|")

	// A window the user named keeps its name
	server.tmux(t, "rename-window", "-t", first, "mine")
	Label(ctx, Claude, "🧙 Gandalf")
	server.expect(t, first, "#{@rostr}|#{window_name}|#{@rostr_renamed}", "🧙 Gandalf|mine|")
	Clear(ctx, Claude)
	server.expect(t, first, "#{@rostr}|#{window_name}|#{automatic-rename}", "|mine|0")

	// A window shared with another pane isn't renamed; only the session's own pane is tagged
	server.tmux(t, "new-window", "sh")
	second := server.tmux(t, "split-window", "-P", "-F", "#{pane_id}", "sh")
	t.Setenv("TMUX_PANE", second)
	Label(ctx, Claude, "🌱 Samwise")
	server.expect(t, second, "#{@rostr}|#{automatic-rename}|#{@rostr_renamed}", "🌱 Samwise|1|")
	server.expect(t, first, "#{@rostr}", "")
}

func TestTmuxIsLeftAloneInsideHerdrOrOutsideTmux(t *testing.T) {
	server := newTmuxServer(t)
	paneID := server.attach(t)
	installFakeHerdr(t)

	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	Label(context.Background(), Claude, "🧙 Gandalf")
	server.expect(t, paneID, "#{@rostr}", "")

	t.Setenv("HERDR_ENV", "")
	t.Setenv("TMUX_PANE", "")
	Label(context.Background(), Claude, "🧙 Gandalf")
	server.expect(t, paneID, "#{@rostr}", "")
}

func TestHerdrReportsAndClearsTheDisplayName(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p2")
	calls := installFakeHerdr(t)

	Label(context.Background(), Claude, "🧙 Gandalf")
	Clear(context.Background(), Claude)
	want := "pane report-metadata w1:p2 --source rostr --agent claude --display-agent 🧙 Gandalf\n" +
		"pane report-metadata w1:p2 --source rostr --agent claude --clear-display-agent\n"
	if got, _ := os.ReadFile(calls); string(got) != want {
		t.Fatalf("herdr calls:\n%s\nwant:\n%s", got, want)
	}

	// HERDR_BIN_PATH wins over PATH, and a failing herdr is ignored
	failing := filepath.Join(t.TempDir(), "herdr")
	os.WriteFile(failing, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("HERDR_BIN_PATH", failing)
	Label(context.Background(), Claude, "🧙 Gandalf")
	if got, _ := os.ReadFile(calls); string(got) != want {
		t.Fatalf("PATH's herdr ran despite HERDR_BIN_PATH:\n%s", got)
	}
}

func TestCodexLabelsOnlyFromTheTUIsOwnProcess(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p2")
	calls := installFakeHerdr(t)

	// The test isn't run by Codex, so the pane in its environment isn't trusted
	Label(context.Background(), Codex, "🧙 Gandalf")
	Clear(context.Background(), Codex)
	if got, _ := os.ReadFile(calls); len(got) != 0 {
		t.Fatalf("labeled a pane outside Codex:\n%s", got)
	}

	herdrLabel(context.Background(), Codex, "🧙 Gandalf")
	want := "pane report-metadata w1:p2 --source rostr --agent codex --display-agent 🧙 Gandalf\n"
	if got, _ := os.ReadFile(calls); string(got) != want {
		t.Fatalf("herdr calls:\n%s\nwant:\n%s", got, want)
	}
}

func TestRanByCodexTUI(t *testing.T) {
	// Process trees seen with Codex 0.164, from the hook's parent up
	tui := []string{"/bin/sh -c rostr hook codex prompt", "/opt/node/vendor/bin/codex --no-daemon",
		"node /opt/node/bin/codex --no-daemon", "-zsh"}
	daemon := []string{"/bin/sh -c rostr hook codex prompt",
		"/home/me/.codex/packages/app-server-daemon/releases/0.164.0/bin/codex app-server --listen unix:// --managed-daemon",
		"/opt/node/vendor/bin/codex", "node /opt/node/bin/codex", "-zsh"}
	sandboxed := []string{"/bin/bash -lc rostr hook codex draft", "codex-linux-sandbox --sandbox-policy x", "codex"}
	for _, tc := range []struct {
		name string
		tree []string
		want bool
	}{
		{"TUI", tui, true},
		{"shared daemon, started by another pane's TUI", daemon, false},
		{"agent command in the sandbox", sandboxed, true},
		{"not Codex", []string{"/bin/sh -c rostr", "-zsh"}, false},
	} {
		info := func(pid int) (int, []string) {
			if pid >= len(tc.tree) {
				return 0, nil
			}
			return pid + 1, strings.Fields(tc.tree[pid])
		}
		if got := ranByCodexTUI(0, info); got != tc.want {
			t.Errorf("%s: ranByCodexTUI = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// tmuxServer is a tmux server of its own (tmux -L), so tests never touch the user's tmux.
type tmuxServer struct{ name string }

// newTmuxServer starts an isolated tmux server with one window, and kills it when the test ends. It skips the test
// when tmux isn't installed.
func newTmuxServer(t *testing.T) *tmuxServer {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux isn't installed")
	}
	suffix := make([]byte, 6)
	rand.Read(suffix)
	s := &tmuxServer{name: "rostr-test-" + hex.EncodeToString(suffix)}
	s.tmux(t, "-f", os.DevNull, "new-session", "-d", "-x", "80", "-y", "24", "sh")
	t.Cleanup(func() { exec.Command("tmux", "-L", s.name, "kill-server").Run() })
	return s
}

// attach points TMUX and TMUX_PANE at the server's first pane, as if the test ran inside it, and returns its ID.
func (s *tmuxServer) attach(t *testing.T) string {
	t.Helper()
	paneID := s.tmux(t, "display-message", "-p", "-t", "0:0.0", "#{pane_id}")
	t.Setenv("TMUX", s.tmux(t, "display-message", "-p", "#{socket_path}")+",0,0")
	t.Setenv("TMUX_PANE", paneID)
	t.Setenv("HERDR_ENV", "")
	return paneID
}

func (s *tmuxServer) tmux(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", append([]string{"-L", s.name}, args...)...).Output()
	if err != nil {
		t.Fatalf("tmux %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func (s *tmuxServer) expect(t *testing.T, paneID, format, want string) {
	t.Helper()
	if got := s.tmux(t, "display-message", "-p", "-t", paneID, format); got != want {
		t.Fatalf("%s: got %q, want %q", format, got, want)
	}
}

// installFakeHerdr puts a herdr on PATH that records its arguments, one call per line, and returns the record's path.
func installFakeHerdr(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}
