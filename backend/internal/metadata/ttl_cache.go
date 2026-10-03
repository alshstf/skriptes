package metadata

import (
	"sync"
	"time"
)

// ttlCache — маленький кэш с истечением: цепочки био и фото одного автора идут
// друг за другом и повторяли бы одни и те же поиски и запросы к Wikidata (#280).
// При переполнении выбрасываются истёкшие записи, а если их нет — все (кэш
// короткоживущий, точная LRU-политика не нужна).
type ttlCache[V any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	size  int
	items map[string]ttlItem[V]
}

type ttlItem[V any] struct {
	v  V
	at time.Time
}

func newTTLCache[V any](ttl time.Duration, size int) *ttlCache[V] {
	return &ttlCache[V]{ttl: ttl, size: size, items: map[string]ttlItem[V]{}}
}

func (c *ttlCache[V]) get(key string) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok || time.Since(it.at) > c.ttl {
		return zero, false
	}
	return it.v, true
}

func (c *ttlCache[V]) put(key string, v V) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.size {
		for k, it := range c.items {
			if time.Since(it.at) > c.ttl {
				delete(c.items, k)
			}
		}
		if len(c.items) >= c.size {
			c.items = map[string]ttlItem[V]{}
		}
	}
	c.items[key] = ttlItem[V]{v: v, at: time.Now()}
}

// lookupCacheTTL — сколько помнить найденную статью и факты: хватает на пару
// «био → фото» одного автора, а новых правок источников за это время не ждём.
const (
	lookupCacheTTL  = 10 * time.Minute
	lookupCacheSize = 2000
)
