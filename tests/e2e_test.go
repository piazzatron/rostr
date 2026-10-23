package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestFirstSetupInTheCloud(t *testing.T) {
	t.Parallel()
	m := newMachine(t)

	out := m.ok("assemble", "work", "--preset", "fellowship", "--cloud")
	if !strings.Contains(out, "Assembled the work roster") || !strings.Contains(out, "Your setup is in the cloud") ||
		strings.Contains(out, "rostr_sk_") {
		t.Fatalf("assemble --cloud: %s", out)
	}
	info, err := os.Stat(m.credentialsPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials: %v, %v", info, err)
	}
	if _, err := os.Stat(m.databasePath()); !os.IsNotExist(err) {
		t.Fatalf("a cloud setup kept rostr.db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "settings.json")); err != nil {
		t.Fatalf("hooks not installed: %v", err)
	}

	l := m.list()
	if len(l.Rosters) != 1 || l.Rosters[0].Name != "work" || l.Rosters[0].EmojiMode != "on" || len(l.Rosters[0].Idle) != 20 {
		t.Fatalf("list: %+v", l)
	}
	if d := m.draft(m.home, "s1", "--task", "Write the e2e tests"); d.Display != "🧙 Gandalf" || d.Roster != "work" || d.Team != "A" {
		t.Fatalf("draft: %+v", d)
	}
	if text := m.ok("list"); !strings.Contains(text, "🧙 Gandalf   Write the e2e tests   -   just now") {
		t.Fatalf("list text:\n%s", text)
	}
	var config struct{ Storage, Default string }
	json.Unmarshal([]byte(m.ok("config", "--json")), &config)
	if config.Storage != "cloud" || config.Default != "work" {
		t.Fatalf("config: %+v", config)
	}

	// --cloud again on a cloud machine just makes another roster there
	m.ok("assemble", "play", "--names", "🧮 Ada, Grace", "--cloud")
	if l := m.list(); len(l.Rosters) != 2 || l.Rosters[1].Name != "play" {
		t.Fatalf("second roster: %+v", l)
	}
}

func TestTwoMachinesShareOneKey(t *testing.T) {
	t.Parallel()
	a, b := newMachine(t), newMachine(t)
	a.ok("assemble", "work", "--names", "🧙 Gandalf, 💍 Frodo, 🌱 Sam", "--cloud")

	// A machine with local rosters can't join; a wrong key is refused; the right key joins
	local := newMachine(t)
	local.ok("assemble", "mine", "--preset", "kanto")
	out, code := local.runTTY([]ttyStep{{"New setup, or do you have a key?", keyDown}, {"I have a key", keyEnter}},
		"cloud", "on")
	if code != 1 || !strings.Contains(out, "already has local rosters") {
		t.Fatalf("join with local rosters: exit %d\n%s", code, out)
	}
	if out, code := b.joinWithKey("rostr_sk_" + strings.Repeat("A", 43)); code != 1 || !strings.Contains(out, "isn't registered") {
		t.Fatalf("join with an unknown key: exit %d\n%s", code, out)
	}
	out, code = b.joinWithKey(a.key())
	if code != 0 || !strings.Contains(out, "cloud mode now, with your default roster work") {
		t.Fatalf("join: exit %d\n%s", code, out)
	}
	if strings.Contains(out, a.key()) {
		t.Fatal("the pasted key was echoed")
	}
	if info, err := os.Stat(b.credentialsPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials: %v, %v", info, err)
	}

	// Both machines draft from the same roster, at once, without colliding
	results := make([]draftOutput, 6)
	var wg sync.WaitGroup
	for i := range results {
		m := []*machine{a, b}[i%2]
		wg.Go(func() {
			r := m.run(m.home, "", "draft", "--json", "--session", fmt.Sprintf("m%d-s%d", i%2, i),
				"--task", fmt.Sprintf("Task %d from machine %d", i, i%2))
			if r.code != 0 || json.Unmarshal([]byte(r.stdout), &results[i]) != nil {
				t.Errorf("draft %d: exit %d %s %s", i, r.code, r.stdout, r.stderr)
			}
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, d := range results {
		if seen[d.Name] {
			t.Fatalf("%s drafted twice: %+v", d.Name, results)
		}
		seen[d.Name] = true
	}

	// Each sees the other's agents, with tasks decrypted
	for _, m := range []*machine{a, b} {
		drafted := m.list().drafted()
		if len(drafted) != 6 {
			t.Fatalf("drafted: %v", drafted)
		}
		for i, d := range results {
			if drafted[d.Name] != fmt.Sprintf("Task %d from machine %d", i, i%2) {
				t.Fatalf("%s's task: %q (all: %v)", d.Name, drafted[d.Name], drafted)
			}
		}
	}
}

func TestServerNeverSeesPlaintext(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	owner, repoName := unique("blindowner"), unique("blindrepo")
	task := unique("Rotate the very secret signing key ")
	repo := m.gitRepo(fmt.Sprintf("git@github.com:%s/%s.git", owner, repoName))

	// A local setup with a link and a task moves to the cloud; then more of each are written in cloud mode
	m.ok("assemble", "work", "--preset", "fellowship")
	m.okIn(repo, "link", "work")
	m.okIn(repo, "draft", "--session", unique("blind-s1-"), "--task", task)
	m.ok("cloud", "on")
	m.okIn(repo, "draft", "--session", unique("blind-s2-"), "--task", task+" again")
	m.okIn(repo, "link", "work")
	key := m.key()
	token := authToken(t, key)
	if drafted := m.list().drafted(); drafted["Gandalf"] != task {
		t.Fatalf("drafted: %v", drafted)
	}

	// Nothing in any table holds the task, the repo, the key, or the auth token, as text or as bytes
	secrets := []string{task, owner, repoName, key, strings.TrimPrefix(key, "rostr_sk_"), token}
	ctx := context.Background()
	rows, err := db.Query(ctx, `select table_name, column_name, data_type from information_schema.columns
		where table_schema = 'public' order by table_name, ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	type column struct{ Table, Name, DataType string }
	columns, err := pgx.CollectRows(rows, pgx.RowToStructByPos[column])
	if err != nil || len(columns) == 0 {
		t.Fatalf("columns: %v", err)
	}
	for _, c := range columns {
		column := pgx.Identifier{c.Name}.Sanitize()
		match := fmt.Sprintf("strpos(lower(%s::text), lower($1)) > 0", column)
		if c.DataType == "bytea" {
			match = fmt.Sprintf("position(convert_to($1, 'UTF8') in %[1]s) > 0 or position(convert_to(lower($1), 'UTF8') in %[1]s) > 0", column)
		}
		for _, secret := range secrets {
			var n int
			err := db.QueryRow(ctx, fmt.Sprintf(`select count(*) from %s where %s`,
				pgx.Identifier{c.Table}.Sanitize(), match), secret).Scan(&n)
			if err != nil {
				t.Fatalf("%s.%s: %v", c.Table, c.Name, err)
			}
			if n > 0 {
				t.Errorf("%s.%s holds %q in %d rows", c.Table, c.Name, secret, n)
			}
		}
	}

	// What the server does hold for this owner is ciphertext and keyed hashes
	var drafts, encryptedDrafts, links int
	err = db.QueryRow(ctx, `
		select count(*), count(*) filter (where length(d.task) >= 28 and length(d.repo) >= 28 and d.repo_hash ~ '^[0-9a-f]{64}$'),
		       (select count(*) from project_links l join owners o on o.id = l.owner_id
		        where o.auth_token_hash = encode(sha256(decode(translate($1, '-_', '+/') || '=', 'base64')), 'hex')
		          and l.repo_hash ~ '^[0-9a-f]{64}$')
		from drafts d join owners o on o.id = d.owner_id
		where o.auth_token_hash = encode(sha256(decode(translate($1, '-_', '+/') || '=', 'base64')), 'hex')`,
		token).Scan(&drafts, &encryptedDrafts, &links)
	if err != nil {
		t.Fatal(err)
	}
	if drafts != 2 || encryptedDrafts != 2 || links != 1 {
		t.Fatalf("owner's rows: %d drafts, %d encrypted, %d links", drafts, encryptedDrafts, links)
	}
}

func TestMovingALocalSetupToTheCloud(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	api, web := m.gitRepo("git@github.com:Acme/API.git"), m.gitRepo("https://github.com/acme/web")

	// A populated local setup: two rosters, a link, settings, and active drafts with tasks and repos
	m.ok("assemble", "work", "--preset", "fellowship")
	m.ok("assemble", "play", "--names", "🧮 Ada, Grace, Linus", "--emoji", "off")
	m.okIn(web, "link", "play")
	m.ok("config", "expiry", "48h")
	m.ok("config", "auto-draft", "off")
	m.ok("config", "default", "play")
	m.okIn(api, "draft", "--session", "s-api", "--roster", "work", "--task", "Fix auth redirect loop")
	m.okIn(web, "draft", "--session", "s-web", "--task", "Redesign the home page")
	m.okIn(web, "draft", "--session", "s-web2")
	before := m.list()

	out := m.ok("cloud", "on")
	if !strings.Contains(out, "Moved your setup to the cloud: 2 rosters, 1 linked project, and 3 drafted agents.") ||
		strings.Contains(out, "rostr_sk_") {
		t.Fatalf("cloud on: %s", out)
	}
	if _, err := os.Stat(m.databasePath()); !os.IsNotExist(err) {
		t.Fatalf("rostr.db is still there: %v", err)
	}
	if info, err := os.Stat(m.credentialsPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials: %v, %v", info, err)
	}

	// Rosters (in order), their drafts with decrypted tasks and repos, and idle members all came across
	after := m.list()
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("list changed:\nbefore %s\nafter  %s", beforeJSON, afterJSON)
	}
	if after.Rosters[0].Name != "work" || after.Rosters[0].Drafted[0].Repo != "acme/api" ||
		after.Rosters[1].Drafted[0].Task != "Redesign the home page" || after.Rosters[1].EmojiMode != "off" {
		t.Fatalf("after: %+v", after)
	}

	// Settings and links too
	var config struct {
		Storage, Default, Expiry string
		AutoDraft                bool
	}
	json.Unmarshal([]byte(m.ok("config", "--json")), &config)
	if config.Storage != "cloud" || config.Default != "play" || config.Expiry != "2d" || config.AutoDraft {
		t.Fatalf("config: %+v", config)
	}
	if out := m.okIn(web, "link"); out != "acme/web uses the play roster (linked).\n" {
		t.Fatalf("link: %q", out)
	}
	if out := m.okIn(api, "link"); out != "acme/api uses the play roster (your default).\n" {
		t.Fatalf("link: %q", out)
	}

	// Active drafts keep their sessions: re-drafting gives the same agent, and new sessions draft around them
	if d := m.draft(m.home, "s-api"); d.Display != "🧙 Gandalf" {
		t.Fatalf("redraft: %+v", d)
	}
	if d := m.draft(web, "s-new"); d.Display != "Linus" {
		t.Fatalf("new draft: %+v", d)
	}
	m.fails("already in cloud mode", "cloud", "on")
}

func TestPublicAPIWithTheTokenAndRecipe(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	repo := m.gitRepo("git@github.com:acme/dashboard.git")
	m.ok("assemble", "work", "--names", "🧙 Gandalf, 💍 Frodo", "--cloud")
	m.okIn(repo, "draft", "--session", "s1", "--task", "Build the who's-busy widget")

	// Both key commands refuse without a terminal, and print at one
	m.fails("only runs in a terminal", "config", "key")
	m.fails("only runs in a terminal", "config", "key", "--token")
	key := m.key()
	out, code := m.runTTY(nil, "config", "key", "--token")
	token := ""
	if match := tokenPattern.FindStringSubmatch(out); match != nil {
		token = match[1]
	}
	if code != 0 || token != authToken(t, key) {
		t.Fatalf("config key --token: exit %d, %q (want the recipe's auth token)", code, out)
	}

	var body map[string]any
	if status := apiGet(t, "/v1/rosters", token, &body); status != 200 {
		t.Fatalf("GET /v1/rosters: %d %v", status, body)
	}
	raw, _ := json.Marshal(body)
	var listed struct {
		Rosters []struct {
			Name      string `json:"name"`
			EmojiMode string `json:"emojiMode"`
			Drafted   []struct {
				Name      string    `json:"name"`
				Emoji     string    `json:"emoji"`
				Team      *string   `json:"team"`
				Task      *string   `json:"task"`
				Repo      *string   `json:"repo"`
				DraftedAt time.Time `json:"draftedAt"`
			} `json:"drafted"`
			Idle []struct{ Name, Emoji, Team string } `json:"idle"`
		} `json:"rosters"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}

	// The documented shape: plaintext rosters and members, ciphertext tasks and repos
	if len(listed.Rosters) != 1 || len(listed.Rosters[0].Drafted) != 1 || len(listed.Rosters[0].Idle) != 1 {
		t.Fatalf("shape: %s", raw)
	}
	r, d := listed.Rosters[0], listed.Rosters[0].Drafted[0]
	if r.Name != "work" || r.EmojiMode != "on" || d.Name != "Gandalf" || d.Emoji != "🧙" || d.Team == nil || *d.Team != "a" ||
		d.Task == nil || d.Repo == nil || time.Since(d.DraftedAt) > time.Minute || r.Idle[0].Name != "Frodo" || r.Idle[0].Team != "a" {
		t.Fatalf("shape: %s", raw)
	}
	for _, field := range []string{"Build the", "acme/dashboard"} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("plaintext %q in the response: %s", field, raw)
		}
	}

	// Decrypting with only the key and the recipe
	if task := decrypt(t, key, *d.Task); task != "Build the who's-busy widget" {
		t.Fatalf("task: %q", task)
	}
	if repo := decrypt(t, key, *d.Repo); repo != "acme/dashboard" {
		t.Fatalf("repo: %q", repo)
	}
}

func TestClaudeCodeHooksInCloudMode(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	repo := m.gitRepo("git@github.com:acme/hooks.git")
	m.ok("assemble", "work", "--preset", "fellowship", "--cloud")
	envFile := filepath.Join(t.TempDir(), "claude-env")
	hook := func(event, input string) result {
		return m.with("CLAUDE_ENV_FILE="+envFile).run(repo, input, "hook", "claude-code", event)
	}
	start := func(source string) string {
		return fmt.Sprintf(`{"session_id":"hs1","cwd":%q,"hook_event_name":"SessionStart","source":%q}`, repo, source)
	}
	expectIdentity := func(name string, r result) {
		t.Helper()
		var out struct {
			HookSpecificOutput struct{ AdditionalContext string }
		}
		if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil ||
			!strings.HasPrefix(out.HookSpecificOutput.AdditionalContext, `You're 🧙 Gandalf for this session (drafted by rostr from the "work" roster).`) {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", name, r.code, r.stdout, r.stderr)
		}
	}

	// Session start writes ROSTR_SESSION and drafts nothing
	if r := hook("session-start", start("startup")); r.code != 0 || r.stdout != "" {
		t.Fatalf("start: %+v", r)
	}
	if env, _ := os.ReadFile(envFile); string(env) != "export ROSTR_SESSION=hs1\n" {
		t.Fatalf("CLAUDE_ENV_FILE: %q", env)
	}
	if len(m.list().drafted()) != 0 {
		t.Fatal("session start drafted")
	}

	// The first prompt drafts from the next state assemble cached, without waiting on the API, tells the agent who
	// it is and to record its task in its reply, and titles the session; the server hears of the draft just after
	if _, err := os.Stat(m.nextStatePath()); err != nil {
		t.Fatalf("assemble didn't cache the next state: %v", err)
	}
	prompt := fmt.Sprintf(`{"session_id":"hs1","transcript_path":"/nonexistent","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Fix the flaky login test\nplease","prompt_id":"p1"}`, repo)
	r := hook("prompt", prompt)
	if r.code != 0 || !strings.Contains(r.stdout, `You're 🧙 Gandalf for this session`) || !strings.Contains(r.stdout, `🧙 Gandalf · Fix the flaky login test`) ||
		!strings.Contains(r.stdout, "rostr task --session hs1") {
		t.Fatalf("prompt: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	waitFor(t, "the draft to be confirmed", func() bool { return len(m.list().drafted()) == 1 })
	l := m.list()
	if d := l.Rosters[0].Drafted; len(d) != 1 || d[0].Task != "Fix the flaky login test" || d[0].Repo != "acme/hooks" {
		t.Fatalf("drafted: %+v", l)
	}

	// Later prompts answer from the session's cached draft; the title follows the task without a call
	later := fmt.Sprintf(`{"session_id":"hs1","transcript_path":"/nonexistent","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"next","prompt_id":"p2","session_title":"🧙 Gandalf · Fix the flaky login test"}`, repo)
	if r := hook("prompt", later); r.code != 0 || r.stdout != "" {
		t.Fatalf("later prompt: %+v", r)
	}

	// The agent sets its task through ROSTR_SESSION; compaction re-tells the same name
	if out := m.with("ROSTR_SESSION=hs1").okIn(repo, "task", "Deflake login test"); out != "🧙 Gandalf · Deflake login test\n" {
		t.Fatalf("task: %q", out)
	}
	expectIdentity("compact", hook("session-start", start("compact")))

	// Session end releases; resume gets the old name back
	if r := hook("session-end", `{"session_id":"hs1","hook_event_name":"SessionEnd"}`); r.code != 0 {
		t.Fatalf("end: %+v", r)
	}
	if drafted := m.list().drafted(); len(drafted) != 0 {
		t.Fatalf("still drafted after session end: %v", drafted)
	}
	expectIdentity("resume", hook("session-start", start("resume")))
	waitFor(t, "the resumed draft to be confirmed", func() bool { return m.list().drafted()["Gandalf"] == "Deflake login test" })
}

func TestHooksWhenTheAPIIsDown(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	repo := m.gitRepo("git@github.com:acme/offline.git")
	m.ok("assemble", "work", "--preset", "fellowship", "--cloud")
	key, err := os.ReadFile(m.credentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	token := authToken(t, strings.TrimSpace(string(key)))

	// A port nothing listens on (the server is gone), and a server that accepts connections but never answers
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedURL := "http://" + closed.Addr().String()
	closed.Close()
	hung, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hung.Close()
	go func() {
		for {
			conn, err := hung.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	for _, down := range []struct{ name, url string }{{"server stopped", closedURL}, {"server hung", "http://" + hung.Addr().String()}} {
		offline := m.with("ROSTR_API_URL=" + down.url)
		session := "down-" + strings.ReplaceAll(down.name, " ", "-")
		envFile := filepath.Join(t.TempDir(), "claude-env")
		hook := func(event, input string) result {
			return offline.with("CLAUDE_ENV_FILE="+envFile).run(repo, input, "hook", "claude-code", event)
		}

		// Every hook exits 0 within the 3-second timeout, and session start still sets ROSTR_SESSION. The first prompt
		// names the session from the cached next state (its confirmation fails in the background), and resume re-tells
		// that name from the session's cache; the hooks that need the server stay quiet
		for _, h := range []struct {
			event, input string
			named        bool
		}{
			{"session-start", fmt.Sprintf(`{"session_id":%q,"cwd":%q,"source":"startup"}`, session, repo), false},
			{"prompt", fmt.Sprintf(`{"session_id":%q,"transcript_path":"/nonexistent","cwd":%q,"prompt":"Hello","prompt_id":"p1"}`, session, repo), true},
			{"session-start", fmt.Sprintf(`{"session_id":%q,"cwd":%q,"source":"resume"}`, session, repo), true},
			{"session-start", fmt.Sprintf(`{"session_id":%q,"cwd":%q,"source":"compact"}`, session, repo), false},
			{"session-end", fmt.Sprintf(`{"session_id":%q}`, session), false},
		} {
			r := hook(h.event, h.input)
			if r.code != 0 || strings.Contains(r.stdout, "You're ") != h.named || r.took > 5*time.Second {
				t.Fatalf("%s, %s: exit %d after %v, stdout %q, stderr %q", down.name, h.event, r.code, r.took, r.stdout, r.stderr)
			}
		}
		if env, _ := os.ReadFile(envFile); !strings.HasPrefix(string(env), "export ROSTR_SESSION="+session+"\n") {
			t.Fatalf("%s: CLAUDE_ENV_FILE %q", down.name, env)
		}
		if r := offline.run(repo, "", "list"); r.code != 1 || !strings.Contains(r.stderr, "can't reach the rostr API") {
			t.Fatalf("%s: list: %+v", down.name, r)
		}

		// The real server never heard of the session: it's unnamed
		if status := apiGet(t, "/v1/sessions/"+session, token, nil); status != 404 {
			t.Fatalf("%s: session lookup %d, want 404", down.name, status)
		}
	}
	if drafted := m.list().drafted(); len(drafted) != 0 {
		t.Fatalf("drafted while down: %v", drafted)
	}
}

func TestConcurrentDraftsNeverCollide(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.ok("assemble", "small", "--names", "A, B, C", "--cloud")

	const processes = 24
	names := make([]string, processes)
	var wg sync.WaitGroup
	for i := range processes {
		wg.Go(func() {
			r := m.run(m.home, "", "draft", "--json", "--session", fmt.Sprintf("c%d", i))
			var d draftOutput
			if r.code != 0 || json.Unmarshal([]byte(r.stdout), &d) != nil {
				t.Errorf("process %d: exit %d, %s %s", i, r.code, r.stdout, r.stderr)
			}
			names[i] = d.Name
		})
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Fatalf("%s drafted twice: %v", name, names)
		}
		seen[name] = true
	}
	// 24 drafts over 3 members fill copies 1 through 8 of each
	for copy := 1; copy <= 8; copy++ {
		for _, member := range []string{"A", "B", "C"} {
			name := member
			if copy > 1 {
				name = fmt.Sprintf("%s %d", member, copy)
			}
			if !seen[name] {
				t.Fatalf("missing %s: %v", name, names)
			}
		}
	}
	if drafted := m.list().drafted(); len(drafted) != processes {
		t.Fatalf("list shows %d drafted", len(drafted))
	}
}

func TestTwoMachinesReserveDifferentNamesAndAStaleOneGetsACopy(t *testing.T) {
	t.Parallel()
	a, b := newMachine(t), newMachine(t)
	a.ok("assemble", "work", "--names", "🧙 Gandalf, 💍 Frodo, 🌱 Sam", "--cloud")
	if out, code := b.joinWithKey(a.key()); code != 0 {
		t.Fatalf("join: exit %d\n%s", code, out)
	}
	for _, m := range []*machine{a, b} {
		if _, err := os.Stat(m.nextStatePath()); err != nil {
			t.Fatalf("no cached next state: %v", err)
		}
	}
	prompt := func(m *machine, session, id, title string) result {
		return m.run(m.home, fmt.Sprintf(`{"session_id":%q,"transcript_path":"/nonexistent","cwd":%q,"hook_event_name":"UserPromptSubmit","prompt":"Hello there","prompt_id":%q,"session_title":%q}`,
			session, m.home, id, title), "hook", "claude-code", "prompt")
	}

	// Each machine holds a different next pick, so their first sessions never announce the same name
	if r := prompt(a, "a1", "p1", ""); !strings.Contains(r.stdout, "You're 🧙 Gandalf for this session") {
		t.Fatalf("machine a: %+v", r)
	}
	if r := prompt(b, "b0", "p1", ""); !strings.Contains(r.stdout, "You're 💍 Frodo for this session") {
		t.Fatalf("machine b: %+v", r)
	}
	waitFor(t, "both drafts", func() bool {
		d := a.list().drafted()
		_, ga := d["Gandalf"]
		_, fr := d["Frodo"]
		return ga && fr
	})
	b.with("ROSTR_SESSION=b0").ok("release")

	// A machine whose cache is stale (here: edited to still offer Gandalf, who a has since taken) announces a taken
	// name; the server gives it Gandalf 2
	var stale map[string]any
	data, _ := os.ReadFile(b.nextStatePath())
	if err := json.Unmarshal(data, &stale); err != nil {
		t.Fatal(err)
	}
	work := stale["rosters"].([]any)[0].(map[string]any)
	work["reserved"] = "Gandalf"
	work["idle"] = append([]any{map[string]any{"name": "Gandalf", "emoji": "🧙", "team": "A"}}, work["idle"].([]any)...)
	data, _ = json.Marshal(stale)
	if err := os.WriteFile(b.nextStatePath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := prompt(b, "b1", "p1", ""); !strings.Contains(r.stdout, "You're 🧙 Gandalf for this session") {
		t.Fatalf("machine b, stale: %+v", r)
	}
	waitFor(t, "b's draft", func() bool { return b.list().drafted()["Gandalf 2"] != "" })

	// On its next prompt, b's session is told its real name, once, and retitled
	r := prompt(b, "b1", "p2", "🧙 Gandalf · Hello there")
	if !strings.Contains(r.stdout, "you're 🧙 Gandalf 2 now, not 🧙 Gandalf") || !strings.Contains(r.stdout, `"sessionTitle":"🧙 Gandalf 2 · Hello there"`) {
		t.Fatalf("rename: %+v", r)
	}
	if r := prompt(b, "b1", "p3", "🧙 Gandalf 2 · Hello there"); r.code != 0 || r.stdout != "" {
		t.Fatalf("after the rename: %+v", r)
	}
	// Neither session has a second draft, and b's agent can set its task under the new name
	if drafted := a.list().drafted(); len(drafted) != 2 {
		t.Fatalf("drafted: %v", drafted)
	}
	if out := b.with("ROSTR_SESSION=b1").ok("task", "Greet"); out != "🧙 Gandalf 2 · Greet\n" {
		t.Fatalf("task: %q", out)
	}
}

func TestAssembleWizardCloudOption(t *testing.T) {
	t.Parallel()
	m := newMachine(t)

	// Fellowship, its default name, emoji on, the cloud, auto-draft on
	out, code := m.runTTY([]ttyStep{
		{"How do you want to build your roster?", keyEnter},
		{"Pick a roster: type to search", keyEnter},
		{"Name this roster", keyEnter},
		{"How should agents show up?", keyEnter},
		{"Where should rostr live?", keyDown},
		{"The cloud", keyEnter},
		{"Draft names automatically in new sessions", keyEnter},
	}, "assemble")
	if code != 0 || !strings.Contains(out, "Assembled the fellowship roster") || !strings.Contains(out, "Your setup is in the cloud") {
		t.Fatalf("wizard: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(m.credentialsPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.databasePath()); !os.IsNotExist(err) {
		t.Fatalf("rostr.db: %v", err)
	}
	if l := m.list(); len(l.Rosters) != 1 || l.Rosters[0].Name != "fellowship" {
		t.Fatalf("list: %+v", l)
	}

	// Running it again can replace the cast; a drafted agent keeps their name until released
	m.draft(m.home, "s1")
	out, code = m.runTTY([]ttyStep{
		{"What do you want to do?", keyDown},
		{"Replace a roster's cast", keyEnter},
		{"Whose cast do you want to replace?", keyEnter},
		{"How do you want to build your roster?", keyEnter},
		{"Pick a roster: type to search", "hogwarts"},
		{"hogwarts", keyEnter},
	}, "assemble")
	if code != 0 || !strings.Contains(out, "Replaced the fellowship roster's cast") {
		t.Fatalf("replace cast: exit %d\n%s", code, out)
	}
	l := m.list()
	if d := l.Rosters[0].Drafted; len(d) != 1 || d[0].Display != "🧙 Gandalf" || d[0].Team != "" || l.Rosters[0].Idle[0].Name != "Harry" {
		t.Fatalf("after replacing the cast: %+v", l)
	}
	if d := m.draft(m.home, "s2"); d.Name != "Harry" {
		t.Fatalf("drafted %+v", d)
	}
}

func TestEveryCommandInCloudMode(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	repo := m.gitRepo("git@github.com:Acme/API.git")
	m.ok("assemble", "work", "--preset", "fellowship", "--cloud")
	m.ok("assemble", "play", "--names", "🧮 Ada, Grace", "--emoji", "off")
	m.ok("assemble", "side", "--preset", "free-agents")
	m.fails("already exists", "assemble", "WORK", "--preset", "hogwarts")

	// link: bare, named, and --repo
	if out := m.okIn(repo, "link"); out != "acme/api uses the work roster (your default).\n" {
		t.Fatalf("bare link: %q", out)
	}
	if out := m.okIn(repo, "link", "play"); out != "Linked acme/api to the play roster.\n" {
		t.Fatalf("link: %q", out)
	}
	if out := m.ok("link", "--repo", "Acme/API"); out != "acme/api uses the play roster (linked).\n" {
		t.Fatalf("link --repo: %q", out)
	}

	// draft: the linked roster in the repo, the default outside it, --roster over both
	if d := m.draft(repo, "s1"); d != (draftOutput{Name: "Ada", Emoji: "🧮", Team: "A", Roster: "play", Display: "Ada"}) {
		t.Fatalf("draft in repo: %+v", d)
	}
	if d := m.draft(m.home, "s2"); d.Display != "🧙 Gandalf" || d.Roster != "work" {
		t.Fatalf("draft outside repo: %+v", d)
	}
	if d := m.draft(repo, "s3", "--roster", "side"); d.Display != "🧙 Gandalf" || d.Roster != "side" {
		t.Fatalf("draft --roster: %+v", d)
	}

	// task and release, by session and by name across rosters
	if out := m.with("ROSTR_SESSION=s1").okIn(repo, "task", "Fix auth redirect loop"); out != "Ada · Fix auth redirect loop\n" {
		t.Fatalf("task: %q", out)
	}
	m.fails("Gandalf is drafted in more than one roster (work, side); pass --roster", "task", "gandalf", "x")
	m.ok("task", "GANDALF", "Review the PR", "--roster", "work")
	if out := m.ok("release", "gandalf", "--roster", "side"); out != "Released 🧙 Gandalf from the side roster.\n" {
		t.Fatalf("release by name: %q", out)
	}
	m.fails(`no agent named "ada 2" is drafted`, "release", "ada 2")
	if drafted := m.list().drafted(); len(drafted) != 2 || drafted["Ada"] != "Fix auth redirect loop" || drafted["Gandalf"] != "Review the PR" {
		t.Fatalf("drafted: %v", drafted)
	}
	if out := m.with("ROSTR_SESSION=s2").ok("release"); out != "Released 🧙 Gandalf from the work roster.\n" {
		t.Fatalf("release by session: %q", out)
	}
	if out := m.with("ROSTR_SESSION=s2").ok("release"); out != "This session has no drafted agent.\n" {
		t.Fatalf("release again: %q", out)
	}

	// unlink
	if out := m.okIn(repo, "unlink"); out != "Unlinked acme/api. It uses your default roster (work).\n" {
		t.Fatalf("unlink: %q", out)
	}
	if out := m.okIn(repo, "unlink"); out != "acme/api isn't linked. It uses your default roster (work).\n" {
		t.Fatalf("unlink again: %q", out)
	}

	// edit: drafted Ada can't be removed; other changes save
	editor := func(json string) *machine {
		file := filepath.Join(t.TempDir(), "edited.json")
		if err := os.WriteFile(file, []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
		return m.with("EDITOR=cp " + file)
	}
	editor(`{"emojiMode":"off","aTeam":[{"name":"Grace","emoji":"🏆"}],"bTeam":[]}`).fails("drafted members: Ada", "edit", "play")
	if out := editor(`{"emojiMode":"on","aTeam":[{"name":"Ada","emoji":"🧮"},{"name":"Linus","emoji":"🐧"}],"bTeam":[{"name":"Grace","emoji":"🏆"}]}`).ok("edit", "play"); out != "Saved the play roster.\n" {
		t.Fatalf("edit: %q", out)
	}
	if d := m.draft(repo, "s4", "--roster", "play"); d.Display != "🐧 Linus" {
		t.Fatalf("after edit: %+v", d)
	}

	// config setters
	m.ok("config", "default", "PLAY")
	m.ok("config", "expiry", "2w")
	m.ok("config", "auto-draft", "off")
	m.ok("config", "emoji", "only", "--roster", "work")
	m.fails("expiry must be", "config", "expiry", "soon")
	var config struct {
		Storage, Default, Expiry string
		AutoDraft                bool
		Emoji                    map[string]string
	}
	json.Unmarshal([]byte(m.ok("config", "--json")), &config)
	if config.Storage != "cloud" || config.Default != "play" || config.Expiry != "14d" || config.AutoDraft || config.Emoji["work"] != "only" {
		t.Fatalf("config: %+v", config)
	}

	// disband: asks first, releases drafts, promotes the oldest roster, and starts over after the last
	m.fails("pass --yes to disband the play roster (2 agents drafted, 0 linked projects)", "disband", "play")
	if out := m.ok("disband", "play", "--yes"); out != "Disbanded the play roster. 🧮 Ada says bye.\nwork is your default roster now.\n" {
		t.Fatalf("disband: %q", out)
	}
	if out := m.with("ROSTR_SESSION=s1").ok("release"); out != "This session has no drafted agent.\n" {
		t.Fatalf("release after disband: %q", out)
	}
	m.ok("disband", "side", "--yes")
	token := authToken(t, m.key())
	if out := m.ok("disband", "work", "--yes"); !strings.Contains(out, "your cloud setup is deleted and this machine is back to local mode") {
		t.Fatalf("disband last: %q", out)
	}

	// The server kept nothing: the key's token no longer works, and this machine dropped its credentials
	if status := apiGet(t, "/v1/settings", token, nil); status != http.StatusUnauthorized {
		t.Fatalf("settings after disbanding everything: %d", status)
	}
	if _, err := os.Stat(m.credentialsPath()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("credentials still there: %v", err)
	}
	m.fails("No rosters yet", "list")
	m.fails("No rosters yet", "draft", "--session", "s5")
}
