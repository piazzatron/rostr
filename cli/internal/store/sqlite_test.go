package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/piazzatron/rostr/cli/internal/presets"
	"github.com/piazzatron/rostr/cli/internal/roster"
)

var ctx = context.Background()

// testClock is a settable clock shared by a test's stores.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func openTestStore(t *testing.T) (*SQLite, *testClock) {
	t.Helper()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "rostr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	clock := &testClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	st.SetClock(clock.Now)
	return st, clock
}

func members(names ...string) []roster.Member {
	out := make([]roster.Member, len(names))
	for i, n := range names {
		out[i] = roster.Member{Name: n, Emoji: fmt.Sprintf("e%d", i)}
	}
	return out
}

func createRoster(t *testing.T, st Store, name string, cast []roster.Member) {
	t.Helper()
	if _, err := st.CreateRoster(ctx, Roster{Name: name, EmojiMode: roster.EmojiOn, Members: cast}); err != nil {
		t.Fatal(err)
	}
}

func createFellowship(t *testing.T, st Store, name string) {
	t.Helper()
	p, _ := presets.ByID("fellowship")
	createRoster(t, st, name, p.Members)
}

func draft(t *testing.T, st Store, session, rosterName string) Draft {
	t.Helper()
	d, err := st.Draft(ctx, DraftRequest{SessionID: session, Roster: rosterName})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func bench(t *testing.T, st Store, session string) {
	t.Helper()
	if _, err := st.Bench(ctx, session); err != nil {
		t.Fatal(err)
	}
}

func TestDraftsInRosterOrder(t *testing.T) {
	st, _ := openTestStore(t)
	createFellowship(t, st, "work")

	want := []string{"Gandalf", "Frodo", "Samwise", "Aragorn", "Legolas", "Gimli", "Boromir", "Merry"}
	for i, name := range want {
		d := draft(t, st, fmt.Sprintf("s%d", i), "work")
		if d.Name() != name {
			t.Fatalf("draft %d: got %s, want %s", i, d.Name(), name)
		}
	}

	// The rest of the roster, never drafted, comes before Frodo again once he's freed
	bench(t, st, "s1")
	if d := draft(t, st, "s-next", "work"); d.Name() != "Pippin" {
		t.Errorf("got %s, want Pippin, who's never been drafted", d.Name())
	}
}

func TestDraftsNumberedCopiesWhenEveryoneIsBusy(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "small", members("Gandalf", "Frodo"))

	var got []string
	for i := range 5 {
		got = append(got, draft(t, st, fmt.Sprintf("s%d", i), "small").Name())
	}
	want := []string{"Gandalf", "Frodo", "Gandalf 2", "Frodo 2", "Gandalf 3"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// The first free slot is reused, copies included
	bench(t, st, "s2")
	if d := draft(t, st, "s-next", "small"); d.Name() != "Gandalf 2" || d.Copy != 2 {
		t.Errorf("got %s, want Gandalf 2", d.Name())
	}
	if d := draft(t, st, "s-next", "small"); d.Display() != "e0 Gandalf 2" {
		t.Errorf("display %q", d.Display())
	}
}

func TestSameSessionGetsSameName(t *testing.T) {
	st, clock := openTestStore(t)
	createFellowship(t, st, "work")
	createFellowship(t, st, "play")

	first := draft(t, st, "s1", "work")
	clock.Advance(time.Hour)
	again, err := st.Draft(ctx, DraftRequest{SessionID: "s1", Roster: "play", Task: "new task"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Name() != first.Name() || again.Roster != "work" {
		t.Fatalf("redraft gave %s from %s, want %s from work", again.Name(), again.Roster, first.Name())
	}
	if again.Task != "new task" || !again.LastSeenAt.Equal(clock.Now()) {
		t.Errorf("redraft didn't update task and last seen: %+v", again)
	}
	if d := draft(t, st, "s2", "work"); d.Name() != "Frodo" {
		t.Errorf("second session got %s, want Frodo", d.Name())
	}
}

func TestResumeGetsTheOldNameBackIfFree(t *testing.T) {
	st, _ := openTestStore(t)
	createFellowship(t, st, "work")

	draft(t, st, "s1", "work") // Gandalf
	if _, err := st.Draft(ctx, DraftRequest{SessionID: "s2", Roster: "work", Repo: "acme/api", Task: "old task"}); err != nil {
		t.Fatal(err)
	}
	bench(t, st, "s1")
	bench(t, st, "s2")

	// s2 had Frodo; Gandalf is the first free, but resume asks for Frodo back, with its repo and task
	d := draft(t, st, "s2", "work")
	if d.Name() != "Frodo" || d.Repo != "acme/api" || d.Task != "old task" {
		t.Fatalf("resume gave %+v, want Frodo with acme/api and the old task", d)
	}
}

func TestASessionThatCantResumeDraftsFromTheRosterItAsksFor(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf", "Frodo"))
	createFellowship(t, st, "play")

	draft(t, st, "s1", "work") // Gandalf
	bench(t, st, "s1")
	draft(t, st, "s2", "work") // Frodo, never drafted
	bench(t, st, "s2")
	draft(t, st, "s3", "work") // takes Gandalf, drafted longest ago

	d := draft(t, st, "s1", "play")
	if d.Name() != "Gandalf" || d.Roster != "play" {
		t.Fatalf("got %s from %s, want Gandalf from play", d.Name(), d.Roster)
	}
}

func TestDraftingMatchesNamesLoosely(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("Eowyn"))

	draft(t, st, "s1", "work") // Eowyn
	if err := st.ReplaceCast(ctx, "work", members("Éowyn")); err != nil {
		t.Fatal(err)
	}
	if d := draft(t, st, "s2", "work"); d.Name() != "Éowyn 2" {
		t.Fatalf("got %s, want a copy: Eowyn is still drafted", d.Name())
	}
}

func TestReclaimNeverDraftsANewMember(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf"))

	// No history: nothing
	if d, err := st.Reclaim(ctx, "s1"); err != nil || d != nil {
		t.Fatalf("got %v, %v; want nil", d, err)
	}

	// Active: the same draft
	draft(t, st, "s1", "work")
	if d, _ := st.Reclaim(ctx, "s1"); d == nil || d.Name() != "Gandalf" {
		t.Fatalf("got %v, want Gandalf", d)
	}

	// Released and free: reclaimed
	bench(t, st, "s1")
	if d, _ := st.Reclaim(ctx, "s1"); d == nil || d.Name() != "Gandalf" {
		t.Fatalf("got %v, want Gandalf reclaimed", d)
	}

	// Released and taken: nothing, rather than a different name
	bench(t, st, "s1")
	draft(t, st, "s2", "work")
	if d, _ := st.Reclaim(ctx, "s1"); d != nil {
		t.Fatalf("got %s, want nothing", d.Name())
	}
}

func TestExpiryReleasesStaleDrafts(t *testing.T) {
	st, clock := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf", "Frodo"))

	draft(t, st, "s1", "work") // Gandalf
	clock.Advance(6 * 24 * time.Hour)
	draft(t, st, "s2", "work") // Frodo; Gandalf isn't stale yet
	clock.Advance(2 * 24 * time.Hour)

	// s1 is 8 days unseen (released), s2 only 2
	active, err := st.ActiveDrafts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Name() != "Frodo" {
		t.Fatalf("active drafts %v, want only Frodo", active)
	}
	if d := draft(t, st, "s3", "work"); d.Name() != "Gandalf" {
		t.Errorf("got %s, want the expired Gandalf", d.Name())
	}
}

func TestExpiryFollowsTheSetting(t *testing.T) {
	st, clock := openTestStore(t)
	createFellowship(t, st, "work")
	if err := st.SetExpiry(ctx, "48h"); err != nil {
		t.Fatal(err)
	}

	draft(t, st, "s1", "work")
	clock.Advance(47 * time.Hour)
	if d, _ := st.SessionDraft(ctx, "s1"); d == nil {
		t.Fatal("expired before 48h")
	}
	clock.Advance(2 * time.Hour)
	if d, _ := st.SessionDraft(ctx, "s1"); d != nil {
		t.Fatal("still active after 49h")
	}
}

func TestKeepAliveBumpPreventsExpiry(t *testing.T) {
	st, clock := openTestStore(t)
	createFellowship(t, st, "work")

	draft(t, st, "s1", "work")
	for range 10 {
		clock.Advance(24 * time.Hour)
		if d, _ := st.Reclaim(ctx, "s1"); d == nil || d.Name() != "Gandalf" {
			t.Fatalf("lost the name while kept alive: %v", d)
		}
	}
}

func TestExpiredSessionReclaimsItsNameIfFree(t *testing.T) {
	st, clock := openTestStore(t)
	createFellowship(t, st, "work")

	draft(t, st, "s1", "work")
	clock.Advance(8 * 24 * time.Hour)
	if d, _ := st.Reclaim(ctx, "s1"); d == nil || d.Name() != "Gandalf" {
		t.Fatalf("got %v, want Gandalf reclaimed after expiry", d)
	}
}

func TestBenchedDraftsAreKeptForSevenDays(t *testing.T) {
	st, clock := openTestStore(t)
	createFellowship(t, st, "work")
	if err := st.SetExpiry(ctx, "4w"); err != nil {
		t.Fatal(err)
	}

	draft(t, st, "s1", "work")
	bench(t, st, "s1")
	clock.Advance(6 * 24 * time.Hour)
	if d, _ := st.Reclaim(ctx, "s1"); d == nil {
		t.Fatal("forgot the session's old name within 7 days")
	}
	bench(t, st, "s1")
	clock.Advance(8 * 24 * time.Hour)
	if d, _ := st.Reclaim(ctx, "s1"); d != nil {
		t.Fatalf("remembered %s past the 7-day retention", d.Name())
	}
}

func TestASessionKeepsItsDraftAcrossProjects(t *testing.T) {
	st, clock := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf", "Frodo"))
	createRoster(t, st, "play", members("Mario", "Link"))

	if _, err := st.Draft(ctx, DraftRequest{SessionID: "s1", Roster: "work", Repo: "acme/api"}); err != nil {
		t.Fatal(err)
	}

	// The agent cd's into a project linked to "play": still Gandalf from work
	if d := draft(t, st, "s1", "play"); d.Name() != "Gandalf" || d.Roster != "work" {
		t.Fatalf("got %s from %s", d.Name(), d.Roster)
	}

	// Once its name is gone, a new member comes from the roster and project it's in now
	draft(t, st, "s2", "work") // Frodo
	clock.Advance(8 * 24 * time.Hour)
	draft(t, st, "s3", "work") // both expired; takes Gandalf, drafted longest ago
	d, err := st.Draft(ctx, DraftRequest{SessionID: "s1", Roster: "play", Repo: "other/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Roster != "play" || d.Name() != "Mario" || d.Repo != "other/repo" {
		t.Fatalf("got %+v, want Mario from play in other/repo", d)
	}
}

func TestReplaceCastWhileAgentsAreDrafted(t *testing.T) {
	st, _ := openTestStore(t)
	createFellowship(t, st, "work")
	if err := st.LinkProject(ctx, "acme/api", "work"); err != nil {
		t.Fatal(err)
	}
	draft(t, st, "s1", "work") // Gandalf

	hogwarts, _ := presets.ByID("hogwarts")
	if err := st.ReplaceCast(ctx, "work", hogwarts.Members); err != nil {
		t.Fatal(err)
	}

	// Gandalf keeps his name and emoji until his session ends, though he's left the cast
	active, _ := st.ActiveDrafts(ctx)
	if len(active) != 1 || active[0].Display() != "🧙 Gandalf" {
		t.Fatalf("active drafts %+v", active)
	}

	// New sessions draft from the new cast; the link and default stay
	if d := draft(t, st, "s2", "work"); d.Name() != "Harry" {
		t.Errorf("got %s, want Harry", d.Name())
	}
	if linked, _ := st.ProjectLink(ctx, "acme/api"); linked != "work" {
		t.Errorf("link lost: %q", linked)
	}
	if settings, _ := st.Settings(ctx); settings.DefaultRoster != "work" {
		t.Errorf("default lost: %q", settings.DefaultRoster)
	}

	// Gandalf can still be released, and a resumed session doesn't get a member who's left the cast
	if _, err := st.BenchAgent(ctx, active[0].Key()); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Reclaim(ctx, "s1"); d != nil {
		t.Errorf("reclaimed %s, who left the cast", d.Name())
	}
}

func TestEditRefusesRenamingOrRemovingDraftedMembers(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf", "Frodo", "Boromir"))
	draft(t, st, "s1", "work") // Gandalf

	renamed := []roster.Member{{Name: "Gandalf the White", Emoji: "🧙"}, {Name: "Frodo", Emoji: "💍"}}
	if err := st.EditRoster(ctx, "work", roster.EmojiOn, renamed); err == nil {
		t.Fatal("renaming a drafted member was allowed")
	}

	// Moving Gandalf down the roster, changing his emoji, and removing idle Frodo are fine
	edited := []roster.Member{{Name: "Boromir", Emoji: "🛡️"}, {Name: "Gandalf", Emoji: "🪄"}}
	if err := st.EditRoster(ctx, "work", roster.EmojiOff, edited); err != nil {
		t.Fatal(err)
	}
	r, _ := st.Roster(ctx, "work")
	if r.EmojiMode != roster.EmojiOff || !slices.Equal(r.Members, edited) {
		t.Fatalf("edit not saved: %+v", r)
	}
	// The draft keeps the emoji it was drafted with
	if d, _ := st.SessionDraft(ctx, "s1"); d.Emoji != "e0" {
		t.Errorf("draft %+v", d)
	}
}

func TestRosterNamesAreUniqueLoosely(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "Éowyn's", members("A"))

	_, err := st.CreateRoster(ctx, Roster{Name: "eowyn's", EmojiMode: roster.EmojiOn, Members: members("B")})
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("got %v, want ErrNameTaken", err)
	}
	if r, err := st.Roster(ctx, "EOWYN'S"); err != nil || r.Name != "Éowyn's" {
		t.Fatalf("loose lookup: %v %v", r, err)
	}
	if _, err := st.Roster(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestFirstRosterBecomesDefault(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("A"))
	createRoster(t, st, "play", members("B"))
	settings, _ := st.Settings(ctx)
	if settings.DefaultRoster != "work" || !settings.AutoDraft || settings.Expiry != "7d" {
		t.Fatalf("settings %+v", settings)
	}
}

func TestDisband(t *testing.T) {
	st, clock := openTestStore(t)
	createRoster(t, st, "work", members("A", "B"))
	clock.Advance(time.Second)
	createRoster(t, st, "play", members("C"))
	clock.Advance(time.Second)
	createRoster(t, st, "side", members("D"))
	if err := st.LinkProject(ctx, "acme/api", "work"); err != nil {
		t.Fatal(err)
	}
	draft(t, st, "s1", "work")
	draft(t, st, "s2", "work")

	usage, _ := st.Usage(ctx, "work")
	if usage != (Usage{Drafted: 2, Links: 1}) {
		t.Fatalf("usage %+v", usage)
	}

	// Disbanding the default promotes the oldest remaining roster, releases drafts, and drops links
	result, err := st.Disband(ctx, "work")
	if err != nil || result != (DisbandResult{WasDefault: true, NewDefault: "play"}) {
		t.Fatalf("got %+v, %v", result, err)
	}
	if linked, _ := st.ProjectLink(ctx, "acme/api"); linked != "" {
		t.Errorf("link survived: %q", linked)
	}
	if d, _ := st.Reclaim(ctx, "s1"); d != nil {
		t.Errorf("draft survived: %v", d)
	}
	if _, err := st.Bench(ctx, "s2"); err != nil {
		t.Errorf("release after disband: %v", err)
	}

	// Disbanding a non-default roster leaves the default alone
	if result, _ := st.Disband(ctx, "side"); result.WasDefault {
		t.Errorf("side wasn't the default")
	}

	// Disbanding the last roster starts over
	if err := st.SetAutoDraft(ctx, false); err != nil {
		t.Fatal(err)
	}
	if result, _ := st.Disband(ctx, "play"); result != (DisbandResult{WasDefault: true}) {
		t.Errorf("got %+v", result)
	}
	if settings, _ := st.Settings(ctx); settings != (Settings{AutoDraft: true, Expiry: DefaultExpiry}) {
		t.Errorf("settings not reset: %+v", settings)
	}
}

func TestEmojiOnlyNeedsUniqueEmoji(t *testing.T) {
	st, _ := openTestStore(t)
	cast := []roster.Member{{Name: "Pikachu", Emoji: "⚡"}, {Name: "Zeus", Emoji: "⚡"}}
	createRoster(t, st, "mixed", cast)
	if err := st.SetEmojiMode(ctx, "mixed", roster.EmojiOnly); err == nil {
		t.Fatal("only mode allowed duplicate emoji")
	}
	if err := st.SetEmojiMode(ctx, "mixed", roster.EmojiOff); err != nil {
		t.Fatal(err)
	}
}

func TestLinks(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("A"))
	createRoster(t, st, "play", members("B"))

	if err := st.LinkProject(ctx, "acme/api", "PLAY"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkProject(ctx, "acme/api", "work"); err != nil {
		t.Fatal(err)
	}
	if linked, _ := st.ProjectLink(ctx, "acme/api"); linked != "work" {
		t.Fatalf("got %q", linked)
	}
	if removed, _ := st.UnlinkProject(ctx, "acme/api"); !removed {
		t.Fatal("unlink found nothing")
	}
	if removed, _ := st.UnlinkProject(ctx, "acme/api"); removed {
		t.Fatal("unlinked twice")
	}
}

func TestSetTaskAndBenchAgent(t *testing.T) {
	st, clock := openTestStore(t)
	createFellowship(t, st, "work")
	d := draft(t, st, "s1", "work")

	clock.Advance(time.Hour)
	updated, err := st.SetTask(ctx, d.Key(), "Fix auth redirect loop")
	if err != nil || updated.Task != "Fix auth redirect loop" {
		t.Fatalf("got %+v, %v", updated, err)
	}
	if got, _ := st.SessionDraft(ctx, "s1"); !got.LastSeenAt.Equal(clock.Now()) {
		t.Errorf("task didn't bump last seen")
	}

	if _, err := st.BenchAgent(ctx, d.Key()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BenchAgent(ctx, d.Key()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestConcurrentDraftsNeverShareAMember drafts from many goroutines, each with its own connection to one database
// file, the way separate rostr processes would.
func TestConcurrentDraftsNeverShareAMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rostr.db")
	setup, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	createRoster(t, setup, "small", members("A", "B", "C", "D"))
	setup.Close()

	const workers, draftsEach = 8, 6
	var mu sync.Mutex
	seen := map[string]string{}
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		wg.Go(func() {
			st, err := OpenSQLite(path)
			if err != nil {
				errs <- err
				return
			}
			defer st.Close()
			for i := range draftsEach {
				session := fmt.Sprintf("w%d-s%d", w, i)
				d, err := st.Draft(ctx, DraftRequest{SessionID: session, Roster: "small"})
				if err != nil {
					errs <- err
					return
				}
				mu.Lock()
				if other, dup := seen[d.Name()]; dup {
					errs <- fmt.Errorf("%s drafted by both %s and %s", d.Name(), other, session)
				}
				seen[d.Name()] = session
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(seen) != workers*draftsEach {
		t.Errorf("got %d distinct names, want %d", len(seen), workers*draftsEach)
	}
}

func TestParseExpiry(t *testing.T) {
	for in, want := range map[string]time.Duration{"48h": 48 * time.Hour, "7d": 7 * 24 * time.Hour, "2W": 14 * 24 * time.Hour} {
		if got, err := ParseExpiry(in); err != nil || got != want {
			t.Errorf("ParseExpiry(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "7", "0d", "-1d", "1.5d", "3m", "d"} {
		if _, err := ParseExpiry(in); err == nil {
			t.Errorf("ParseExpiry(%q) accepted", in)
		}
	}
}

func TestFirstDraftUsesFlagThenLinkThenDefault(t *testing.T) {
	st, _ := openTestStore(t)
	if _, err := st.Draft(ctx, DraftRequest{SessionID: "s0"}); !errors.Is(err, ErrNoRosters) {
		t.Fatalf("draft with no rosters: %v", err)
	}
	createRoster(t, st, "work", members("Gandalf"))
	createRoster(t, st, "play", members("Mario"))
	createRoster(t, st, "side", members("Ada"))
	if err := st.LinkProject(ctx, "acme/api", "play"); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		req  DraftRequest
		want string
	}{
		{DraftRequest{SessionID: "s1"}, "Gandalf"},
		{DraftRequest{SessionID: "s2", Repo: "acme/api"}, "Mario"},
		{DraftRequest{SessionID: "s3", Repo: "acme/api", Roster: "SIDE"}, "Ada"},
		{DraftRequest{SessionID: "s4", Repo: "acme/unlinked"}, "Gandalf 2"},
	} {
		if d, err := st.Draft(ctx, c.req); err != nil || d.Name() != c.want {
			t.Errorf("%+v: drafted %s (%v), want %s", c.req, d.Name(), err, c.want)
		}
	}
}

func TestExportReadsTheWholeSetup(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf", "Frodo"))
	createRoster(t, st, "play", members("Mario"))
	if err := st.LinkProject(ctx, "acme/api", "play"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetExpiry(ctx, "48h"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Draft(ctx, DraftRequest{SessionID: "s1", Repo: "acme/api", Task: "Fix auth"}); err != nil {
		t.Fatal(err)
	}

	setup, err := st.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.Rosters) != 2 || setup.Rosters[0].Name != "work" || setup.Settings.DefaultRoster != "work" ||
		setup.Settings.Expiry != "48h" || len(setup.Links) != 1 || setup.Links[0] != (ProjectLink{"acme/api", "play"}) ||
		len(setup.Drafts) != 1 || setup.Drafts[0].Member != "Mario" || setup.Drafts[0].Task != "Fix auth" {
		t.Fatalf("export: %+v", setup)
	}
}

func TestMigratesReleasedAtToBenchedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rostr.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	createFellowship(t, st, "work")
	draft(t, st, "s1", "work")

	// Turn it back into a version 1 database
	splitTeams(t, st, 6)
	if _, err := st.db.Exec(`alter table drafts rename column benched_at to released_at;
		alter table drafts drop column turn_started_at; alter table drafts drop column turn_ended_at;
		pragma user_version = 1`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	recordTurn(t, st, "s1", true, time.Now())
	if d, err := st.Bench(ctx, "s1"); err != nil || d == nil {
		t.Fatalf("bench after migrating: %v, %v", d, err)
	}
}

// splitTeams turns a version 4 database's rosters back into version 3's A team and B team columns, the first n
// members on the A team.
func splitTeams(t *testing.T, st *SQLite, n int) {
	t.Helper()
	rosters, err := allRosters(ctx, st.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`alter table rosters rename column members to a_team;
		alter table rosters add column b_team text not null default '[]'; pragma user_version = 3`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rosters {
		split := min(n, len(r.Members))
		if _, err := st.db.Exec("update rosters set a_team = ?, b_team = ? where id = ?",
			mustJSON(r.Members[:split]), mustJSON(r.Members[split:]), r.id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigratingMergesTheTeams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rostr.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	createFellowship(t, st, "work")
	createRoster(t, st, "small", members("Ada"))
	draft(t, st, "s1", "work")
	splitTeams(t, st, 6)
	st.Close()

	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fellowship, _ := presets.ByID("fellowship")
	work, _ := st.Roster(ctx, "work")
	small, _ := st.Roster(ctx, "small")
	if !slices.Equal(work.Members, fellowship.Members) || !slices.Equal(small.Members, members("Ada")) {
		t.Fatalf("members after migrating: %v, %v", work.Members, small.Members)
	}
	if d, _ := st.SessionDraft(ctx, "s1"); d == nil || d.Name() != "Gandalf" {
		t.Fatalf("draft after migrating: %+v", d)
	}
}

func TestAutoDraftOffOnlyKeepsAnActiveDraft(t *testing.T) {
	st, _ := openTestStore(t)
	createRoster(t, st, "work", members("Gandalf"))
	draft(t, st, "s1", "work")
	if err := st.SetAutoDraft(ctx, false); err != nil {
		t.Fatal(err)
	}

	if d, err := st.Draft(ctx, DraftRequest{SessionID: "s1", IfAutoDraft: true}); err != nil || d.Name() != "Gandalf" {
		t.Fatalf("got %v, %v; want the active Gandalf", d, err)
	}
	bench(t, st, "s1")
	if _, err := st.Draft(ctx, DraftRequest{SessionID: "s1", IfAutoDraft: true}); !errors.Is(err, ErrAutoDraftOff) {
		t.Fatalf("got %v, want ErrAutoDraftOff rather than resuming", err)
	}
}
