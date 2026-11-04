package freshness

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaimCache_LRUEviction(t *testing.T) {
	// Cache with capacity 2
	cache := NewClaimCache(2, map[string]int{CategoryPricing: 1})

	c1 := Claim{Category: CategoryPricing, Entity: "E1", Attribute: "A1", ValueInAnswer: "V1", Verdict: VerdictCurrent}
	c2 := Claim{Category: CategoryPricing, Entity: "E2", Attribute: "A2", ValueInAnswer: "V2", Verdict: VerdictCurrent}
	c3 := Claim{Category: CategoryPricing, Entity: "E3", Attribute: "A3", ValueInAnswer: "V3", Verdict: VerdictCurrent}

	cache.Set(c1)
	cache.Set(c2)

	// Access c1 so c2 becomes the least recently used
	_, found := cache.Get(CategoryPricing, "E1", "A1", "V1")
	if !found {
		t.Fatal("expected c1 to be found")
	}

	// Insert c3: should evict c2
	cache.Set(c3)

	if _, found := cache.Get(CategoryPricing, "E2", "A2", "V2"); found {
		t.Fatal("expected c2 to have been evicted by LRU")
	}

	if _, found := cache.Get(CategoryPricing, "E1", "A1", "V1"); !found {
		t.Fatal("expected c1 to still exist")
	}
	if _, found := cache.Get(CategoryPricing, "E3", "A3", "V3"); !found {
		t.Fatal("expected c3 to exist")
	}
}

func TestClaimCache_SingleFlight(t *testing.T) {
	cache := NewClaimCache(100, nil)
	key := "test-single-flight-key"

	var execCount int32
	var wg sync.WaitGroup

	numGoroutines := 10
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := cache.DoSingleFlight(key, func() (Claim, error) {
				atomic.AddInt32(&execCount, 1)
				time.Sleep(20 * time.Millisecond) // Simulate search latency
				return Claim{Entity: "SingleFlightEntity"}, nil
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Wait()

	// Only 1 execution should have occurred across all 10 concurrent callers
	if count := atomic.LoadInt32(&execCount); count != 1 {
		t.Fatalf("expected exactly 1 execution from single-flight, got %d", count)
	}
}

func TestClaimCache_ConcurrentRace(t *testing.T) {
	cache := NewClaimCache(50, nil)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			entity := fmt.Sprintf("Entity-%d", idx%5)
			c := Claim{
				Category:      CategoryPricing,
				Entity:        entity,
				Attribute:     "price",
				ValueInAnswer: "$10",
				Verdict:       VerdictCurrent,
			}
			cache.Set(c)
			_, _ = cache.Get(CategoryPricing, entity, "price", "$10")
		}()
	}

	wg.Wait()
}

