package claudecode

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// transcriptLines builds a transcript: "prompt:<text>", "text:<text>", "tool:<id>:<name>:<input json>", "result".
func transcriptLines(t *testing.T, lines ...string) string {
	t.Helper()
	var out []string
	for _, l := range lines {
		kind, rest, _ := strings.Cut(l, ":")
		var entry map[string]any
		switch kind {
		case "prompt":
			entry = map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": rest}}}}
		case "text":
			entry = map[string]any{"type": "assistant", "message": map[string]any{"id": "m", "content": []any{map[string]any{"type": "text", "text": rest}}}}
		case "tool":
			parts := strings.SplitN(rest, ":", 3)
			var input any
			json.Unmarshal([]byte(parts[2]), &input)
			entry = map[string]any{"type": "assistant", "message": map[string]any{"id": "m", "content": []any{
				map[string]any{"type": "tool_use", "id": parts[0], "name": parts[1], "input": input}}}}
		case "result":
			entry = map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "ok"}}}}
		}
		data, _ := json.Marshal(entry)
		out = append(out, string(data))
	}
	return strings.Join(out, "\n") + "\n"
}

func TestEndsPlainAnswer(t *testing.T) {
	search := `{"query":"select:` + LocalTitleTool + `"}`
	title := `{"session_id":"self","title":"🧹 Ginny · Deepest lake"}`
	for _, c := range []struct {
		name  string
		lines []string
		want  bool
	}{
		{"answer, then the title tool loaded and called", []string{"prompt:How deep is Baikal?", "text:About 1,642 m.",
			"tool:t1:ToolSearch:" + search, "result", "tool:t2:" + LocalTitleTool + ":" + title}, true},
		{"answer and the remote title call", []string{"prompt:How deep is Baikal?", "text:About 1,642 m.",
			"tool:t2:" + RemoteTitleTool + ":" + title}, true},
		{"answer and the task command", []string{"prompt:How deep is Baikal?", "text:About 1,642 m.",
			`tool:t2:Bash:{"command":"rostr task --session s1 \"Deepest lake\""}`}, true},
		{"title call alongside real work", []string{"prompt:List the newest files", "tool:t1:ToolSearch:" + search, "result",
			"tool:t2:" + LocalTitleTool + ":" + title, `tool:t3:Bash:{"command":"ls -lt"}`}, false},
		{"title call before any answer", []string{"prompt:How deep is Baikal?", "tool:t2:" + RemoteTitleTool + ":" + title}, false},
		{"work, then the answer, then the title call", []string{"prompt:Check the logs", `tool:t0:Bash:{"command":"tail log"}`, "result",
			"text:The logs are clean.", "tool:t2:" + RemoteTitleTool + ":" + title}, true},
		{"text, then more work, then the title call", []string{"prompt:Check the logs", "text:Let me look.",
			`tool:t0:Bash:{"command":"tail log"}`, "result", "tool:t2:" + RemoteTitleTool + ":" + title}, false},
		{"an earlier turn's work doesn't count", []string{"prompt:Check the logs", `tool:t0:Bash:{"command":"tail log"}`, "result",
			"text:Clean.", "prompt:How deep is Baikal?", "text:About 1,642 m.", "tool:t2:" + RemoteTitleTool + ":" + title}, true},
		{"a keyword ToolSearch after the answer is titling", []string{"prompt:How deep is Baikal?", "text:About 1,642 m.",
			`tool:t1:ToolSearch:{"query":"session title"}`, "result", "tool:t2:" + RemoteTitleTool + ":" + title}, true},
	} {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		write(t, path, transcriptLines(t, c.lines...))
		if got, reason := EndsPlainAnswer(path, "t2"); got != c.want || (!got && reason == "") {
			t.Errorf("%s: got %v (%q)", c.name, got, reason)
		}
	}

	// A call that isn't in the transcript (yet) never ends the turn, and says so
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	write(t, path, transcriptLines(t, "prompt:How deep is Baikal?", "text:About 1,642 m."))
	if ends, reason := EndsPlainAnswer(path, "t2"); ends || !strings.Contains(reason, "isn't in the transcript") {
		t.Errorf("without the call: %v, %q", ends, reason)
	}
	if ends, _ := EndsPlainAnswer(filepath.Join(t.TempDir(), "missing"), "t2"); ends {
		t.Error("ended the turn without a transcript")
	}
	if ends, _ := EndsPlainAnswer(path, ""); ends {
		t.Error("ended the turn without a tool_use_id")
	}
	// The reason names the work that followed the answer
	write(t, path, transcriptLines(t, "prompt:Check the logs", "text:Let me look.", `tool:t0:Bash:{"command":"tail log"}`, "result",
		"tool:t2:"+RemoteTitleTool+":"+title))
	if _, reason := EndsPlainAnswer(path, "t2"); !strings.Contains(reason, "Bash") {
		t.Errorf("reason: %q", reason)
	}
}
