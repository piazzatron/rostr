package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// livePusher sends fresh counts to the live Worker after writes (see docs/live-counts.md). Writes within delay of the
// first share one push, and pushes go out one at a time, so the Worker never gets an older count after a newer one.
type livePusher struct {
	url    string
	secret string
	delay  time.Duration
	db     querier
	logger *slog.Logger

	mu        sync.Mutex
	scheduled bool
	sending   sync.Mutex
}

// notify schedules a push unless one is already waiting. A nil pusher (LIVE_URL or LIVE_SECRET unset) does nothing.
func (p *livePusher) notify() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scheduled {
		return
	}
	p.scheduled = true
	time.AfterFunc(p.delay, p.push)
}

func (p *livePusher) push() {
	p.sending.Lock()
	defer p.sending.Unlock()

	// Writes from here on schedule the next push, since this one may count from before them
	p.mu.Lock()
	p.scheduled = false
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	counts, err := queryLiveCounts(ctx, p.db)
	if err != nil {
		p.logger.Error("live push", "error", err)
		return
	}

	body, _ := json.Marshal(counts)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/update", bytes.NewReader(body))
	if err != nil {
		p.logger.Error("live push", "error", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.logger.Error("live push", "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		p.logger.Error("live push", "status", resp.StatusCode)
	}
}
