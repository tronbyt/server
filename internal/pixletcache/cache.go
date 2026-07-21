// Package pixletcache wraps a pixlet runtime.Cache to add on-demand cache
// invalidation. Pixlet's HTTP cache honors the per-request TTL configured by an
// app's http.get(ttl_seconds=...) call, which means dynamic schema data (e.g.
// dropdown options fetched from GitHub inside get_schema) only refreshes after
// that TTL expires. BypassCache lets the server force a one-shot refetch so a
// user can pull the latest options without waiting out the app's TTL.
package pixletcache

import (
	"context"
	"strings"
	"sync"

	"github.com/tronbyt/pixlet/runtime"
)

// BypassCache wraps a pixlet runtime.Cache and can force cache reads for keys
// matching a prefix to miss, causing the caller to refetch and overwrite the
// stale entry. All other behavior is delegated to the wrapped cache.
type BypassCache struct {
	inner runtime.Cache

	mu       sync.RWMutex
	prefixes map[string]int // active bypass prefix -> refcount
}

// New wraps the given cache.
func New(inner runtime.Cache) *BypassCache {
	return &BypassCache{
		inner:    inner,
		prefixes: make(map[string]int),
	}
}

// Get returns a miss for keys currently being bypassed; otherwise it delegates
// to the wrapped cache.
func (c *BypassCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if c.bypassed(key) {
		return nil, false, nil
	}
	return c.inner.Get(ctx, key)
}

// Set delegates to the wrapped cache.
func (c *BypassCache) Set(ctx context.Context, key string, value []byte, ttl int64) error {
	return c.inner.Set(ctx, key, value, ttl)
}

// Close delegates to the wrapped cache.
func (c *BypassCache) Close() {
	c.inner.Close()
}

// WithBypass runs fn while forcing cache reads for keys with the given prefix to
// miss, so any http.get issued during fn refetches from origin and overwrites
// the cached entry (resetting its TTL). Reads are restored once fn returns.
// Concurrent calls with the same prefix are reference-counted so they don't
// clear each other prematurely.
func (c *BypassCache) WithBypass(prefix string, fn func() error) error {
	c.add(prefix)
	defer c.remove(prefix)
	return fn()
}

func (c *BypassCache) add(prefix string) {
	c.mu.Lock()
	c.prefixes[prefix]++
	c.mu.Unlock()
}

func (c *BypassCache) remove(prefix string) {
	c.mu.Lock()
	if c.prefixes[prefix] <= 1 {
		delete(c.prefixes, prefix)
	} else {
		c.prefixes[prefix]--
	}
	c.mu.Unlock()
}

func (c *BypassCache) bypassed(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for prefix := range c.prefixes {
		// Guard against an empty prefix, which would match every key and
		// inadvertently bypass the entire cache.
		if prefix != "" && strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
