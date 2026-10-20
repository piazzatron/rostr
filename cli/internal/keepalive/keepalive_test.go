package keepalive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBumpsAtMostOnceAnHour(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keepalive")
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	if !Due(dir, "s1", start) {
		t.Fatal("a session never bumped should be due")
	}
	if err := Mark(dir, "s1", start); err != nil {
		t.Fatal(err)
	}
	if Due(dir, "s1", start.Add(59*time.Minute)) {
		t.Error("due again within the hour")
	}
	if !Due(dir, "s1", start.Add(time.Hour)) {
		t.Error("not due after an hour")
	}
	if !Due(dir, "s2", start) {
		t.Error("sessions share a throttle")
	}

	Forget(dir, "s1")
	if !Due(dir, "s1", start) {
		t.Error("still throttled after Forget")
	}
}

func TestSessionIDsCantEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := Mark(dir, "../../escape", time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != ".._.._escape" {
		t.Fatalf("got %v", entries)
	}
}
