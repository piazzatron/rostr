package api

import (
	"sync"
	"time"
)

// ownerCacheTTL is how long an instance trusts a token's owner without asking the database again.
const ownerCacheTTL = time.Minute

// ownerCache remembers which owner each auth token hash belongs to, so most requests skip the lookup. Only a deleted
// owner (its last roster disbanded) makes an entry wrong: this instance forgets it right away, and another instance
// trusts it for at most ownerCacheTTL, during which withOwnerTx finds no owner row and answers 401.
type ownerCache struct {
	mu        sync.Mutex
	entries   map[string]ownerCacheEntry // by token hash
	lastSweep time.Time
	now       func() time.Time
}

type ownerCacheEntry struct {
	ownerID string
	expires time.Time
}

func newOwnerCache() *ownerCache {
	return &ownerCache{entries: map[string]ownerCacheEntry{}, now: time.Now}
}

// get returns the cached owner for a token hash, if there's one that hasn't expired.
func (c *ownerCache) get(hash string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[hash]
	if !ok || !c.now().Before(e.expires) {
		return "", false
	}
	return e.ownerID, true
}

// put caches a token hash's owner, first dropping expired entries if it hasn't in a while.
func (c *ownerCache) put(hash, ownerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.Sub(c.lastSweep) >= ownerCacheTTL {
		for h, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, h)
			}
		}
		c.lastSweep = now
	}
	c.entries[hash] = ownerCacheEntry{ownerID: ownerID, expires: now.Add(ownerCacheTTL)}
}

// forgetHash drops a token hash, as when it's registered to a new owner.
func (c *ownerCache) forgetHash(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, hash)
}

// forgetOwner drops every token hash cached for an owner, as when the owner is deleted.
func (c *ownerCache) forgetOwner(ownerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for h, e := range c.entries {
		if e.ownerID == ownerID {
			delete(c.entries, h)
		}
	}
}
