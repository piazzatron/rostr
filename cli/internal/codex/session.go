// Package codex holds what rostr knows about Codex: its hook input and output shapes, how to tell a thread's first
// prompt, setting a thread's name through the Codex app-server, and installing the hooks and skills.
package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// HookInput is the JSON Codex sends a hook on stdin. Fields a given event doesn't send stay empty. session_id is the
// thread ID, which Codex also gives the agent's commands as CODEX_THREAD_ID.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Source         string `json:"source"` // SessionStart: startup, resume, clear, compact, or fork
	Prompt         string `json:"prompt"`
	TurnID         string `json:"turn_id"`
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

// IsFirstPrompt reports whether the thread's rollout holds no earlier prompt from the user, so threads that existed
// before rostr was installed (or forks, which carry their parent's history) are never drafted mid-conversation. Codex
// runs the hook before it records the prompt, but a recorded copy of this same prompt is allowed for. A missing
// rollout (a new thread) counts as a first prompt.
func IsFirstPrompt(input HookInput) bool {
	file, err := os.Open(input.TranscriptPath)
	if err != nil {
		return true
	}
	defer file.Close()

	var prompts []string
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Type == "event_msg" && entry.Payload.Type == "user_message" {
			prompts = append(prompts, entry.Payload.Message)
		}
		if err != nil {
			break
		}
	}
	return len(prompts) == 0 || (len(prompts) == 1 && prompts[0] == input.Prompt)
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
