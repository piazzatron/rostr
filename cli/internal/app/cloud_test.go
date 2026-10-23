package app

import (
	"testing"

	"github.com/piazzatron/rostr/cli/internal/crypto"
)

func TestCloudSessionsHaveNoMachineID(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// A cloud session's container would only hold a reservation it never uses
	t.Setenv("CLAUDE_CODE_REMOTE", "true")
	if c, err := newCloudStore(key); err != nil || c.MachineID != "" {
		t.Fatalf("cloud session: machine %q, %v", c.MachineID, err)
	}
	t.Setenv("CLAUDE_CODE_REMOTE", "")
	if c, err := newCloudStore(key); err != nil || c.MachineID == "" {
		t.Fatalf("this machine: machine %q, %v", c.MachineID, err)
	}
}
