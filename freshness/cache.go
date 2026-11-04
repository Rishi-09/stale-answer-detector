package freshness

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// CachedVerdict wraps a Claim verdict with its expiration time.
type CachedVerdict struct {
	Claim     Claim
	ExpiresAt time.Time
}

type lruEntry struct {
	key       string
	verdict   CachedVerdict
	element   *list.Element
	category  string
	createdAt time.Time
}

// ClaimCache provides bounded LRU eviction, per-category TTLs, and single-flight synchronization.
type ClaimCache struct {
	mu           sync.RWMutex
	capacity     int
	items        map[string]*lruEntry
	evictionList *list.List
	categoryTTLs map[string]time.Duration
	flightMu     sync.Mutex
	flightGroup  map[string]*singleFlightCall
	hits         int64
	misses       int64
}

type singleFlightCall struct {
	wg  sync.WaitGroup
	val Claim
	err error
}

// NewClaimCache constructs a bounded LRU + TTL cache.
func NewClaimCache(capacity int, categoryTTLHours map[string]int) *ClaimCache {
	if capacity <= 0 {
		capacity = 5000
	}

	ttls := make(map[string]time.Duration)
	for cat, hours := range categoryTTLHours {
		if hours > 0 {
			ttls[cat] = time.Duration(hours) * time.Hour
		} else {
			ttls[cat] = 24 * time.Hour
		}
	}

	return &ClaimCache{
		capacity:     capacity,
		items:        make(map[string]*lruEntry, capacity),
		evictionList: list.New(),
		categoryTTLs: ttls,
		flightGroup:  make(map[string]*singleFlightCall),
	}
}

// ComputeClaimKey returns the unique hash for deduplication.
func ComputeClaimKey(category, entity, attribute, value string) string {
	h := sha256.New()
	norm := fmt.Sprintf("%s|%s|%s|%s",
		strings.ToLower(strings.TrimSpace(category)),
		strings.ToLower(strings.TrimSpace(entity)),
		strings.ToLower(strings.TrimSpace(attribute)),
		strings.ToLower(strings.TrimSpace(value)),
	)
	h.Write([]byte(norm))
	return hex.EncodeToString(h.Sum(nil))
}

// Get looks up a cached claim verdict. Returns claim and true if valid and unexpired.
func (c *ClaimCache) Get(category, entity, attribute, value string) (Claim, bool) {
	key := ComputeClaimKey(category, entity, attribute, value)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.items[key]
	if !ok {
		c.misses++
		return Claim{}, false
	}

	// Check TTL expiration
	if time.Now().After(entry.verdict.ExpiresAt) {
		c.evictEntry(entry)
		c.misses++
		return Claim{}, false
	}

	// Move to front of LRU list
	c.evictionList.MoveToFront(entry.element)
	c.hits++

	cachedClaim := entry.verdict.Claim
	cachedClaim.FromCache = true
	return cachedClaim, true
}

// Set stores a verified claim with category-specific TTL.
func (c *ClaimCache) Set(claim Claim) {
	key := ComputeClaimKey(claim.Category, claim.Entity, claim.Attribute, claim.ValueInAnswer)

	ttl, ok := c.categoryTTLs[claim.Category]
	if !ok || ttl <= 0 {
		ttl = 24 * time.Hour
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expiresAt := now.Add(ttl)

	// Update existing item
	if existing, found := c.items[key]; found {
		existing.verdict = CachedVerdict{Claim: claim, ExpiresAt: expiresAt}
		c.evictionList.MoveToFront(existing.element)
		return
	}

	// Evict oldest if capacity exceeded
	for c.evictionList.Len() >= c.capacity {
		oldest := c.evictionList.Back()
		if oldest == nil {
			break
		}
		if entry, ok := oldest.Value.(*lruEntry); ok {
			c.evictEntry(entry)
		}
	}

	entry := &lruEntry{
		key:       key,
		verdict:   CachedVerdict{Claim: claim, ExpiresAt: expiresAt},
		category:  claim.Category,
		createdAt: now,
	}
	elem := c.evictionList.PushFront(entry)
	entry.element = elem
	c.items[key] = entry
}

func (c *ClaimCache) evictEntry(entry *lruEntry) {
	delete(c.items, entry.key)
	c.evictionList.Remove(entry.element)
}

// DoSingleFlight deduplicates concurrent executions of identical claim verification.
func (c *ClaimCache) DoSingleFlight(key string, fn func() (Claim, error)) (Claim, error) {
	c.flightMu.Lock()
	call, exists := c.flightGroup[key]
	if exists {
		c.flightMu.Unlock()
		call.wg.Wait()
		return call.val, call.err
	}

	call = new(singleFlightCall)
	call.wg.Add(1)
	c.flightGroup[key] = call
	c.flightMu.Unlock()

	call.val, call.err = fn()
	call.wg.Done()

	c.flightMu.Lock()
	delete(c.flightGroup, key)
	c.flightMu.Unlock()

	return call.val, call.err
}

// Stats returns cache hits and misses.
func (c *ClaimCache) Stats() (hits, misses int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses
}

// Clear flushes all cached entries.
func (c *ClaimCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*lruEntry, c.capacity)
	c.evictionList.Init()
}
