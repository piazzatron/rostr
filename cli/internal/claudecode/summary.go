package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// summaryPromptLimit caps what's sent to be summarized; the start of a prompt says what it's about.
const summaryPromptLimit = 4000

// Summarize asks Claude Code, headless with Haiku and the user's own login, for a 3-5 word title for a prompt: the
// summary a terminal session's title gets, since the terminal has no title tool for the agent. It runs with hooks,
// settings, tools, and MCP servers off, so nothing of the user's setup (rostr's hooks included) runs for it. The
// binary is ROSTR_CLAUDE_BIN, or claude on PATH. Any failure, including a used-up plan, is an error, and the caller
// leaves the title as it was.
func Summarize(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) > summaryPromptLimit {
		prompt = prompt[:summaryPromptLimit]
	}
	if prompt == "" {
		return "", errors.New("nothing to summarize")
	}
	input := "Summarize what the user wants in 3 to 5 words, as a title: capitalize the first word, no period, no " +
		"quotes. Keep technical terms and code identifiers as written. Reply with the title only.\n\n<prompt>\n" +
		prompt + "\n</prompt>\n"

	binary := os.Getenv("ROSTR_CLAUDE_BIN")
	if binary == "" {
		binary = "claude"
	}
	cmd := exec.CommandContext(ctx, binary, "-p", "--model", "haiku", "--no-session-persistence",
		"--setting-sources", "", "--settings", `{"disableAllHooks":true}`, "--strict-mcp-config",
		"--disable-slash-commands", "--tools", "", "--output-format", "json")
	cmd.Stdin = strings.NewReader(input)
	cmd.WaitDelay = 2 * time.Second // so a process left holding the output can't keep this one alive
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// claude -p reports most failures in its JSON output, not on stderr
		return "", fmt.Errorf("claude -p: %w: %.300s %.300s", err, strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String()))
	}

	var result struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", fmt.Errorf("claude -p: unexpected output: %.200s", stdout.String())
	}
	if result.IsError {
		return "", fmt.Errorf("claude -p: %.200s", result.Result)
	}
	return cleanSummary(result.Result)
}

// cleanSummary tidies a model's title and rejects anything that isn't one short line.
func cleanSummary(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimRight(s, ".")
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "\n\r") || utf8.RuneCountInString(s) > 80 {
		return "", fmt.Errorf("not a title: %.200q", s)
	}
	return s, nil
}
