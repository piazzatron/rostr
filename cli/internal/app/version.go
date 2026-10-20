package app

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is set at release time with -ldflags "-X github.com/piazzatron/rostr/cli/internal/app.version=v1.2.3".
// Development builds fall back to the git commit they were built from.
var version = ""

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print rostr's version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "rostr "+versionString())
		},
	}
}

// versionString is the release version, or for development builds "dev" plus the commit (and "-dirty" if the working
// tree had uncommitted changes).
func versionString() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var commit, dirty string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			commit = setting.Value
		case "vcs.modified":
			if setting.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}
	if commit == "" {
		return "dev"
	}
	return "dev (" + commit + dirty + ")"
}
