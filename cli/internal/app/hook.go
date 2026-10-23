package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/codex"
	"github.com/piazzatron/rostr/cli/internal/keepalive"
	"github.com/piazzatron/rostr/cli/internal/pane"
	"github.com/piazzatron/rostr/cli/internal/paths"
	"github.com/piazzatron/rostr/cli/internal/project"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// newHookCommand is the hidden `rostr hook` command the installed hooks call. Hooks never block a session: failures
// are written to stderr and the hook still exits 0.
func newHookCommand() *cobra.Command {
	hook := &cobra.Command{Use: "hook", Hidden: true}
	claudeCode := &cobra.Command{Use: "claude-code", Short: "Claude Code hooks"}
	codexHooks := &cobra.Command{Use: "codex", Short: "Codex hooks"}
	hook.AddCommand(claudeCode, codexHooks, newHookConfirmDraftCommand(), newHookSummarizeCommand())

	claudeCode.AddCommand(
		hookSubcommand("session-start", claudecode.ReadHookInput, hookSessionStart),
		hookSubcommand("prompt", claudecode.ReadHookInput, hookPrompt),
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

// hookPrompt runs before Claude sees each prompt. On the first, it drafts the session, tells the agent its name and to
// title the session in its reply, and titles the session with the start of the prompt for now; later prompts keep the
// draft alive and the title in step with the agent's task.
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
	if d, err := knownSessionDraft(ctx, st, input.SessionID); err != nil || d != nil {
		st.Close()
		if err != nil {
			return err
		}
		return laterPrompt(ctx, input, stdout)
	}

	// TODO(headless): headless `claude -p` runs shouldn't be drafted, but hook input has no reliable way to tell them
	// apart yet (see todo.md).
	placeholder := claudecode.PlaceholderTask(input.Prompt)
	d, drafted, err := autoDraftSession(ctx, st, input.SessionID, hookDir(input.Cwd), placeholder)
	if err != nil || !drafted {
		return err
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	pane.Label(ctx, d.Display())

	// The agent titles the session with its summary where it has a title tool; in a terminal, rostr summarizes the
	// prompt in the background and the next prompt's hook puts it in the title
	where := titleWhere(input.SessionID)
	context := claudecode.Identity(d.Display(), d.Roster, input.SessionID)
	if instruction := claudecode.TitleInstruction(d.Display(), input.SessionID, where); instruction != "" {
		context += "\n" + instruction
	}
	fmt.Fprintln(stdout, claudecode.PromptOutput(context, claudecode.Title(d.Display(), placeholder)))
	if where.IsTerminal() {
		_ = spawnHook(summarizeInput{SessionID: input.SessionID, Prompt: claudecode.UserPrompt(input.Prompt), Placeholder: placeholder},
			"hook", "summarize")
	}
	return nil
}

// laterPrompt keeps the draft alive and the title in step with the agent's task.
func laterPrompt(ctx context.Context, input claudecode.HookInput, stdout io.Writer) error {
	if err := keepDraftAlive(ctx, input.SessionID); err != nil {
		return err
	}
	return syncTitleWithTask(ctx, input, stdout)
}

// syncTitleWithTask retitles the session "<name> · <task>" when the agent's task has changed since the title was set,
// and tells the agent when the server confirmed a different name than the one it was told (see store.Cloud).
func syncTitleWithTask(ctx context.Context, input claudecode.HookInput, stdout io.Writer) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.SessionDraft(ctx, input.SessionID)
	if err != nil || d == nil {
		return err
	}
	var context, title string
	if d.RenamedFrom != "" {
		context = claudecode.Renamed(d.Display(), d.RenamedFrom)
		if cloud, ok := st.(*store.Cloud); ok {
			cloud.MarkAnnounced(input.SessionID)
		}
	}
	if d.Task != "" {
		if t := claudecode.Title(d.Display(), d.Task); t != input.SessionTitle {
			title = t
		}
	}
	if context != "" || title != "" {
		fmt.Fprintln(stdout, claudecode.PromptOutput(context, title))
	}
	return nil
}

// hookTitleSet is the PostToolUse hook on the title tools and Bash. When the agent titles the session
// "<name> · <summary>", it records the summary as the agent's task. When the title call (or the agent recording its
// task) came after the answer, it ends the turn, so Claude doesn't have to reply to the tool's result: Claude Code
// doesn't let a turn end without visible text, so that reply would repeat the answer. When a title call came before
// the answer, it tells Claude to answer the user rather than report the rename.
func hookTitleSet(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	isTaskCommand := claudecode.IsTaskCommand(input.ToolInput.Command)
	if input.ToolName == "Bash" && !isTaskCommand {
		return nil
	}
	// Every time the turn isn't ended, Claude replies to the tool's result, so the reason goes in the log
	if ends, reason := claudecode.EndsPlainAnswer(input.TranscriptPath, input.ToolUseID); ends {
		fmt.Fprintln(stdout, `{"continue": false, "suppressOutput": true}`)
	} else {
		logBackground("title-set for session %s didn't end the turn: %s", input.SessionID, reason)
		if !isTaskCommand {
			fmt.Fprintln(stdout, claudecode.AdditionalContext("PostToolUse", claudecode.TitleSetContext))
		}
	}
	if isTaskCommand {
		return nil
	}

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

			d, err := fastDraftSession(cmd.Context(), st, session, workingDir(), "")
			if err != nil {
				fmt.Fprintln(out, "rostr couldn't draft this session:", err)
				return nil
			}
			_ = keepalive.Mark(paths.KeepAliveDir(), session, time.Now())
			pane.Label(cmd.Context(), d.Display())
			fmt.Fprintln(out, claudecode.Identity(d.Display(), d.Roster, session))
			if instruction := claudecode.TitleInstruction(d.Display(), session, titleWhere(session)); instruction != "" {
				fmt.Fprintf(out, "\n%s\n", instruction)
			}
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
		// Not a fast draft: only the server knows whether the session's old name is still free
		repo, _ := project.Identify(dir)
		d, err := st.Draft(ctx, store.DraftRequest{SessionID: sessionID, Repo: repo, IfAutoDraft: true})
		if errors.Is(err, store.ErrAutoDraftOff) {
			return st.Reclaim(ctx, sessionID)
		}
		if err != nil {
			return nil, err
		}
		return &d, nil
	}
	return st.Reclaim(ctx, sessionID)
}

// knownSessionDraft is the session's draft as this machine knows it: in cloud mode from the session's cache alone,
// since every draft this machine makes is cached there and a session can't have been drafted from another machine,
// so the first prompt makes no call to find that out.
func knownSessionDraft(ctx context.Context, st store.Store, sessionID string) (*store.Draft, error) {
	if cloud, ok := st.(*store.Cloud); ok {
		return cloud.CachedSessionDraft(sessionID), nil
	}
	return st.SessionDraft(ctx, sessionID)
}

// fastDraftSession drafts for a hook: in cloud mode from the local cache when it can, confirming with the server in
// the background, so the session isn't kept waiting.
func fastDraftSession(ctx context.Context, st store.Store, sessionID, dir, task string) (store.Draft, error) {
	repo, _ := project.Identify(dir)
	return st.Draft(ctx, store.DraftRequest{SessionID: sessionID, Repo: repo, Task: task, Fast: true})
}

// autoDraftSession is fastDraftSession when auto-draft is on; drafted is false when it's off.
func autoDraftSession(ctx context.Context, st store.Store, sessionID, dir, task string) (d store.Draft, drafted bool, err error) {
	repo, _ := project.Identify(dir)
	d, err = st.Draft(ctx, store.DraftRequest{SessionID: sessionID, Repo: repo, Task: task, Fast: true, IfAutoDraft: true})
	if errors.Is(err, store.ErrAutoDraftOff) {
		return store.Draft{}, false, nil
	}
	return d, err == nil, err
}

// newHookConfirmDraftCommand is `rostr hook confirm-draft`, which a Fast draft starts in the background to confirm
// its pick with the server. It reads the store.Confirmation as JSON from stdin and prints nothing.
func newHookConfirmDraftCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "confirm-draft",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var conf store.Confirmation
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&conf); err != nil {
				return nil
			}
			st, err := openStore()
			if err != nil {
				return nil
			}
			defer st.Close()
			if cloud, ok := st.(*store.Cloud); ok {
				_, _ = cloud.ConfirmDraft(cmd.Context(), conf)
			}
			return nil
		},
	}
}

// logBackground appends a line to the background log, for what hooks decide where nobody sees their output.
func logBackground(format string, args ...any) {
	_ = os.MkdirAll(filepath.Dir(paths.BackgroundLog()), 0o700)
	f, err := os.OpenFile(paths.BackgroundLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s rostr: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// spawnConfirmDraft starts `rostr hook confirm-draft` in the background.
func spawnConfirmDraft(conf store.Confirmation) error {
	return spawnHook(conf, "hook", "confirm-draft")
}

// spawnHook starts rostr with args detached from this process, with input as JSON on its stdin, so a hook can exit
// while the work goes on. What the child writes to stderr goes to ~/.cache/rostr/background.log, since nobody else
// would see it.
func spawnHook(input any, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = os.MkdirAll(filepath.Dir(paths.BackgroundLog()), 0o700)
	if log, err := os.OpenFile(paths.BackgroundLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		defer log.Close()
		cmd.Stderr = log
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err = json.NewEncoder(stdin).Encode(input)
	stdin.Close()
	_ = cmd.Process.Release()
	return err
}

// summarizeInput is what `rostr hook summarize` reads: the session, the prompt to summarize, and the placeholder task
// the summary replaces.
type summarizeInput struct {
	SessionID   string `json:"session"`
	Prompt      string `json:"prompt"`
	Placeholder string `json:"placeholder"`
}

// summaryTimeout bounds the headless Claude run behind a summary.
const summaryTimeout = 45 * time.Second

// newHookSummarizeCommand is `rostr hook summarize`, which the first-prompt hook starts in the background for a
// terminal session. It summarizes the prompt with Summarize and records the result as the session's task, unless the
// agent has set a task of its own meanwhile. It prints nothing; when the summary fails, the task stays as it was.
func newHookSummarizeCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "summarize",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var in summarizeInput
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&in); err != nil {
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), summaryTimeout)
			defer cancel()
			summary, err := claudecode.Summarize(ctx, in.Prompt)
			if err != nil {
				logBackground("summarizing the prompt for session %s: %v", in.SessionID, err)
				return nil
			}
			st, err := openStore()
			if err != nil {
				return nil
			}
			defer st.Close()
			d, err := st.SessionDraft(ctx, in.SessionID)
			if err != nil || d == nil || d.Task != in.Placeholder {
				return nil
			}
			_, _ = st.SetTask(ctx, d.Key(), summary)
			return nil
		},
	}
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

// hookDir is the session's directory, from the hook input's cwd.
func hookDir(cwd string) string {
	if cwd != "" {
		return cwd
	}
	return workingDir()
}

// titleWhere tells TitleInstruction how the session's agent titles it.
func titleWhere(sessionID string) claudecode.TitleWhere {
	return claudecode.TitleWhere{
		DesktopLocal:    claudecode.IsDesktopLocal(paths.ClaudeDir(), sessionID),
		ServerSessionID: claudecode.ServerSessionID(paths.ClaudeDir(), sessionID),
	}
}
