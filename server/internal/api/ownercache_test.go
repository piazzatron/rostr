package api

import (
	"net/http"
	"testing"
	"time"
)

func TestOwnerCacheExpiresAndForgets(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	c := newOwnerCache()
	c.now = func() time.Time { return now }

	c.put("h1", "owner-1")
	c.put("h2", "owner-1")
	c.put("h3", "owner-2")
	if id, ok := c.get("h1"); !ok || id != "owner-1" {
		t.Fatalf("get h1: %q %v", id, ok)
	}

	c.forgetOwner("owner-1")
	if _, ok := c.get("h1"); ok {
		t.Fatal("h1 still cached after forgetting its owner")
	}
	if _, ok := c.get("h2"); ok {
		t.Fatal("h2 still cached after forgetting its owner")
	}
	c.forgetHash("h3")
	if _, ok := c.get("h3"); ok {
		t.Fatal("h3 still cached after forgetting it")
	}

	// Entries expire, and the next put sweeps them out
	c.put("h4", "owner-4")
	now = now.Add(ownerCacheTTL)
	if _, ok := c.get("h4"); ok {
		t.Fatal("h4 still cached after its TTL")
	}
	c.put("h5", "owner-5")
	if _, ok := c.entries["h4"]; ok || len(c.entries) != 1 {
		t.Fatalf("expired entries left after a sweep: %v", c.entries)
	}
}

// A second instance that cached an owner since deleted on the first answers its next write with 401 and forgets it, and
// once the token is registered again it finds the new owner.
func TestOwnerCacheAcrossInstances(t *testing.T) {
	t.Parallel()
	c := newOwner(t)
	createRoster(c, "only", members("Gandalf"), nil)
	other := &client{t: t, handler: NewHandler(testDB, Config{RegistrationsPerHour: 1_000_000, RequestsPerMinute: 1_000_000}), token: c.token}
	call[settingsResponse](other, "GET", "/v1/settings", nil, http.StatusOK) // caches the owner

	call[map[string]any](c, "DELETE", "/v1/rosters/only", nil, http.StatusOK) // deletes the owner
	callError(other, "POST", "/v1/rosters", map[string]any{"name": "new", "aTeam": members("Frodo")},
		http.StatusUnauthorized, "unauthorized")

	call[settingsResponse](c, "POST", "/v1/register", map[string]any{"authToken": c.token}, http.StatusCreated)
	createRoster(other, "new", members("Frodo"), nil)
	if l := list(c); len(l.Rosters) != 1 || l.Rosters[0].Name != "new" {
		t.Fatalf("after re-registering: %+v", l)
	}
}
