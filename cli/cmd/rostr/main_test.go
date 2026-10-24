package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// fails runs rostr and fails the test unless it exits 1. It doesn't check the error's wording.
func (s *sandbox) fails(dir string, env []string, args ...string) {
	s.t.Helper()
	r := s.run(dir, "", env, args...)
	if r.code != 1 {
		s.t.Fatalf("rostr %v: exit %d, stderr %q; want exit 1", args, r.code, r.stderr)
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
		"ROSTR_API_URL=http://127.0.0.1:1", "ROSTR_CLAUDE_BIN=/usr/bin/false")
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
		Drafted []struct{ Name, Display, Task, Repo, Session string }
		Bench   []struct{ Name string }
	}
}

func (s *sandbox) list(args ...string) listOutput {
	s.t.Helper()
	var out listOutput
	if err := json.Unmarshal([]byte(s.ok(s.home, nil, append([]string{"stats", "--json"}, args...)...)), &out); err != nil {
		s.t.Fatal(err)
	}
	return out
}

type draftOutput struct{ Name, Emoji, Roster, Display string }

func (s *sandbox) draft(dir, sessionID string, args ...string) draftOutput {
	s.t.Helper()
	var out draftOutput
	stdout := s.ok(dir, nil, append([]string{"draft", "--json", "--session", sessionID}, args...)...)
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		s.t.Fatalf("%v: %s", err, stdout)
	}
	return out
}

func TestRosterAndAgentLifecycle(t *testing.T) {
	s := newSandbox(t)
	repo := s.gitRepo("api-checkout", "git@github.com:Acme/API.git")

	// Nothing works before assemble
	s.fails(s.home, nil, "stats")
	s.fails(s.home, session("s0"), "draft")

	// assemble: presets, own names, taken names, and the default name
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.ok(s.home, nil, "create", "play", "--names", "🧮 Ada, Grace", "--emoji", "off")
	s.fails(s.home, nil, "assemble", "WORK", "--preset", "hogwarts")
	s.ok(s.home, nil, "assemble", "--preset", "hogwarts", "--size", "4")
	s.fails(s.home, nil, "assemble")
	s.fails(s.home, nil, "assemble", "x", "--preset", "kanto", "--cloud")
	s.fails(s.home, nil, "assemble", "x", "--preset", "kanto", "--size", "lots")
	s.fails(s.home, nil, "assemble", "x", "--size", "4")

	// A preset's team is its first 16 unless --size says otherwise
	listing := s.list()
	if len(listing.Rosters) != 3 || listing.Rosters[2].Name != "hogwarts" || len(listing.Rosters[0].Bench) != 16 ||
		len(listing.Rosters[2].Bench) != 4 {
		t.Fatalf("list: %+v", listing)
	}
	if l := s.list("work"); len(l.Rosters) != 1 || l.Rosters[0].Name != "work" {
		t.Fatalf("list work: %+v", l)
	}

	// link: outside git it errors; in the repo it links by owner/repo
	s.fails(s.home, nil, "link", "play")
	s.ok(repo, nil, "link")
	s.ok(repo, nil, "link", "play")
	s.ok(s.home, nil, "link", "--repo", "Acme/API")

	// draft: the linked roster in the repo, the default outside it, --roster over both
	s.fails(s.home, nil, "draft")
	if d := s.draft(repo, "s1"); d != (draftOutput{Name: "Ada", Emoji: "🧮", Roster: "play", Display: "Ada"}) {
		t.Fatalf("draft in repo: %+v", d)
	}
	if d := s.draft(s.home, "s2"); d.Display != "🧙 Gandalf" || d.Roster != "work" {
		t.Fatalf("draft outside repo: %+v", d)
	}
	if d := s.draft(repo, "s3", "--roster", "Hogwarts"); d.Name != "Harry" {
		t.Fatalf("draft --roster: %+v", d)
	}
	if out := s.ok(repo, session("s1"), "draft"); !strings.Contains(out, "Ada") {
		t.Fatalf("redraft: %q", out)
	}

	// task: the session's agent, or a named one
	s.ok(repo, session("s1"), "task", "Fix auth redirect loop")
	s.ok(s.home, nil, "task", "GANDALF", "Review the PR")
	s.fails(s.home, nil, "task", "Something")
	s.fails(s.home, session("nobody"), "task", "Something")

	listing = s.list("play,", "work")
	ada, gandalf := listing.Rosters[0].Drafted[0], listing.Rosters[1].Drafted[0]
	if ada.Task != "Fix auth redirect loop" || ada.Repo != "acme/api" || ada.Session != "s1" || gandalf.Task != "Review the PR" || gandalf.Repo != "" {
		t.Fatalf("list: %+v", listing)
	}
	// list is an alias for stats
	if text := s.ok(s.home, nil, "list", "play"); !strings.Contains(text, "Ada") || !strings.Contains(text, "Grace") {
		t.Fatalf("list text:\n%s", text)
	}

	// bench: by name, by session, and nothing to bench
	s.ok(s.home, nil, "bench", "ada")
	s.fails(s.home, nil, "bench", "ada")
	// names are separated by commas, since they can have spaces the shell splits apart
	s.draft(s.home, "s4", "--roster", "play") // Grace, never drafted
	s.draft(s.home, "s5", "--roster", "play") // Ada
	// The copy goes to whoever was drafted longer ago, timed to the second, so a tie gives Ada (roster order) and a
	// second boundary between s4 and s5 gives Grace
	copy := s.draft(s.home, "s6", "--roster", "play")
	other := map[string]string{"Ada 2": "Grace", "Grace 2": "Ada"}[copy.Name]
	if other == "" {
		t.Fatalf("draft copy: %+v", copy)
	}
	member, number, _ := strings.Cut(copy.Name, " ")
	s.ok(s.home, nil, "bench", strings.ToLower(member), number+",", strings.ToLower(other))
	if drafted := s.list("play").Rosters[0].Drafted; len(drafted) != 1 || drafted[0].Name != member {
		t.Fatalf("after benching %s and %s: %+v", copy.Name, other, drafted)
	}
	s.ok(s.home, nil, "bench", strings.ToLower(member), "--roster", "play")
	s.ok(s.home, session("s2"), "bench")
	if drafted := s.list("work").Rosters[0].Drafted; len(drafted) != 0 {
		t.Fatalf("bench by session: %+v", drafted)
	}
	s.ok(s.home, session("s2"), "bench")

	// unlink and config
	s.ok(repo, nil, "unlink")
	if d := s.draft(repo, "s7"); d.Roster != "work" {
		t.Fatalf("draft after unlink: %+v", d)
	}
	s.ok(s.home, session("s7"), "bench")
	s.ok(s.home, nil, "config", "default", "PLAY")
	s.ok(s.home, nil, "config", "expiry", "2w")
	s.ok(s.home, nil, "config", "auto-draft", "off")
	s.ok(s.home, nil, "config", "emoji", "only", "--roster", "work")
	s.fails(s.home, nil, "config", "expiry", "soon")
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
	s.fails(s.home, nil, "disband", "play")
	s.ok(s.home, nil, "disband", "play", "--yes")
	if err := json.Unmarshal([]byte(s.ok(s.home, nil, "config", "--json")), &config); err != nil || config.Default != "work" {
		t.Fatalf("default after disband: %+v, %v", config, err)
	}
	s.ok(s.home, nil, "disband", "hogwarts", "--yes")
	s.ok(s.home, nil, "disband", "work", "--yes")
	s.fails(s.home, nil, "stats")
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

func TestBenchByNameAsksWhichRosterWhenAmbiguous(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.ok(s.home, nil, "assemble", "side", "--names", "🧙 Gandalf, 🐸 Yoda")
	s.draft(s.home, "s1")
	s.draft(s.home, "s2", "--roster", "side")

	s.fails(s.home, nil, "bench", "gandalf")
	s.ok(s.home, nil, "bench", "gandalf", "--roster", "side")
	if l := s.list("work,", "side"); len(l.Rosters[0].Drafted) != 1 || len(l.Rosters[1].Drafted) != 0 {
		t.Fatalf("list: %+v", l)
	}
}

func TestBenchSeveralNamesAndAll(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.ok(s.home, nil, "assemble", "side", "--names", "🧙 Gandalf, 🐸 Yoda")
	for _, session := range []string{"s1", "s2", "s3"} {
		s.draft(s.home, session)
	}
	s.draft(s.home, "s4", "--roster", "side")
	names := s.list("work").Rosters[0].Drafted

	// A typo benches nobody
	s.fails(s.home, nil, "bench", names[0].Name+",", "nobody", "--roster", "work")

	// Several names at once
	if len(s.list("work").Rosters[0].Drafted) != 3 {
		t.Fatal("a typo benched someone")
	}

	// Several names at once
	s.ok(s.home, nil, "bench", names[0].Name+",", names[1].Name, "--roster", "work")
	if drafted := s.list("work").Rosters[0].Drafted; len(drafted) != 1 || drafted[0].Name != names[2].Name {
		t.Fatalf("bench several: %+v", drafted)
	}

	// all, narrowed to a roster, then everyone
	s.ok(s.home, nil, "bench", "all", "--roster", "side")
	if l := s.list("work,", "side"); len(l.Rosters[0].Drafted) != 1 || len(l.Rosters[1].Drafted) != 0 {
		t.Fatalf("bench all --roster: %+v", l)
	}
	s.ok(s.home, nil, "bench", "all")
	if l := s.list(); len(l.Rosters[0].Drafted) != 0 || len(l.Rosters[1].Drafted) != 0 {
		t.Fatalf("bench all: %+v", l)
	}
	s.ok(s.home, nil, "bench", "all")
}

func TestEditNeedsATerminal(t *testing.T) {
	s := newSandbox(t)
	s.fails(s.home, nil, "edit")
	s.ok(s.home, nil, "assemble", "work", "--names", "🧙 Gandalf, 💍 Frodo")
	s.fails(s.home, nil, "edit")
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
			!strings.Contains(out.HookSpecificOutput.AdditionalContext, display) {
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
	// In a terminal the agent isn't asked to title anything: rostr summarizes the prompt itself, in the background
	// (here with a stand-in for claude -p), and the summary becomes the task
	fakeClaude := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(fakeClaude, []byte("#!/bin/sh\ncat >/dev/null; printf '{\"result\":\"Fix login redirect\"}\n'\n"), 0o755)
	out := readPromptOutput(hook("prompt", firstPrompt("s1"), "ROSTR_CLAUDE_BIN="+fakeClaude))
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "🧙 Gandalf") ||
		!strings.Contains(out.HookSpecificOutput.AdditionalContext, "rostr task --session s1") ||
		strings.Contains(out.HookSpecificOutput.AdditionalContext, "set_session_title") ||
		out.HookSpecificOutput.SessionTitle != "🧙 Gandalf · Fix the login redirect loop" {
		t.Fatalf("first prompt: %+v", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.list().Rosters[0].Drafted[0].Task != "Fix login redirect" {
		if time.Now().After(deadline) {
			t.Fatalf("the summary never became the task: %+v", s.list().Rosters[0].Drafted)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The next prompt puts it in the title
	if out := readPromptOutput(hook("prompt", firstPrompt("s1"))); out.HookSpecificOutput.SessionTitle != "🧙 Gandalf · Fix login redirect" {
		t.Fatalf("title after the summary: %+v", out)
	}
	s.ok(s.home, []string{"ROSTR_SESSION=s1"}, "task", "Fix the login redirect loop")

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
	if ctx := readPromptOutput(hook("prompt", firstPrompt("s2"))).HookSpecificOutput.AdditionalContext; !strings.Contains(ctx, "mcp__claude-code-remote__set_session_title") ||
		!strings.Contains(ctx, `"session_xyz"`) || !strings.Contains(ctx, "💍 Frodo · ") {
		t.Fatalf("Remote Control first prompt: %s", ctx)
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
	// What the title-set hook tells Claude: carry on (more context, no "continue"), or stop the turn
	type toolOutput struct {
		Continue           *bool
		HookSpecificOutput struct{ HookEventName, AdditionalContext string }
	}
	readToolOutput := func(name string, r result) toolOutput {
		t.Helper()
		var out toolOutput
		if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}
		return out
	}
	// (Without a transcript the call can't be seen to follow an answer, so the hook tells Claude to carry on)
	expectCarryOn := func(name string, r result) {
		t.Helper()
		if out := readToolOutput(name, r); out.Continue != nil || out.HookSpecificOutput.HookEventName != "PostToolUse" ||
			out.HookSpecificOutput.AdditionalContext == "" {
			t.Fatalf("%s: stdout %q", name, r.stdout)
		}
	}
	expectCarryOn("title set", hook("title-set", titleSet("💍 Frodo · Untangle the auth flow")))
	if task := frodoTask(); task != "Untangle the auth flow" {
		t.Fatalf("task after title set: %q", task)
	}
	expectCarryOn("other title set", hook("title-set", titleSet("My own title")))
	if task := frodoTask(); task != "Untangle the auth flow" {
		t.Fatalf("task after another title: %q", task)
	}

	// When the title call (or the agent recording its task) finishes a plain answer, the turn ends there; alongside
	// other work it goes on, and other Bash calls are left alone
	answer := filepath.Join(t.TempDir(), "answer.jsonl")
	os.WriteFile(answer, []byte(`{"type":"user","message":{"content":"How deep is Baikal?"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"About 1,642 m."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"rostr task \"Deepest lake\""}}]}}
`), 0o644)
	work := filepath.Join(t.TempDir(), "work.jsonl")
	os.WriteFile(work, []byte(`{"type":"user","message":{"content":"List the newest files"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__claude-code-remote__set_session_title","input":{}},{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"ls -lt"}}]}}
`), 0o644)
	toolCall := func(transcript, tool, input string) string {
		return fmt.Sprintf(`{"session_id":"s2","transcript_path":%q,"hook_event_name":"PostToolUse","tool_name":%q,"tool_use_id":"t1","tool_input":%s}`, transcript, tool, input)
	}
	if out := readToolOutput("plain answer", hook("title-set", toolCall(answer, "Bash", `{"command":"rostr task \"Deepest lake\""}`))); out.Continue == nil || *out.Continue {
		t.Fatalf("plain answer: %+v", out)
	}
	expectCarryOn("title call before the answer", hook("title-set", toolCall(work, "mcp__claude-code-remote__set_session_title", `{"title":"💍 Frodo · Find the newest files"}`)))
	expectQuiet("other Bash call", hook("title-set", toolCall(answer, "Bash", `{"command":"ls"}`)), 0)

	// Local desktop session: the name shows right away, and the agent uses the app's own tool
	os.WriteFile(filepath.Join(sessionsDir, "4343.json"), []byte(`{"sessionId":"s9","bridgeSessionId":"session_l","entrypoint":"claude-desktop"}`), 0o644)
	if out := readPromptOutput(hook("prompt", firstPrompt("s9"))); !strings.HasSuffix(out.HookSpecificOutput.SessionTitle, " · Fix the login redirect loop") ||
		!strings.Contains(out.HookSpecificOutput.AdditionalContext, "mcp__ccd_session_mgmt__set_session_title") ||
		!strings.Contains(out.HookSpecificOutput.AdditionalContext, `"self"`) {
		t.Fatalf("Local first prompt: %+v", out)
	}
	expectQuiet("Local end", hook("session-end", `{"session_id":"s9","hook_event_name":"SessionEnd"}`), 0)

	// Cloud session: the agent titles it through the server session its container names
	if out := readPromptOutput(hook("prompt", firstPrompt("s3"), "CLAUDE_CODE_REMOTE_SESSION_ID=cse_abc")); !strings.Contains(
		out.HookSpecificOutput.AdditionalContext, `"session_abc"`) {
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
	s.ok(s.home, []string{"ROSTR_SESSION=s1"}, "task", "Deflake the login test")
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
	expectIdentity("resume, new", hook("session-start", start("s3", "resume")), "👑 Aragorn")
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
	if r.code != 0 || !strings.Contains(r.stdout, "🏹 Legolas") || !strings.Contains(r.stdout, "rostr task") {
		t.Fatalf("/draft: exit %d, stdout %q", r.code, r.stdout)
	}
	before := len(s.list().Rosters[0].Drafted)
	if r := s.run(repo, "", nil, "hook", "claude-code", "draft"); r.code != 0 || r.stdout == "" || len(s.list().Rosters[0].Drafted) != before {
		t.Fatalf("/draft without a session: %+v", r)
	}

	// In cloud mode with a bad key or no API, hooks still exit 0 and the session goes unnamed
	expectQuiet("prompt, bad key", hook("prompt", firstPrompt("s7"), "ROSTR_KEY=rostr_sk_x"), 0)
	s.ok(s.home, nil, "config", "auto-draft", "on")
	expectQuiet("prompt, no API", hook("prompt", firstPrompt("s7"), "ROSTR_KEY="+unregisteredKey), 0)
	expectQuiet("resume, no API", hook("session-start", start("s7", "resume"), "ROSTR_KEY="+unregisteredKey), 0)
	expectQuiet("end, no API", hook("session-end", `{"session_id":"s7"}`, "ROSTR_KEY="+unregisteredKey), 0)
}

// Headless `claude -p` runs fire the same hooks but aren't drafted. Here a stand-in for claude runs the hook the way
// Claude Code does, through sh -c, so the hook sees the stand-in's arguments.
func TestClaudeCodeHooksSkipHeadlessRuns(t *testing.T) {
	s := newSandbox(t)
	repo := s.gitRepo("api", "git@github.com:acme/api.git")
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	claude := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(claude, []byte("#!/bin/sh\n/bin/sh -c '\"$ROSTR_BIN\" hook claude-code \"$ROSTR_EVENT\"'\n"), 0o755)
	runAsClaude := func(args []string, event, input string, env ...string) string {
		t.Helper()
		cmd := exec.Command(claude, args...)
		cmd.Dir = repo
		cmd.Env = append(append(s.baseEnv(), "ROSTR_BIN="+binary, "ROSTR_EVENT="+event), env...)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		return string(out)
	}
	prompt := func(id string) string {
		return fmt.Sprintf(`{"session_id":%q,"transcript_path":"/nonexistent","cwd":%q,"prompt":"Fix it","prompt_id":"p1"}`, id, repo)
	}
	resume := fmt.Sprintf(`{"session_id":"h1","cwd":%q,"source":"resume"}`, repo)

	printRun := []string{"-p", "Fix it"}
	if out := runAsClaude(printRun, "prompt", prompt("h1")); out != "" {
		t.Fatalf("headless prompt: %q", out)
	}
	if out := runAsClaude(printRun, "session-start", resume); out != "" {
		t.Fatalf("headless resume: %q", out)
	}
	if drafted := s.list().Rosters[0].Drafted; len(drafted) != 0 {
		t.Fatalf("a headless run was drafted: %+v", drafted)
	}

	// Remote Control sessions are --print runs too, driven over --sdk-url and stream-json. Their arguments run long
	// enough that ps would cut them at a terminal's width, so here a long one sits between --print and the flags that
	// make it a driven session
	remoteControl := []string{"--print", "--debug-file", "/tmp/" + strings.Repeat("x", 200) + ".log", "--sdk-url",
		"wss://example.com/v1/sessions/h2", "--session-id", "h2", "--input-format", "stream-json"}
	// (COLUMNS puts ps's cut inside the long argument, with room for the ppid column before the arguments)
	columns := fmt.Sprintf("COLUMNS=%d", len("/bin/sh "+claude+" --print --debug-file /tmp/")+20)
	if out := runAsClaude(remoteControl, "prompt", prompt("h2"), columns); !strings.Contains(out, "🧙 Gandalf") {
		t.Fatalf("Remote Control prompt: %q", out)
	}
	if drafted := s.list().Rosters[0].Drafted; len(drafted) != 1 || drafted[0].Session != "h2" {
		t.Fatalf("drafted: %+v", drafted)
	}
}

func TestAssembleInstallsClaudeCodeOnceAndKeepsExistingSettings(t *testing.T) {
	s := newSandbox(t)
	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	os.WriteFile(settingsPath, []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`), 0o644)

	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	first, _ := os.ReadFile(settingsPath)
	s.ok(s.home, nil, "assemble", "play", "--preset", "kanto")
	second, _ := os.ReadFile(settingsPath)
	if !bytes.Equal(first, second) {
		t.Fatal("second assemble changed settings.json")
	}

	var settings struct {
		Theme string
		Hooks map[string][]any
	}
	json.Unmarshal(second, &settings)
	if settings.Theme != "dark" || len(settings.Hooks["Stop"]) != 2 || len(settings.Hooks["SessionStart"]) != 1 ||
		len(settings.Hooks["UserPromptSubmit"]) != 1 || len(settings.Hooks["PostToolUse"]) != 2 || len(settings.Hooks["SessionEnd"]) != 1 {
		t.Fatalf("settings: %s", second)
	}
	for _, path := range []string{"skills/rostr/SKILL.md", "commands/draft.md"} {
		if _, err := os.Stat(filepath.Join(s.home, ".claude", path)); err != nil {
			t.Error(err)
		}
	}
}

func TestCloudInstallAddsClaudeCodeHooksOnlyInCloudSessions(t *testing.T) {
	s := newSandbox(t)
	settingsPath := filepath.Join(s.home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0o644)

	// On a person's own machine it points to assemble and changes nothing
	s.fails(s.home, nil, "cloud", "install")
	if settings, _ := os.ReadFile(settingsPath); strings.Contains(string(settings), "rostr hook") {
		t.Fatalf("installed outside a cloud session: %s", settings)
	}

	cloud := []string{"CLAUDE_CODE_REMOTE=true"}
	s.ok(s.home, cloud, "cloud", "install")
	settings, _ := os.ReadFile(settingsPath)
	var kept struct{ Theme string }
	if json.Unmarshal(settings, &kept) != nil || kept.Theme != "dark" || !strings.Contains(string(settings), "rostr hook claude-code") {
		t.Fatalf("settings: %s", settings)
	}
	s.ok(s.home, cloud, "cloud", "install")
	if again, _ := os.ReadFile(settingsPath); !bytes.Equal(settings, again) {
		t.Fatalf("second install changed settings.json: %s", again)
	}
	s.fails(s.home, nil, "stats")
}

func TestAssembleLinksTheRepoYoureIn(t *testing.T) {
	s := newSandbox(t)
	repo := s.gitRepo("api", "git@github.com:Acme/API.git")

	// The first roster is the default, so --link has nothing to do
	s.ok(repo, nil, "assemble", "work", "--preset", "fellowship", "--link")

	// A later roster with --link is linked to the repo you're in
	s.ok(repo, nil, "assemble", "side", "--preset", "nba", "--link")
	if d := s.draft(repo, "s1"); d.Roster != "side" {
		t.Fatalf("draft in the linked repo: %+v", d)
	}
	if d := s.draft(s.home, "s2"); d.Roster != "work" {
		t.Fatalf("draft outside it: %+v", d)
	}

	// Outside a repo, --link says how to use it
	if r := s.run(s.home, "", nil, "assemble", "play", "--preset", "kanto", "--link"); r.code != 1 || !strings.Contains(r.stderr, "Usage:") {
		t.Fatalf("--link outside a repo: exit %d, %s", r.code, r.stderr)
	}
}

func TestUninstallRemovesEverything(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.ok(s.home, nil, "assemble", "side", "--preset", "nba")
	s.ok(s.home, nil, "draft", "--session", "s1")

	// Without a terminal it needs --yes, and says how to use it
	s.fails(s.home, nil, "uninstall")
	if r := s.run(s.home, "", nil, "uninstall"); !strings.Contains(r.stderr, "Usage:") {
		t.Fatalf("no usage: %s", r.stderr)
	}

	s.ok(s.home, nil, "uninstall", "--yes")
	if _, err := os.Stat(filepath.Join(s.home, ".claude", "commands", "draft.md")); err == nil {
		t.Fatal("left the /draft command")
	}
	settings, _ := os.ReadFile(filepath.Join(s.home, ".claude", "settings.json"))
	if strings.Contains(string(settings), "rostr") {
		t.Fatalf("rostr left in settings.json: %s", settings)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".config", "rostr", "rostr.db")); !os.IsNotExist(err) {
		t.Fatalf("database still there: %v", err)
	}
	s.fails(s.home, nil, "stats")

	// Running it again finds nothing left
	s.ok(s.home, nil, "uninstall", "--yes")
}

func TestMissingArgumentsPrintUsage(t *testing.T) {
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")

	for _, args := range [][]string{{"disband"}, {"config", "emoji"}, {"assemble", "--emoji", "on"}, {"stats", "--bogus"}} {
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
			r := s.run(s.home, "", nil, "draft", "--json", "--session", fmt.Sprintf("p%d", i))
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
	s.fails(s.home, nil, "cloud", "on")
	s.fails(s.home, nil, "config", "key")

	// A failed upload leaves the local setup untouched, and saves no key
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	s.draft(s.home, "s1")
	s.fails(s.home, nil, "cloud", "on")
	if _, err := os.Stat(database); err != nil {
		t.Fatalf("local database: %v", err)
	}
	if _, err := os.Stat(credentials); err == nil {
		t.Fatal("credentials saved after a failed upload")
	}
	if l := s.list(); len(l.Rosters) != 1 || len(l.Rosters[0].Drafted) != 1 {
		t.Fatalf("local setup changed: %+v", l)
	}
	s.fails(s.home, nil, "assemble", "play", "--preset", "kanto", "--cloud")

	// A key puts the machine in cloud mode: a bad one is an error, and an unreachable API is reported
	s.fails(s.home, []string{"ROSTR_KEY=rostr_sk_x"}, "stats")
	s.fails(s.home, []string{"ROSTR_KEY=" + unregisteredKey}, "stats")
	s.fails(s.home, []string{"ROSTR_KEY=" + unregisteredKey}, "cloud", "on")

	// A first setup in the cloud that can't reach the API saves no key
	fresh := newSandbox(t)
	fresh.fails(fresh.home, nil, "assemble", "--preset", "fellowship", "--cloud")
	if _, err := os.Stat(filepath.Join(fresh.home, ".config", "rostr", "credentials")); err == nil {
		t.Fatal("credentials saved after a failed setup")
	}

	if out := s.ok(s.home, nil, "presets"); !strings.HasPrefix(out, "fellowship") || !strings.Contains(out, "free-agents") {
		t.Fatalf("presets: %s", out)
	}
}

func TestCloudModeWithADeadKey(t *testing.T) {
	// An API that recognizes no key, as when the key's cloud setup has been deleted
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": {"code": "unauthorized", "message": "Unknown key."}}`)
	}))
	defer api.Close()
	apiURL := []string{"ROSTR_API_URL=" + api.URL}
	envKey := append([]string{"ROSTR_KEY=" + unregisteredKey}, apiURL...)

	// A machine in cloud mode with the hooks installed, whose saved key no longer works
	s := newSandbox(t)
	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")
	credentials := filepath.Join(s.home, ".config", "rostr", "credentials")
	if err := os.WriteFile(credentials, []byte(unregisteredKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.home, ".config", "rostr", "rostr.db")); err != nil {
		t.Fatal(err)
	}

	// Errors say how to get going again, which depends on where the key comes from
	s.fails(s.home, apiURL, "stats")
	s.fails(s.home, envKey, "stats")

	// cloud on replaces a dead saved key (here, without a terminal, it stops at having nothing to move), but not ROSTR_KEY
	s.fails(s.home, apiURL, "cloud", "on")
	s.fails(s.home, envKey, "cloud", "on")

	// uninstall has no cloud setup to delete, so it cleans up this machine
	s.ok(s.home, apiURL, "uninstall", "--yes")
	if settings, _ := os.ReadFile(filepath.Join(s.home, ".claude", "settings.json")); strings.Contains(string(settings), "rostr") {
		t.Fatalf("rostr left in settings.json: %s", settings)
	}
	if _, err := os.Stat(credentials); !os.IsNotExist(err) {
		t.Fatalf("credentials still there: %v", err)
	}
	// The API may have rejected the key only because it was the wrong server, so the key is kept
	if old, _ := os.ReadFile(credentials + ".old"); string(old) != unregisteredKey+"\n" {
		t.Fatalf("credentials.old: %q", old)
	}
	s.fails(s.home, nil, "stats")
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
			!strings.Contains(out.HookSpecificOutput.AdditionalContext, display) {
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
	// The first prompt's title step reaches the app-server in the background
	expectNamedInBackground := func(name string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(codexCalls); err == nil {
				break
			}
		}
		expectNamed(name)
	}
	firstPrompt := "Fix the login redirect loop\nmore"

	// Before assemble, hooks never fail a session
	expectQuiet("prompt, no roster", hook("prompt", prompt("t0", "null", firstPrompt)))
	expectQuiet("bad input", hook("prompt", "not json"))
	expectQuiet("start, no roster", hook("session-start", start("t0", "resume")))

	s.ok(s.home, nil, "assemble", "work", "--preset", "fellowship")

	// First prompt: drafts and tells the agent who it is before its first reply, and leaves the thread for Codex to
	// title, which the title step then reads from the app-server in the background
	expectIdentity("first prompt", hook("prompt", prompt("t1", "null", firstPrompt)), "UserPromptSubmit", "🧙 Gandalf")
	expectNamedInBackground("first prompt")
	if _, err := os.Stat(filepath.Join(keepAliveDir, "t1")); err != nil {
		t.Fatalf("no keep-alive mark: %v", err)
	}
	drafted := s.list().Rosters[0].Drafted
	if len(drafted) != 1 || drafted[0].Task != "Fix the login redirect loop" || drafted[0].Repo != "acme/api" || drafted[0].Session != "t1" {
		t.Fatalf("drafted: %+v", drafted)
	}

	// The agent's own rostr commands find the session through CODEX_THREAD_ID, unless ROSTR_SESSION says otherwise
	s.ok(repo, []string{"CODEX_THREAD_ID=t1"}, "task", "Fix auth redirects")
	s.ok(repo, []string{"CODEX_THREAD_ID=t1", "ROSTR_SESSION=other"}, "draft")
	sessions := map[string]string{}
	for _, d := range s.list().Rosters[0].Drafted {
		sessions[d.Session] = d.Task
	}
	if len(sessions) != 2 || sessions["t1"] != "Fix auth redirects" {
		t.Fatalf("ROSTR_SESSION should win over CODEX_THREAD_ID: %v", sessions)
	}
	s.ok(repo, session("other"), "bench")

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

	// A fork is a new thread: it gets its own member and its own name (whoever's been drafted least recently)
	expectIdentity("fork", hook("session-start", start("t2", "fork")), "SessionStart", "🌱 Samwise")
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
	if r.code != 0 || !strings.Contains(r.stdout, "👑 Aragorn") || !strings.Contains(r.stdout, "rostr task --session t3") {
		t.Fatalf("$rostr-draft: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	expectNamed("$rostr-draft")
	before := len(s.list().Rosters[0].Drafted)
	if r := s.run(repo, "", []string{path}, "hook", "codex", "draft"); r.code != 0 || r.stdout == "" || len(s.list().Rosters[0].Drafted) != before {
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
	s.ok(s.home, []string{noCodex}, "assemble", "work", "--preset", "fellowship")
	for _, dir := range []string{".codex", ".agents"} {
		if _, err := os.Stat(filepath.Join(s.home, dir)); err == nil {
			t.Errorf("created ~/%s", dir)
		}
	}

	// With a ~/.codex, the hooks and skills go in once
	os.MkdirAll(filepath.Join(s.home, ".codex"), 0o755)
	s.ok(s.home, []string{noCodex}, "assemble", "play", "--preset", "kanto")
	s.ok(s.home, []string{noCodex}, "assemble", "side", "--preset", "hogwarts")
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
	s.ok(s.home, nil, "uninstall", "--yes")
	if hooks, _ := os.ReadFile(filepath.Join(s.home, ".codex", "hooks.json")); strings.Contains(string(hooks), "rostr") {
		t.Fatalf("rostr left in hooks.json: %s", hooks)
	}
	for _, skill := range []string{"rostr", "rostr-draft"} {
		if _, err := os.Stat(filepath.Join(s.home, ".agents", "skills", skill)); err == nil {
			t.Errorf("left the %s skill", skill)
		}
	}
}

func TestHooksLabelTheHerdrPane(t *testing.T) {
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
	afterPrompt, _ := os.ReadFile(herdrCalls)
	t.Logf("after prompt: %q", afterPrompt)
	compact := s.run(s.home, `{"session_id":"s1","source":"compact"}`, env, "hook", "claude-code", "session-start")
	afterCompact, _ := os.ReadFile(herdrCalls)
	t.Logf("after compact: %q", afterCompact)
	s.run(s.home, `{"session_id":"s1"}`, env, "hook", "claude-code", "session-end")
	want := "pane report-metadata w1:p3 --source rostr --agent claude --display-agent 🧙 Gandalf\n" +
		"pane report-metadata w1:p3 --source rostr --agent claude --display-agent 🧙 Gandalf\n" +
		"pane report-metadata w1:p3 --source rostr --agent claude --clear-display-agent\n"
	if calls, _ := os.ReadFile(herdrCalls); string(calls) != want {
		t.Logf("compact: exit %d, stdout %q, stderr %q", compact.code, compact.stdout, compact.stderr)
		t.Fatalf("herdr calls:\n%s", calls)
	}

	// A Codex hook that the Codex TUI didn't run (here, the test did) may have the shared daemon's environment, which
	// names another pane, so it doesn't label w1:p3: it looks for the pane by its title, and herdr has none here
	os.Remove(herdrCalls)
	r = s.run(s.home, `{"session_id":"t1","transcript_path":null,"prompt":"Fix it"}`, env, "hook", "codex", "prompt")
	if r.code != 0 || !strings.Contains(r.stdout, "💍 Frodo") {
		t.Fatalf("codex prompt: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	s.run(s.home, `{"session_id":"t1"}`, env, "hook", "codex", "session-end")
	if calls, _ := os.ReadFile(herdrCalls); strings.Contains(string(calls), "report-metadata") {
		t.Fatalf("a Codex hook outside Codex labeled the environment's pane:\n%s", calls)
	}
	os.Remove(herdrCalls)

	// Run by the Codex TUI itself (`codex --no-daemon`), it labels the pane as Codex's. A copy of sh named codex stands
	// in for the TUI, and the trailing ":" keeps sh from exec'ing rostr in its place.
	fakeCodex := filepath.Join(t.TempDir(), "codex")
	shell, _ := os.ReadFile("/bin/sh")
	os.WriteFile(fakeCodex, shell, 0o755)
	byCodex := func(stdin, event string) {
		cmd := exec.Command(fakeCodex, "-c", binary+" hook codex "+event+"; :")
		cmd.Dir, cmd.Env, cmd.Stdin = s.home, append(s.baseEnv(), env...), strings.NewReader(stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("codex %s: %v\n%s", event, err, out)
		}
	}
	byCodex(`{"session_id":"t2","transcript_path":null,"prompt":"Fix it"}`, "prompt")
	byCodex(`{"session_id":"t2"}`, "session-end")
	want = "pane report-metadata w1:p3 --source rostr --agent codex --display-agent 🌱 Samwise\n" +
		"pane report-metadata w1:p3 --source rostr --agent codex --clear-display-agent\n"
	if calls, _ := os.ReadFile(herdrCalls); string(calls) != want {
		t.Fatalf("herdr calls from Codex:\n%s", calls)
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
