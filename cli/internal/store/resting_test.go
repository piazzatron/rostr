package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func recordTurn(t *testing.T, st Store, session string, ended bool, at time.Time) {
	t.Helper()
	if err := st.RecordTurn(ctx, session, ended, at); err != nil {
		t.Fatal(err)
	}
}

func restingOf(t *testing.T, st Store, name string) time.Time {
	t.Helper()
	drafts, err := st.ActiveDrafts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drafts {
		if d.Name() == name {
			return d.RestingSince
		}
	}
	t.Fatalf("%s isn't drafted", name)
	return time.Time{}
}

func TestATurnThatEndedFifteenMinutesAgoRests(t *testing.T) {
	st, clock := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf"), nil)
	draft(t, st, "s", "work")

	ended := clock.Now()
	recordTurn(t, st, "s", true, ended)
	clock.Advance(14 * time.Minute)
	if !restingOf(t, st, "Gandalf").IsZero() {
		t.Fatal("resting 14 minutes after its turn ended")
	}
	clock.Advance(time.Minute)
	if got := restingOf(t, st, "Gandalf"); !got.Equal(ended) {
		t.Fatalf("resting since %v, want %v", got, ended)
	}

	// A late report of an earlier start or end changes nothing; a new start wakes it
	recordTurn(t, st, "s", false, ended.Add(-time.Minute))
	recordTurn(t, st, "s", true, ended.Add(-time.Minute))
	if got := restingOf(t, st, "Gandalf"); !got.Equal(ended) {
		t.Fatalf("after late reports, resting since %v, want %v", got, ended)
	}
	recordTurn(t, st, "s", false, clock.Now())
	if !restingOf(t, st, "Gandalf").IsZero() {
		t.Fatal("still resting after a new turn started")
	}

	if err := st.RecordTurn(ctx, "other", true, clock.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recording a turn for an undrafted session: %v", err)
	}
}

func TestADraftTakesTheLongestRestingNameBeforeCopies(t *testing.T) {
	st, clock := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf", "Frodo", "Samwise"), nil)
	draft(t, st, "gandalf", "work")
	draft(t, st, "frodo", "work")
	draft(t, st, "samwise", "work")
	recordTurn(t, st, "frodo", true, clock.Now())
	clock.Advance(5 * time.Minute)
	recordTurn(t, st, "gandalf", true, clock.Now())
	clock.Advance(20 * time.Minute)

	if d := draft(t, st, "a", "work"); d.Name() != "Frodo" {
		t.Fatalf("drafted %s, want Frodo (resting longest)", d.Name())
	}
	if d := draft(t, st, "b", "work"); d.Name() != "Gandalf" {
		t.Fatalf("drafted %s, want Gandalf (resting next)", d.Name())
	}
	if d := draft(t, st, "c", "work"); d.Copy != 2 {
		t.Fatalf("drafted %s, want a copy (nobody else resting)", d.Name())
	}

	// Frodo's session lost the name, and can't reclaim it
	if err := st.RecordTurn(ctx, "frodo", false, clock.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recording a turn after the name was taken: %v", err)
	}
	if d, err := st.Reclaim(ctx, "frodo"); err != nil || d != nil {
		t.Fatalf("reclaimed %v, %v", d, err)
	}
}

func TestAFreeNameComesBeforeAResting(t *testing.T) {
	st, clock := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf", "Frodo"), nil)
	draft(t, st, "gandalf", "work")
	recordTurn(t, st, "gandalf", true, clock.Now())
	clock.Advance(time.Hour)

	if d := draft(t, st, "new", "work"); d.Name() != "Frodo" {
		t.Fatalf("drafted %s, want Frodo (free)", d.Name())
	}
	if d, err := st.SessionDraft(ctx, "gandalf"); err != nil || d == nil {
		t.Fatalf("the resting session lost its name while one was free: %v, %v", d, err)
	}
}

func TestOpeningAVersionTwoDatabaseAddsTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rostr.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	createRoster(t, st, "work", members("Gandalf"), nil)
	draft(t, st, "s", "work")

	// Turn it back into a version 2 database, from before turns
	if _, err := st.db.Exec(`alter table drafts drop column turn_started_at; alter table drafts drop column turn_ended_at;
		pragma user_version = 2`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	recordTurn(t, st, "s", true, time.Now())
}

func TestAStartInTheSameSecondAsTheEndCountsAsWorking(t *testing.T) {
	st, clock := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf"), nil)
	draft(t, st, "s", "work")

	// SQLite keeps seconds, so a queued prompt right after the turn ended is stored at the same time
	recordTurn(t, st, "s", true, clock.Now().Add(200*time.Millisecond))
	recordTurn(t, st, "s", false, clock.Now().Add(900*time.Millisecond))
	clock.Advance(time.Hour)
	if !restingOf(t, st, "Gandalf").IsZero() {
		t.Fatal("resting while working on a turn that started in the same second the last one ended")
	}
}
