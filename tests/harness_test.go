// Package tests runs the real rostr CLI against the real API server and Postgres from Docker Compose. Run it with
// tests/run.sh, which starts the stack, runs these tests, and tears it down.
package tests

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// binary is the rostr CLI, built once from ../cli.
	binary string
	// apiURL is the compose API server, and db its Postgres.
	apiURL string
	db     *pgxpool.Pool
)

func TestMain(m *testing.M) {
	apiURL = envOr("ROSTR_E2E_API_URL", "http://localhost:58080")
	databaseURL := envOr("ROSTR_E2E_DATABASE_URL", "postgres://rostr:rostr@localhost:55432/rostr?sslmode=disable")

	// The stack must already be up
	if resp, err := http.Get(apiURL + "/health"); err != nil || resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "the API at %s isn't healthy (%v); run tests/run.sh, which starts it\n", apiURL, err)
		os.Exit(1)
	}
	var err error
	if db, err = pgxpool.New(context.Background(), databaseURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Build the CLI
	dir, err := os.MkdirTemp("", "rostr-e2e-bin")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "rostr")
	build := exec.Command("go", "build", "-o", binary, "./cmd/rostr")
	build.Dir = "../cli"
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building rostr: %v\n%s", err, out)
		os.Exit(1)
	}

	code := m.Run()
	db.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

// machine is one computer running rostr: its own temp HOME (and XDG and Claude Code dirs), so nothing touches the
// real home directory.
type machine struct {
	t    *testing.T
	home string
	env  []string
}

type result struct {
	stdout, stderr string
	code           int
	took           time.Duration
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	home := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "HOME" || name == "VISUAL" || name == "EDITOR" || strings.HasPrefix(name, "XDG_") ||
			strings.HasPrefix(name, "CLAUDE") || strings.HasPrefix(name, "ROSTR_") || strings.HasPrefix(name, "GIT_") ||
			strings.HasPrefix(name, "CODEX_") || strings.HasPrefix(name, "TMUX") || strings.HasPrefix(name, "HERDR_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"),
		"ROSTR_API_URL="+apiURL,
		"ROSTR_CLAUDE_BIN=/usr/bin/false", // no real claude -p summaries
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"TERM=xterm-256color",
	)
	return &machine{t: t, home: home, env: env}
}

// with returns a copy of the machine with extra environment variables ("KEY=value"), which override its own.
func (m *machine) with(env ...string) *machine {
	return &machine{t: m.t, home: m.home, env: append(append([]string{}, m.env...), env...)}
}

// run runs rostr in dir, without a terminal (as agents and hooks do), with stdin.
func (m *machine) run(dir, stdin string, args ...string) result {
	m.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = m.env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String(), took: time.Since(start)}
	if exit, ok := err.(*exec.ExitError); ok {
		r.code = exit.ExitCode()
	} else if err != nil {
		m.t.Fatal(err)
	}
	return r
}

// ok runs rostr in the machine's home and fails the test unless it exits 0.
func (m *machine) ok(args ...string) string {
	m.t.Helper()
	return m.okIn(m.home, args...)
}

func (m *machine) okIn(dir string, args ...string) string {
	m.t.Helper()
	r := m.run(dir, "", args...)
	if r.code != 0 {
		m.t.Fatalf("rostr %v exited %d\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

// fails runs rostr and fails the test unless it exits 1 with stderr containing want.
func (m *machine) fails(want string, args ...string) {
	m.t.Helper()
	r := m.run(m.home, "", args...)
	if r.code != 1 || !strings.Contains(r.stderr, want) {
		m.t.Fatalf("rostr %v: exit %d, stderr %q; want exit 1 mentioning %q", args, r.code, r.stderr, want)
	}
}

// ttyStep waits for text in a terminal's output, then types input.
type ttyStep struct {
	waitFor string
	input   string
}

const (
	keyUp    = "\x1b[A"
	keyDown  = "\x1b[B"
	keyLeft  = "\x1b[D"
	keyEnter = "\r"
	keyEsc   = "\x1b"
)

// runTTY runs rostr in a pseudo-terminal, as a person at a terminal would, answering its prompts step by step. It
// returns everything the terminal showed and the exit code.
func (m *machine) runTTY(steps []ttyStep, args ...string) (string, int) {
	m.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = m.home
	cmd.Env = m.env
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 200})
	if err != nil {
		m.t.Fatal(err)
	}
	defer terminal.Close()

	// Collect the output, answering the background-color and cursor-position queries that terminal UI libraries send,
	// so they don't wait for a reply that never comes
	var mu sync.Mutex
	var output bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			mu.Lock()
			output.Write(buf[:n])
			mu.Unlock()
			if bytes.Contains(buf[:n], []byte("\x1b]11;?")) {
				terminal.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
			}
			if bytes.Contains(buf[:n], []byte("\x1b[6n")) {
				terminal.Write([]byte("\x1b[1;1R"))
			}
			if err != nil {
				return
			}
		}
	}()
	shown := func() string {
		mu.Lock()
		defer mu.Unlock()
		return output.String()
	}

	for _, step := range steps {
		deadline := time.Now().Add(15 * time.Second)
		for !strings.Contains(shown(), step.waitFor) {
			if time.Now().After(deadline) {
				cmd.Process.Kill()
				m.t.Fatalf("rostr %v: never showed %q; showed:\n%s", args, step.waitFor, shown())
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond) // let the prompt finish drawing before typing
		if _, err := terminal.Write([]byte(step.input)); err != nil {
			m.t.Fatal(err)
		}
	}

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err = <-waited:
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		m.t.Fatalf("rostr %v didn't exit; showed:\n%s", args, shown())
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
	}
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	}
	return shown(), code
}

var (
	keyPattern   = regexp.MustCompile(`rostr_sk_[A-Za-z0-9_-]{43}`)
	tokenPattern = regexp.MustCompile(`([A-Za-z0-9_-]{43})\r?\n`)
)

// key prints the machine's rostr key with `rostr config key`, at a terminal.
func (m *machine) key() string {
	m.t.Helper()
	out, code := m.runTTY(nil, "config", "key")
	key := keyPattern.FindString(out)
	if code != 0 || key == "" {
		m.t.Fatalf("config key: exit %d, output %q", code, out)
	}
	return key
}

// joinWithKey runs `rostr cloud on` at a terminal and pastes key as an existing key.
func (m *machine) joinWithKey(key string) (string, int) {
	m.t.Helper()
	return m.runTTY([]ttyStep{
		{"New setup, or do you have a key?", keyDown},
		{"I have a key", keyEnter},
		{"Paste your rostr key", key + keyEnter},
	}, "cloud", "on")
}

func (m *machine) credentialsPath() string {
	return filepath.Join(m.home, ".config", "rostr", "credentials")
}

func (m *machine) databasePath() string {
	return filepath.Join(m.home, ".config", "rostr", "rostr.db")
}

// gitRepo makes a repo in a temp dir with the given origin.
func (m *machine) gitRepo(origin string) string {
	m.t.Helper()
	dir := filepath.Join(m.t.TempDir(), "checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = m.env
		if out, err := cmd.CombinedOutput(); err != nil {
			m.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// listing is `rostr stats --json`.
type listing struct {
	Rosters []struct {
		Name      string
		EmojiMode string
		Drafted   []struct{ Name, Emoji, Display, Task, Repo string }
		Bench     []struct{ Name, Emoji string }
	}
}

func (m *machine) list() listing {
	m.t.Helper()
	var out listing
	if err := json.Unmarshal([]byte(m.ok("stats", "--json")), &out); err != nil {
		m.t.Fatal(err)
	}
	return out
}

// drafted maps each drafted agent's name to its task, across all rosters.
func (l listing) drafted() map[string]string {
	out := map[string]string{}
	for _, r := range l.Rosters {
		for _, d := range r.Drafted {
			out[d.Name] = d.Task
		}
	}
	return out
}

type draftOutput struct{ Name, Emoji, Roster, Display string }

func (m *machine) draft(dir, session string, args ...string) draftOutput {
	m.t.Helper()
	var out draftOutput
	stdout := m.okIn(dir, append([]string{"draft", "--json", "--session", session}, args...)...)
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		m.t.Fatalf("%v: %s", err, stdout)
	}
	return out
}

// derivedKey follows the published recipe: HKDF-SHA256 over the key's 32 bytes, no salt, with an info label.
func derivedKey(t *testing.T, rostrKey, info string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(rostrKey, "rostr_sk_"))
	if err != nil || len(raw) != 32 {
		t.Fatalf("bad key %q: %v", rostrKey, err)
	}
	derived, err := hkdf.Key(sha256.New, raw, nil, info, 32)
	if err != nil {
		t.Fatal(err)
	}
	return derived
}

// decrypt opens a value from the API with the recipe: standard base64 of a 12-byte nonce, then AES-256-GCM
// ciphertext and tag.
func decrypt(t *testing.T, rostrKey, value string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) < 28 {
		t.Fatalf("bad ciphertext %q: %v", value, err)
	}
	block, err := aes.NewCipher(derivedKey(t, rostrKey, "encrypt"))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := gcm.Open(nil, raw[:12], raw[12:], nil)
	if err != nil {
		t.Fatalf("decrypting %q: %v", value, err)
	}
	return string(plaintext)
}

// authToken is the bearer token derived from a key by the recipe.
func authToken(t *testing.T, rostrKey string) string {
	return base64.RawURLEncoding.EncodeToString(derivedKey(t, rostrKey, "auth"))
}

// apiGet calls the API with a bearer token and decodes the JSON response.
func apiGet(t *testing.T, path, token string, out any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", apiURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

// waitFor polls cond for up to timeout, failing the test if it never holds. Hooks do some of their work in the
// background (drafting ahead of a first prompt, sending its task), so what the server shows lags a hook by a moment.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// unique makes a string no other test (or earlier run) uses, so database-wide checks only see this test's data.
func unique(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
