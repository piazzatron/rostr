// Package proc reads the process tree with ps, so it works on macOS and Linux alike.
package proc

import (
	"os/exec"
	"strconv"
	"strings"
)

// Info returns a process's parent and arguments (split on spaces, which is enough to find flags), or 0 and nil if they
// can't be read. -ww keeps ps from cutting the arguments at the terminal's width (a COLUMNS inherited from a terminal
// does it otherwise), which would drop the flags at the end.
func Info(pid int) (int, []string) {
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
