package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/codex"
	"github.com/piazzatron/rostr/cli/internal/keepalive"
	"github.com/piazzatron/rostr/cli/internal/pane"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// codexSessionEndBudget keeps release under the 3-second timeout Codex allows SessionEnd hooks, cloud call included.
const codexSessionEndBudget = 2 * time.Second

// hookCodexSessionStart brings the installed hooks up to date, drafts on resume and fork, and re-tells the name after compaction. A fork is a new thread
// that starts with its parent's history and name, so it gets its own member and its own thread name. Codex needs no
// session env var: it gives the agent's commands CODEX_THREAD_ID.
func hookCodexSessionStart(ctx context.Context, input codex.HookInput, stdout, stderr io.Writer) error {
	syncHooksIfStale()
	if input.Source != "resume" && input.Source != "compact" && input.Source != "fork" {
		return nil
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	d, err := draftOrReclaim(ctx, st, input.SessionID, hookDir(input.Cwd), input.Source != "compact")
	if err != nil || d == nil {
		return err
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	if input.Source == "fork" {
		_ = codex.SetThreadName(ctx, input.SessionID, codexThreadName(*d))
	}
	pane.Label(ctx, pane.Codex, d.Display())
	fmt.Fprintln(stdout, codex.AdditionalContext("SessionStart", claudecode.Identity(d.Display(), d.Roster, input.SessionID)))
	return nil
}

// hookCodexPrompt runs on every prompt. On a thread's first prompt (with auto-draft on) it drafts the session, names
// the thread, and adds who the agent is to its context. Codex hooks can't wake the agent later, so this happens
// before the first reply. On every other prompt it keeps the draft alive, at most once an hour.
func hookCodexPrompt(ctx context.Context, input codex.HookInput, stdout, stderr io.Writer) error {
	if !codex.IsFirstPrompt(input) {
		return codexLaterPrompt(ctx, input, stdout)
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	d, drafted, err := autoDraftSession(ctx, st, input.SessionID, hookDir(input.Cwd), claudecode.PlaceholderTask(input.Prompt))
	if err != nil || !drafted {
		return err
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	_ = codex.SetThreadName(ctx, input.SessionID, codexThreadName(d))
	pane.Label(ctx, pane.Codex, d.Display())
	fmt.Fprintln(stdout, codex.AdditionalContext("UserPromptSubmit", claudecode.Identity(d.Display(), d.Roster, input.SessionID)))
	return nil
}

// hookCodexSessionEnd releases the session's member and clears its pane's label, within Codex's SessionEnd timeout.
func hookCodexSessionEnd(ctx context.Context, input codex.HookInput, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, codexSessionEndBudget)
	defer cancel()

	keepalive.Forget(paths.KeepAliveDir(), input.SessionID)
	keepalive.Forget(paths.TurnDir(), input.SessionID)
	pane.Clear(ctx, pane.Codex)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.Bench(ctx, input.SessionID)
	return err
}

// newHookCodexDraftCommand is run by the $rostr-draft skill. The agent reads its output, so it prints plain text and
// never fails. Its session is CODEX_THREAD_ID, which Codex gives the agent's commands.
func newHookCodexDraftCommand() *cobra.Command {
	var session string
	cmd := &cobra.Command{
		Use:  "draft",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if session == "" {
				fmt.Fprintln(out, "rostr couldn't draft this session: it doesn't know the session's ID.")
				return nil
			}
			st, err := openStore()
			if err != nil {
				fmt.Fprintln(out, "rostr couldn't draft this session:", err)
				return nil
			}
			defer st.Close()

			d, err := hookDraftSession(cmd.Context(), st, session, workingDir(), "")
			if err != nil {
				fmt.Fprintln(out, "rostr couldn't draft this session:", err)
				return nil
			}
			_ = keepalive.Mark(paths.KeepAliveDir(), session, time.Now())
			_ = codex.SetThreadName(cmd.Context(), session, codexThreadName(d))
			pane.Label(cmd.Context(), pane.Codex, d.Display())
			fmt.Fprintln(out, claudecode.Identity(d.Display(), d.Roster, session))
			return nil
		},
	}
	sessionFlag(cmd, &session)
	return cmd
}

// codexThreadName is the Codex thread name for a draft: `<display> · <task>`, or just the display without a task.
func codexThreadName(d store.Draft) string {
	if d.Task == "" {
		return d.Display()
	}
	return d.Display() + " · " + d.Task
}

// codexLaterPrompt records that the thread's turn started and keeps its draft alive. When the thread lost its name
// while resting, it renames the thread and tells the agent.
func codexLaterPrompt(ctx context.Context, input codex.HookInput, stdout io.Writer) error {
	d, from, err := wakeAfterRest(ctx, pane.Codex, input.SessionID, hookDir(input.Cwd), true)
	if err != nil {
		return err
	}
	if d != nil {
		_ = codex.SetThreadName(ctx, input.SessionID, codexThreadName(*d))
		fmt.Fprintln(stdout, codex.AdditionalContext("UserPromptSubmit", claudecode.RenamedAfterRest(d.Display(), from)))
	}
	return keepDraftAlive(ctx, input.SessionID)
}

// hookCodexTurnEnd is Codex's Stop hook, which records that the thread's turn ended.
func hookCodexTurnEnd(ctx context.Context, input codex.HookInput, stdout, stderr io.Writer) error {
	return recordTurnEnd(ctx, input.SessionID)
}

// hookCodexWorking is Codex's PostToolUse hook: a tool call reports the turn's start in the background, if nothing
// has since the last turn ended (see hookWorking).
func hookCodexWorking(ctx context.Context, input codex.HookInput, stdout, stderr io.Writer) error {
	_, _, err := wakeAfterRest(ctx, pane.Codex, input.SessionID, hookDir(input.Cwd), false)
	return err
}
