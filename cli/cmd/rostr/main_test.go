package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// sandbox is a temp HOME for one test, so nothing touches the real ~/.claude, ~/.config/rostr, or ~/.cache/rostr.
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
			strings.HasPrefix(name, "CLAUDE") || strings.HasPrefix(name, "ROSTR_") || strings.HasPrefix(name, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	// Cloud mode calls a closed local port unless a test says otherwise, so nothing here reaches the real API
	return append(env, "HOME="+s.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "ROSTR_REWAKE_DELAY=0",
		"ROSTR_API_URL=http://127.0.0.1:1")
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
	if len(listing.Rosters) != 3 || listing.Rosters[2].Name != "hogwarts" || len(listing.Rosters[0].Idle) != 14 {
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
	if out := s.ok(s.home, nil, "disband", "play", "--yes"); out != "Disbanded the play roster.\nwork is your default roster now.\n" {
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

	// First prompt in a terminal session: drafts, then wakes Claude (exit 2) with its identity and the task step
	r := hook("prompt", firstPrompt("s1"))
	if r.code != 2 || r.stdout != "" || !strings.HasPrefix(r.stderr, "You're 🧙 Gandalf for this session (drafted by rostr from the \"work\" roster).") ||
		!strings.Contains(r.stderr, `run: rostr agent task "<your 3-5 word summary`) || strings.Contains(r.stderr, "set_session_title") {
		t.Fatalf("first prompt: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(keepAliveDir, "s1")); err != nil {
		t.Fatalf("no keep-alive mark: %v", err)
	}
	drafted := s.list().Rosters[0].Drafted
	if len(drafted) != 1 || drafted[0].Task != "Fix the login redirect loop" || drafted[0].Repo != "acme/api" {
		t.Fatalf("drafted: %+v", drafted)
	}

	// Desktop session: the wake-up asks Claude to set the app's title through the bridge session
	sessionsDir := filepath.Join(s.home, ".claude", "sessions")
	os.MkdirAll(sessionsDir, 0o755)
	os.WriteFile(filepath.Join(sessionsDir, "4242.json"), []byte(`{"sessionId":"s2","bridgeSessionId":"session_xyz"}`), 0o644)
	r = hook("prompt", firstPrompt("s2"))
	if r.code != 2 || !strings.Contains(r.stderr, `set_session_title with session_id "session_xyz" and title "💍 Frodo · <your 3-5 word summary`) {
		t.Fatalf("desktop prompt: exit %d, stderr %q", r.code, r.stderr)
	}

	// Later prompts only keep the draft alive, at most once an hour
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	os.WriteFile(transcript, []byte(`{"type":"user","origin":{"kind":"human"},"promptId":"p1"}`+"\n"), 0o644)
	laterPrompt := fmt.Sprintf(`{"session_id":"s1","transcript_path":%q,"cwd":%q,"prompt":"next","prompt_id":"p2"}`, transcript, repo)
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(keepAliveDir, "s1"), twoHoursAgo, twoHoursAgo)
	expectQuiet("later prompt", hook("prompt", laterPrompt), 0)
	if info, _ := os.Stat(filepath.Join(keepAliveDir, "s1")); time.Since(info.ModTime()) > time.Minute {
		t.Fatal("keep-alive didn't bump after an hour")
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
	r = s.run(repo, "", nil, "hook", "claude-code", "draft", "--session", "s4")
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
		len(settings.Hooks["UserPromptSubmit"]) != 1 || len(settings.Hooks["SessionEnd"]) != 1 {
		t.Fatalf("settings: %s", second)
	}
	for _, path := range []string{"skills/rostr/SKILL.md", "commands/draft.md"} {
		if _, err := os.Stat(filepath.Join(s.home, ".claude", path)); err != nil {
			t.Error(err)
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
