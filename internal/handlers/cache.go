package handlers

import (
	"sync"
	"time"
)

// analyticsCacheTTL keeps expensive analytics queries warm across page
// views while still converging on freshly ingested votes.
const analyticsCacheTTL = 10 * time.Minute

// ttlCache memoizes expensive read-only query results under the string key
// describing their parameters. Entries expire after ttl; set prunes expired
// entries and, if still at capacity, drops the oldest — bounded memory
// without a magic full-flush.
type ttlCache[T any] struct {
	ttl time.Duration
	max int

	mu sync.Mutex
	m  map[string]ttlEntry[T]
}

type ttlEntry[T any] struct {
	val T
	at  time.Time
}

func newTTLCache[T any](ttl time.Duration, max int) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, max: max, m: map[string]ttlEntry[T]{}}
}

func (c *ttlCache[T]) get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Since(e.at) >= c.ttl {
		var zero T
		return zero, false
	}
	return e.val, true
}

func (c *ttlCache[T]) set(key string, val T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.m {
		if now.Sub(e.at) >= c.ttl {
			delete(c.m, k)
		}
	}
	for len(c.m) >= c.max {
		var oldestKey string
		var oldest time.Time
		first := true
		for k, e := range c.m {
			if first || e.at.Before(oldest) {
				oldestKey, oldest, first = k, e.at, false
			}
		}
		delete(c.m, oldestKey)
	}
	c.m[key] = ttlEntry[T]{val: val, at: now}
}
