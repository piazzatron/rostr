package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/keepalive"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// defaultRewakeDelay is how long the draft step waits before waking Claude, so its first reply starts at normal speed.
// ROSTR_REWAKE_DELAY overrides it (tests set it to 0).
const defaultRewakeDelay = 5 * time.Second

// rewakeExitCode is the exit code with which an asyncRewake hook wakes Claude with its stderr.
const rewakeExitCode = 2

// newHookCommand is the hidden `rostr hook` command the installed hooks call. Hooks never block a session: failures
// are written to stderr and the hook still exits 0. The one exception is the prompt hook's exit 2, which wakes Claude.
func newHookCommand() *cobra.Command {
	hook := &cobra.Command{Use: "hook", Hidden: true}
	claudeCode := &cobra.Command{Use: "claude-code", Short: "Claude Code hooks"}
	hook.AddCommand(claudeCode)

	claudeCode.AddCommand(
		hookSubcommand("session-start", hookSessionStart),
		hookSubcommand("prompt", hookPrompt),
		hookSubcommand("session-end", hookSessionEnd),
		newHookDraftCommand(),
	)
	return hook
}

// hookSubcommand wraps a hook that reads Claude Code's JSON input from stdin.
func hookSubcommand(use string, run func(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error) *cobra.Command {
	return &cobra.Command{
		Use:  use,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := claudecode.ReadHookInput(cmd.InOrStdin())
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

	var d *store.Draft
	switch input.Source {
	case "resume":
		// Resume drafts (asking for the old name back) when auto-draft is on; with it off, it only gives back a name
		// the session already had, so sessions that were never drafted stay unnamed.
		// TODO(headless): headless `claude -p` runs shouldn't be drafted, but hook input has no reliable way to tell
		// them apart yet (see todo.md).
		settings, err := st.Settings(ctx)
		if err != nil {
			return err
		}
		if settings.AutoDraft {
			drafted, err := draftSession(ctx, st, input.SessionID, "", hookDir(input), "")
			if err != nil {
				return err
			}
			d = &drafted
		} else if d, err = st.Reclaim(ctx, input.SessionID); err != nil {
			return err
		}
	case "compact":
		// Never a new member after compaction: the active draft, or the last member if still free, or nothing
		if d, err = st.Reclaim(ctx, input.SessionID); err != nil {
			return err
		}
	}
	if d == nil {
		return nil
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	fmt.Fprintln(stdout, claudecode.AdditionalContext("SessionStart", claudecode.Identity(d.Display(), d.Roster)))
	return nil
}

// hookPrompt runs on every prompt, as an asyncRewake hook. On a session's first prompt (with auto-draft on) it drafts
// the session, waits, and exits 2 to wake Claude with who it is and an instruction to title the session. On every
// other prompt it keeps the draft alive, at most once an hour.
func hookPrompt(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	if !claudecode.IsFirstPrompt(input) {
		return keepDraftAlive(ctx, input.SessionID)
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// TODO(headless): headless `claude -p` runs shouldn't be drafted, but hook input has no reliable way to tell them
	// apart yet (see todo.md).
	settings, err := st.Settings(ctx)
	if err != nil || !settings.AutoDraft {
		return err
	}
	d, err := draftSession(ctx, st, input.SessionID, "", hookDir(input), claudecode.PlaceholderTask(input.Prompt))
	if err != nil {
		return err
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())

	// Wake Claude a few seconds in, once its first reply is under way
	bridgeSessionID := claudecode.BridgeSessionID(paths.ClaudeDir(), input.SessionID)
	time.Sleep(rewakeDelay())
	fmt.Fprintf(stderr, "%s\n\n%s Reply with nothing else, and don't mention the title or the task.\n",
		claudecode.Identity(d.Display(), d.Roster), claudecode.TitleInstruction(d.Display(), bridgeSessionID))
	return exitError{code: rewakeExitCode}
}

// hookSessionEnd releases the session's member.
func hookSessionEnd(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	keepalive.Forget(paths.KeepAliveDir(), input.SessionID)
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
			bridgeSessionID := claudecode.BridgeSessionID(paths.ClaudeDir(), session)
			fmt.Fprintf(out, "%s\n\n%s\n", claudecode.Identity(d.Display(), d.Roster),
				claudecode.TitleInstruction(d.Display(), bridgeSessionID))
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

func hookDir(input claudecode.HookInput) string {
	if input.Cwd != "" {
		return input.Cwd
	}
	return workingDir()
}

func rewakeDelay() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("ROSTR_REWAKE_DELAY")); err == nil {
		return d
	}
	return defaultRewakeDelay
}
