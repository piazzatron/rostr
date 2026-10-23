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

func TestForgetTurnEndKeepsANewerOne(t *testing.T) {
	dir := t.TempDir()
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := SaveTurnEnd(dir, "s1", TurnEnd{At: first, Display: "Ada"}); err != nil {
		t.Fatal(err)
	}

	// A newer turn ended before the report for the first one finished
	if err := SaveTurnEnd(dir, "s1", TurnEnd{At: first.Add(time.Minute), Display: "Ada"}); err != nil {
		t.Fatal(err)
	}
	ForgetTurnEnd(dir, "s1", first)
	if end, ok := LoadTurnEnd(dir, "s1"); !ok || !end.At.Equal(first.Add(time.Minute)) {
		t.Fatalf("removed the newer turn's end: %+v, %v", end, ok)
	}
	ForgetTurnEnd(dir, "s1", first.Add(time.Minute))
	if _, ok := LoadTurnEnd(dir, "s1"); ok {
		t.Fatal("kept the turn's end after its start was reported")
	}
}
