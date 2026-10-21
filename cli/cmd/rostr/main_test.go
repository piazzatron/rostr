package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// binary is the rostr binary built once for these tests.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rostr-bin")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "rostr")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("building rostr: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// sandbox is a temp HOME for one test, so nothing touches the real ~/.claude, ~/.codex, ~/.agents, ~/.config/rostr,
// or ~/.cache/rostr. Its environment drops the terminal's tmux, herdr, and Codex variables, so hooks never label the
// pane the tests run in.
type sandbox struct {
	t    *testing.T
	home string
}

type result struct {
	stdout, stderr string
	code           int
}

func newSandbox(t *testing.T) *sandbox {
	return &sandbox{t: t, home: t.TempDir()}
}

// run runs rostr in dir with stdin and extra environment variables ("KEY=value").
func (s *sandbox) run(dir, stdin string, env []string, args ...string) result {
	s.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = append(s.baseEnv(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		s.t.Fatal(err)
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// ok runs rostr and fails the test unless it exits 0.
func (s *sandbox) ok(dir string, env []string, args ...string) string {
	s.t.Helper()
	r := s.run(dir, "", env, args...)
	if r.code != 0 {
		s.t.Fatalf("rostr %v exited %d\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

// fails runs rostr and fails the test unless it exits 1 with stderr containing want.
func (s *sandbox) fails(dir string, env []string, want string, args ...string) {
	s.t.Helper()
	r := s.run(dir, "", env, args...)
	if r.code != 1 || !strings.Contains(r.stderr, want) {
		s.t.Fatalf("rostr %v: exit %d, stderr %q; want exit 1 mentioning %q", args, r.code, r.stderr, want)
	}
}

func (s *sandbox) baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "HOME" || name == "VISUAL" || name == "EDITOR" || strings.HasPrefix(name, "XDG_") ||
			strings.HasPrefix(name, "CLAUDE") || strings.HasPrefix(name, "ROSTR_") || strings.HasPrefix(name, "GIT_") ||
			strings.HasPrefix(name, "CODEX_") || strings.HasPrefix(name, "TMUX") || strings.HasPrefix(name, "HERDR_") {
			continue
		}
		env = append(env, kv)
	}
	// Cloud mode calls a closed local port unless a test says otherwise, so nothing here reaches the real API
	return append(env, "HOME="+s.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"ROSTR_DRAFT_PICK=first", "ROSTR_API_URL=http://127.0.0.1:1")
}

// gitRepo makes a repo in a temp dir, with an origin unless origin is "".
func (s *sandbox) gitRepo(name, origin string) string {
	s.t.Helper()
	dir := filepath.Join(s.t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = s.baseEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			s.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if origin != "" {
		git("remote", "add", "origin", origin)
	}
	return dir
}

func session(id string) []string { return []string{"ROSTR_SESSION=" + id} }

type listOutput struct {
	Rosters []struct {
		Name    string
		Drafted []struct{ Name, Display, Task, Repo, Session, Team string }
		Idle    []struct{ Name string }
	}
}

func (s *sandbox) list(args ...string) listOutput {
	s.t.Helper()
	var out listOutput
	if err := json.Unmarshal([]byte(s.ok(s.home, nil, append([]string{"list", "--json"}, args...)...)), &out); err != nil {
		s.t.Fatal(err)
	}
	return out
}

type draftOutput struct{ Name, Emoji, Team, Roster, Display string }

func (s *sandbox) draft(dir, sessionID string, args ...string) draftOutput {
	s.t.Helper()
	var out draftOutput
	stdout := s.ok(dir, nil, append([]string{"agent", "draft", "--json", "--session", sessionID}, args...)...)
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		s.t.Fatalf("%v: %s", err, stdout)
	}
	return out
}

func TestRosterAndAgentLifecycle(t *testing.T) {
	s := newSandbox(t)
	repo := s.gitRepo("api-checkout", "git@github.com:Acme/API.git")

	// Nothing works before assemble
	s.fails(s.home, nil, "No rosters yet. Run `rostr assemble`.", "list")
	s.fails(s.home, session("s0"), "No rosters yet", "agent", "draft")

	// assemble: presets, own names, taken names, and the default name
	out := s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	if !strings.Contains(out, "Assembled the work roster") || !strings.Contains(out, "default roster") {
		t.Fatalf("assemble output: %s", out)
	}
	s.ok(s.home, nil, "create", "play", "--names", "🧮 Ada, Grace", "--emoji", "off")
	s.fails(s.home, nil, `already have a roster named "work"`, "assemble", "WORK", "--preset", "hogwarts")
	s.ok(s.home, nil, "assemble", "--preset", "hogwarts")
	s.fails(s.home, nil, "--preset", "assemble")
	s.fails(s.home, nil, "rostr config cloud on", "assemble", "x", "--preset", "kanto", "--cloud")

	listing := s.list()
	if len(listing.Rosters) != 3 || listing.Rosters[2].Name != "hogwarts" || len(listing.Rosters[0].Idle) != 20 {
		t.Fatalf("list: %+v", listing)
	}
	if text := s.ok(s.home, nil, "list", "work"); !strings.HasPrefix(text, "work roster (default)\n") || strings.Contains(text, "play roster") {
		t.Fatalf("list work: %s", text)
	}

	// link: outside git it errors; in the repo it links by owner/repo
	s.fails(s.home, nil, "not in a git repo", "link", "play")
	if out := s.ok(repo, nil, "link"); out != "acme/api uses the work roster (your default).\n" {
		t.Fatalf("bare link: %q", out)
	}
	if out := s.ok(repo, nil, "link", "play"); out != "Linked acme/api to the play roster.\n" {
		t.Fatalf("link: %q", out)
	}
	if out := s.ok(s.home, nil, "link", "--repo", "Acme/API"); out != "acme/api uses the play roster (linked).\n" {
		t.Fatalf("bare link --repo: %q", out)
	}

	// agent draft: the linked roster in the repo, the default outside it, --roster over both
	s.fails(s.home, nil, "no session", "agent", "draft")
	if d := s.draft(repo, "s1"); d != (draftOutput{Name: "Ada", Emoji: "🧮", Team: "A", Roster: "play", Display: "Ada"}) {
		t.Fatalf("draft in repo: %+v", d)
	}
	if d := s.draft(s.home, "s2"); d.Display != "🧙 Gandalf" || d.Roster != "work" {
		t.Fatalf("draft outside repo: %+v", d)
	}
	if d := s.draft(repo, "s3", "--roster", "Hogwarts"); d.Name != "Harry" {
		t.Fatalf("draft --roster: %+v", d)
	}
	if out := s.ok(repo, session("s1"), "agent", "draft"); out != "Ada\n" {
		t.Fatalf("redraft: %q", out)
	}

	// agent task: the session's agent, or a named one
	if out := s.ok(repo, session("s1"), "agent", "task", "Fix auth redirect loop"); out != "Ada · Fix auth redirect loop\n" {
		t.Fatalf("task: %q", out)
	}
	s.ok(s.home, nil, "agent", "task", "GANDALF", "Review the PR")
	s.fails(s.home, nil, "no session", "agent", "task", "Something")
	s.fails(s.home, session("nobody"), "no drafted agent", "agent", "task", "Something")

	listing = s.list("play", "work")
	ada, gandalf := listing.Rosters[0].Drafted[0], listing.Rosters[1].Drafted[0]
	if ada.Task != "Fix auth redirect loop" || ada.Repo != "acme/api" || ada.Session != "s1" || gandalf.Task != "Review the PR" || gandalf.Repo != "" {
		t.Fatalf("list: %+v", listing)
	}
	if text := s.ok(s.home, nil, "list", "play"); !strings.Contains(text, "Ada   Fix auth redirect loop   acme/api   just now") || !strings.Contains(text, "Idle: Grace") {
		t.Fatalf("list text:\n%s", text)
	}

	// agent release: by name, by session, and nothing to release
	if out := s.ok(s.home, nil, "agent", "release", "ada"); out != "Released Ada from the play roster.\n" {
		t.Fatalf("release by name: %q", out)
	}
	s.fails(s.home, nil, `no agent named "ada" is drafted`, "agent", "release", "ada")
	if out := s.ok(s.home, session("s2"), "agent", "release"); out != "Released 🧙 Gandalf from the work roster.\n" {
		t.Fatalf("release by session: %q", out)
	}
	if out := s.ok(s.home, session("s2"), "agent", "release"); out != "This session has no drafted agent.\n" {
		t.Fatalf("release again: %q", out)
	}

	// unlink and config
	if out := s.ok(repo, nil, "unlink"); out != "Unlinked acme/api. It uses your default roster (work).\n" {
		t.Fatalf("unlink: %q", out)
	}
	s.ok(s.home, nil, "config", "default", "PLAY")
	s.ok(s.home, nil, "config", "expiry", "2w")
	s.ok(s.home, nil, "config", "auto-draft", "off")
	s.ok(s.home, nil, "config", "emoji", "only", "--roster", "work")
	s.fails(s.home, nil, "expiry must be", "config", "expiry", "soon")
	var config struct {
		Storage, Default, Expiry string
		AutoDraft                bool
		Emoji                    map[string]string
	}
	if err := json.Unmarshal([]byte(s.ok(s.home, nil, "config", "--json")), &config); err != nil {
		t.Fatal(err)
	}
	if config.Default != "play" || config.Expiry != "2w" || config.AutoDraft || config.Emoji["work"] != "only" || config.Storage != "local" {
		t.Fatalf("config: %+v", config)
	}

	// disband: asks first, promotes the oldest roster when the default goes, starts over after the last
	s.fails(s.home, nil, "pass --yes to disband the play roster (0 agents drafted, 0 linked projects)", "disband", "play")
	if out := s.ok(s.home, nil, "disband", "play", "--yes"); out != "Disbanded the play roster. Ada says bye.\nwork is your default roster now.\n" {
		t.Fatalf("disband: %q", out)
	}
	s.ok(s.home, nil, "disband", "hogwarts", "--yes")
	if out := s.ok(s.home, nil, "disband", "work", "--yes"); !strings.Contains(out, "last roster") {
		t.Fatalf("disband last: %q", out)
	}
	s.fails(s.home, nil, "No rosters yet", "list")
}

func TestSessionKeepsItsRosterWhenItChangesDirectory(t *testing.T) {
	s := newSandbox(t)
	api := s.gitRepo("api", "https://github.com/acme/api.git")
	web := s.gitRepo("web", "git@github.com:acme/web.git")
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.ok(s.home, nil, "assemble", "play", "--preset", "smash-bros")
	s.ok(web, nil, "link", "play")

	first := s.draft(api, "s1")
	again := s.draft(web, "s1")
	if first.Display != "🧙 Gandalf" || again != first {
		t.Fatalf("drafted %+v then %+v", first, again)
	}
	if d := s.draft(web, "s2"); d.Display != "🍄 Mario" {
		t.Fatalf("a new session in web: %+v", d)
	}
}

func TestReleaseByNameAsksWhichRosterWhenAmbiguous(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.ok(s.home, nil, "assemble", "side", "--preset", "free-agents")
	s.draft(s.home, "s1")
	s.draft(s.home, "s2", "--roster", "side")

	s.fails(s.home, nil, "Gandalf is drafted in more than one roster (work, side); pass --roster", "agent", "release", "gandalf")
	if out := s.ok(s.home, nil, "agent", "release", "gandalf", "--roster", "side"); out != "Released 🧙 Gandalf from the side roster.\n" {
		t.Fatalf("got %q", out)
	}
}

func TestEditWithEditor(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--names", "🧙 Gandalf, 💍 Frodo")
	s.draft(s.home, "s1") // Gandalf

	editor := func(json string) []string {
		replacement := filepath.Join(t.TempDir(), "edited.json")
		if err := os.WriteFile(replacement, []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
		return []string{"EDITOR=cp " + replacement}
	}

	// Removing drafted Gandalf is refused
	s.fails(s.home, editor(`{"emojiMode":"on","aTeam":[{"name":"Frodo","emoji":"💍"}],"bTeam":[]}`),
		"Gandalf is drafted", "edit", "work")
	s.fails(s.home, editor(`{"emojiMode":"on","aTeam":[],"bTeam":[]`), "valid roster JSON", "edit")

	// Changing his emoji and team, and adding a member, is fine
	out := s.ok(s.home, editor(`{"emojiMode":"off","aTeam":[{"name":"Frodo","emoji":"💍"},{"name":"Sam","emoji":"🌱"}],"bTeam":[{"name":"Gandalf","emoji":"🪄"}]}`), "edit")
	if out != "Saved the work roster.\n" {
		t.Fatalf("edit: %q", out)
	}
	if d := s.draft(s.home, "s2"); d.Display != "Frodo" {
		t.Fatalf("after edit: %+v", d)
	}

	s.fails(s.home, nil, "needs a terminal", "edit")
}

func TestClaudeCodeHooks(t *testing.T) {
	s := newSandbox(t)
	repo := s.gitRepo("api", "git@github.com:acme/api.git")
	envFile := filepath.Join(t.TempDir(), "claude-env")
	keepAliveDir := filepath.Join(s.home, ".cache", "rostr", "keepalive")
	hook := func(event, input string, env ...string) result {
		return s.run(repo, input, env, "hook", "claude-code", event)
	}
	firstPrompt := func(id string) string {
		return fmt.Sprintf(`{"session_id":%q,"transcript_path":"/nonexistent","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Fix the login redirect loop\nmore","prompt_id":"p1"}`, id, repo)
	}
	start := func(id, source string) string {
		return fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":"SessionStart","source":%q}`, id, repo, source)
	}
	expectQuiet := func(name string, r result, code int) {
		t.Helper()
		if r.code != code || r.stdout != "" {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}
	}
	expectIdentity := func(name string, r result, display string) {
		t.Helper()
		var out struct {
			HookSpecificOutput struct{ HookEventName, AdditionalContext string }
		}
		if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil || out.HookSpecificOutput.HookEventName != "SessionStart" ||
			!strings.HasPrefix(out.HookSpecificOutput.AdditionalContext, "You're "+display+" for this session") {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}
	}

	// Before assemble, hooks never fail a session
	expectQuiet("start, no roster", hook("session-start", start("s1", "startup"), "CLAUDE_ENV_FILE="+envFile), 0)
	expectQuiet("prompt, no roster", hook("prompt", firstPrompt("s0")), 0)
	expectQuiet("bad input", hook("prompt", "not json"), 0)
	if env, _ := os.ReadFile(envFile); string(env) != "export ROSTR_SESSION=s1\n" {
		t.Fatalf("CLAUDE_ENV_FILE: %q", env)
	}

	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")

	// First prompt: drafts before Claude sees it, telling the agent its name and to record its task, and titling the
	// session with the prompt
	type promptOutput struct {
		HookSpecificOutput struct{ HookEventName, AdditionalContext, SessionTitle string }
	}
	readPromptOutput := func(r result) promptOutput {
		var out promptOutput
		if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil || out.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
			t.Fatalf("prompt: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
		}
		return out
	}
	out := readPromptOutput(hook("prompt", firstPrompt("s1")))
	if !strings.HasPrefix(out.HookSpecificOutput.AdditionalContext, "You're 🧙 Gandalf for this session (drafted by rostr from the \"work\" roster).") ||
		!strings.Contains(out.HookSpecificOutput.AdditionalContext, `run rostr agent task --session s1 "<3-5 word summary`) ||
		strings.Contains(out.HookSpecificOutput.AdditionalContext, "set_session_title") ||
		out.HookSpecificOutput.SessionTitle != "🧙 Gandalf · Fix the login redirect loop" {
		t.Fatalf("first prompt: %+v", out)
	}

	// The Stop hook older installs have does nothing
	expectQuiet("old title step", hook("title", `{"session_id":"s1","hook_event_name":"Stop"}`), 0)

	// A prompt that looks like the first again neither re-tells the name nor retitles the session with its text
	if out := readPromptOutput(hook("prompt", firstPrompt("s1"))); out.HookSpecificOutput.AdditionalContext != "" ||
		out.HookSpecificOutput.SessionTitle != "🧙 Gandalf · Fix the login redirect loop" {
		t.Fatalf("repeated first prompt: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(keepAliveDir, "s1")); err != nil {
		t.Fatalf("no keep-alive mark: %v", err)
	}
	drafted := s.list().Rosters[0].Drafted
	if len(drafted) != 1 || drafted[0].Task != "Fix the login redirect loop" || drafted[0].Repo != "acme/api" {
		t.Fatalf("drafted: %+v", drafted)
	}

	// Remote Control session: the agent titles the app's session through its server session
	sessionsDir := filepath.Join(s.home, ".claude", "sessions")
	os.MkdirAll(sessionsDir, 0o755)
	os.WriteFile(filepath.Join(sessionsDir, "4242.json"), []byte(`{"sessionId":"s2","bridgeSessionId":"session_xyz","entrypoint":"sdk-cli"}`), 0o644)
	if out := readPromptOutput(hook("prompt", firstPrompt("s2"))); !strings.Contains(out.HookSpecificOutput.AdditionalContext,
		`mcp__claude-code-remote__set_session_title with session_id "session_xyz" and title "💍 Frodo · <3-5 word summary`) {
		t.Fatalf("Remote Control first prompt: %+v", out)
	}

	// Setting the app's title "<name> · <summary>" records the summary as the task; other titles are left alone
	titleSet := func(title string) string {
		return fmt.Sprintf(`{"session_id":"s2","hook_event_name":"PostToolUse","tool_name":"mcp__claude-code-remote__set_session_title","tool_input":{"session_id":"session_xyz","title":%q}}`, title)
	}
	frodoTask := func() string {
		for _, d := range s.list().Rosters[0].Drafted {
			if d.Name == "Frodo" {
				return d.Task
			}
		}
		return ""
	}
	expectQuiet("title set", hook("title-set", titleSet("💍 Frodo · Untangle the auth flow")), 0)
	if task := frodoTask(); task != "Untangle the auth flow" {
		t.Fatalf("task after title set: %q", task)
	}
	expectQuiet("other title set", hook("title-set", titleSet("My own title")), 0)
	if task := frodoTask(); task != "Untangle the auth flow" {
		t.Fatalf("task after another title: %q", task)
	}

	// When the title call (or the agent recording its task) finishes a plain answer, the turn ends there; alongside
	// other work it goes on, and other Bash calls are left alone
	answer := filepath.Join(t.TempDir(), "answer.jsonl")
	os.WriteFile(answer, []byte(`{"type":"user","message":{"content":"How deep is Baikal?"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"About 1,642 m."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"rostr agent task \"Deepest lake\""}}]}}
`), 0o644)
	work := filepath.Join(t.TempDir(), "work.jsonl")
	os.WriteFile(work, []byte(`{"type":"user","message":{"content":"List the newest files"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__claude-code-remote__set_session_title","input":{}},{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"ls -lt"}}]}}
`), 0o644)
	toolCall := func(transcript, tool, input string) string {
		return fmt.Sprintf(`{"session_id":"s2","transcript_path":%q,"hook_event_name":"PostToolUse","tool_name":%q,"tool_use_id":"t1","tool_input":%s}`, transcript, tool, input)
	}
	if r := hook("title-set", toolCall(answer, "Bash", `{"command":"rostr agent task \"Deepest lake\""}`)); r.code != 0 || strings.TrimSpace(r.stdout) != `{"continue": false, "suppressOutput": true}` {
		t.Fatalf("plain answer: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	expectQuiet("title call alongside work", hook("title-set", toolCall(work, "mcp__claude-code-remote__set_session_title", `{"title":"💍 Frodo · Find the newest files"}`)), 0)
	expectQuiet("other Bash call", hook("title-set", toolCall(answer, "Bash", `{"command":"ls"}`)), 0)

	// Local desktop session: the app titles it straight away, so rostr doesn't, and the agent uses the app's own tool
	os.WriteFile(filepath.Join(sessionsDir, "4343.json"), []byte(`{"sessionId":"s9","bridgeSessionId":"session_l","entrypoint":"claude-desktop"}`), 0o644)
	if out := readPromptOutput(hook("prompt", firstPrompt("s9"))); out.HookSpecificOutput.SessionTitle != "" ||
		!strings.Contains(out.HookSpecificOutput.AdditionalContext, `mcp__ccd_session_mgmt__set_session_title (load it with ToolSearch first`) {
		t.Fatalf("Local first prompt: %+v", out)
	}
	expectQuiet("Local end", hook("session-end", `{"session_id":"s9","hook_event_name":"SessionEnd"}`), 0)

	// Cloud session: the agent titles it through the server session its container names
	if out := readPromptOutput(hook("prompt", firstPrompt("s3"), "CLAUDE_CODE_REMOTE_SESSION_ID=cse_abc")); !strings.Contains(
		out.HookSpecificOutput.AdditionalContext, `set_session_title with session_id "session_abc"`) {
		t.Fatalf("cloud first prompt: %+v", out)
	}
	expectQuiet("cloud end", hook("session-end", `{"session_id":"s3","hook_event_name":"SessionEnd"}`), 0)

	// Later prompts keep the draft alive, at most once an hour, and the title in step with the task
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	os.WriteFile(transcript, []byte(`{"type":"user","origin":{"kind":"human"},"promptId":"p1"}`+"\n"), 0o644)
	laterPrompt := func(title string) string {
		return fmt.Sprintf(`{"session_id":"s1","transcript_path":%q,"cwd":%q,"prompt":"next","prompt_id":"p2","session_title":%q}`, transcript, repo, title)
	}
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(keepAliveDir, "s1"), twoHoursAgo, twoHoursAgo)
	expectQuiet("later prompt", hook("prompt", laterPrompt("🧙 Gandalf · Fix the login redirect loop")), 0)
	if info, _ := os.Stat(filepath.Join(keepAliveDir, "s1")); time.Since(info.ModTime()) > time.Minute {
		t.Fatal("keep-alive didn't bump after an hour")
	}
	s.ok(s.home, []string{"ROSTR_SESSION=s1"}, "agent", "task", "Deflake the login test")
	if out := readPromptOutput(hook("prompt", laterPrompt("🧙 Gandalf · Fix the login redirect loop"))); out.HookSpecificOutput.SessionTitle != "🧙 Gandalf · Deflake the login test" {
		t.Fatalf("title after a new task: %+v", out)
	}

	// Compaction re-tells the name without drafting
	expectIdentity("compact", hook("session-start", start("s1", "compact")), "🧙 Gandalf")
	expectQuiet("compact, never drafted", hook("session-start", start("never", "compact")), 0)

	// Session end releases; compaction afterwards reclaims the same member while free
	expectQuiet("end", hook("session-end", `{"session_id":"s1","hook_event_name":"SessionEnd"}`), 0)
	if len(s.list().Rosters[0].Drafted) != 1 {
		t.Fatal("session end didn't release")
	}
	if _, err := os.Stat(filepath.Join(keepAliveDir, "s1")); err == nil {
		t.Fatal("session end left the keep-alive file")
	}
	expectIdentity("compact after end", hook("session-start", start("s1", "compact")), "🧙 Gandalf")

	// Resume asks for the old name back, or drafts a new one
	hook("session-end", `{"session_id":"s1"}`)
	expectIdentity("resume", hook("session-start", start("s1", "resume"), "CLAUDE_ENV_FILE="+envFile), "🧙 Gandalf")
	expectIdentity("resume, new", hook("session-start", start("s3", "resume")), "🌱 Samwise")
	if env, _ := os.ReadFile(envFile); !strings.HasSuffix(string(env), "export ROSTR_SESSION=s1\n") {
		t.Fatalf("resume didn't write CLAUDE_ENV_FILE: %q", env)
	}

	// Auto-draft off: no draft step, no drafting on resume, but ROSTR_SESSION and /draft still work
	s.ok(s.home, nil, "config", "auto-draft", "off")
	expectQuiet("prompt, auto-draft off", hook("prompt", firstPrompt("s4")), 0)
	expectQuiet("resume, auto-draft off", hook("session-start", start("s5", "resume")), 0)
	expectQuiet("start, auto-draft off", hook("session-start", start("s6", "startup"), "CLAUDE_ENV_FILE="+envFile), 0)
	if env, _ := os.ReadFile(envFile); !strings.HasSuffix(string(env), "export ROSTR_SESSION=s6\n") {
		t.Fatalf("CLAUDE_ENV_FILE: %q", env)
	}
	r := s.run(repo, "", nil, "hook", "claude-code", "draft", "--session", "s4")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "You're 👑 Aragorn for this session") || !strings.Contains(r.stdout, "rostr agent task") {
		t.Fatalf("/draft: exit %d, stdout %q", r.code, r.stdout)
	}
	if r := s.run(repo, "", nil, "hook", "claude-code", "draft"); r.code != 0 || !strings.Contains(r.stdout, "couldn't draft") {
		t.Fatalf("/draft without a session: %+v", r)
	}

	// In cloud mode with a bad key or no API, hooks still exit 0 and the session goes unnamed
	expectQuiet("prompt, bad key", hook("prompt", firstPrompt("s7"), "ROSTR_KEY=rostr_sk_x"), 0)
	s.ok(s.home, nil, "config", "auto-draft", "on")
	expectQuiet("prompt, no API", hook("prompt", firstPrompt("s7"), "ROSTR_KEY="+unregisteredKey), 0)
	expectQuiet("resume, no API", hook("session-start", start("s7", "resume"), "ROSTR_KEY="+unregisteredKey), 0)
	expectQuiet("end, no API", hook("session-end", `{"session_id":"s7"}`, "ROSTR_KEY="+unregisteredKey), 0)
}

func TestAssembleInstallsClaudeCodeOnceAndKeepsExistingSettings(t *testing.T) {
	s := newSandbox(t)
	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	os.WriteFile(settingsPath, []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`), 0o644)

	out := s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	if !strings.Contains(out, "Installed for Claude Code: hooks in") || !strings.Contains(out, "the /draft command") {
		t.Fatalf("first assemble: %s", out)
	}
	first, _ := os.ReadFile(settingsPath)
	if out := s.ok(s.home, nil, "assemble", "play", "--preset", "kanto"); strings.Contains(out, "Installed") {
		t.Fatalf("second assemble reinstalled: %s", out)
	}
	second, _ := os.ReadFile(settingsPath)
	if !bytes.Equal(first, second) {
		t.Fatal("second assemble changed settings.json")
	}

	var settings struct {
		Theme string
		Hooks map[string][]any
	}
	json.Unmarshal(second, &settings)
	if settings.Theme != "dark" || len(settings.Hooks["Stop"]) != 1 || len(settings.Hooks["SessionStart"]) != 1 ||
		len(settings.Hooks["UserPromptSubmit"]) != 1 || len(settings.Hooks["PostToolUse"]) != 1 || len(settings.Hooks["SessionEnd"]) != 1 {
		t.Fatalf("settings: %s", second)
	}
	for _, path := range []string{"skills/rostr/SKILL.md", "commands/draft.md"} {
		if _, err := os.Stat(filepath.Join(s.home, ".claude", path)); err != nil {
			t.Error(err)
		}
	}
}

func TestInstallAddsClaudeCodeHooksWithoutARosterAndStaysHidden(t *testing.T) {
	s := newSandbox(t)
	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0o644)

	if out := s.ok(s.home, nil, "install"); !strings.Contains(out, "Installed for Claude Code: hooks in") {
		t.Fatalf("install: %s", out)
	}
	settings, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(settings), `"theme": "dark"`) || !strings.Contains(string(settings), "rostr hook claude-code") {
		t.Fatalf("settings: %s", settings)
	}
	if out := s.ok(s.home, nil, "install"); strings.Contains(out, "Installed") {
		t.Fatalf("second install reinstalled: %s", out)
	}
	s.fails(s.home, nil, "No rosters yet", "list")
	if out := s.ok(s.home, nil, "--help"); strings.Contains(out, "\n  install ") {
		t.Fatalf("install shows in help: %s", out)
	}
}

func TestAssembleLinksTheRepoYoureIn(t *testing.T) {
	s := newSandbox(t)
	repo := s.gitRepo("api", "git@github.com:Acme/API.git")

	// The first roster is the default, so --link has nothing to do
	if out := s.ok(repo, nil, "assemble", "work", "--preset", "fellowship", "--link"); strings.Contains(out, "Linked") {
		t.Fatalf("first roster linked: %s", out)
	}

	// A later roster with --link is linked to the repo you're in
	if out := s.ok(repo, nil, "assemble", "side", "--preset", "nba", "--link"); !strings.Contains(out, "Linked acme/api to it.") {
		t.Fatalf("assemble --link: %s", out)
	}
	if out := s.ok(repo, nil, "link"); !strings.Contains(out, "acme/api uses the side roster (linked)") {
		t.Fatalf("link: %s", out)
	}

	// Outside a repo, --link says how to use it
	if r := s.run(s.home, "", nil, "assemble", "play", "--preset", "kanto", "--link"); r.code != 1 || !strings.Contains(r.stderr, "--link needs a git repo") || !strings.Contains(r.stderr, "Usage:") {
		t.Fatalf("--link outside a repo: exit %d, %s", r.code, r.stderr)
	}
}

func TestUninstallRemovesEverything(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.ok(s.home, nil, "assemble", "side", "--preset", "nba")
	s.ok(s.home, nil, "agent", "draft", "--session", "s1")

	// Without a terminal it needs --yes, and says how to use it
	s.fails(s.home, nil, "pass --yes", "uninstall")
	if r := s.run(s.home, "", nil, "uninstall"); !strings.Contains(r.stderr, "Usage:\n  rostr uninstall [flags]") {
		t.Fatalf("no usage: %s", r.stderr)
	}

	out := s.ok(s.home, nil, "uninstall", "--yes")
	if !strings.Contains(out, "Deleted 2 rosters.") || !strings.Contains(out, "Removed hooks in") || !strings.Contains(out, "Removed the /draft command") {
		t.Fatalf("uninstall: %s", out)
	}
	settings, _ := os.ReadFile(filepath.Join(s.home, ".claude", "settings.json"))
	if strings.Contains(string(settings), "rostr") {
		t.Fatalf("rostr left in settings.json: %s", settings)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".config", "rostr", "rostr.db")); !os.IsNotExist(err) {
		t.Fatalf("database still there: %v", err)
	}
	s.fails(s.home, nil, "No rosters yet", "list")

	// Running it again finds nothing left
	if out := s.ok(s.home, nil, "uninstall", "--yes"); !strings.Contains(out, "Nothing to remove") {
		t.Fatalf("second uninstall: %s", out)
	}
}

func TestMissingArgumentsPrintUsage(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")

	for _, args := range [][]string{{"disband"}, {"config", "emoji"}, {"assemble", "--emoji", "on"}, {"list", "--bogus"}} {
		r := s.run(s.home, "", nil, args...)
		if r.code != 1 || !strings.Contains(r.stderr, "Usage:") {
			t.Errorf("rostr %v: exit %d, stderr %q", args, r.code, r.stderr)
		}
	}

	// Errors that aren't about missing input don't print usage
	if r := s.run(s.home, "", nil, "disband", "nope", "--yes"); r.code != 1 || strings.Contains(r.stderr, "Usage:") {
		t.Errorf("disband nope: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestVersion(t *testing.T) {
	s := newSandbox(t)
	for _, args := range [][]string{{"version"}, {"--version"}} {
		if out := s.ok(s.home, nil, args...); !strings.HasPrefix(out, "rostr ") {
			t.Errorf("rostr %v: %q", args, out)
		}
	}
}

func TestConcurrentProcessesNeverDraftTheSameMember(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "small", "--names", "A, B, C")

	const processes = 16
	names := make([]string, processes)
	var wg sync.WaitGroup
	for i := range processes {
		wg.Go(func() {
			r := s.run(s.home, "", nil, "agent", "draft", "--json", "--session", fmt.Sprintf("p%d", i))
			var d draftOutput
			if r.code != 0 || json.Unmarshal([]byte(r.stdout), &d) != nil {
				t.Errorf("process %d: exit %d, %s %s", i, r.code, r.stdout, r.stderr)
			}
			names[i] = d.Name
		})
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Errorf("%s drafted twice: %v", name, names)
		}
		seen[name] = true
	}
	for _, want := range []string{"A", "B", "C", "A 2", "C 5", "A 6"} {
		if !seen[want] {
			t.Errorf("missing %s: %v", want, names)
		}
	}
}

// unregisteredKey is a well-formed rostr key (32 zero bytes) that no server knows.
var unregisteredKey = "rostr_sk_" + strings.Repeat("A", 43)

func TestCloudModeWithoutTheAPI(t *testing.T) {
	s := newSandbox(t)
	database := filepath.Join(s.home, ".config", "rostr", "rostr.db")
	credentials := filepath.Join(s.home, ".config", "rostr", "credentials")

	// With nothing local to move and no terminal to paste a key into, cloud on explains the other ways in
	s.fails(s.home, nil, "no local rosters to move", "config", "cloud", "on")
	s.fails(s.home, nil, "only runs in a terminal", "config", "key")

	// A failed upload leaves the local setup untouched, and saves no key
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.draft(s.home, "s1")
	s.fails(s.home, nil, "your local setup is untouched", "config", "cloud", "on")
	if _, err := os.Stat(database); err != nil {
		t.Fatalf("local database: %v", err)
	}
	if _, err := os.Stat(credentials); err == nil {
		t.Fatal("credentials saved after a failed upload")
	}
	if l := s.list(); len(l.Rosters) != 1 || len(l.Rosters[0].Drafted) != 1 {
		t.Fatalf("local setup changed: %+v", l)
	}
	s.fails(s.home, nil, "rostr config cloud on", "assemble", "play", "--preset", "kanto", "--cloud")

	// A key puts the machine in cloud mode: a bad one is an error, and an unreachable API is reported
	s.fails(s.home, []string{"ROSTR_KEY=rostr_sk_x"}, "ROSTR_KEY", "list")
	s.fails(s.home, []string{"ROSTR_KEY=" + unregisteredKey}, "can't reach the rostr API at http://127.0.0.1:1", "list")
	s.fails(s.home, []string{"ROSTR_KEY=" + unregisteredKey}, "already in cloud mode", "config", "cloud", "on")

	// A first setup in the cloud that can't reach the API saves no key
	fresh := newSandbox(t)
	fresh.fails(fresh.home, nil, "uploading to the rostr API failed", "assemble", "--preset", "fellowship", "--cloud")
	if _, err := os.Stat(filepath.Join(fresh.home, ".config", "rostr", "credentials")); err == nil {
		t.Fatal("credentials saved after a failed setup")
	}

	if out := s.ok(s.home, nil, "presets"); !strings.HasPrefix(out, "fellowship") || !strings.Contains(out, "free-agents") {
		t.Fatalf("presets: %s", out)
	}
}

func TestCodexHooks(t *testing.T) {
	s := newSandbox(t)
	repo := s.gitRepo("api", "git@github.com:acme/api.git")
	keepAliveDir := filepath.Join(s.home, ".cache", "rostr", "keepalive")

	// A fake codex records rostr's attempts to name threads, and fails like a Codex without its app-server
	bin := t.TempDir()
	codexCalls := filepath.Join(bin, "calls")
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho \"$*\" >> "+codexCalls+"\nexit 1\n"), 0o755)
	path := "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")
	hook := func(event, input string, env ...string) result {
		return s.run(repo, input, append([]string{path}, env...), "hook", "codex", event)
	}
	prompt := func(id, transcript, text string) string {
		return fmt.Sprintf(`{"session_id":%q,"transcript_path":%s,"cwd":%q,"hook_event_name":"UserPromptSubmit","model":"gpt-5",`+
			`"permission_mode":"default","turn_id":"t1","prompt":%q}`, id, transcript, repo, text)
	}
	start := func(id, source string) string {
		return fmt.Sprintf(`{"session_id":%q,"transcript_path":null,"cwd":%q,"hook_event_name":"SessionStart","model":"gpt-5","source":%q}`, id, repo, source)
	}
	expectQuiet := func(name string, r result) {
		t.Helper()
		if r.code != 0 || r.stdout != "" {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}
	}
	expectIdentity := func(name string, r result, event, display string) {
		t.Helper()
		var out struct {
			HookSpecificOutput struct{ HookEventName, AdditionalContext string }
		}
		if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil || out.HookSpecificOutput.HookEventName != event ||
			!strings.HasPrefix(out.HookSpecificOutput.AdditionalContext, "You're "+display+" for this session") {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}
	}
	expectNamed := func(name string) {
		t.Helper()
		calls, _ := os.ReadFile(codexCalls)
		if string(calls) != "app-server proxy\n" {
			t.Fatalf("%s: codex calls %q", name, calls)
		}
		os.Remove(codexCalls)
	}
	firstPrompt := "Fix the login redirect loop\nmore"

	// Before assemble, hooks never fail a session
	expectQuiet("prompt, no roster", hook("prompt", prompt("t0", "null", firstPrompt)))
	expectQuiet("bad input", hook("prompt", "not json"))
	expectQuiet("start, no roster", hook("session-start", start("t0", "resume")))

	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")

	// First prompt: drafts, names the thread, and tells the agent who it is before its first reply
	expectIdentity("first prompt", hook("prompt", prompt("t1", "null", firstPrompt)), "UserPromptSubmit", "🧙 Gandalf")
	expectNamed("first prompt")
	if _, err := os.Stat(filepath.Join(keepAliveDir, "t1")); err != nil {
		t.Fatalf("no keep-alive mark: %v", err)
	}
	drafted := s.list().Rosters[0].Drafted
	if len(drafted) != 1 || drafted[0].Task != "Fix the login redirect loop" || drafted[0].Repo != "acme/api" || drafted[0].Session != "t1" {
		t.Fatalf("drafted: %+v", drafted)
	}

	// The agent's own rostr commands find the session through CODEX_THREAD_ID, unless ROSTR_SESSION says otherwise
	if out := s.ok(repo, []string{"CODEX_THREAD_ID=t1"}, "agent", "task", "Fix auth redirects"); out != "🧙 Gandalf · Fix auth redirects\n" {
		t.Fatalf("task: %q", out)
	}
	if out := s.ok(repo, []string{"CODEX_THREAD_ID=t1", "ROSTR_SESSION=other"}, "agent", "draft"); out != "💍 Frodo\n" {
		t.Fatalf("ROSTR_SESSION should win over CODEX_THREAD_ID: %q", out)
	}
	s.ok(repo, session("other"), "agent", "release")

	// Later prompts (with an earlier prompt in the rollout) only keep the draft alive, at most once an hour
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	earlier, _ := json.Marshal(firstPrompt)
	os.WriteFile(rollout, []byte(`{"timestamp":"t","type":"event_msg","payload":{"type":"user_message","message":`+string(earlier)+`}}`+"\n"+
		`{"timestamp":"t","type":"event_msg","payload":{"type":"agent_message","message":"Done"}}`+"\n"), 0o644)
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(keepAliveDir, "t1"), twoHoursAgo, twoHoursAgo)
	expectQuiet("later prompt", hook("prompt", prompt("t1", strconv.Quote(rollout), "next")))
	if info, _ := os.Stat(filepath.Join(keepAliveDir, "t1")); time.Since(info.ModTime()) > time.Minute {
		t.Fatal("keep-alive didn't bump after an hour")
	}
	if _, err := os.Stat(codexCalls); err == nil {
		t.Fatal("a later prompt renamed the thread")
	}

	// A thread that existed before rostr isn't drafted mid-conversation
	expectQuiet("old thread", hook("prompt", prompt("old", strconv.Quote(rollout), "next")))

	// Startup and clear do nothing; compaction re-tells the name without drafting
	expectQuiet("startup", hook("session-start", start("t1", "startup")))
	expectQuiet("clear", hook("session-start", start("t9", "clear")))
	expectIdentity("compact", hook("session-start", start("t1", "compact")), "SessionStart", "🧙 Gandalf")
	expectQuiet("compact, never drafted", hook("session-start", start("never", "compact")))

	// A fork is a new thread: it gets its own member and its own name
	expectIdentity("fork", hook("session-start", start("t2", "fork")), "SessionStart", "💍 Frodo")
	expectNamed("fork")

	// Session end releases quickly; resume asks for the old name back
	began := time.Now()
	expectQuiet("end", hook("session-end", `{"session_id":"t1","transcript_path":null,"cwd":"/","hook_event_name":"SessionEnd","reason":"other"}`))
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("session end took %s", took)
	}
	if _, err := os.Stat(filepath.Join(keepAliveDir, "t1")); err == nil {
		t.Fatal("session end left the keep-alive file")
	}
	for _, d := range s.list().Rosters[0].Drafted {
		if d.Session == "t1" {
			t.Fatal("session end didn't release")
		}
	}
	expectIdentity("resume", hook("session-start", start("t1", "resume")), "SessionStart", "🧙 Gandalf")

	// Auto-draft off: no draft step, but $rostr-draft drafts through CODEX_THREAD_ID
	s.ok(s.home, nil, "config", "auto-draft", "off")
	expectQuiet("prompt, auto-draft off", hook("prompt", prompt("t3", "null", firstPrompt)))
	expectQuiet("fork, auto-draft off", hook("session-start", start("t4", "fork")))
	r := s.run(repo, "", []string{path, "CODEX_THREAD_ID=t3"}, "hook", "codex", "draft")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "You're 🌱 Samwise for this session") || !strings.Contains(r.stdout, "rostr agent task --session t3") {
		t.Fatalf("$rostr-draft: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	expectNamed("$rostr-draft")
	if r := s.run(repo, "", []string{path}, "hook", "codex", "draft"); r.code != 0 || !strings.Contains(r.stdout, "couldn't draft") {
		t.Fatalf("$rostr-draft without a session: %+v", r)
	}

	// In cloud mode with no API, hooks still exit 0 and the session goes unnamed
	s.ok(s.home, nil, "config", "auto-draft", "on")
	expectQuiet("prompt, no API", hook("prompt", prompt("t5", "null", firstPrompt), "ROSTR_KEY="+unregisteredKey))
	expectQuiet("end, no API", hook("session-end", `{"session_id":"t5"}`, "ROSTR_KEY="+unregisteredKey))
}

func TestAssembleInstallsCodexOnlyWhereItsInstalled(t *testing.T) {
	// Without Codex (no ~/.codex, no codex on PATH), assemble leaves it alone
	s := newSandbox(t)
	noCodex := "PATH=" + pathWithout("codex")
	out := s.ok(s.home, []string{noCodex}, "assemble", "work", "--preset", "fellowship")
	if strings.Contains(out, "for Codex") {
		t.Fatalf("assemble without Codex: %s", out)
	}
	for _, dir := range []string{".codex", ".agents"} {
		if _, err := os.Stat(filepath.Join(s.home, dir)); err == nil {
			t.Errorf("created ~/%s", dir)
		}
	}

	// With a ~/.codex, the hooks and skills go in once, and the output says to approve the hooks
	os.MkdirAll(filepath.Join(s.home, ".codex"), 0o755)
	out = s.ok(s.home, []string{noCodex}, "assemble", "play", "--preset", "kanto")
	if !strings.Contains(out, "Installed for Codex: hooks in "+filepath.Join(s.home, ".codex", "hooks.json")+", the rostr skill, the $rostr-draft skill.") ||
		!strings.Contains(out, "open /hooks in Codex") {
		t.Fatalf("assemble with Codex: %s", out)
	}
	if out := s.ok(s.home, []string{noCodex}, "assemble", "side", "--preset", "hogwarts"); strings.Contains(out, "for Codex") {
		t.Fatalf("third assemble reinstalled: %s", out)
	}
	hooks, _ := os.ReadFile(filepath.Join(s.home, ".codex", "hooks.json"))
	for _, subcommand := range []string{"session-start", "prompt", "session-end"} {
		if strings.Count(string(hooks), "rostr hook codex "+subcommand+";") != 1 {
			t.Errorf("hooks.json: %s", hooks)
		}
	}
	for _, skill := range []string{"rostr", "rostr-draft"} {
		if _, err := os.Stat(filepath.Join(s.home, ".agents", "skills", skill, "SKILL.md")); err != nil {
			t.Error(err)
		}
	}

	// CODEX_HOME moves Codex's folder
	other := newSandbox(t)
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	os.MkdirAll(codexHome, 0o755)
	other.ok(other.home, []string{noCodex, "CODEX_HOME=" + codexHome}, "assemble", "work", "--preset", "fellowship")
	if _, err := os.Stat(filepath.Join(codexHome, "hooks.json")); err != nil {
		t.Fatal(err)
	}

	// uninstall takes rostr's pieces back out of Codex too
	out = s.ok(s.home, nil, "uninstall", "--yes")
	if !strings.Contains(out, "Removed hooks in "+filepath.Join(s.home, ".codex", "hooks.json")+" (Codex)") ||
		!strings.Contains(out, "Removed the $rostr-draft skill (Codex)") {
		t.Fatalf("uninstall: %s", out)
	}
	if hooks, _ := os.ReadFile(filepath.Join(s.home, ".codex", "hooks.json")); strings.Contains(string(hooks), "rostr") {
		t.Fatalf("rostr left in hooks.json: %s", hooks)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".agents", "skills", "rostr")); err == nil {
		t.Fatal("left the rostr skill")
	}
}

func TestClaudeCodeHooksLabelTheHerdrPane(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")

	// A fake herdr records its arguments; the tmux variables point nowhere, and herdr wins over them
	bin := t.TempDir()
	herdrCalls := filepath.Join(bin, "calls")
	os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\necho \"$*\" >> "+herdrCalls+"\n"), 0o755)
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + pathWithout("codex"), "HERDR_ENV=1", "HERDR_PANE_ID=w1:p3",
		"TMUX=/nonexistent,1,0", "TMUX_PANE=%99"}

	r := s.run(s.home, `{"session_id":"s1","transcript_path":"/nonexistent","prompt":"Fix it","prompt_id":"p1"}`, env, "hook", "claude-code", "prompt")
	if r.code != 0 {
		t.Fatalf("prompt: exit %d, stderr %q", r.code, r.stderr)
	}
	s.run(s.home, `{"session_id":"s1","source":"compact"}`, env, "hook", "claude-code", "session-start")
	s.run(s.home, `{"session_id":"s1"}`, env, "hook", "claude-code", "session-end")
	want := "pane report-metadata w1:p3 --source rostr --agent claude --display-agent 🧙 Gandalf\n" +
		"pane report-metadata w1:p3 --source rostr --agent claude --display-agent 🧙 Gandalf\n" +
		"pane report-metadata w1:p3 --source rostr --agent claude --clear-display-agent\n"
	if calls, _ := os.ReadFile(herdrCalls); string(calls) != want {
		t.Fatalf("herdr calls:\n%s", calls)
	}

	// Codex hooks never label panes: their environment belongs to Codex's shared daemon
	os.Remove(herdrCalls)
	r = s.run(s.home, `{"session_id":"t1","transcript_path":null,"prompt":"Fix it"}`, env, "hook", "codex", "prompt")
	if r.code != 0 || !strings.Contains(r.stdout, "You're 🧙 Gandalf") {
		t.Fatalf("codex prompt: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	s.run(s.home, `{"session_id":"t1"}`, env, "hook", "codex", "session-end")
	if _, err := os.Stat(herdrCalls); err == nil {
		t.Fatal("a Codex hook called herdr")
	}
}

func TestClaudeCodeHooksLabelTheTmuxPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux isn't installed")
	}
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")

	// A tmux server of the test's own, never the user's
	suffix := make([]byte, 6)
	rand.Read(suffix)
	server := "rostr-test-" + hex.EncodeToString(suffix)
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-L", server}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tmux("-f", os.DevNull, "new-session", "-d", "-x", "80", "-y", "24", "sh")
	t.Cleanup(func() { exec.Command("tmux", "-L", server, "kill-server").Run() })
	paneID := tmux("display-message", "-p", "#{pane_id}")
	env := []string{"TMUX=" + tmux("display-message", "-p", "#{socket_path}") + ",0,0", "TMUX_PANE=" + paneID}
	state := func() string {
		return tmux("display-message", "-p", "-t", paneID, "#{@rostr}|#{window_name}|#{automatic-rename}")
	}

	// The draft step tags the pane and names its window; session end hands the name back to tmux
	r := s.run(s.home, `{"session_id":"s1","transcript_path":"/nonexistent","prompt":"Fix it","prompt_id":"p1"}`, env, "hook", "claude-code", "prompt")
	if got := state(); r.code != 0 || got != "🧙 Gandalf|🧙 Gandalf|0" {
		t.Fatalf("after the draft step: exit %d, tmux %q", r.code, got)
	}
	s.run(s.home, `{"session_id":"s1"}`, env, "hook", "claude-code", "session-end")
	if got := state(); !strings.HasPrefix(got, "|") || !strings.HasSuffix(got, "|1") {
		t.Fatalf("after session end: tmux %q", got)
	}

	// Inside herdr, tmux is left alone
	r = s.run(s.home, `{"session_id":"s2","transcript_path":"/nonexistent","prompt":"Fix it","prompt_id":"p1"}`,
		append(env, "HERDR_ENV=1", "HERDR_PANE_ID=w1:p1", "HERDR_BIN_PATH=/nonexistent/herdr"), "hook", "claude-code", "prompt")
	if got := state(); r.code != 0 || !strings.HasPrefix(got, "|") {
		t.Fatalf("inside herdr: exit %d, tmux %q", r.code, got)
	}
}

// pathWithout is PATH without the directories that hold the named program.
func pathWithout(program string) string {
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, program)); err != nil {
			kept = append(kept, dir)
		}
	}
	return strings.Join(kept, string(os.PathListSeparator))
}
