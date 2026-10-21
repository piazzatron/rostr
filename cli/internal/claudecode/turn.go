package claudecode

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// transcriptWriteWait is how long EndsPlainAnswer waits for Claude Code to write the message holding the tool call. It
// writes a message once the message is complete, which can be after the message's first tool call has run.
const transcriptWriteWait = 500 * time.Millisecond

// EndsPlainAnswer reports whether a title call just made the turn a finished plain answer: Claude has written text,
// and every tool call since the prompt was the ToolSearch that loaded a title tool, a title tool, or the agent
// recording its task. Ending the turn there spares Claude a reply to the tool's result. Any other tool call means
// Claude is working, so the turn goes on.
func EndsPlainAnswer(transcriptPath, toolUseID string) bool {
	if toolUseID == "" {
		return false
	}
	deadline := time.Now().Add(transcriptWriteWait)
	for {
		data, err := os.ReadFile(transcriptPath)
		if err != nil {
			return false
		}
		done, written := plainAnswerSoFar(data, toolUseID)
		if written || time.Now().After(deadline) {
			return done
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// plainAnswerSoFar checks the current turn in a transcript, reporting whether it's a plain answer and whether the
// message holding the tool call has been written yet.
func plainAnswerSoFar(transcript []byte, toolUseID string) (done, written bool) {
	hasText, working := false, false
	for _, line := range strings.Split(string(transcript), "\n") {
		var entry struct {
			Type    string
			Message struct {
				Content json.RawMessage
			}
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		var blocks []transcriptBlock
		switch entry.Type {
		case "user":
			// A prompt (text, not tool results) starts a new turn
			var text string
			isPrompt := json.Unmarshal(entry.Message.Content, &text) == nil ||
				json.Unmarshal(entry.Message.Content, &blocks) == nil && len(blocks) > 0 && blocks[0].Type == "text"
			if isPrompt {
				hasText, working, written = false, false, false
			}
		case "assistant":
			if json.Unmarshal(entry.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				switch {
				case b.Type == "text" && strings.TrimSpace(b.Text) != "":
					hasText = true
				case b.Type == "tool_use" && b.ID == toolUseID:
					written = true
				case b.Type == "tool_use" && !b.isTitling():
					working = true
				}
			}
		}
	}
	return written && hasText && !working, written
}

type transcriptBlock struct {
	Type, ID, Name, Text string
	Input                struct{ Query, Command string }
}

// isTitling reports whether a tool call is part of titling the session rather than the work.
func (b transcriptBlock) isTitling() bool {
	switch b.Name {
	case LocalTitleTool, RemoteTitleTool:
		return true
	case "ToolSearch":
		return strings.Contains(b.Input.Query, LocalTitleTool) || strings.Contains(b.Input.Query, RemoteTitleTool)
	case "Bash":
		return IsTaskCommand(b.Input.Command)
	}
	return false
}
