package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude stands in for claude -p: it prints the JSON result given, after checking it was called headless.
func fakeClaude(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROSTR_CLAUDE_BIN", path)
}

func TestSummarizeRunsClaudeHeadlessAndCleansTheTitle(t *testing.T) {
	fakeClaude(t, `case "$*" in *"-p"*"--model haiku"*"--output-format json"*) ;; *) echo "bad args: $*" >&2; exit 2 ;; esac
input=$(cat)
case "$input" in *"<prompt>"*"what is the oldest documented person"*) ;; *) echo "bad input" >&2; exit 2 ;; esac
printf '{"type":"result","is_error":false,"result":"\\"Oldest documented person.\\"","total_cost_usd":0}\n'`)
	summary, err := Summarize(context.Background(), "what is the oldest documented person")
	if err != nil || summary != "Oldest documented person" {
		t.Fatalf("got %q, %v", summary, err)
	}
}

func TestSummarizeFailsCleanly(t *testing.T) {
	for name, script := range map[string]string{
		"not logged in": `echo "Not logged in" >&2; exit 1`,
		"error result":  `printf '{"is_error":true,"result":"usage limit reached"}\n'`,
		"not json":      `echo hello`,
		"several lines": `printf '{"result":"One\nTwo"}\n'`,
		"empty":         `printf '{"result":"  "}\n'`,
		"far too long":  `printf '{"result":"%s"}\n' "$(printf 'a%.0s' $(seq 1 100))"`,
	} {
		t.Run(name, func(t *testing.T) {
			fakeClaude(t, script)
			if summary, err := Summarize(context.Background(), "anything"); err == nil {
				t.Fatalf("got %q, want an error", summary)
			}
		})
	}
	if _, err := Summarize(context.Background(), "   "); err == nil {
		t.Fatal("summarized nothing")
	}
}

func TestUserPromptSkipsSystemReminders(t *testing.T) {
	prompt := "<system-reminder>\nstuff\n</system-reminder>\n<system-reminder>more</system-reminder>\n  Fix the login bug\nplease"
	if got := UserPrompt(prompt); !strings.HasPrefix(got, "Fix the login bug") {
		t.Fatalf("got %q", got)
	}
	if got := UserPrompt("<system-reminder>unterminated"); got != "" {
		t.Fatalf("got %q", got)
	}
}
