package api

import "sync"

// ownerLocks makes one owner's drafts on this instance take turns, so a burst of sessions starting at once doesn't
// have every draft pick the same name and retry. Drafts on different instances can still race; the unique indexes
// catch those (see draftSession).
type ownerLocks struct {
	mu    sync.Mutex
	locks map[string]*ownerLock
}

type ownerLock struct {
	sync.Mutex
	waiting int // holders and waiters, so the lock is dropped once nobody needs it
}

// lock takes the owner's lock and returns the function that releases it.
func (l *ownerLocks) lock(ownerID string) (unlock func()) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[string]*ownerLock{}
	}
	ol := l.locks[ownerID]
	if ol == nil {
		ol = &ownerLock{}
		l.locks[ownerID] = ol
	}
	ol.waiting++
	l.mu.Unlock()

	ol.Lock()
	return func() {
		ol.Unlock()
		l.mu.Lock()
		if ol.waiting--; ol.waiting == 0 {
			delete(l.locks, ownerID)
		}
		l.mu.Unlock()
	}
}
