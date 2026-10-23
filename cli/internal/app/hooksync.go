package app

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/codex"
	"github.com/piazzatron/rostr/cli/internal/paths"
)

// Installed hooks are one-liners that call rostr, so an upgrade changes what they do right away; only a change to
// what rostr installs (a new hook event, say, or new skill text) needs files rewritten. Each agent's stamp records the
// fingerprint of what rostr last installed for it, and a session start that finds a stamp out of date reinstalls in
// the background, so upgrading is just `brew upgrade`.

// hookSet is one agent's hooks.
type hookSet struct {
	agent       string
	fingerprint string
	installed   func() bool // whether rostr's hooks are installed for the agent at all
	install     func() error
}

func hookSets() []hookSet {
	return []hookSet{
		{
			agent:       "claude-code",
			fingerprint: claudecode.Fingerprint(),
			installed:   func() bool { return claudecode.Installed(paths.ClaudeDir()) },
			install: func() error {
				_, err := claudecode.Install(paths.ClaudeDir())
				return err
			},
		},
		{
			agent:       "codex",
			fingerprint: codex.Fingerprint(),
			installed:   func() bool { return codex.Installed(paths.CodexDir()) },
			install: func() error {
				_, err := codex.Install(paths.CodexDir(), paths.AgentsSkillsDir())
				return err
			},
		},
	}
}

// syncHooksIfStale starts `rostr hook sync` in the background when any agent's stamp doesn't match what this binary
// installs. The session start that runs it keeps going without waiting; Claude Code picks up the rewritten settings on
// its own.
func syncHooksIfStale() {
	for _, set := range hookSets() {
		if stamp, _ := os.ReadFile(paths.HooksStamp(set.agent)); string(stamp) != set.fingerprint {
			_ = spawnHook(nil, "hook", "sync")
			return
		}
	}
}

// newHookSyncCommand is `rostr hook sync`, which a session start runs in the background when the hooks are out of
// date, and the install script runs after an upgrade. It prints nothing.
func newHookSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Bring the installed Claude Code and Codex hooks and skills up to date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return syncHooks()
		},
	}
}

// syncHooks reinstalls each agent's hooks and skills where rostr's hooks are installed already, never adding them for
// an agent that doesn't have them, and records the stamps. A failed install is logged and leaves its stamp out of date, so the
// next session start tries again.
func syncHooks() error {
	for _, set := range hookSets() {
		if set.installed() {
			if err := set.install(); err != nil {
				logBackground("reinstalling the %s hooks: %v", set.agent, err)
				continue
			}
		}
		path := paths.HooksStamp(set.agent)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(set.fingerprint), 0o600); err != nil {
			return err
		}
	}
	return nil
}
