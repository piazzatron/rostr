package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/codex"
	"github.com/piazzatron/rostr/cli/internal/keepalive"
	"github.com/piazzatron/rostr/cli/internal/pane"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// keepGoingExitCode is the exit code with which a Stop hook keeps Claude going, with its stderr as the instruction.
const keepGoingExitCode = 2

// newHookCommand is the hidden `rostr hook` command the installed hooks call. Hooks never block a session: failures
// are written to stderr and the hook still exits 0. The one exception is the Claude Code title step's exit 2, which
// keeps Claude going.
func newHookCommand() *cobra.Command {
	hook := &cobra.Command{Use: "hook", Hidden: true}
	claudeCode := &cobra.Command{Use: "claude-code", Short: "Claude Code hooks"}
	codexHooks := &cobra.Command{Use: "codex", Short: "Codex hooks"}
	hook.AddCommand(claudeCode, codexHooks)

	claudeCode.AddCommand(
		hookSubcommand("session-start", claudecode.ReadHookInput, hookSessionStart),
		hookSubcommand("prompt", claudecode.ReadHookInput, hookPrompt),
		hookSubcommand("title", claudecode.ReadHookInput, hookTitle),
		hookSubcommand("title-set", claudecode.ReadHookInput, hookTitleSet),
		hookSubcommand("session-end", claudecode.ReadHookInput, hookSessionEnd),
		newHookDraftCommand(),
	)
	codexHooks.AddCommand(
		hookSubcommand("session-start", codex.ReadHookInput, hookCodexSessionStart),
		hookSubcommand("prompt", codex.ReadHookInput, hookCodexPrompt),
		hookSubcommand("session-end", codex.ReadHookInput, hookCodexSessionEnd),
		newHookCodexDraftCommand(),
	)
	return hook
}

// hookSubcommand wraps a hook that reads the agent's JSON input from stdin.
func hookSubcommand[Input any](use string, read func(io.Reader) (Input, error), run func(ctx context.Context, input Input, stdout, stderr io.Writer) error) *cobra.Command {
	return &cobra.Command{
		Use:  use,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := read(cmd.InOrStdin())
			if err == nil {
				err = run(cmd.Context(), input, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			if _, isExit := err.(exitError); isExit {
				return err
			}
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "rostr:", err)
			}
			return nil
		},
	}
}

// hookSessionStart writes ROSTR_SESSION for every session, drafts on resume, and re-tells the name after compaction.
func hookSessionStart(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	if err := writeSessionEnv(input.SessionID); err != nil {
		fmt.Fprintln(stderr, "rostr: writing CLAUDE_ENV_FILE:", err)
	}
	if input.Source != "resume" && input.Source != "compact" {
		return nil
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// TODO(headless): headless `claude -p` runs shouldn't be drafted on resume, but hook input has no reliable way to
	// tell them apart yet (see todo.md).
	d, err := draftOrReclaim(ctx, st, input.SessionID, hookDir(input.Cwd), input.Source == "resume")
	if err != nil || d == nil {
		return err
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	pane.Label(ctx, d.Display())
	fmt.Fprintln(stdout, claudecode.AdditionalContext("SessionStart", claudecode.Identity(d.Display(), d.Roster, input.SessionID)))
	return nil
}

// hookPrompt runs before Claude sees each prompt. On the first, it drafts the session, tells the agent its name, and
// titles the session with the start of the prompt; later prompts keep the draft alive and the title in step with the
// agent's task.
func hookPrompt(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	if !claudecode.IsFirstPrompt(input) {
		return laterPrompt(ctx, input, stdout)
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// A session that already has a name isn't on its first prompt
	if d, err := st.SessionDraft(ctx, input.SessionID); err != nil || d != nil {
		st.Close()
		if err != nil {
			return err
		}
		return laterPrompt(ctx, input, stdout)
	}

	// TODO(headless): headless `claude -p` runs shouldn't be drafted, but hook input has no reliable way to tell them
	// apart yet (see todo.md).
	settings, err := st.Settings(ctx)
	if err != nil || !settings.AutoDraft {
		return err
	}
	placeholder := claudecode.PlaceholderTask(input.Prompt)
	d, err := draftSession(ctx, st, input.SessionID, "", hookDir(input.Cwd), placeholder)
	if err != nil {
		return err
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	markTitlePending(input.SessionID, d.Display())
	pane.Label(ctx, d.Display())
	fmt.Fprintln(stdout, claudecode.PromptOutput(claudecode.Identity(d.Display(), d.Roster, input.SessionID),
		claudecode.Title(d.Display(), placeholder)))
	return nil
}

// laterPrompt keeps the draft alive and the title in step with the agent's task.
func laterPrompt(ctx context.Context, input claudecode.HookInput, stdout io.Writer) error {
	if err := keepDraftAlive(ctx, input.SessionID); err != nil {
		return err
	}
	return syncTitleWithTask(ctx, input, stdout)
}

// syncTitleWithTask retitles the session "<name> · <task>" when the agent's task has changed since the title was set.
func syncTitleWithTask(ctx context.Context, input claudecode.HookInput, stdout io.Writer) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.SessionDraft(ctx, input.SessionID)
	if err != nil || d == nil || d.Task == "" {
		return err
	}
	if title := claudecode.Title(d.Display(), d.Task); title != input.SessionTitle {
		fmt.Fprintln(stdout, claudecode.PromptOutput("", title))
	}
	return nil
}

// hookTitle is the Stop hook, run whenever Claude finishes a turn. In a session drafted on its first prompt, once the
// first turn finishes, it keeps Claude going to summarize the conversation as its task and, in the desktop app and
// cloud sessions, its title. An interrupted turn doesn't end in Stop, so the step waits for the next turn that finishes.
// It reads the agent's name from the pending marker, so it never calls the API.
func hookTitle(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	// Claude is already continuing because of a Stop hook, or the title step isn't due
	if input.StopHookActive {
		return nil
	}
	display := pendingTitleDisplay(input.SessionID)
	if display == "" {
		return nil
	}

	// Claude Code doesn't let a turn end with no visible text (it asks Claude to continue, and Claude repeats its
	// answer), so the agent ends on the new title
	if err := os.Remove(titlePendingFile(input.SessionID)); err != nil {
		return err
	}
	serverSessionID := claudecode.ServerSessionID(paths.ClaudeDir(), input.SessionID)
	fmt.Fprintf(stderr, "%s Then end your turn by writing only the new title, on one line: don't repeat your answer.\n",
		claudecode.TitleInstruction(display, serverSessionID, input.SessionID))
	return exitError{code: keepGoingExitCode}
}

// hookTitleSet is the PostToolUse hook on set_session_title. When the agent titles the session "<name> · <summary>",
// it records the summary as the agent's task, so the title step takes one tool call. Other titles are left alone.
func hookTitleSet(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.SessionDraft(ctx, input.SessionID)
	if err != nil || d == nil {
		return err
	}
	task, ok := claudecode.TaskFromTitle(d.Display(), input.ToolInput.Title)
	if !ok || task == d.Task {
		return nil
	}
	_, err = st.SetTask(ctx, d.Key(), task)
	return err
}

// hookSessionEnd releases the session's member and clears its pane's label.
func hookSessionEnd(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	keepalive.Forget(paths.KeepAliveDir(), input.SessionID)
	_ = os.Remove(titlePendingFile(input.SessionID))
	pane.Clear(ctx)
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.Release(ctx, input.SessionID)
	return err
}

// newHookDraftCommand is run by the /draft slash command, whose output becomes part of the prompt Claude sees, so it
// prints plain text and never fails.
func newHookDraftCommand() *cobra.Command {
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

			d, err := draftSession(cmd.Context(), st, session, "", workingDir(), "")
			if err != nil {
				fmt.Fprintln(out, "rostr couldn't draft this session:", err)
				return nil
			}
			_ = keepalive.Mark(paths.KeepAliveDir(), session, time.Now())
			pane.Label(cmd.Context(), d.Display())
			serverSessionID := claudecode.ServerSessionID(paths.ClaudeDir(), session)
			fmt.Fprintf(out, "%s\n\n%s\n", claudecode.Identity(d.Display(), d.Roster, session),
				claudecode.TitleInstruction(d.Display(), serverSessionID, session))
			return nil
		},
	}
	sessionFlag(cmd, &session)
	return cmd
}

// keepDraftAlive bumps the session's draft at most once an hour. If the draft has expired, it reclaims the same
// member when free, and otherwise leaves the session unnamed.
func keepDraftAlive(ctx context.Context, sessionID string) error {
	now := time.Now()
	if !keepalive.Due(paths.KeepAliveDir(), sessionID, now) {
		return nil
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.Reclaim(ctx, sessionID); err != nil {
		return err
	}
	return keepalive.Mark(paths.KeepAliveDir(), sessionID, now)
}

// draftOrReclaim is how a session start finds its name. With draftNew (a resume, or a Codex fork) and auto-draft on,
// it drafts, asking for the session's old name back. Otherwise (compaction, or auto-draft off) it never drafts a new
// member: the active draft, or the last member if still free, or nil, so sessions never drafted stay unnamed.
func draftOrReclaim(ctx context.Context, st store.Store, sessionID, dir string, draftNew bool) (*store.Draft, error) {
	if draftNew {
		settings, err := st.Settings(ctx)
		if err != nil {
			return nil, err
		}
		if settings.AutoDraft {
			d, err := draftSession(ctx, st, sessionID, "", dir, "")
			if err != nil {
				return nil, err
			}
			return &d, nil
		}
	}
	return st.Reclaim(ctx, sessionID)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// writeSessionEnv appends `export ROSTR_SESSION=<id>` to CLAUDE_ENV_FILE, so the agent's rostr commands know their
// session.
func writeSessionEnv(sessionID string) error {
	path := os.Getenv("CLAUDE_ENV_FILE")
	if path == "" {
		return nil
	}
	value := sessionID
	if !shellSafe.MatchString(value) {
		value = "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "export ROSTR_SESSION=%s\n", value)
	return err
}

// markTitlePending records that the session's title step is due once its first turn finishes, keeping the agent's
// display name for it.
func markTitlePending(sessionID, display string) {
	if !shellSafe.MatchString(sessionID) || os.MkdirAll(paths.TitlePendingDir(), 0o755) != nil {
		return
	}
	_ = os.WriteFile(titlePendingFile(sessionID), []byte(display), 0o644)
}

// pendingTitleDisplay returns the agent's display name while the session's title step is due, or "".
func pendingTitleDisplay(sessionID string) string {
	if !shellSafe.MatchString(sessionID) {
		return ""
	}
	data, _ := os.ReadFile(titlePendingFile(sessionID))
	return strings.TrimSpace(string(data))
}

func titlePendingFile(sessionID string) string {
	return filepath.Join(paths.TitlePendingDir(), sessionID)
}

// hookDir is the session's directory, from the hook input's cwd.
func hookDir(cwd string) string {
	if cwd != "" {
		return cwd
	}
	return workingDir()
}
