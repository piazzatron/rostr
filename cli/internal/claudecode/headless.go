package claudecode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// IsHeadless reports whether the Claude Code process running this hook is a headless `claude -p` run: one a script or
// tool started for a single answer, which shouldn't take a name from the roster. Neither the hook input nor the
// environment tells: CLAUDE_CODE_ENTRYPOINT ("sdk-cli") and the session file's entrypoint are inherited by a `claude
// -p` that an agent starts from inside its own session, and Remote Control sessions report "sdk-cli" too. So this
// reads how the Claude process itself was started. Sessions driven over stream-json (the Agent SDK and app hosts) and
// Remote Control sessions aren't headless, and anything that can't be read counts as not headless.
func IsHeadless(claudeDir, sessionID string) bool {
	if readSessionFile(claudeDir, sessionID).BridgeSessionID != "" {
		return false
	}
	return isPrintRun(claudeArgs())
}

// isPrintRun reports whether Claude Code's arguments start a one-shot print run, rather than a session another
// program drives over stream-json.
func isPrintRun(args []string) bool {
	printRun := false
	for i, arg := range args {
		switch {
		case arg == "-p" || arg == "--print":
			printRun = true
		case arg == "--input-format=stream-json", arg == "--input-format" && i+1 < len(args) && args[i+1] == "stream-json",
			arg == "--sdk-url" || strings.HasPrefix(arg, "--sdk-url="):
			return false
		}
	}
	return printRun
}

// claudeArgs returns the arguments of the Claude Code process that ran this hook: rostr's parent, or the parent of the
// `sh -c` Claude Code runs a hook's command with. It returns nil if they can't be read.
func claudeArgs() []string {
	pid := os.Getppid()
	for range 2 {
		ppid, args := processInfo(pid)
		if len(args) == 0 {
			return nil
		}
		if !isShellCommand(args) {
			return args
		}
		pid = ppid
	}
	return nil
}

// isShellCommand reports whether args are a shell running a command string (`sh -c "..."`).
func isShellCommand(args []string) bool {
	switch strings.TrimPrefix(filepath.Base(args[0]), "-") {
	case "sh", "bash", "zsh", "dash", "ash", "ksh", "fish":
		return len(args) > 1 && strings.HasPrefix(args[1], "-") && !strings.HasPrefix(args[1], "--") &&
			strings.Contains(args[1], "c")
	}
	return false
}

// processInfo returns a process's parent and arguments (split on spaces, which is enough to find flags), using ps so
// it works on macOS and Linux alike. -ww keeps ps from cutting the arguments at the terminal's width (a COLUMNS
// inherited from a terminal does it otherwise), which would drop the flags that come after --print.
func processInfo(pid int) (int, []string) {
	if pid <= 1 {
		return 0, nil
	}
	out, err := exec.Command("ps", "-ww", "-o", "ppid=", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, nil
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return 0, nil
	}
	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, nil
	}
	return ppid, fields[1:]
}
