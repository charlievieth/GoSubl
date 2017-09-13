package lru

import (
	"container/list"
	"go/build"
)

type Cache struct {
	// MaxEntries is the maximum number of cache entries before
	// an item is evicted. Zero means no limit.
	MaxEntries int

	ll    *list.List
	cache map[uint64]map[string]*list.Element
}

type entry struct {
	sum   uint64
	key   string
	value *Package
}

// New creates a new Cache.
// If maxEntries is zero, the cache has no limit and it's assumed
// that eviction is done by the caller.
func New(maxEntries int) *Cache {
	return &Cache{
		MaxEntries: maxEntries,
		ll:         list.New(),
		cache:      make(map[uint64]map[string]*list.Element),
	}
}

// Add adds a value to the cache.
func (c *Cache) Add(ctxt *build.Context, key string, value *Package) {
	if c.cache == nil {
		c.cache = make(map[uint64]map[string]*list.Element)
		c.ll = list.New()
	}
	sum := hash(ctxt)
	m, ok := c.cache[sum]
	if !ok {
		m = make(map[string]*list.Element)
		c.cache[sum] = m
		// c.cache[sum] = make(map[string]*list.Element)
	}
	if ee, ok := m[key]; ok {
		c.ll.MoveToFront(ee)
		ee.Value.(*entry).value = value
		return
	}
	ele := c.ll.PushFront(&entry{sum: sum, key: key, value: value})
	m[key] = ele
	if c.MaxEntries != 0 && c.ll.Len() > c.MaxEntries {
		c.RemoveOldest()
	}
}

// Get looks up a key's value from the cache.
func (c *Cache) Get(ctxt *build.Context, key string) (value *Package, ok bool) {
	if c.cache == nil {
		return
	}
	if m, ok := c.cache[hash(ctxt)]; ok {
		if ele, hit := m[key]; hit {
			c.ll.MoveToFront(ele)
			return ele.Value.(*entry).value, true
		}
	}
	return
}

// Remove removes the provided key from the cache.
func (c *Cache) Remove(ctxt *build.Context, key string) {
	if c.cache == nil {
		return
	}
	if m, ok := c.cache[hash(ctxt)]; ok {
		if ele, hit := m[key]; hit {
			c.removeElement(ele)
		}
	}
}

// RemoveOldest removes the oldest item from the cache.
func (c *Cache) RemoveOldest() {
	if c.cache == nil {
		return
	}
	ele := c.ll.Back()
	if ele != nil {
		c.removeElement(ele)
	}
}

func (c *Cache) removeElement(e *list.Element) {
	c.ll.Remove(e)
	kv := e.Value.(*entry)
	delete(c.cache[kv.sum], kv.key)
	if len(c.cache[kv.sum]) == 0 {
		delete(c.cache, kv.sum)
	}
}

// Len returns the number of items in the cache.
func (c *Cache) Len() int {
	if c.cache == nil {
		return 0
	}
	return c.ll.Len()
}
