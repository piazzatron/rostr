// Package claudecode holds what rostr knows about Claude Code: its hook input and output shapes, how to tell a
// session's first prompt, how to find a desktop session's server ID, what the agent is told, and installing the
// hooks, skill, and /draft command.
package claudecode

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// HookInput is the JSON Claude Code sends a hook on stdin. Fields a given event doesn't send stay empty.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Source         string `json:"source"` // SessionStart: startup, resume, clear, or compact
	Prompt         string `json:"prompt"`
	PromptID       string `json:"prompt_id"`
}

// ReadHookInput decodes a hook's stdin.
func ReadHookInput(r io.Reader) (HookInput, error) {
	var input HookInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return input, fmt.Errorf("reading hook input: %w", err)
	}
	if input.SessionID == "" {
		return input, errors.New("hook input has no session_id")
	}
	return input, nil
}

// IsFirstPrompt reports whether the transcript holds no earlier prompt typed by the user, so sessions that existed
// before rostr was installed are never drafted mid-conversation. A missing transcript counts as a first prompt.
func IsFirstPrompt(input HookInput) bool {
	file, err := os.Open(input.TranscriptPath)
	if err != nil {
		return true
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		var entry struct {
			Type     string `json:"type"`
			PromptID string `json:"promptId"`
			Origin   struct {
				Kind string `json:"kind"`
			} `json:"origin"`
		}
		if json.Unmarshal(line, &entry) == nil {
			isHumanPrompt := entry.Type == "user" && entry.Origin.Kind == "human"
			if isHumanPrompt && entry.PromptID != input.PromptID {
				return false
			}
		}
		if err != nil {
			return true
		}
	}
}

// BridgeSessionID returns the server session ID of a desktop or Remote Control session, from the per-process session
// files Claude Code writes to ~/.claude/sessions/. Terminal sessions have none, so it returns "".
func BridgeSessionID(claudeDir, sessionID string) string {
	paths, _ := filepath.Glob(filepath.Join(claudeDir, "sessions", "*.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var session struct {
			SessionID       string `json:"sessionId"`
			BridgeSessionID string `json:"bridgeSessionId"`
		}
		if json.Unmarshal(data, &session) == nil && session.SessionID == sessionID {
			return session.BridgeSessionID
		}
	}
	return ""
}

// AdditionalContext is a hook's JSON reply adding text to the agent's context.
func AdditionalContext(eventName, context string) string {
	out, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     eventName,
			"additionalContext": context,
		},
	})
	return string(out)
}

// Identity is what the agent is told whenever it's drafted or re-told its name. The task command names the session
// explicitly, so it works even where ROSTR_SESSION isn't set (a session started before the hooks were installed).
func Identity(display, rosterName, sessionID string) string {
	return strings.Join([]string{
		fmt.Sprintf("You're %s for this session (drafted by rostr from the %q roster).", display, rosterName),
		fmt.Sprintf("- When you write somewhere other than this chat, sign it \"— %s\": Slack messages, emails, "+
			"PR descriptions, PR and issue comments.", display),
		"- Don't put the name in code, commit messages, or files.",
		"- Your user's own instructions about attribution or sign-offs take precedence over these.",
		fmt.Sprintf("- When your work changes, run: %s \"<new task>\"", taskCommand(sessionID)),
	}, "\n")
}

// TitleInstruction asks the agent to title the session and record the same summary as its task. Desktop and Remote
// Control sessions (with a bridge session ID) can set the app's title; terminal sessions only record the task.
func TitleInstruction(display, bridgeSessionID, sessionID string) string {
	if bridgeSessionID == "" {
		return fmt.Sprintf("Now run: %s \"<your 3-5 word summary of this conversation>\".", taskCommand(sessionID))
	}
	return fmt.Sprintf("Now call mcp__claude-code-remote__set_session_title with session_id %q and title "+
		"\"%s · <your 3-5 word summary of this conversation>\", then run: %s \"<the same summary>\".",
		bridgeSessionID, display, taskCommand(sessionID))
}

// taskCommand is the command the agent runs to set its task, naming its session.
func taskCommand(sessionID string) string {
	return "rostr agent task --session " + sessionID
}

// PlaceholderTask is the start of the first prompt, used as the task until the agent sets its own.
func PlaceholderTask(prompt string) string {
	const maxRunes = 50
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	line = strings.TrimSpace(line)
	if runes := []rune(line); len(runes) > maxRunes {
		return strings.TrimSpace(string(runes[:maxRunes])) + "…"
	}
	return line
}
