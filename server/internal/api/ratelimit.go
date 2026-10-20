package api

import (
	"sync"
	"time"
)

// rateLimiter allows up to limit calls per key in each fixed window. Counts live in memory and reset when a new window
// starts, so a restart or a second instance resets them too.
type rateLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	windowStart time.Time
	counts      map[string]int
	now         func() time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, counts: map[string]int{}, now: time.Now}
}

// allow counts a call for key and reports whether it's within the limit, and if not, how long until the window resets.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if now.Sub(l.windowStart) >= l.window {
		l.windowStart = now
		l.counts = map[string]int{}
	}

	if l.counts[key] >= l.limit {
		return false, l.window - now.Sub(l.windowStart)
	}
	l.counts[key]++
	return true, 0
}
