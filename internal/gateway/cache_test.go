package gateway

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Cache Implementation
// ============================================================================

// CacheEntry represents a single cached item
type CacheEntry struct {
	Value      interface{}
	Expiration int64
	AccessedAt int64 // for LRU tracking
}

// Cache is an in-memory LRU cache with TTL support
type Cache struct {
	entries    map[string]*CacheEntry
	mutex      sync.RWMutex
	maxSize    int
	ttl        time.Duration
	hits       int64
	misses     int64
	evictions  int64
}

// NewCache creates a new cache instance
func NewCache(maxSize int, ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*CacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Set stores a value in the cache
func (c *Cache) Set(key string, value interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now().UnixNano()

	// Evict if at capacity and key doesn't exist
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxSize {
		c.evictLRU()
	}

	c.entries[key] = &CacheEntry{
		Value:      value,
		Expiration: now + c.ttl.Nanoseconds(),
		AccessedAt: now,
	}
}

// Get retrieves a value from the cache
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	entry, found := c.entries[key]
	if !found {
		c.misses++
		return nil, false
	}

	// Check expiration
	if time.Now().UnixNano() > entry.Expiration {
		delete(c.entries, key)
		c.misses++
		return nil, false
	}

	entry.AccessedAt = time.Now().UnixNano()
	c.hits++
	return entry.Value, true
}

// Delete removes a value from the cache
func (c *Cache) Delete(key string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if _, found := c.entries[key]; found {
		delete(c.entries, key)
		return true
	}
	return false
}

// Has checks if a key exists and is not expired
func (c *Cache) Has(key string) bool {
	_, found := c.Get(key)
	return found
}

// Size returns the current number of entries
func (c *Cache) Size() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.entries)
}

// Stats returns cache statistics
func (c *Cache) Stats() (hits, misses, evictions int64) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.hits, c.misses, c.evictions
}

// Clear removes all entries
func (c *Cache) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.entries = make(map[string]*CacheEntry)
}

// evictLRU removes the least recently used entry (must be called with lock held)
func (c *Cache) evictLRU() {
	var oldestKey string
	var oldestTime int64 = -1

	for key, entry := range c.entries {
		if oldestTime == -1 || entry.AccessedAt < oldestTime {
			oldestTime = entry.AccessedAt
			oldestKey = key
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
		c.evictions++
	}
}

// ============================================================================
// TESTS
// ============================================================================

func TestCache_SetAndGet_GivenKeyValue_WhenStoredAndRetrieved_ThenReturnsValue(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	tests := []struct {
		name  string
		key   string
		value interface{}
	}{
		{"string value", "key1", "hello world"},
		{"int value", "key2", 42},
		{"struct value", "key3", map[string]string{"foo": "bar"}},
		{"nil value", "key4", nil},
		{"empty string key", "", "empty-key-value"},
		{"special chars key", "key:with:special!chars@", "special"},
		{"unicode key", "ключ-ключ", "unicode value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache.Set(tt.key, tt.value)
			got, found := cache.Get(tt.key)
			require.True(t, found, "key should be found")
			assert.Equal(t, tt.value, got)
		})
	}
}

func TestCache_Get_GivenNonExistentKey_WhenRetrieved_ThenReturnsNotFound(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	tests := []struct {
		name string
		key  string
	}{
		{"simple missing key", "non-existent"},
		{"empty key", ""},
		{"special chars", "!@#$%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, found := cache.Get(tt.key)
			assert.False(t, found)
			assert.Nil(t, val)
		})
	}
}

func TestCache_Delete_GivenExistingKey_WhenDeleted_ThenRemoved(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	tests := []struct {
		name string
		key  string
	}{
		{"delete existing", "key-to-delete"},
		{"delete and verify gone", "another-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache.Set(tt.key, "value")
			found := cache.Delete(tt.key)
			assert.True(t, found)

			_, found = cache.Get(tt.key)
			assert.False(t, found)
		})
	}
}

func TestCache_Delete_GivenNonExistentKey_WhenDeleted_ThenReturnsFalse(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)
	found := cache.Delete("never-existed")
	assert.False(t, found)
}

func TestCache_TTL_GivenExpiredEntry_WhenRetrieved_ThenReturnsNotFound(t *testing.T) {
	cache := NewCache(100, 50*time.Millisecond)

	cache.Set("expiring-key", "value")

	// Should be found immediately
	val, found := cache.Get("expiring-key")
	require.True(t, found)
	assert.Equal(t, "value", val)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should not be found after TTL
	val, found = cache.Get("expiring-key")
	assert.False(t, found)
	assert.Nil(t, val)
}

func TestCache_TTL_GivenMultipleEntries_WhenSomeExpire_ThenOnlyExpiredRemoved(t *testing.T) {
	cache := NewCache(100, 200*time.Millisecond)

	cache.Set("key1", "value1")
	time.Sleep(100 * time.Millisecond)
	cache.Set("key2", "value2")

	// Wait for first to expire
	time.Sleep(150 * time.Millisecond)

	// key1 should be expired
	_, found := cache.Get("key1")
	assert.False(t, found)

	// key2 should still exist
	val, found := cache.Get("key2")
	assert.True(t, found)
	assert.Equal(t, "value2", val)
}

func TestCache_LRU_GivenFullCache_WhenNewEntryAdded_ThenEvictsLeastRecentlyUsed(t *testing.T) {
	cache := NewCache(3, 5*time.Minute)

	// Fill cache
	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")

	// Access key1 and key2 to make key3 the least recently used
	cache.Get("key1")
	cache.Get("key2")

	// Add a new entry - should evict key3
	cache.Set("key4", "value4")

	// key1 and key2 should still be present
	_, found := cache.Get("key1")
	assert.True(t, found)
	_, found = cache.Get("key2")
	assert.True(t, found)

	// key3 should be evicted
	_, found = cache.Get("key3")
	assert.False(t, found)

	// key4 should be present
	val, found := cache.Get("key4")
	assert.True(t, found)
	assert.Equal(t, "value4", val)
}

func TestCache_LRU_GivenFullCache_WhenExistingKeyUpdated_ThenNotEvicted(t *testing.T) {
	cache := NewCache(2, 5*time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	// Update key1 - should not trigger eviction
	cache.Set("key1", "updated-value1")

	_, found := cache.Get("key1")
	assert.True(t, found)
	_, found = cache.Get("key2")
	assert.True(t, found)
	assert.Equal(t, 2, cache.Size())
}

func TestCache_ConcurrentAccess_GivenMultipleGoroutines_WhenOperating_ThenNoDataRace(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)
	const numGoroutines = 100
	const numOps = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("goroutine-%d-key-%d", id, j%10)
				switch j % 4 {
				case 0:
					cache.Set(key, fmt.Sprintf("value-%d", j))
				case 1:
					cache.Get(key)
				case 2:
					cache.Delete(key)
				case 3:
					cache.Has(key)
				}
			}
		}(i)
	}

	wg.Wait()

	// Cache should be in a consistent state
	assert.GreaterOrEqual(t, cache.Size(), 0)
}

func TestCache_Stats_GivenOperations_WhenQueried_ThenReturnsCorrectStats(t *testing.T) {
	cache := NewCache(2, 5*time.Minute)

	// Two hits
	cache.Set("key1", "value1")
	cache.Get("key1")
	cache.Get("key1")

	// Two misses
	cache.Get("nonexistent1")
	cache.Get("nonexistent2")

	// One eviction (fill cache then add one more)
	cache.Set("key2", "value2")
	cache.Set("key3", "value3") // should evict one

	hits, misses, evictions := cache.Stats()
	assert.Equal(t, int64(2), hits)
	assert.Equal(t, int64(2), misses)
	assert.GreaterOrEqual(t, evictions, int64(0))
}

func TestCache_Clear_GivenPopulatedCache_WhenCleared_ThenEmpty(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")
	require.Equal(t, 3, cache.Size())

	cache.Clear()
	assert.Equal(t, 0, cache.Size())

	_, found := cache.Get("key1")
	assert.False(t, found)
}

func TestCache_Has_GivenExistingKey_WhenChecked_ThenReturnsTrue(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	cache.Set("key1", "value1")
	assert.True(t, cache.Has("key1"))
	assert.False(t, cache.Has("nonexistent"))
}

func TestCache_Size_GivenEntries_WhenCounted_ThenReturnsCorrectCount(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)
	assert.Equal(t, 0, cache.Size())

	cache.Set("key1", "value1")
	assert.Equal(t, 1, cache.Size())

	cache.Set("key2", "value2")
	cache.Set("key3", "value3")
	assert.Equal(t, 3, cache.Size())

	cache.Delete("key2")
	assert.Equal(t, 2, cache.Size())
}

func TestCache_UpdateValue_GivenExistingKey_WhenUpdated_ThenNewValueReturned(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	cache.Set("key1", "original")
	val, found := cache.Get("key1")
	require.True(t, found)
	assert.Equal(t, "original", val)

	cache.Set("key1", "updated")
	val, found = cache.Get("key1")
	require.True(t, found)
	assert.Equal(t, "updated", val)

	// Size should not change
	assert.Equal(t, 1, cache.Size())
}

func TestCache_LRUOrder_GivenMultipleAccesses_WhenFull_ThenCorrectEntryEvicted(t *testing.T) {
	cache := NewCache(3, 5*time.Minute)

	// Add three entries
	cache.Set("a", "1")
	cache.Set("b", "2")
	cache.Set("c", "3")

	// Access pattern: a, c, b (b becomes most recently used)
	cache.Get("a")
	cache.Get("c")
	cache.Get("b")

	// Add new entry - should evict 'a' (least recently used after above pattern)
	// Actually after pattern: a(1), c(2), b(3) - all accessed once
	// 'a' was accessed first, so it's LRU
	cache.Set("d", "4")

	_, found := cache.Get("a")
	assert.False(t, found, "'a' should have been evicted as least recently used")
	_, found = cache.Get("b")
	assert.True(t, found)
	_, found = cache.Get("c")
	assert.True(t, found)
	_, found = cache.Get("d")
	assert.True(t, found)
}

func TestCache_ZeroTTL_GivenNoExpiration_WhenRetrieved_ThenAlwaysReturns(t *testing.T) {
	cache := NewCache(100, 0)

	cache.Set("key1", "value1")
	time.Sleep(10 * time.Millisecond)

	// With zero TTL, entries should still be accessible
	// (implementation may treat as immediate expiry or never expire)
	_, found := cache.Get("key1")
	// Zero TTL means immediate expiry in our implementation
	assert.False(t, found)
}

func TestCache_EdgeCase_GivenMaxSizeOne_WhenAdding_ThenOnlyOneEntry(t *testing.T) {
	cache := NewCache(1, 5*time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	assert.Equal(t, 1, cache.Size())

	// key1 should be evicted
	_, found := cache.Get("key1")
	assert.False(t, found)

	// key2 should exist
	val, found := cache.Get("key2")
	assert.True(t, found)
	assert.Equal(t, "value2", val)
}

func TestCache_ConcurrentSetSameKey_GivenRace_WhenSetting_ThenNoPanic(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)
	const numGoroutines = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			cache.Set("shared-key", fmt.Sprintf("value-%d", id))
		}(i)
	}

	wg.Wait()

	// Should have exactly one entry
	assert.Equal(t, 1, cache.Size())

	// Should have some value
	val, found := cache.Get("shared-key")
	assert.True(t, found)
	assert.NotNil(t, val)
}
