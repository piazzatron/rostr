// Package claudecode holds what rostr knows about Claude Code: its hook input and output shapes, how to tell a
// session's first prompt, how to find a desktop or cloud session's server ID, what the agent is told, and installing the
// hooks, skill, and /draft command.
package claudecode

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// HookInput is the JSON Claude Code sends a hook on stdin. Fields a given event doesn't send stay empty.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Source         string `json:"source"` // SessionStart: startup, resume, clear, or compact
	Prompt         string `json:"prompt"`
	PromptID       string `json:"prompt_id"`
	SessionTitle   string `json:"session_title"` // the session's custom title, if it has one
	// PostToolUse: the tool's arguments. rostr only reads a title tool's title
	ToolInput struct {
		Title string `json:"title"`
	} `json:"tool_input"`
}

// The tools that set a session's title in the desktop app: the app's own tool in Local sessions, and Claude Code's
// remote tool in Remote Control and cloud sessions.
const (
	LocalTitleTool  = "mcp__ccd_session_mgmt__set_session_title"
	RemoteTitleTool = "mcp__claude-code-remote__set_session_title"
)

// ReadHookInput decodes a hook's stdin.
func ReadHookInput(r io.Reader) (HookInput, error) {
	var input HookInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return input, fmt.Errorf("reading hook input: %w", err)
	}
	if input.SessionID == "" {
		return input, errors.New("hook input has no session_id")
	}
	return input, nil
}

// IsFirstPrompt reports whether the transcript holds no earlier prompt typed by the user, so sessions that existed
// before rostr was installed are never drafted mid-conversation. A missing transcript counts as a first prompt.
func IsFirstPrompt(input HookInput) bool {
	file, err := os.Open(input.TranscriptPath)
	if err != nil {
		return true
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		var entry struct {
			Type     string `json:"type"`
			PromptID string `json:"promptId"`
			Origin   struct {
				Kind string `json:"kind"`
			} `json:"origin"`
		}
		if json.Unmarshal(line, &entry) == nil {
			isHumanPrompt := entry.Type == "user" && entry.Origin.Kind == "human"
			if isHumanPrompt && entry.PromptID != input.PromptID {
				return false
			}
		}
		if err != nil {
			return true
		}
	}
}

// ServerSessionID returns the server session ID the app's title is set on. Desktop and Remote Control sessions have it
// in the per-process session files Claude Code writes to ~/.claude/sessions/; a cloud session's container has it in
// CLAUDE_CODE_REMOTE_SESSION_ID, as "cse_<id>" for the server's "session_<id>". Terminal sessions have none, so it
// returns "".
func ServerSessionID(claudeDir, sessionID string) string {
	if id := readSessionFile(claudeDir, sessionID).BridgeSessionID; id != "" {
		return id
	}
	if cloudID, ok := strings.CutPrefix(os.Getenv("CLAUDE_CODE_REMOTE_SESSION_ID"), "cse_"); ok {
		return "session_" + cloudID
	}
	return ""
}

// IsDesktopLocal reports whether the session is one the desktop app runs as a Local session. Those have the app's own
// title tool, and the app shows a hook's title for them; Remote Control sessions run by Claude Code itself don't.
func IsDesktopLocal(claudeDir, sessionID string) bool {
	// The app has more than one entrypoint ("claude-desktop", and "claude-desktop-3p" with a third-party provider)
	return strings.HasPrefix(readSessionFile(claudeDir, sessionID).Entrypoint, "claude-desktop")
}

// sessionFile is what rostr reads from the per-process session files Claude Code writes to ~/.claude/sessions/.
type sessionFile struct {
	SessionID       string `json:"sessionId"`
	BridgeSessionID string `json:"bridgeSessionId"`
	Entrypoint      string `json:"entrypoint"`
}

func readSessionFile(claudeDir, sessionID string) sessionFile {
	paths, _ := filepath.Glob(filepath.Join(claudeDir, "sessions", "*.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var session sessionFile
		if json.Unmarshal(data, &session) == nil && session.SessionID == sessionID {
			return session
		}
	}
	return sessionFile{}
}

// AdditionalContext is a hook's JSON reply adding text to the agent's context.
func AdditionalContext(eventName, context string) string {
	out, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     eventName,
			"additionalContext": context,
		},
	})
	return string(out)
}

// PromptOutput is a UserPromptSubmit hook's reply: context for the agent and a session title, either of which may be
// empty. (A hook's title only sets the local title for now; the desktop app doesn't pick it up yet.)
func PromptOutput(context, title string) string {
	output := map[string]any{"hookEventName": "UserPromptSubmit"}
	if context != "" {
		output["additionalContext"] = context
	}
	if title != "" {
		output["sessionTitle"] = title
	}
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": output})
	return string(out)
}

// Title is a session's title: the agent's name, then what it's working on, if anything yet.
func Title(display, task string) string {
	if task == "" {
		return display
	}
	return display + " · " + task
}

// RenamedAfterRest tells the agent that another session took its name while it was resting.
func RenamedAfterRest(display, from string) string {
	return fmt.Sprintf("rostr renamed this session: you're %s now, not %s (another session took that name while this "+
		"one was resting). Sign as %s from now on.", display, from, display)
}

// Identity is what the agent is told whenever it's drafted or re-told its name. The task command names the session
// explicitly, so it works even where ROSTR_SESSION isn't set (a session started before the hooks were installed).
func Identity(display, rosterName, sessionID string) string {
	return strings.Join([]string{
		fmt.Sprintf("You're %s for this session (drafted by rostr from the %q roster).", display, rosterName),
		fmt.Sprintf("- When you write somewhere other than this chat, sign it \"— %s\": Slack messages, emails, "+
			"PR descriptions, PR and issue comments.", display),
		"- Don't put the name in code, commit messages, or files.",
		"- Your user's own instructions about attribution or sign-offs take precedence over these.",
		fmt.Sprintf("- When your work changes, run this first, before you write anything: %s \"<new task>\"",
			taskCommand(sessionID)),
	}, "\n")
}

// TitleWhere is where a session runs, which decides how its agent titles it.
type TitleWhere struct {
	DesktopLocal    bool   // a desktop app Local session, titled with the app's own tool
	ServerSessionID string // a Remote Control or cloud session, titled with Claude Code's remote tool
}

// TitleInstruction asks the agent to title the session "<display> · <summary>" with its title tool.
//
// The call comes first, before any text. Opus 5.5 and other recent models can return text written just before a tool
// call as a progress update: a summary of the answer, followed, after the title call, by the answer again. The first
// thing in a turn is never turned into one, and the answer then ends the turn as usual.
//
// Terminal sessions have no title tool, so the agent isn't asked: rostr summarizes the prompt itself (Summarize) and
// the next prompt's hook retitles the session.
func TitleInstruction(display, sessionID string, where TitleWhere) string {
	title := fmt.Sprintf("%q", display+" · <3-5 word summary of what the user wants>")
	switch {
	case where.DesktopLocal:
		return fmt.Sprintf("Title this session: load %s with ToolSearch, then call it with session_id \"self\" and "+
			"title %s. Make these calls first, before you write anything, then carry on with the user's request as "+
			"usual. Never mention the calls or their results.", LocalTitleTool, title)
	case where.ServerSessionID != "":
		// A Remote Control host's own first session doesn't get the tool; only sessions started from the app do
		return fmt.Sprintf("Title this session: call %s with session_id %q and title %s. Make this call first, "+
			"before you write anything, then carry on with the user's request as usual. Never mention the call or "+
			"its result. If you don't have that tool, skip this step.", RemoteTitleTool, where.ServerSessionID, title)
	}
	return ""
}

// TitleSetContext is what the PostToolUse hook tells the agent after its title call, which comes before its answer,
// so it answers the user rather than reporting the rename.
const TitleSetContext = "The session is titled; the user sees neither this call nor its result, so don't mention " +
	"either. Carry on and answer the user."

// ShowsHookTitles reports whether the app shows a title set by a hook: it does in a terminal and in desktop Local
// sessions, but not in Remote Control or cloud sessions.
func (w TitleWhere) ShowsHookTitles() bool {
	return w.DesktopLocal || w.ServerSessionID == ""
}

// IsTerminal reports whether the session has no title tool for its agent: it's neither a desktop Local session nor a
// Remote Control or cloud one.
func (w TitleWhere) IsTerminal() bool {
	return !w.DesktopLocal && w.ServerSessionID == ""
}

// UserPrompt is what the user typed: the desktop app puts <system-reminder> blocks ahead of it.
func UserPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	for strings.HasPrefix(prompt, "<system-reminder>") {
		_, rest, found := strings.Cut(prompt, "</system-reminder>")
		if !found {
			return ""
		}
		prompt = strings.TrimSpace(rest)
	}
	return prompt
}

// TaskFromTitle returns the task in a title of the form "<display> · <task>", reporting false for any other title.
func TaskFromTitle(display, title string) (string, bool) {
	task, ok := strings.CutPrefix(title, display+" · ")
	task = strings.TrimSpace(task)
	return task, ok && task != ""
}

// taskCommand is the command the agent runs to set its task, naming its session.
func taskCommand(sessionID string) string {
	return "rostr task --session " + sessionID
}

// PlaceholderTask is the start of the first prompt, used as the task until the agent sets its own.
func PlaceholderTask(prompt string) string {
	const maxRunes = 50
	line, _, _ := strings.Cut(UserPrompt(prompt), "\n")
	line = strings.TrimSpace(line)

	// A slash command ("/rostr") or a message Claude Code injected ("<task-notification>") says nothing about the work
	if strings.HasPrefix(line, "/") || strings.HasPrefix(line, "<") {
		return ""
	}
	if runes := []rune(line); len(runes) > maxRunes {
		return strings.TrimSpace(string(runes[:maxRunes])) + "…"
	}
	return line
}
