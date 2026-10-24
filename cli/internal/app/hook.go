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

// newHookCommand is the `rostr hook` command the installed hooks call, listed in the help but not documented. Hooks never block a session: failures
// are written to stderr and the hook still exits 0.
func newHookCommand() *cobra.Command {
	hook := &cobra.Command{Use: "hook", Short: "What the installed hooks run (and `rostr hook sync` to reinstall them)"}
	claudeCode := &cobra.Command{Use: "claude-code", Short: "Claude Code hooks"}
	codexHooks := &cobra.Command{Use: "codex", Short: "Codex hooks"}
	hook.AddCommand(claudeCode, codexHooks, newHookDraftAheadCommand(), newHookSummarizeCommand(), newHookTurnCommand(), newHookSyncCommand())

	claudeCode.AddCommand(
		hookSubcommand("session-start", claudecode.ReadHookInput, hookSessionStart),
		hookSubcommand("prompt", claudecode.ReadHookInput, hookPrompt),
		hookSubcommand("title-set", claudecode.ReadHookInput, hookTitleSet),
		hookSubcommand("turn-end", claudecode.ReadHookInput, hookTurnEnd),
		hookSubcommand("working", claudecode.ReadHookInput, hookWorking),
		hookSubcommand("session-end", claudecode.ReadHookInput, hookSessionEnd),
		newHookDraftCommand(),
	)
	codexHooks.AddCommand(
		hookSubcommand("session-start", codex.ReadHookInput, hookCodexSessionStart),
		hookSubcommand("prompt", codex.ReadHookInput, hookCodexPrompt),
		hookSubcommand("turn-end", codex.ReadHookInput, hookCodexTurnEnd),
		hookSubcommand("working", codex.ReadHookInput, hookCodexWorking),
		hookSubcommand("session-end", codex.ReadHookInput, hookCodexSessionEnd),
		newHookCodexTitleCommand(),
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

// hookSessionStart writes ROSTR_SESSION for every session, brings the installed hooks up to date, drafts on resume,
// and re-tells the name after compaction.
func hookSessionStart(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	syncHooksIfStale()
	if err := writeSessionEnv(input.SessionID); err != nil {
		fmt.Fprintln(stderr, "rostr: writing CLAUDE_ENV_FILE:", err)
	}
	if input.Source != "resume" && input.Source != "compact" {
		// Headless `claude -p` runs don't take a name. That's told from the Claude process, which a background
		// process can't see, so it's checked here.
		if !claudecode.IsHeadless(paths.ClaudeDir(), input.SessionID) {
			draftAhead(input.SessionID, hookDir(input.Cwd))
		}
		return nil
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// Headless `claude -p` runs don't take a name, even resuming a session that had one
	if claudecode.IsHeadless(paths.ClaudeDir(), input.SessionID) {
		return nil
	}
	d, err := draftOrReclaim(ctx, st, input.SessionID, hookDir(input.Cwd), input.Source == "resume")
	if err != nil || d == nil {
		return err
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	pane.Label(ctx, pane.Claude, d.Display())
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

	// A session whose agent already knows its name isn't on its first prompt
	known, told, err := knownSessionDraft(ctx, st, input.SessionID)
	if err != nil || told {
		st.Close()
		if err != nil {
			return err
		}
		return laterPrompt(ctx, input, stdout)
	}

	// Headless `claude -p` runs fire the same hooks, but a script's one-off answer doesn't take a name
	if claudecode.IsHeadless(paths.ClaudeDir(), input.SessionID) {
		return nil
	}
	placeholder := claudecode.PlaceholderTask(input.Prompt)
	var d store.Draft
	if known != nil {
		// Drafted when the session started: the turn starting now stops the draft counting as idle (a session idle long
		// enough to lose its name gets a new one), and the task goes to the server in the background ("--" so a prompt
		// starting with "-" isn't read as flags)
		d = *known
		renamed, _, err := wakeAfterRest(ctx, input.SessionID, hookDir(input.Cwd), true)
		if err != nil {
			return err
		}
		if renamed != nil {
			d = *renamed
		}
		if placeholder != "" {
			_ = spawnHook(nil, "task", "--session", input.SessionID, "--", placeholder)
		}
	} else {
		drafted, ok, err := autoDraftSession(ctx, st, input.SessionID, hookDir(input.Cwd), placeholder)
		if err != nil || !ok {
			return err
		}
		d = drafted
	}
	if cloud, ok := st.(*store.Cloud); ok {
		cloud.MarkAnnounced(input.SessionID)
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), input.SessionID, time.Now())
	pane.Label(ctx, pane.Claude, d.Display())

	// The agent titles the session with its summary where it has a title tool; in a terminal, rostr summarizes the
	// prompt in the background and the next prompt's hook puts it in the title. Until then a terminal shows the start
	// of the prompt as the task. A desktop Local session doesn't: its agent titles it first thing, and a placeholder
	// would only change the title once more
	where := titleWhere(input.SessionID)
	context := claudecode.Identity(d.Display(), d.Roster, input.SessionID)
	if instruction := claudecode.TitleInstruction(d.Display(), input.SessionID, where); instruction != "" {
		context += "\n" + instruction
	}
	title := claudecode.Title(d.Display(), placeholder)
	if where.DesktopLocal {
		title = ""
	}
	fmt.Fprintln(stdout, claudecode.PromptOutput(context, title))
	if where.IsTerminal() {
		_ = spawnHook(summarizeInput{SessionID: input.SessionID, Prompt: claudecode.UserPrompt(input.Prompt), Placeholder: placeholder},
			"hook", "summarize")
	}
	return nil
}

// laterPrompt records that the session's turn started, keeps the draft alive, and keeps the title in step with the
// agent's task.
func laterPrompt(ctx context.Context, input claudecode.HookInput, stdout io.Writer) error {
	note, err := wakeSession(ctx, input)
	if err != nil {
		return err
	}
	if err := keepDraftAlive(ctx, input.SessionID); err != nil {
		return err
	}
	return syncTitleWithTask(ctx, input, stdout, note)
}

// wakeSession is wakeAfterRest for Claude Code: note tells the agent its new name, if it has one, and in Remote
// Control and cloud sessions, whose apps don't show a hook's title, asks it to retitle the session.
func wakeSession(ctx context.Context, input claudecode.HookInput) (note string, err error) {
	d, from, err := wakeAfterRest(ctx, input.SessionID, hookDir(input.Cwd), true)
	if err != nil || d == nil {
		return "", err
	}
	pane.Label(ctx, pane.Claude, d.Display())
	note = claudecode.RenamedAfterRest(d.Display(), from)
	if where := titleWhere(input.SessionID); !where.ShowsHookTitles() {
		note += "\n" + claudecode.TitleInstruction(d.Display(), input.SessionID, where)
	}
	return note, nil
}

// hookWorking is the PostToolUse hook on every tool. A turn can start without a prompt (another Stop hook continues
// it, or a background task wakes the agent), so a tool call reports the start too, in the background, if nothing has
// since the last turn ended. A session that lost its name while resting hears about it on its next prompt. The title
// calls are left to hookTitleSet, which may end the turn at them.
func hookWorking(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	switch input.ToolName {
	case claudecode.LocalTitleTool, claudecode.RemoteTitleTool:
		return nil
	case "Bash":
		if claudecode.IsTaskCommand(input.ToolInput.Command) {
			return nil
		}
	}
	_, _, err := wakeAfterRest(ctx, input.SessionID, hookDir(input.Cwd), false)
	return err
}

// wakeCheckAfter is how long after a turn ended the next start is checked with the store before going on, rather than
// reported in the background: less than RestAfter, so a clock a few minutes behind the server's can't miss a rest.
const wakeCheckAfter = store.RestAfter - 5*time.Minute

// wakeAfterRest records that the session's turn started, when the Stop hook recorded that its last one ended and no
// start has been recorded since. The record of the turn's end is kept until the start is reported, so a failed report
// is tried again on the next tool call or prompt.
//
// With check (a prompt), a session that rested may have lost its name to another session meanwhile, so the start is
// reported before going on: if the name was taken, the session gets its old one back if that's free, or a new one,
// which wakeAfterRest returns with the name it had (from). Otherwise, and for a turn that ended recently, the start is
// reported in the background.
func wakeAfterRest(ctx context.Context, sessionID, dir string, check bool) (renamed *store.Draft, from string, err error) {
	end, ok := keepalive.LoadTurnEnd(paths.TurnDir(), sessionID)
	if !ok {
		return nil, "", nil
	}
	now := time.Now()
	if !check || now.Sub(end.At) < wakeCheckAfter {
		return nil, "", spawnHook(turnInput{SessionID: sessionID, At: now, TurnEnd: end.At}, "hook", "turn")
	}

	st, err := openStore()
	if err != nil {
		return nil, "", err
	}
	defer st.Close()
	err = st.RecordTurn(ctx, sessionID, false, now)
	if !errors.Is(err, store.ErrNotFound) {
		if err == nil {
			keepalive.Forget(paths.TurnDir(), sessionID)
		}
		return nil, "", err
	}
	keepalive.Forget(paths.TurnDir(), sessionID)

	// The name was taken (or expired): the old one back if it's free, else a new one, with the session's task
	d, err := st.Reclaim(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	if d == nil {
		drafted, ok, err := autoDraftSession(ctx, st, sessionID, dir, end.Task)
		if err != nil || !ok {
			return nil, "", err
		}
		d = &drafted
	}
	_ = keepalive.Mark(paths.KeepAliveDir(), sessionID, now)
	if d.Display() == end.Display {
		return nil, "", nil
	}
	return d, end.Display, nil
}

// syncTitleWithTask retitles the session "<name> · <task>" when the agent's task has changed since the title was set,
// and tells the agent when the server confirmed a different name than the one it was told (see store.Cloud). note is
// more context for the agent, if any.
func syncTitleWithTask(ctx context.Context, input claudecode.HookInput, stdout io.Writer, note string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.SessionDraft(ctx, input.SessionID)
	if err != nil || d == nil {
		return err
	}
	context, title := note, ""
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
// "<name> · <summary>", it records the summary as the agent's task. A title call comes before the answer, so it tells
// Claude to answer the user rather than report the rename. When the agent recording its task came after the answer,
// it ends the turn, so Claude doesn't have to reply to the tool's result: Claude Code doesn't let a turn end without
// visible text, so that reply would repeat the answer.
func hookTitleSet(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	isTaskCommand := claudecode.IsTaskCommand(input.ToolInput.Command)
	if input.ToolName == "Bash" && !isTaskCommand {
		return nil
	}
	// Every time the turn isn't ended after the answer, Claude replies to the tool's result, so the reason goes in the log
	if !isTaskCommand {
		fmt.Fprintln(stdout, claudecode.AdditionalContext("PostToolUse", claudecode.TitleSetContext))
	} else if ends, reason := claudecode.EndsPlainAnswer(input.TranscriptPath, input.ToolUseID); ends {
		fmt.Fprintln(stdout, `{"continue": false, "suppressOutput": true}`)
		// Ending the turn here may skip the Stop hook, so the turn's end is recorded here too
		if err := recordTurnEnd(ctx, input.SessionID); err != nil {
			fmt.Fprintln(stderr, "rostr:", err)
		}
	} else {
		logBackground("title-set for session %s didn't end the turn: %s", input.SessionID, reason)
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

// hookTurnEnd is the Stop hook, which records that the session's turn ended.
func hookTurnEnd(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	return recordTurnEnd(ctx, input.SessionID)
}

// recordTurnEnd remembers the session's name and when its turn ended, for the next prompt, and reports the end in the
// background. A session with no draft has nothing to record.
func recordTurnEnd(ctx context.Context, sessionID string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	d, _, err := knownSessionDraft(ctx, st, sessionID)
	st.Close()
	if err != nil || d == nil {
		return err
	}
	now := time.Now()
	end := keepalive.TurnEnd{At: now, Display: d.Display(), Task: d.Task}
	if err := keepalive.SaveTurnEnd(paths.TurnDir(), sessionID, end); err != nil {
		return err
	}
	return spawnHook(turnInput{SessionID: sessionID, Ended: true, At: now}, "hook", "turn")
}

// hookSessionEnd releases the session's member and clears its pane's label.
func hookSessionEnd(ctx context.Context, input claudecode.HookInput, stdout, stderr io.Writer) error {
	keepalive.Forget(paths.KeepAliveDir(), input.SessionID)
	keepalive.Forget(paths.TurnDir(), input.SessionID)
	pane.Clear(ctx, pane.Claude, "")
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.Bench(ctx, input.SessionID)
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

			d, err := hookDraftSession(cmd.Context(), st, session, workingDir(), "")
			if err != nil {
				fmt.Fprintln(out, "rostr couldn't draft this session:", err)
				return nil
			}
			_ = keepalive.Mark(paths.KeepAliveDir(), session, time.Now())
			pane.Label(cmd.Context(), pane.Claude, d.Display())
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

// knownSessionDraft is the session's draft as this machine knows it, and whether the agent has been told that name: in
// cloud mode from the session's cache alone, since every draft this machine makes is cached there and a session can't
// have been drafted from another machine, so the first prompt makes no call to find that out. A session drafted when it
// started is known but not yet told.
func knownSessionDraft(ctx context.Context, st store.Store, sessionID string) (d *store.Draft, told bool, err error) {
	if cloud, ok := st.(*store.Cloud); ok {
		d, told = cloud.CachedSessionDraft(sessionID)
		return d, told, nil
	}
	d, err = st.SessionDraft(ctx, sessionID)
	return d, d != nil, err
}

// hookDraftSession drafts the session in dir.
func hookDraftSession(ctx context.Context, st store.Store, sessionID, dir, task string) (store.Draft, error) {
	repo, _ := project.Identify(dir)
	return st.Draft(ctx, store.DraftRequest{SessionID: sessionID, Repo: repo, Task: task})
}

// autoDraftSession is hookDraftSession when auto-draft is on; drafted is false when it's off.
func autoDraftSession(ctx context.Context, st store.Store, sessionID, dir, task string) (d store.Draft, drafted bool, err error) {
	repo, _ := project.Identify(dir)
	d, err = st.Draft(ctx, store.DraftRequest{SessionID: sessionID, Repo: repo, Task: task, IfAutoDraft: true})
	if errors.Is(err, store.ErrAutoDraftOff) {
		return store.Draft{}, false, nil
	}
	return d, err == nil, err
}

// draftAheadInput is what `rostr hook draft-ahead` reads: the session that just started, and its directory.
type draftAheadInput struct {
	SessionID string `json:"session"`
	Dir       string `json:"dir"`
}

// draftAhead starts `rostr hook draft-ahead` in the background when a cloud-mode session starts, so its name is waiting
// in the session's cache by the time its first prompt comes. Local mode drafts without a network call, so it drafts on
// the first prompt as before.
func draftAhead(sessionID, dir string) {
	if !hasCloudKey() {
		return
	}
	st, err := openStore()
	if err != nil {
		return
	}
	defer st.Close()
	if cloud, ok := st.(*store.Cloud); ok && cloud.AutoDraftOn() {
		_ = spawnHook(draftAheadInput{SessionID: sessionID, Dir: dir}, "hook", "draft-ahead")
	}
}

// newHookDraftAheadCommand is `rostr hook draft-ahead`, which a session start runs in the background. With auto-draft on,
// it drafts the session as idle, so the draft rests if no prompt comes, and records the draft as the end of a turn, so
// the first prompt reports the turn starting (see wakeAfterRest). The agent learns its name on that first prompt. It
// reads a draftAheadInput as JSON from stdin and prints nothing.
func newHookDraftAheadCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "draft-ahead",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var in draftAheadInput
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&in); err != nil {
				return nil
			}
			st, err := openStore()
			if err != nil {
				return nil
			}
			defer st.Close()
			repo, _ := project.Identify(in.Dir)
			d, err := st.Draft(cmd.Context(), store.DraftRequest{SessionID: in.SessionID, Repo: repo, IfAutoDraft: true, Idle: true})
			if err != nil {
				if !errors.Is(err, store.ErrAutoDraftOff) {
					logBackground("drafting session %s ahead of its first prompt: %v", in.SessionID, err)
				}
				return nil
			}
			// Unless the first prompt got there first (it drafted, and the server returned its draft): then the session's
			// turn is already under way, or its Stop hook has recorded a newer end
			if cloud, ok := st.(*store.Cloud); ok {
				if _, told := cloud.CachedSessionDraft(in.SessionID); told {
					return nil
				}
			}
			if _, recorded := keepalive.LoadTurnEnd(paths.TurnDir(), in.SessionID); recorded {
				return nil
			}
			end := keepalive.TurnEnd{At: d.DraftedAt, Display: d.Display(), Task: d.Task}
			_ = keepalive.SaveTurnEnd(paths.TurnDir(), in.SessionID, end)
			return nil
		},
	}
}

// turnInput is what `rostr hook turn` reads: that a session's turn started or ended, and when.
type turnInput struct {
	SessionID string    `json:"session"`
	Ended     bool      `json:"ended"`
	At        time.Time `json:"at"`
	// TurnEnd is, for a start, the end of the turn before it, whose record is removed once the start is recorded
	TurnEnd time.Time `json:"turnEnd,omitzero"`
}

// newHookTurnCommand is `rostr hook turn`, which the hooks start in the background to record that a session's turn
// started or ended. It reads a turnInput as JSON from stdin and prints nothing.
func newHookTurnCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "turn",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var in turnInput
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&in); err != nil {
				return nil
			}
			st, err := openStore()
			if err != nil {
				return nil
			}
			defer st.Close()
			err = st.RecordTurn(cmd.Context(), in.SessionID, in.Ended, in.At)
			switch {
			case err == nil && !in.TurnEnd.IsZero():
				keepalive.ForgetTurnEnd(paths.TurnDir(), in.SessionID, in.TurnEnd)
			case err != nil && !errors.Is(err, store.ErrNotFound):
				// A start that wasn't recorded keeps the turn's end, so it's tried again
				logBackground("recording a turn for session %s: %v", in.SessionID, err)
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
