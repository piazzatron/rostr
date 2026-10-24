package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piazzatron/rostr/cli/internal/claudecode"
	"github.com/piazzatron/rostr/cli/internal/codex"
	"github.com/piazzatron/rostr/cli/internal/paths"
)

func TestSyncUpgradesInstalledHooksOnly(t *testing.T) {
	claudeDir, codexDir := t.TempDir(), filepath.Join(t.TempDir(), "codex")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CODEX_HOME", codexDir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// An older install: rostr's prompt hook, without the Stop hook, next to one of the user's own
	settingsPath := filepath.Join(claudeDir, "settings.json")
	old := `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "echo mine"}]},
		{"hooks": [{"type": "command", "command": "if command -v rostr >/dev/null; then rostr hook claude-code prompt; fi"}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncHooks(io.Discard); err != nil {
		t.Fatal(err)
	}
	settings, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(settings), claudecode.HookCommand("turn-end")) || !strings.Contains(string(settings), "echo mine") {
		t.Fatalf("settings: %s", settings)
	}

	// Codex never had rostr's hooks, so it's left alone
	if _, err := os.Stat(codexDir); !os.IsNotExist(err) {
		t.Fatalf("created Codex's config: %v", err)
	}

	// Both stamps are current, so the next session start doesn't sync again
	for agent, fingerprint := range map[string]string{"claude-code": claudecode.Fingerprint(), "codex": codex.Fingerprint()} {
		if stamp, _ := os.ReadFile(paths.HooksStamp(agent)); string(stamp) != fingerprint {
			t.Errorf("%s stamp %q, want %q", agent, stamp, fingerprint)
		}
	}
}
