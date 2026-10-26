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
	Clear(ctx, Claude, "")
	server.expect(t, first, "#{@rostr}|#{automatic-rename}|#{@rostr_renamed}", "|1|")

	// A window the user named is renamed too
	server.tmux(t, "rename-window", "-t", first, "mine")
	Label(ctx, Claude, "🧙 Gandalf")
	server.expect(t, first, "#{@rostr}|#{window_name}|#{@rostr_renamed}", "🧙 Gandalf|🧙 Gandalf|1")
	Clear(ctx, Claude, "")
	server.expect(t, first, "#{@rostr}|#{automatic-rename}|#{@rostr_renamed}", "|1|")

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
	Clear(context.Background(), Claude, "")
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

func TestCodexDaemonHooksFindTheHerdrPaneByItsTitle(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir()) // no tmux server to find panes in
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p9") // the daemon's, which belongs to another pane
	calls := installFakeHerdr(t)
	list := func(panes string) {
		os.WriteFile(os.Getenv("FAKE_HERDR_PANES"), []byte(`{"id":"cli:pane:list","result":{"panes":[`+panes+`],"type":"pane_list"}}`), 0o644)
	}
	expect := func(want string) {
		t.Helper()
		got, _ := os.ReadFile(calls)
		os.Remove(calls)
		if string(got) != want {
			t.Fatalf("herdr calls:\n%s\nwant:\n%s", got, want)
		}
	}

	// The test isn't run by Codex, so it labels the Codex pane whose title shows the name: not a Claude pane, and not
	// a name that only starts with it
	list(`{"pane_id":"w1:p1","agent":"codex","terminal_title":"🧙 Gandalf · Fix it | proj","agent_status":"idle"},
		{"pane_id":"w1:p2","agent":"claude","terminal_title":"🧙 Gandalf · Fix it","agent_status":"idle"},
		{"pane_id":"w1:p3","agent":"codex","terminal_title":"⠋ 🧙 Gandalfo | proj","agent_status":"working"},
		{"pane_id":"w1:p9","agent":"codex","terminal_title":"proj","agent_status":"idle"}`)
	Label(context.Background(), Codex, "🧙 Gandalf")
	expect("pane list\npane report-metadata w1:p1 --source rostr --agent codex --display-agent 🧙 Gandalf\n")

	// Session end clears the pane labeled with the name, whatever its title is by then
	list(`{"pane_id":"w1:p1","agent":"codex","terminal_title":"proj","display_agent":"🧙 Gandalf","agent_status":"idle"},
		{"pane_id":"w1:p9","agent":"codex","terminal_title":"proj","agent_status":"idle"}`)
	Clear(context.Background(), Codex, "🧙 Gandalf")
	expect("pane list\npane report-metadata w1:p1 --source rostr --agent codex --clear-display-agent\n")

	// Two panes showing the name are one too many to guess between (it waits a second for a title to settle)
	list(`{"pane_id":"w1:p1","agent":"codex","terminal_title":"🧙 Gandalf | a","agent_status":"idle"},
		{"pane_id":"w1:p3","agent":"codex","terminal_title":"🧙 Gandalf | b","agent_status":"idle"}`)
	Label(context.Background(), Codex, "🧙 Gandalf")
	if got, _ := os.ReadFile(calls); strings.Contains(string(got), "report-metadata") {
		t.Fatalf("labeled one of two matching panes:\n%s", got)
	}
}

func TestCodexDaemonHooksFindTheTmuxPaneByItsTitle(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux isn't installed")
	}
	// The daemon wasn't started in tmux, so rostr looks in the default tmux server: here, one of the test's own. Its
	// folder is under /tmp, since a socket under macOS's long temp folder is past the socket path limit
	tmuxDir, err := os.MkdirTemp("/tmp", "rostr-tmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmuxDir) })
	t.Setenv("TMUX_TMPDIR", tmuxDir)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "no-herdr"))
	server := &tmuxServer{name: "default"}
	server.tmux(t, "-f", os.DevNull, "new-session", "-d", "-x", "80", "-y", "24", "sh")
	t.Cleanup(func() { exec.Command("tmux", "-L", server.name, "kill-server").Run() })
	first := server.tmux(t, "display-message", "-p", "-t", "0:0.0", "#{pane_id}")
	second := server.tmux(t, "new-window", "-P", "-F", "#{pane_id}", "sh")
	server.tmux(t, "select-pane", "-t", first, "-T", "proj")
	server.tmux(t, "select-pane", "-t", second, "-T", "🌱 Samwise · Fix it | proj")

	Label(context.Background(), Codex, "🌱 Samwise")
	server.expect(t, second, "#{@rostr}", "🌱 Samwise")
	server.expect(t, first, "#{@rostr}", "")
	Clear(context.Background(), Codex, "🌱 Samwise")
	server.expect(t, second, "#{@rostr}", "")

	// A resting thread still titled and labeled with the name is skipped
	stale := server.tmux(t, "new-window", "-P", "-F", "#{pane_id}", "sh")
	server.tmux(t, "select-pane", "-t", stale, "-T", "🌱 Samwise · Old work | proj")
	server.tmux(t, "set-option", "-p", "-t", stale, "@rostr", "🌱 Samwise")
	Label(context.Background(), Codex, "🌱 Samwise")
	server.expect(t, second, "#{@rostr}", "🌱 Samwise")
}

func TestShowsName(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  bool
	}{
		{"🌱 Sam · Fix it | proj", true},
		{"⠋ 🌱 Sam | proj", true},
		{"🌱 Sam", true},
		{"🌱 Samwise · Fix it", false},
		{"🌱 Samwise and 🌱 Sam", true},
		{"proj", false},
	} {
		if got := showsName(tc.title, "🌱 Sam"); got != tc.want {
			t.Errorf("showsName(%q) = %v, want %v", tc.title, got, tc.want)
		}
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
	panes := filepath.Join(dir, "panes")
	t.Setenv("FAKE_HERDR_PANES", panes)
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\nif [ \"$1 $2\" = \"pane list\" ]; then cat " + panes + "; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}
