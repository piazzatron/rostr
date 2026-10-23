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

// EndsPlainAnswer reports whether a title call just finished the turn: Claude has written its answer, and every tool
// call since that text was titling (the ToolSearch that loaded a title tool, a title tool, or the agent recording its
// task). Ending the turn there spares Claude a reply to the tool's result. Work before the answer is fine; any other
// tool call after it means Claude is still working, so the turn goes on. When it doesn't end the turn, reason says
// why, since Claude then replies to the tool's result, usually by repeating its answer.
func EndsPlainAnswer(transcriptPath, toolUseID string) (ends bool, reason string) {
	if toolUseID == "" {
		return false, "no tool_use_id"
	}
	deadline := time.Now().Add(transcriptWriteWait)
	for {
		data, err := os.ReadFile(transcriptPath)
		if err != nil {
			return false, "transcript unreadable: " + err.Error()
		}
		done, written, reason := plainAnswerSoFar(data, toolUseID)
		if written || time.Now().After(deadline) {
			return done, reason
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// plainAnswerSoFar checks the current turn in a transcript, reporting whether its answer is finished and whether the
// message holding the tool call has been written yet.
func plainAnswerSoFar(transcript []byte, toolUseID string) (done, written bool, reason string) {
	hasText, workSinceText := false, false
	var work string
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
				hasText, workSinceText, written = false, false, false
			}
		case "assistant":
			if json.Unmarshal(entry.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				switch {
				case b.Type == "text" && strings.TrimSpace(b.Text) != "":
					hasText, workSinceText = true, false
				case b.Type == "tool_use" && b.ID == toolUseID:
					written = true
				case b.Type == "tool_use" && !b.isTitling():
					workSinceText, work = true, b.Name
				}
			}
		}
	}
	switch {
	case !written:
		reason = "the title call isn't in the transcript yet"
	case !hasText:
		reason = "no answer text before the title call"
	case workSinceText:
		reason = "a tool call after the answer text: " + work
	}
	return written && hasText && !workSinceText, written, reason
}

type transcriptBlock struct {
	Type, ID, Name, Text string
	Input                struct{ Command string }
}

// isTitling reports whether a tool call is part of titling the session rather than the work. Any ToolSearch
// counts: after the answer it's the agent loading the title tool, even when its query doesn't name it.
func (b transcriptBlock) isTitling() bool {
	switch b.Name {
	case LocalTitleTool, RemoteTitleTool, "ToolSearch":
		return true
	case "Bash":
		return IsTaskCommand(b.Input.Command)
	}
	return false
}
