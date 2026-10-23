package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLivePushCoalescesWrites(t *testing.T) {
	t.Parallel()
	pushes := fakeWorker(t)
	p := &livePusher{url: pushes.url, secret: "s3cret", delay: 50 * time.Millisecond, db: testDB}

	// A burst of writes makes one push, with the secret and the fresh counts
	for range 5 {
		p.notify()
	}
	push := pushes.next(t)
	if push.path != "/update" || push.auth != "Bearer s3cret" {
		t.Fatalf("push to %s with %q", push.path, push.auth)
	}
	var body map[string]int
	if err := json.Unmarshal(push.body, &body); err != nil || len(body) != 2 {
		t.Fatalf("push body %s: %v", push.body, err)
	}
	pushes.none(t)

	// A later write pushes again
	p.notify()
	pushes.next(t)
}

func TestWritesPushLiveStats(t *testing.T) {
	t.Parallel()
	pushes := fakeWorker(t)
	c := &client{t: t, token: newToken(), handler: NewHandler(testDB, Config{
		RegistrationsPerHour: 1_000_000, RequestsPerMinute: 1_000_000, LiveURL: pushes.url, LiveSecret: "s3cret",
	})}

	// Registering, creating a roster, and drafting within half a second make one push
	call[settingsResponse](c, "POST", "/v1/register", map[string]any{"authToken": c.token}, http.StatusCreated)
	createRoster(c, "work", members("Gandalf"), nil)
	draftSessionNamed(c, c.session("s1"))
	pushes.next(t)
	pushes.none(t)

	// Reads don't push
	list(c)
	pushes.none(t)

	// Live stats are off without both settings
	var off *livePusher
	off.notify()
}

type livePush struct {
	path, auth string
	body       []byte
}

// workerPushes are the pushes a fake live Worker received.
type workerPushes struct {
	url string
	ch  chan livePush
}

func fakeWorker(t *testing.T) workerPushes {
	ch := make(chan livePush, 10)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch <- livePush{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: body}
	}))
	t.Cleanup(worker.Close)
	return workerPushes{url: worker.URL, ch: ch}
}

func (w workerPushes) next(t *testing.T) livePush {
	t.Helper()
	select {
	case push := <-w.ch:
		return push
	case <-time.After(3 * time.Second):
		t.Fatal("no push")
		return livePush{}
	}
}

func (w workerPushes) none(t *testing.T) {
	t.Helper()
	select {
	case push := <-w.ch:
		t.Fatalf("unexpected push: %s", push.body)
	case <-time.After(700 * time.Millisecond):
	}
}
