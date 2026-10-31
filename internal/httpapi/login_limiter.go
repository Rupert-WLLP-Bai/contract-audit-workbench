package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Local username/password logins are throttled in the process that answers
// them. Counting stays bounded so hostile traffic can neither grow memory nor
// lock an account forever: each key observes one fixed failure window, a
// block always expires, and the counter map holds at most
// loginCounterMaxEntries keys — stale windows are dropped and the least
// recently failed key is evicted beyond that.
//
// Every failed attempt is counted on two independent dimensions:
//   - the submitted username, which caps guessing against one account no
//     matter how many sources the guesser controls;
//   - the trusted request source, which caps credential spraying across many
//     usernames from one place.
//
// The trusted source is the TCP peer address only. Browser-supplied
// X-Forwarded-For / X-Real-IP headers are never consulted: any client can set
// them, and the internal gateway strips every forwarded header by design, so
// no request header names a trustworthy origin. Two deployment consequences
// follow and are accepted here: behind the gateway all requests share one
// source key (the gateway's own address), and each replica of a multi-replica
// deployment counts independently, so effective budgets multiply by replica
// count. Per-client granularity or a global budget needs limiting at the
// shared edge instead — the delivered nginx sites throttle POST /api/session
// for exactly that reason. Unknown usernames are counted like existing ones,
// and a block answers with the same body as any other failed login, so
// responses never enumerate accounts.
const (
	// loginAccountFailureLimit bounds guesses against one username.
	loginAccountFailureLimit = 5
	// loginSourceFailureLimit bounds spraying from one source address.
	loginSourceFailureLimit = 50
	// loginFailureWindow is how long a failure count is observed.
	loginFailureWindow = 15 * time.Minute
	// loginLockoutDuration is how long a tripped counter refuses attempts.
	loginLockoutDuration = 15 * time.Minute
	// loginCounterMaxEntries bounds the counter map against hostile key growth.
	loginCounterMaxEntries = 4096
)

type loginCounter struct {
	mu      sync.Mutex
	entries map[string]*loginFailures
}

// loginFailures is one dimension's fixed window of failed attempts.
type loginFailures struct {
	windowStart  time.Time
	lastFailure  time.Time
	failures     int
	blockedUntil time.Time
}

func newLoginCounter() *loginCounter {
	return &loginCounter{entries: make(map[string]*loginFailures)}
}

// loginSource returns the only origin the service can trust: the TCP peer
// address without its port. It never reads forwarded headers.
func loginSource(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return peer
}

func loginKey(dimension, value string) string {
	return dimension + "\x00" + value
}

// allowed reports whether a login attempt may run its credential check. A
// blocked dimension refuses without distinguishing known from unknown users.
func (c *loginCounter) allowed(source, username string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	return !c.blocked(loginKey("account", username), now) &&
		!c.blocked(loginKey("source", source), now)
}

func (c *loginCounter) blocked(key string, now time.Time) bool {
	entry := c.entries[key]
	return entry != nil && now.Before(entry.blockedUntil)
}

// recordFailure counts one failed credential check on both dimensions.
func (c *loginCounter) recordFailure(source, username string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.count(loginKey("account", username), loginAccountFailureLimit, now)
	c.count(loginKey("source", source), loginSourceFailureLimit, now)
	c.trim(now)
}

func (c *loginCounter) count(key string, limit int, now time.Time) {
	entry := c.entries[key]
	if entry == nil || now.Sub(entry.windowStart) >= loginFailureWindow {
		entry = &loginFailures{windowStart: now}
		c.entries[key] = entry
	}
	entry.lastFailure = now
	entry.failures++
	if entry.failures > limit {
		// The window restarts when the block is set, so the entry expires
		// cleanly and a lifted block starts with a fresh bounded budget.
		entry.blockedUntil = now.Add(loginLockoutDuration)
		entry.windowStart = now
		entry.failures = 0
	}
}

// recordSuccess clears the username dimension after a correct credential.
// The source dimension deliberately survives: a valid login must not let a
// co-located attacker reset the spraying budget of the address they share.
func (c *loginCounter) recordSuccess(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, loginKey("account", username))
}

// trim keeps the counter map bounded. It runs only once the map is full:
// expired windows and lifted blocks are dropped first, and if hostile keys
// still fill the map the least recently failed entries are evicted.
func (c *loginCounter) trim(now time.Time) {
	if len(c.entries) < loginCounterMaxEntries {
		return
	}
	for key, entry := range c.entries {
		if now.Sub(entry.lastFailure) >= loginFailureWindow && now.After(entry.blockedUntil) {
			delete(c.entries, key)
		}
	}
	for len(c.entries) >= loginCounterMaxEntries {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.lastFailure.Before(oldest) {
				oldestKey, oldest = key, entry.lastFailure
			}
		}
		delete(c.entries, oldestKey)
	}
}
