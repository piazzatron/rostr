package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsFirstPrompt(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "session.jsonl")
	input := HookInput{TranscriptPath: transcript, PromptID: "p2"}

	if !IsFirstPrompt(input) {
		t.Fatal("a missing transcript should count as a first prompt")
	}

	// Only this prompt, plus non-human entries and a malformed line
	writeLines(t, transcript,
		`{"type":"summary"}`,
		`not json`,
		`{"type":"user","origin":{"kind":"hook"},"promptId":"p1"}`,
		`{"type":"assistant","promptId":"p1"}`,
		`{"type":"user","origin":{"kind":"human"},"promptId":"p2"}`,
	)
	if !IsFirstPrompt(input) {
		t.Fatal("only the current prompt is in the transcript")
	}

	// An earlier human prompt
	writeLines(t, transcript,
		`{"type":"user","origin":{"kind":"human"},"promptId":"p1"}`,
		`{"type":"user","origin":{"kind":"human"},"promptId":"p2"}`,
	)
	if IsFirstPrompt(input) {
		t.Fatal("an earlier human prompt should make this not the first")
	}

	// A last line without a newline is still read
	if err := os.WriteFile(transcript, []byte(`{"type":"user","origin":{"kind":"human"},"promptId":"p0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsFirstPrompt(input) {
		t.Fatal("missed a prompt on the last line")
	}
}

func TestServerSessionID(t *testing.T) {
	t.Setenv("CLAUDE_CODE_REMOTE_SESSION_ID", "")
	claudeDir := t.TempDir()
	sessions := filepath.Join(claudeDir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(sessions, "111.json"), `{"pid":111,"sessionId":"terminal-session"}`)
	write(t, filepath.Join(sessions, "222.json"), `{"pid":222,"sessionId":"desktop-session","bridgeSessionId":"session_abc"}`)
	write(t, filepath.Join(sessions, "333.json"), `garbage`)

	if got := ServerSessionID(claudeDir, "desktop-session"); got != "session_abc" {
		t.Errorf("got %q", got)
	}
	for _, id := range []string{"terminal-session", "unknown"} {
		if got := ServerSessionID(claudeDir, id); got != "" {
			t.Errorf("%s: got %q", id, got)
		}
	}

	// A cloud session's container names its server session
	t.Setenv("CLAUDE_CODE_REMOTE_SESSION_ID", "cse_xyz")
	if got := ServerSessionID(t.TempDir(), "cloud-session"); got != "session_xyz" {
		t.Errorf("cloud: got %q", got)
	}
}

func TestIsDesktopLocal(t *testing.T) {
	claudeDir := t.TempDir()
	sessions := filepath.Join(claudeDir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(sessions, "1.json"), `{"sessionId":"local","entrypoint":"claude-desktop","bridgeSessionId":"session_l"}`)
	write(t, filepath.Join(sessions, "2.json"), `{"sessionId":"remote-control","entrypoint":"sdk-cli","bridgeSessionId":"session_r"}`)
	write(t, filepath.Join(sessions, "3.json"), `{"sessionId":"local-3p","entrypoint":"claude-desktop-3p"}`)
	if !IsDesktopLocal(claudeDir, "local") || !IsDesktopLocal(claudeDir, "local-3p") || IsDesktopLocal(claudeDir, "remote-control") ||
		IsDesktopLocal(claudeDir, "unknown") {
		t.Error("IsDesktopLocal")
	}
}

func TestReadHookInput(t *testing.T) {
	input, err := ReadHookInput(strings.NewReader(`{"session_id":"s1","hook_event_name":"SessionStart","source":"resume","cwd":"/x","extra":1}`))
	if err != nil || input.SessionID != "s1" || input.Source != "resume" || input.Cwd != "/x" {
		t.Fatalf("got %+v, %v", input, err)
	}
	for _, bad := range []string{"", "{", `{"source":"startup"}`} {
		if _, err := ReadHookInput(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestAdditionalContextShape(t *testing.T) {
	var out struct {
		HookSpecificOutput map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(AdditionalContext("SessionStart", "You're 🧙 Gandalf")), &out); err != nil {
		t.Fatal(err)
	}
	if out.HookSpecificOutput["hookEventName"] != "SessionStart" || out.HookSpecificOutput["additionalContext"] != "You're 🧙 Gandalf" {
		t.Fatalf("got %+v", out)
	}
}

func TestTaskFromTitle(t *testing.T) {
	for title, want := range map[string]string{
		"🧙 Gandalf · Fix login tests":   "Fix login tests",
		"🧙 Gandalf ·  Fix login tests ": "Fix login tests",
		"🧙 Gandalf · ":                  "",
		"🧙 Gandalf":                     "",
		"Fix login tests":               "",
		"💍 Frodo · Fix login tests":     "",
	} {
		task, ok := TaskFromTitle("🧙 Gandalf", title)
		if ok != (want != "") || ok && task != want {
			t.Errorf("TaskFromTitle(%q) = %q, %v", title, task, ok)
		}
	}
}

func TestIdentityText(t *testing.T) {
	// The name, the roster, and the command to record the task; the wording around them is free to change
	got := Identity("🧙 Gandalf", "work", "s1")
	for _, want := range []string{"🧙 Gandalf", `"work"`, "rostr task --session s1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in:\n%s", want, got)
		}
	}
}

func TestTitleInstruction(t *testing.T) {
	const title = `"🧙 Gandalf · <3-5 word summary of what the user wants>"`
	local := TitleInstruction("🧙 Gandalf", "s1", TitleWhere{DesktopLocal: true, ServerSessionID: "session_abc"})
	if !strings.Contains(local, LocalTitleTool) || !strings.Contains(local, `"self"`) || !strings.Contains(local, title) {
		t.Errorf("local: %s", local)
	}
	remote := TitleInstruction("🧙 Gandalf", "s1", TitleWhere{ServerSessionID: "session_abc"})
	if !strings.Contains(remote, RemoteTitleTool) || !strings.Contains(remote, `"session_abc"`) || !strings.Contains(remote, title) {
		t.Errorf("remote: %s", remote)
	}
	terminal := TitleInstruction("🧙 Gandalf", "s1", TitleWhere{})
	if terminal != "" || !(TitleWhere{}).IsTerminal() || (TitleWhere{DesktopLocal: true}).IsTerminal() || (TitleWhere{ServerSessionID: "x"}).IsTerminal() {
		t.Fatalf("terminal: %q", terminal)
	}
	// Terminals and Local sessions show a hook's title; Remote Control and cloud sessions don't
	if (TitleWhere{ServerSessionID: "x"}).ShowsHookTitles() || !(TitleWhere{DesktopLocal: true, ServerSessionID: "x"}).ShowsHookTitles() || !(TitleWhere{}).ShowsHookTitles() {
		t.Error("ShowsHookTitles")
	}
}

func TestPlaceholderTask(t *testing.T) {
	for in, want := range map[string]string{
		"  Fix the auth redirect loop  ": "Fix the auth redirect loop",
		"First line\nsecond line":        "First line",
		strings.Repeat("é", 60):          strings.Repeat("é", 50) + "…",
		"":                               "",
		"/rostr":                         "",
		"<task-notification> <summary>":  "",
		"<system-reminder>\nNo project folder.\n</system-reminder>\nWhat is the largest desert?": "What is the largest desert?",
		"<system-reminder>never closed": "",
	} {
		if got := PlaceholderTask(in); got != want {
			t.Errorf("PlaceholderTask(%q) = %q", in, got)
		}
	}
}

func TestInstallIsIdempotentAndKeepsOtherSettings(t *testing.T) {
	claudeDir := t.TempDir()
	settingsPath := filepath.Join(claudeDir, "settings.json")
	write(t, settingsPath, `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls:*)"]},
  "hooks": {
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "echo mine"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "rostr hook claude-code session-end"}]}]
  }
}`)

	installed, err := Install(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 3 {
		t.Errorf("installed %v", installed)
	}
	first, _ := os.ReadFile(settingsPath)

	// A second run changes nothing and reports nothing
	if installed, err := Install(claudeDir); err != nil || len(installed) != 0 {
		t.Fatalf("second install: %v, %v", installed, err)
	}
	second, _ := os.ReadFile(settingsPath)
	if string(first) != string(second) {
		t.Fatal("second install rewrote settings.json")
	}

	var settings struct {
		Model       string
		Permissions struct{ Allow []string }
		Hooks       map[string][]struct {
			Matcher string
			Hooks   []struct {
				Command     string
				Async       bool
				AsyncRewake bool
			}
		}
	}
	if err := json.Unmarshal(second, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "opus" || len(settings.Permissions.Allow) != 4 || settings.Permissions.Allow[0] != "Bash(ls:*)" {
		t.Errorf("other settings lost: %+v", settings)
	}
	if !strings.Contains(string(second), ">/dev/null") {
		t.Error("the hook command was HTML-escaped")
	}

	// The prompt hook runs before Claude; the title hook runs, in the foreground, after the title tools
	prompt := settings.Hooks["UserPromptSubmit"]
	if len(prompt) != 2 || prompt[0].Hooks[0].Command != "echo mine" ||
		prompt[1].Hooks[0].Command != HookCommand("prompt") || prompt[1].Hooks[0].AsyncRewake {
		t.Errorf("UserPromptSubmit: %+v", prompt)
	}
	if stop := settings.Hooks["Stop"]; len(stop) != 1 || stop[0].Hooks[0].Command != HookCommand("turn-end") || stop[0].Hooks[0].Async {
		t.Errorf("Stop: %+v", stop)
	}
	// PostToolUse: the title hook on the title tools, and the turn-start check on every tool
	if post := settings.Hooks["PostToolUse"]; len(post) != 2 || post[0].Matcher != RemoteTitleTool+"|"+LocalTitleTool ||
		post[0].Hooks[0].Command != HookCommand("title-set") || post[0].Hooks[0].Async ||
		post[1].Matcher != "" || post[1].Hooks[0].Command != HookCommand("working") {
		t.Errorf("PostToolUse: %+v", post)
	}
	if len(settings.Hooks["SessionEnd"]) != 1 {
		t.Errorf("duplicated an existing rostr hook: %+v", settings.Hooks["SessionEnd"])
	}
	if len(settings.Hooks["SessionStart"]) != 1 {
		t.Errorf("SessionStart: %+v", settings.Hooks["SessionStart"])
	}

	// An older skill is replaced, and a deleted /draft comes back
	skill := filepath.Join(claudeDir, "skills", "rostr", "SKILL.md")
	write(t, skill, "an older version's text")
	os.Remove(filepath.Join(claudeDir, "commands", "draft.md"))
	if installed, _ := Install(claudeDir); len(installed) != 2 {
		t.Errorf("reinstalled %v", installed)
	}
	if got, _ := os.ReadFile(skill); string(got) != skillContent {
		t.Error("kept the older skill")
	}

	// An older rostr /draft is replaced; someone's own /draft isn't
	draft := filepath.Join(claudeDir, "commands", "draft.md")
	write(t, draft, "!`rostr hook claude-code draft` (an older version)")
	if installed, _ := Install(claudeDir); len(installed) != 1 {
		t.Errorf("older /draft: reinstalled %v", installed)
	}
	write(t, draft, "Draft an email to my team")
	if installed, _ := Install(claudeDir); len(installed) != 0 {
		t.Errorf("own /draft: reinstalled %v", installed)
	}
	if got, _ := os.ReadFile(draft); string(got) != "Draft an email to my team" {
		t.Error("overwrote someone's own /draft")
	}
}

func TestUninstallRemovesOnlyWhatRostrAdded(t *testing.T) {
	claudeDir := t.TempDir()
	settingsPath := filepath.Join(claudeDir, "settings.json")
	write(t, settingsPath, `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls:*)"]},
  "hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "echo mine"}]}]}
}`)
	// A /draft the user wrote themselves: Install skips it, and Uninstall must leave it
	myDraft := filepath.Join(claudeDir, "commands", "draft.md")
	write(t, myDraft, "my own draft command")
	if _, err := Install(claudeDir); err != nil {
		t.Fatal(err)
	}

	removed, err := Uninstall(claudeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Errorf("removed %v", removed)
	}

	var settings struct {
		Model       string
		Permissions struct{ Allow []string }
		Hooks       map[string][]struct{ Hooks []struct{ Command string } }
	}
	data, _ := os.ReadFile(settingsPath)
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "opus" || len(settings.Permissions.Allow) != 1 || settings.Permissions.Allow[0] != "Bash(ls:*)" {
		t.Errorf("other settings changed: %s", data)
	}
	if len(settings.Hooks) != 1 || len(settings.Hooks["UserPromptSubmit"]) != 1 || settings.Hooks["UserPromptSubmit"][0].Hooks[0].Command != "echo mine" {
		t.Errorf("hooks: %s", data)
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "skills", "rostr")); !os.IsNotExist(err) {
		t.Errorf("skill folder still there: %v", err)
	}
	if got, _ := os.ReadFile(myDraft); string(got) != "my own draft command" {
		t.Error("removed the user's own /draft")
	}

	// Nothing left to remove the second time
	if removed, err := Uninstall(claudeDir); err != nil || len(removed) != 0 {
		t.Fatalf("second uninstall: %v, %v", removed, err)
	}
}

func TestInstallReplacesTheOldBackgroundHooks(t *testing.T) {
	claudeDir := t.TempDir()
	settingsPath := filepath.Join(claudeDir, "settings.json")
	asyncHook := func(subcommand string) string {
		return `{"hooks": [{"type": "command", "asyncRewake": true, "command": "` + strings.ReplaceAll(HookCommand(subcommand), `"`, `\"`) + `"}]}`
	}
	command := func(subcommand string) string { return strings.ReplaceAll(HookCommand(subcommand), `"`, `\"`) }
	write(t, settingsPath, `{"hooks": {"UserPromptSubmit": [`+asyncHook("prompt")+`, `+asyncHook("title")+`],
		"Stop": [{"hooks": [{"type": "command", "command": "`+command("title")+`"}]}],
		"PostToolUse": [{"matcher": "`+RemoteTitleTool+`", "hooks": [{"type": "command", "async": true, "command": "`+command("title-set")+`"}]}]}}`)
	if _, err := Install(claudeDir); err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct {
				Command     string
				Async       bool
				AsyncRewake bool
			}
		}
	}
	data, _ := os.ReadFile(settingsPath)
	json.Unmarshal(data, &settings)
	prompt, post := settings.Hooks["UserPromptSubmit"], settings.Hooks["PostToolUse"]
	// The old Stop title step is replaced by the turn-end hook
	stop := settings.Hooks["Stop"]
	if len(prompt) != 1 || prompt[0].Hooks[0].Command != HookCommand("prompt") || prompt[0].Hooks[0].AsyncRewake ||
		len(stop) != 1 || stop[0].Hooks[0].Command != HookCommand("turn-end") ||
		len(post) != 2 || post[0].Matcher != RemoteTitleTool+"|"+LocalTitleTool || post[0].Hooks[0].Async {
		t.Fatalf("hooks: %s", data)
	}
}

func TestInstallRefusesUnreadableSettings(t *testing.T) {
	claudeDir := t.TempDir()
	write(t, filepath.Join(claudeDir, "settings.json"), `{not json`)
	if _, err := Install(claudeDir); err == nil {
		t.Fatal("overwrote invalid settings.json")
	}
	if got, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json")); string(got) != `{not json` {
		t.Fatal("changed invalid settings.json")
	}
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	write(t, path, strings.Join(lines, "\n")+"\n")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsPrintRun(t *testing.T) {
	for _, tc := range []struct {
		args string
		want bool
	}{
		{"claude", false},
		{"/opt/claude-code/bin/claude --preload /home/claude/.claude/remote/spare.sock", false},
		{"claude -p say hi", true},
		{"node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js --print --model haiku", true},
		{"claude -p --input-format stream-json --output-format stream-json", false},
		{"claude --print --input-format=stream-json", false},
		{"claude -p --sdk-url wss://example.com", false},
	} {
		if got := isPrintRun(strings.Fields(tc.args)); got != tc.want {
			t.Errorf("isPrintRun(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestIsShellCommand(t *testing.T) {
	for _, tc := range []struct {
		args string
		want bool
	}{
		{"/bin/sh -c rostr hook claude-code prompt", true},
		{"-bash -lc rostr", true},
		{"/bin/sh /tmp/claude -p", false},
		{"bash --norc", false},
		{"claude -p", false},
	} {
		if got := isShellCommand(strings.Fields(tc.args)); got != tc.want {
			t.Errorf("isShellCommand(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestUninstallRemovesAnOlderRostrDraft(t *testing.T) {
	claudeDir := t.TempDir()
	draft := filepath.Join(claudeDir, "commands", "draft.md")
	write(t, draft, "!`rostr hook claude-code draft` (an older version)")
	if removed, err := Uninstall(claudeDir); err != nil || len(removed) != 1 {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Fatal("left an older rostr /draft that would run rostr")
	}
}
