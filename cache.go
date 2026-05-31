package arca

import (
	"container/list"
	"sort"
	"strings"
	"sync"
	"time"
)

// CacheConfig configures the in-memory history cache (equity history, PnL
// history, candles).
type CacheConfig struct {
	// MaxEntries caps the number of cached responses (default 50).
	MaxEntries int
	// TTL is the entry lifetime. Zero uses the 5-minute default; set Disabled
	// to turn caching off entirely.
	TTL time.Duration
	// Disabled turns off caching entirely.
	Disabled bool
}

const (
	defaultCacheMaxEntries = 50
	defaultCacheTTL        = 5 * time.Minute
)

type cacheEntry struct {
	key       string
	value     any
	expiresAt time.Time
}

// historyCache is a small LRU+TTL cache. A nil *historyCache is a valid no-op.
type historyCache struct {
	mu       sync.Mutex
	disabled bool
	max      int
	ttl      time.Duration
	ll       *list.List
	items    map[string]*list.Element
}

func newHistoryCache(cfg *CacheConfig) *historyCache {
	c := &historyCache{
		max:   defaultCacheMaxEntries,
		ttl:   defaultCacheTTL,
		ll:    list.New(),
		items: map[string]*list.Element{},
	}
	if cfg != nil {
		if cfg.Disabled {
			c.disabled = true
		}
		if cfg.MaxEntries > 0 {
			c.max = cfg.MaxEntries
		}
		if cfg.TTL != 0 {
			c.ttl = cfg.TTL
		}
	}
	return c
}

func (c *historyCache) get(key string) (any, bool) {
	if c == nil || c.disabled {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	ent := el.Value.(*cacheEntry)
	if c.ttl > 0 && time.Now().After(ent.expiresAt) {
		c.ll.Remove(el)
		delete(c.items, key)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return ent.value, true
}

func (c *historyCache) set(key string, value any) {
	if c == nil || c.disabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		ent := el.Value.(*cacheEntry)
		ent.value = value
		ent.expiresAt = time.Now().Add(c.ttl)
		c.ll.MoveToFront(el)
		return
	}
	ent := &cacheEntry{key: key, value: value, expiresAt: time.Now().Add(c.ttl)}
	el := c.ll.PushFront(ent)
	c.items[key] = el
	for c.ll.Len() > c.max {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry).key)
	}
}

func (c *historyCache) delete(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.Remove(el)
		delete(c.items, key)
	}
}

func (c *historyCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = map[string]*list.Element{}
}

// buildCacheKey produces a stable key from a prefix and sorted params.
func buildCacheKey(prefix string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(prefix)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	return b.String()
}
