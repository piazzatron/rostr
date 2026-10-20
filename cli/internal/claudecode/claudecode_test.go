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

func TestBridgeSessionID(t *testing.T) {
	claudeDir := t.TempDir()
	sessions := filepath.Join(claudeDir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(sessions, "111.json"), `{"pid":111,"sessionId":"terminal-session"}`)
	write(t, filepath.Join(sessions, "222.json"), `{"pid":222,"sessionId":"desktop-session","bridgeSessionId":"session_abc"}`)
	write(t, filepath.Join(sessions, "333.json"), `garbage`)

	if got := BridgeSessionID(claudeDir, "desktop-session"); got != "session_abc" {
		t.Errorf("got %q", got)
	}
	for _, id := range []string{"terminal-session", "unknown"} {
		if got := BridgeSessionID(claudeDir, id); got != "" {
			t.Errorf("%s: got %q", id, got)
		}
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

func TestIdentityText(t *testing.T) {
	want := `You're 🧙 Gandalf for this session (drafted by rostr from the "work" roster).
- When you write somewhere other than this chat, sign it "— 🧙 Gandalf": Slack messages, emails, PR descriptions, PR and issue comments.
- Don't put the name in code, commit messages, or files.
- Your user's own instructions about attribution or sign-offs take precedence over these.
- When your work changes, run: rostr agent task "<new task>"`
	if got := Identity("🧙 Gandalf", "work"); got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestTitleInstruction(t *testing.T) {
	desktop := TitleInstruction("🧙 Gandalf", "session_abc")
	if !strings.Contains(desktop, `set_session_title with session_id "session_abc"`) || !strings.Contains(desktop, `"🧙 Gandalf · <your 3-5 word summary`) {
		t.Errorf("desktop: %s", desktop)
	}
	if terminal := TitleInstruction("🧙 Gandalf", ""); strings.Contains(terminal, "set_session_title") || !strings.Contains(terminal, "rostr agent task") {
		t.Errorf("terminal: %s", terminal)
	}
}

func TestPlaceholderTask(t *testing.T) {
	for in, want := range map[string]string{
		"  Fix the auth redirect loop  ": "Fix the auth redirect loop",
		"First line\nsecond line":        "First line",
		strings.Repeat("é", 60):          strings.Repeat("é", 50) + "…",
		"":                               "",
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
			Hooks []struct {
				Command     string
				AsyncRewake bool
			}
		}
	}
	if err := json.Unmarshal(second, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "opus" || len(settings.Permissions.Allow) != 3 || settings.Permissions.Allow[0] != "Bash(ls:*)" {
		t.Errorf("other settings lost: %+v", settings)
	}
	if !strings.Contains(string(second), ">/dev/null") {
		t.Error("the hook command was HTML-escaped")
	}

	prompt := settings.Hooks["UserPromptSubmit"]
	if len(prompt) != 2 || prompt[0].Hooks[0].Command != "echo mine" || !prompt[1].Hooks[0].AsyncRewake ||
		prompt[1].Hooks[0].Command != HookCommand("prompt") {
		t.Errorf("UserPromptSubmit: %+v", prompt)
	}
	if len(settings.Hooks["SessionEnd"]) != 1 {
		t.Errorf("duplicated an existing rostr hook: %+v", settings.Hooks["SessionEnd"])
	}
	if len(settings.Hooks["SessionStart"]) != 1 {
		t.Errorf("SessionStart: %+v", settings.Hooks["SessionStart"])
	}

	// An edited skill isn't overwritten; a deleted one comes back
	skill := filepath.Join(claudeDir, "skills", "rostr", "SKILL.md")
	write(t, skill, "my edits")
	os.Remove(filepath.Join(claudeDir, "commands", "draft.md"))
	if installed, _ := Install(claudeDir); len(installed) != 1 || installed[0] != "the /draft command" {
		t.Errorf("reinstalled %v", installed)
	}
	if got, _ := os.ReadFile(skill); string(got) != "my edits" {
		t.Error("overwrote the edited skill")
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
