package codex

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestReadHookInput(t *testing.T) {
	input, err := ReadHookInput(strings.NewReader(`{"session_id":"019a-thread","transcript_path":null,"cwd":"/x",` +
		`"hook_event_name":"UserPromptSubmit","model":"gpt-5","permission_mode":"default","turn_id":"t1","prompt":"Fix it"}`))
	if err != nil {
		t.Fatal(err)
	}
	if input != (HookInput{SessionID: "019a-thread", Cwd: "/x", HookEventName: "UserPromptSubmit", TurnID: "t1", Prompt: "Fix it"}) {
		t.Fatalf("got %+v", input)
	}
	if input, err := ReadHookInput(strings.NewReader(`{"session_id":"s1","source":"fork"}`)); err != nil || input.Source != "fork" {
		t.Fatalf("SessionStart: %+v, %v", input, err)
	}
	if _, err := ReadHookInput(strings.NewReader(`{"cwd":"/x"}`)); err == nil {
		t.Fatal("no error without a session_id")
	}
	if _, err := ReadHookInput(strings.NewReader(`not json`)); err == nil {
		t.Fatal("no error for bad JSON")
	}
}

func TestIsFirstPrompt(t *testing.T) {
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	input := HookInput{TranscriptPath: rollout, Prompt: "Fix the login loop"}

	if !IsFirstPrompt(input) || !IsFirstPrompt(HookInput{Prompt: "x"}) {
		t.Fatal("a missing rollout should count as a first prompt")
	}

	// Session metadata, injected context, other events, and a malformed line aren't prompts
	writeLines(t, rollout,
		`{"timestamp":"t","type":"session_meta","payload":{"id":"s1"}}`,
		`{"timestamp":"t","type":"response_item","payload":{"type":"message","role":"user","content":[]}}`,
		`{"timestamp":"t","type":"event_msg","payload":{"type":"warning","message":"hello"}}`,
		`not json`,
	)
	if !IsFirstPrompt(input) {
		t.Fatal("no prompts in the rollout yet")
	}

	// This same prompt, already recorded
	writeLines(t, rollout, `{"type":"event_msg","payload":{"type":"user_message","message":"Fix the login loop"}}`)
	if !IsFirstPrompt(input) {
		t.Fatal("only the current prompt is in the rollout")
	}

	// An earlier prompt, with or without this one recorded, and on a last line without a newline
	writeLines(t, rollout,
		`{"type":"event_msg","payload":{"type":"user_message","message":"Earlier"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"Fix the login loop"}}`,
	)
	if IsFirstPrompt(input) {
		t.Fatal("an earlier prompt should make this not the first")
	}
	write(t, rollout, `{"type":"event_msg","payload":{"type":"user_message","message":"Earlier"}}`)
	if IsFirstPrompt(input) {
		t.Fatal("missed a prompt on the last line")
	}
}

func TestAdditionalContextShape(t *testing.T) {
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(AdditionalContext("UserPromptSubmit", "You're 🧙 Gandalf")), &out); err != nil {
		t.Fatal(err)
	}
	if out.HookSpecificOutput.HookEventName != "UserPromptSubmit" || out.HookSpecificOutput.AdditionalContext != "You're 🧙 Gandalf" {
		t.Fatalf("got %+v", out)
	}
}

func TestInstallIsIdempotentAndKeepsOtherHooks(t *testing.T) {
	codexDir, skillsDir := t.TempDir(), t.TempDir()
	hooksPath := filepath.Join(codexDir, "hooks.json")
	write(t, hooksPath, `{
  "description": "mine",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "rostr hook codex session-end", "timeout": 3}]}]
  }
}`)

	installed, err := Install(codexDir, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(installed, "; ") != "hooks in "+hooksPath+"; the rostr skill; the $rostr-draft skill" {
		t.Errorf("installed %v", installed)
	}
	first, _ := os.ReadFile(hooksPath)

	// A second run changes nothing and reports nothing
	if installed, err := Install(codexDir, skillsDir); err != nil || len(installed) != 0 {
		t.Fatalf("second install: %v, %v", installed, err)
	}
	if second, _ := os.ReadFile(hooksPath); string(first) != string(second) {
		t.Fatal("second install rewrote hooks.json")
	}

	var config struct {
		Description string
		Hooks       map[string][]struct {
			Matcher string
			Hooks   []struct {
				Type, Command string
				Timeout       int
			}
		}
	}
	if err := json.Unmarshal(first, &config); err != nil {
		t.Fatal(err)
	}
	if config.Description != "mine" || len(config.Hooks["Stop"]) != 1 || len(config.Hooks["SessionEnd"]) != 1 {
		t.Errorf("other hooks lost or rostr's duplicated: %s", first)
	}
	if !strings.Contains(string(first), ">/dev/null") {
		t.Error("the hook command was HTML-escaped")
	}
	for event, subcommand := range map[string]string{"SessionStart": "session-start", "UserPromptSubmit": "prompt"} {
		groups := config.Hooks[event]
		if len(groups) != 1 || groups[0].Matcher != "" || groups[0].Hooks[0].Type != "command" ||
			groups[0].Hooks[0].Command != HookCommand(subcommand) || groups[0].Hooks[0].Timeout != 0 {
			t.Errorf("%s: %+v", event, groups)
		}
	}
	for _, name := range []string{"rostr", "rostr-draft"} {
		data, err := os.ReadFile(filepath.Join(skillsDir, name, "SKILL.md"))
		if err != nil || !strings.HasPrefix(string(data), "---\nname: "+name+"\n") {
			t.Errorf("%s skill: %v %q", name, err, data)
		}
	}
}

func TestInstallSetsSessionEndTimeout(t *testing.T) {
	codexDir := t.TempDir()
	if _, err := Install(codexDir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(codexDir, "hooks.json"))
	var config struct {
		Hooks map[string][]struct{ Hooks []map[string]any }
	}
	json.Unmarshal(data, &config)
	end := config.Hooks["SessionEnd"][0].Hooks[0]
	if end["command"] != HookCommand("session-end") || end["timeout"] != float64(3) {
		t.Fatalf("SessionEnd hook: %v", end)
	}
}

func TestUninstallRemovesOnlyWhatRostrAdded(t *testing.T) {
	codexDir, skillsDir := t.TempDir(), t.TempDir()
	hooksPath := filepath.Join(codexDir, "hooks.json")
	write(t, hooksPath, `{"hooks": {"SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "echo hi"}]}]}}`)
	if _, err := Install(codexDir, skillsDir); err != nil {
		t.Fatal(err)
	}

	// The user edited the draft skill, so it stays
	draftSkill := filepath.Join(skillsDir, "rostr-draft", "SKILL.md")
	write(t, draftSkill, "my own draft skill")

	removed, err := Uninstall(codexDir, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, "; ") != "hooks in "+hooksPath+"; the rostr skill" {
		t.Errorf("removed %v", removed)
	}
	data, _ := os.ReadFile(hooksPath)
	var config struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Command string }
		}
	}
	json.Unmarshal(data, &config)
	if len(config.Hooks) != 1 || config.Hooks["SessionStart"][0].Hooks[0].Command != "echo hi" || config.Hooks["SessionStart"][0].Matcher != "startup" {
		t.Errorf("hooks.json after uninstall: %s", data)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "rostr")); err == nil {
		t.Error("left the rostr skill folder")
	}
	if got, _ := os.ReadFile(draftSkill); string(got) != "my own draft skill" {
		t.Error("removed the user's draft skill")
	}

	// Nothing left to remove, and nothing created where Codex isn't set up
	if removed, err := Uninstall(codexDir, skillsDir); err != nil || len(removed) != 0 {
		t.Fatalf("second uninstall: %v, %v", removed, err)
	}
	missing := filepath.Join(t.TempDir(), "no-codex")
	if removed, err := Uninstall(missing, filepath.Join(missing, "skills")); err != nil || len(removed) != 0 {
		t.Fatalf("uninstall without Codex: %v, %v", removed, err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("uninstall created a Codex folder")
	}
}

func TestInstallRefusesUnreadableHooks(t *testing.T) {
	codexDir := t.TempDir()
	hooksPath := filepath.Join(codexDir, "hooks.json")
	write(t, hooksPath, `{"hooks": `)
	if _, err := Install(codexDir, t.TempDir()); err == nil || !strings.Contains(err.Error(), "didn't change it") {
		t.Fatalf("got %v", err)
	}
	if data, _ := os.ReadFile(hooksPath); string(data) != `{"hooks": ` {
		t.Fatal("overwrote an unreadable hooks.json")
	}
}

func TestDetected(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	codexDir := filepath.Join(t.TempDir(), ".codex")
	if Detected(codexDir) {
		t.Fatal("detected Codex with no folder and nothing on PATH")
	}
	os.MkdirAll(codexDir, 0o755)
	if !Detected(codexDir) {
		t.Fatal("missed the Codex folder")
	}

	bin := t.TempDir()
	write(t, filepath.Join(bin, "codex"), "#!/bin/sh\n")
	os.Chmod(filepath.Join(bin, "codex"), 0o755)
	t.Setenv("PATH", bin)
	if !Detected(filepath.Join(t.TempDir(), ".codex")) {
		t.Fatal("missed codex on PATH")
	}
}

func TestSetThreadNameSpeaksTheAppServerProtocol(t *testing.T) {
	received := make(chan []map[string]any, 1)
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		var messages []map[string]any
		defer func() { received <- messages }()
		for {
			var message map[string]any
			if websocket.JSON.Receive(ws, &message) != nil {
				return
			}
			messages = append(messages, message)
			switch message["method"] {
			case "initialize":
				websocket.JSON.Send(ws, map[string]any{"id": message["id"], "result": map[string]any{"userAgent": "codex/0.162"}})
				websocket.JSON.Send(ws, map[string]any{"method": "account/updated", "params": map[string]any{}})
			case "thread/name/set":
				websocket.JSON.Send(ws, map[string]any{"method": "thread/name/updated", "params": message["params"]})
				websocket.JSON.Send(ws, map[string]any{"id": "server-request", "method": "item/tool/requestUserInput"})
				websocket.JSON.Send(ws, map[string]any{"id": message["id"], "result": map[string]any{}})
			}
		}
	}))
	defer server.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if err := setThreadName(conn, "019a-thread", "🧙 Gandalf · Fix the login loop"); err != nil {
		t.Fatal(err)
	}

	messages := <-received
	if len(messages) != 3 || messages[0]["method"] != "initialize" || messages[1]["method"] != "initialized" ||
		messages[2]["method"] != "thread/name/set" || messages[1]["id"] != nil || messages[0]["jsonrpc"] != nil {
		t.Fatalf("messages: %v", messages)
	}
	clientInfo := messages[0]["params"].(map[string]any)["clientInfo"].(map[string]any)
	params := messages[2]["params"].(map[string]any)
	if clientInfo["name"] != "rostr" || params["threadId"] != "019a-thread" || params["name"] != "🧙 Gandalf · Fix the login loop" {
		t.Fatalf("messages: %v", messages)
	}
}

func TestSetThreadNameReportsErrors(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		for {
			var message map[string]any
			if websocket.JSON.Receive(ws, &message) != nil {
				return
			}
			if message["method"] == "initialize" {
				websocket.JSON.Send(ws, map[string]any{"id": 1, "result": map[string]any{}})
			} else if message["id"] != nil {
				websocket.JSON.Send(ws, map[string]any{"id": 2, "error": map[string]any{"code": -32600, "message": "thread not found"}})
			}
		}
	}))
	defer server.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if err := setThreadName(conn, "nope", "🧙 Gandalf"); err == nil || !strings.Contains(err.Error(), "thread not found") {
		t.Fatalf("got %v", err)
	}
}

func TestSetThreadNameGivesUpQuickly(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)

	// No codex at all
	if err := SetThreadName(context.Background(), "s1", "🧙 Gandalf"); err == nil {
		t.Fatal("no error without codex")
	}

	// No app-server to proxy to
	write(t, filepath.Join(bin, "codex"), "#!/bin/sh\necho 'no app-server running' >&2\nexit 1\n")
	os.Chmod(filepath.Join(bin, "codex"), 0o755)
	if err := SetThreadName(context.Background(), "s1", "🧙 Gandalf"); err == nil {
		t.Fatal("no error when the proxy fails")
	}

	// An app-server that never answers is abandoned at the timeout
	write(t, filepath.Join(bin, "codex"), "#!/bin/sh\nexec /bin/sleep 30\n")
	start := time.Now()
	if err := SetThreadName(context.Background(), "s1", "🧙 Gandalf"); err == nil {
		t.Fatal("no error when the app-server never answers")
	}
	if took := time.Since(start); took > titleTimeout+time.Second {
		t.Fatalf("took %s", took)
	}
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

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	write(t, path, strings.Join(lines, "\n")+"\n")
}
