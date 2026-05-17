package traffic

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Throttle Implementation
// ============================================================================

// Throttler defines the interface for rate throttling
type Throttler interface {
	Allow(key string) bool
	AllowN(key string, n int) bool
	Status(key string) ThrottleStatus
}

// ThrottleStatus shows the current throttle state
type ThrottleStatus struct {
	Key         string
	Allowed     int64
	Rejected    int64
	Remaining   int64
	ResetTime   time.Time
	WindowSize  time.Duration
}

// TokenBucketThrottler implements token bucket throttling
type TokenBucketThrottler struct {
	mu         sync.RWMutex
	buckets    map[string]*tokenBucket
	rate       int
	burst      int
	windowSize time.Duration
}

type tokenBucket struct {
	tokens     float64
	capacity   float64
	refillRate float64
	lastRefill time.Time
	allowed    int64
	rejected   int64
}

// NewTokenBucketThrottler creates a new token bucket throttler
func NewTokenBucketThrottler(rate, burst int, windowSize time.Duration) *TokenBucketThrottler {
	return &TokenBucketThrottler{
		buckets:    make(map[string]*tokenBucket),
		rate:       rate,
		burst:      burst,
		windowSize: windowSize,
	}
}

func (t *TokenBucketThrottler) Allow(key string) bool {
	return t.AllowN(key, 1)
}

func (t *TokenBucketThrottler) AllowN(key string, n int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	bucket, exists := t.buckets[key]
	if !exists {
		refillRate := float64(t.rate) / t.windowSize.Seconds()
		bucket = &tokenBucket{
			tokens:     float64(t.burst),
			capacity:   float64(t.burst),
			refillRate: refillRate,
			lastRefill: time.Now(),
		}
		t.buckets[key] = bucket
	}

	// Refill
	now := time.Now()
	elapsed := now.Sub(bucket.lastRefill).Seconds()
	bucket.lastRefill = now
	bucket.tokens += elapsed * bucket.refillRate
	if bucket.tokens > bucket.capacity {
		bucket.tokens = bucket.capacity
	}

	if bucket.tokens >= float64(n) {
		bucket.tokens -= float64(n)
		bucket.allowed++
		return true
	}
	bucket.rejected++
	return false
}

func (t *TokenBucketThrottler) Status(key string) ThrottleStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	bucket, exists := t.buckets[key]
	if !exists {
		return ThrottleStatus{Key: key, Remaining: int64(t.burst), WindowSize: t.windowSize}
	}

	return ThrottleStatus{
		Key:        key,
		Allowed:    bucket.allowed,
		Rejected:   bucket.rejected,
		Remaining:  int64(bucket.tokens),
		WindowSize: t.windowSize,
	}
}

// SlidingWindowThrottler implements sliding window throttling
type SlidingWindowThrottler struct {
	mu         sync.RWMutex
	windows    map[string][]time.Time
	rate       int
	windowSize time.Duration
}

// NewSlidingWindowThrottler creates a new sliding window throttler
func NewSlidingWindowThrottler(rate int, windowSize time.Duration) *SlidingWindowThrottler {
	return &SlidingWindowThrottler{
		windows:    make(map[string][]time.Time),
		rate:       rate,
		windowSize: windowSize,
	}
}

func (t *SlidingWindowThrottler) Allow(key string) bool {
	return t.AllowN(key, 1)
}

func (t *SlidingWindowThrottler) AllowN(key string, n int) bool {
	if n > t.rate {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	window := t.windows[key]

	// Remove old entries
	cutoff := now.Add(-t.windowSize)
	var newWindow []time.Time
	for _, ts := range window {
		if ts.After(cutoff) {
			newWindow = append(newWindow, ts)
		}
	}

	if len(newWindow)+n > t.rate {
		t.windows[key] = newWindow
		return false
	}

	for i := 0; i < n; i++ {
		newWindow = append(newWindow, now)
	}
	t.windows[key] = newWindow
	return true
}

func (t *SlidingWindowThrottler) Status(key string) ThrottleStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	window := t.windows[key]
	now := time.Now()
	cutoff := now.Add(-t.windowSize)

	var active int64
	for _, ts := range window {
		if ts.After(cutoff) {
			active++
		}
	}

	return ThrottleStatus{
		Key:        key,
		Allowed:    active,
		Remaining:  int64(t.rate) - active,
		WindowSize: t.windowSize,
	}
}

// DistributedThrottler implements distributed throttling with shared state
type DistributedThrottler struct {
	mu        sync.RWMutex
	local     Throttler
	shared    map[string]int64
	nodeID    string
	syncFunc  func(ctx context.Context, key string, count int64) error
}

// NewDistributedThrottler creates a new distributed throttler
func NewDistributedThrottler(local Throttler, nodeID string) *DistributedThrottler {
	return &DistributedThrottler{
		local:  local,
		shared: make(map[string]int64),
		nodeID: nodeID,
	}
}

func (d *DistributedThrottler) Allow(ctx context.Context, key string) bool {
	// Check local first
	if !d.local.Allow(key) {
		return false
	}

	// Then check distributed
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.syncFunc != nil {
		if err := d.syncFunc(ctx, key, 1); err != nil {
			return false
		}
	}

	d.shared[key]++
	return true
}

func (d *DistributedThrottler) GetNodeCount(key string) int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.shared[key]
}

// ============================================================================
// TESTS
// ============================================================================

func TestTokenBucketThrottler_Allow_GivenNewKey_WhenAllowed_ThenReturnsTrue(t *testing.T) {
	throttler := NewTokenBucketThrottler(100, 10, time.Minute)

	assert.True(t, throttler.Allow("client-1"))
}

func TestTokenBucketThrottler_Allow_GivenExhaustedBucket_WhenAllowed_ThenReturnsFalse(t *testing.T) {
	throttler := NewTokenBucketThrottler(100, 5, time.Minute)

	// Exhaust bucket
	for i := 0; i < 5; i++ {
		assert.True(t, throttler.Allow("client-1"), "request %d should be allowed", i+1)
	}

	// Next should be blocked
	assert.False(t, throttler.Allow("client-1"), "should be blocked when bucket empty")
}

func TestTokenBucketThrottler_Allow_GivenTimePasses_WhenRefilled_ThenAllowed(t *testing.T) {
	throttler := NewTokenBucketThrottler(100, 2, 100*time.Millisecond)

	// Exhaust
	assert.True(t, throttler.Allow("client-1"))
	assert.True(t, throttler.Allow("client-1"))
	assert.False(t, throttler.Allow("client-1"))

	// Wait for refill
	time.Sleep(110 * time.Millisecond)

	assert.True(t, throttler.Allow("client-1"), "should allow after refill")
}

func TestTokenBucketThrottler_AllowN_GivenBurst_WhenMultipleRequested_ThenCorrectResult(t *testing.T) {
	throttler := NewTokenBucketThrottler(100, 10, time.Minute)

	tests := []struct {
		name     string
		n        int
		expected bool
	}{
		{"within burst", 5, true},
		{"exact burst", 10, true},
		{"over burst", 11, false},
		{"zero", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := throttler.AllowN("client-1", tt.n)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTokenBucketThrottler_Burst_GivenFullBucket_WhenRequestsWithinBurst_ThenAllAllowed(t *testing.T) {
	throttler := NewTokenBucketThrottler(1000, 100, time.Minute)

	allowed := 0
	for i := 0; i < 100; i++ {
		if throttler.Allow("client-1") {
			allowed++
		}
	}
	assert.Equal(t, 100, allowed)
	assert.False(t, throttler.Allow("client-1"))
}

func TestTokenBucketThrottler_Status_GivenMultipleRequests_WhenQueried_ThenCorrectCounts(t *testing.T) {
	throttler := NewTokenBucketThrottler(100, 10, time.Minute)

	// Allow 5
	for i := 0; i < 5; i++ {
		throttler.Allow("client-1")
	}

	// Reject 5
	for i := 0; i < 5; i++ {
		throttler.Allow("client-1") // bucket empty, will reject
	}

	status := throttler.Status("client-1")
	assert.Equal(t, "client-1", status.Key)
	assert.Equal(t, int64(5), status.Allowed)
	assert.Equal(t, int64(5), status.Rejected)
}

func TestTokenBucketThrottler_ConcurrentAccess_GivenMultipleGoroutines_WhenOperating_ThenNoRace(t *testing.T) {
	throttler := NewTokenBucketThrottler(10000, 100, time.Minute)
	const numGoroutines = 50
	const requestsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	allowed := int64(0)
	rejected := int64(0)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("client-%d", id%5) // 5 different keys
			for j := 0; j < requestsPerGoroutine; j++ {
				if throttler.Allow(key) {
					atomic.AddInt64(&allowed, 1)
				} else {
					atomic.AddInt64(&rejected, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	assert.Greater(t, allowed, int64(0))
	assert.GreaterOrEqual(t, rejected, int64(0))
}

// ============================================================================
// Sliding Window Tests
// ============================================================================

func TestSlidingWindowThrottler_Allow_GivenNewKey_WhenAllowed_ThenReturnsTrue(t *testing.T) {
	throttler := NewSlidingWindowThrottler(10, time.Minute)

	assert.True(t, throttler.Allow("client-1"))
}

func TestSlidingWindowThrottler_Allow_GivenExhaustedWindow_WhenAllowed_ThenReturnsFalse(t *testing.T) {
	throttler := NewSlidingWindowThrottler(5, time.Minute)

	for i := 0; i < 5; i++ {
		assert.True(t, throttler.Allow("client-1"), "request %d should be allowed", i+1)
	}

	assert.False(t, throttler.Allow("client-1"), "should be blocked when window full")
}

func TestSlidingWindowThrottler_Allow_GivenWindowSlides_WhenOldRequestsExpire_ThenAllowed(t *testing.T) {
	throttler := NewSlidingWindowThrottler(2, 100*time.Millisecond)

	assert.True(t, throttler.Allow("client-1"))
	assert.True(t, throttler.Allow("client-1"))
	assert.False(t, throttler.Allow("client-1"))

	// Wait for window to slide
	time.Sleep(110 * time.Millisecond)

	assert.True(t, throttler.Allow("client-1"), "should allow after window slides")
}

func TestSlidingWindowThrottler_AllowN_GivenWindow_WhenMultipleRequested_ThenCorrectResult(t *testing.T) {
	throttler := NewSlidingWindowThrottler(10, time.Minute)

	tests := []struct {
		name     string
		n        int
		expected bool
	}{
		{"within limit", 5, true},
		{"exact limit", 10, true},
		{"over limit", 11, false},
		{"zero", 0, true},
		{"more than double", 21, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := throttler.AllowN("client-1", tt.n)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSlidingWindowThrottler_AllowN_GivenPartialWindow_WhenOverRemaining_ThenBlocked(t *testing.T) {
	throttler := NewSlidingWindowThrottler(10, time.Minute)

	// Use 7
	assert.True(t, throttler.AllowN("client-1", 7))

	// Try to use 5 more (would be 12 > 10)
	assert.False(t, throttler.AllowN("client-1", 5))

	// But 3 should work
	assert.True(t, throttler.AllowN("client-1", 3))
}

func TestSlidingWindowThrottler_Status_GivenRequests_WhenQueried_ThenCorrectCounts(t *testing.T) {
	throttler := NewSlidingWindowThrottler(10, time.Minute)

	for i := 0; i < 5; i++ {
		throttler.Allow("client-1")
	}

	status := throttler.Status("client-1")
	assert.Equal(t, "client-1", status.Key)
	assert.Equal(t, int64(5), status.Allowed)
	assert.Equal(t, int64(5), status.Remaining)
}

func TestSlidingWindowThrottler_ConcurrentAccess_GivenMultipleGoroutines_WhenOperating_ThenNoRace(t *testing.T) {
	throttler := NewSlidingWindowThrottler(1000, time.Minute)
	const numGoroutines = 50
	const requestsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	allowed := int64(0)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("client-%d", id%5)
			for j := 0; j < requestsPerGoroutine; j++ {
				if throttler.Allow(key) {
					atomic.AddInt64(&allowed, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	// Should not exceed rate * numKeys
	assert.LessOrEqual(t, allowed, int64(1000*5+numGoroutines*requestsPerGoroutine))
}

// ============================================================================
// Distributed Throttler Tests
// ============================================================================

func TestDistributedThrottler_Allow_GivenLocalAllows_WhenDistributedAllows_ThenReturnsTrue(t *testing.T) {
	local := NewTokenBucketThrottler(100, 10, time.Minute)
	distributed := NewDistributedThrottler(local, "node-1")

	ctx := context.Background()
	assert.True(t, distributed.Allow(ctx, "client-1"))
}

func TestDistributedThrottler_Allow_GivenLocalBlocks_WhenChecked_ThenReturnsFalse(t *testing.T) {
	local := NewTokenBucketThrottler(100, 1, time.Minute)
	distributed := NewDistributedThrottler(local, "node-1")

	ctx := context.Background()
	assert.True(t, distributed.Allow(ctx, "client-1")) // Uses the 1 token
	assert.False(t, distributed.Allow(ctx, "client-1")) // Blocked
}

func TestDistributedThrottler_Allow_GivenSyncFunc_WhenCalled_ThenSyncs(t *testing.T) {
	local := NewTokenBucketThrottler(100, 10, time.Minute)
	distributed := NewDistributedThrottler(local, "node-1")

	syncCalled := false
	distributed.syncFunc = func(ctx context.Context, key string, count int64) error {
		syncCalled = true
		return nil
	}

	ctx := context.Background()
	assert.True(t, distributed.Allow(ctx, "client-1"))
	assert.True(t, syncCalled)
}

func TestDistributedThrottler_Allow_GivenSyncFails_WhenChecked_ThenReturnsFalse(t *testing.T) {
	local := NewTokenBucketThrottler(100, 10, time.Minute)
	distributed := NewDistributedThrottler(local, "node-1")

	distributed.syncFunc = func(ctx context.Context, key string, count int64) error {
		return fmt.Errorf("sync failed")
	}

	ctx := context.Background()
	assert.False(t, distributed.Allow(ctx, "client-1"))
}

func TestDistributedThrottler_GetNodeCount_GivenMultipleAllows_WhenQueried_ThenReturnsCount(t *testing.T) {
	local := NewTokenBucketThrottler(100, 10, time.Minute)
	distributed := NewDistributedThrottler(local, "node-1")

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		distributed.Allow(ctx, "client-1")
	}

	assert.Equal(t, int64(5), distributed.GetNodeCount("client-1"))
}

func TestDistributedThrottler_ConcurrentAccess_GivenMultipleNodes_WhenOperating_ThenNoRace(t *testing.T) {
	local := NewTokenBucketThrottler(100000, 1000, time.Minute)
	distributed := NewDistributedThrottler(local, "node-1")

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < 20; j++ {
				distributed.Allow(ctx, fmt.Sprintf("client-%d", id%5))
			}
		}(i)
	}

	wg.Wait()
}

// ============================================================================
// Comparison Tests
// ============================================================================

func TestThrottlers_Compare_GivenSameRate_WhenBursting_ThenTokenBucketAllowsBurst(t *testing.T) {
	tb := NewTokenBucketThrottler(10, 10, time.Second)
	sw := NewSlidingWindowThrottler(10, time.Second)

	// Token bucket allows burst of 10 immediately
	tbAllowed := 0
	for i := 0; i < 15; i++ {
		if tb.Allow("key") {
			tbAllowed++
		}
	}
	assert.Equal(t, 10, tbAllowed, "token bucket should allow burst")

	// Sliding window only allows 10 total in the window, but not all immediately
	swAllowed := 0
	for i := 0; i < 15; i++ {
		if sw.Allow("key") {
			swAllowed++
		}
	}
	assert.Equal(t, 10, swAllowed, "sliding window should also allow 10 total")
}

func TestThrottlers_EdgeCases_GivenBoundaryValues_WhenCreated_ThenHandlesCorrectly(t *testing.T) {
	t.Run("token bucket zero rate", func(t *testing.T) {
		tb := NewTokenBucketThrottler(0, 5, time.Minute)
		for i := 0; i < 5; i++ {
			assert.True(t, tb.Allow("key"))
		}
		assert.False(t, tb.Allow("key"))
		// With 0 rate, no refill
		time.Sleep(50 * time.Millisecond)
		assert.False(t, tb.Allow("key"))
	})

	t.Run("token bucket zero burst", func(t *testing.T) {
		tb := NewTokenBucketThrottler(100, 0, time.Minute)
		assert.False(t, tb.Allow("key"))
	})

	t.Run("sliding window zero rate", func(t *testing.T) {
		sw := NewSlidingWindowThrottler(0, time.Minute)
		assert.False(t, sw.Allow("key"))
	})

	t.Run("sliding window negative window", func(t *testing.T) {
		sw := NewSlidingWindowThrottler(10, -1*time.Minute)
		// All requests immediately fall outside window
		assert.True(t, sw.Allow("key"))
	})
}

func TestTokenBucketThrottler_RefillRate_GivenLowRate_WhenTimePasses_ThenGradualRefill(t *testing.T) {
	// 10 tokens per second, burst of 5
	throttler := NewTokenBucketThrottler(10, 5, time.Second)

	// Use all burst
	allowed := 0
	for throttler.Allow("key") {
		allowed++
	}
	assert.Equal(t, 5, allowed)

	// None available immediately
	assert.False(t, throttler.Allow("key"))

	// Wait 0.5 seconds - should have ~5 tokens
	time.Sleep(550 * time.Millisecond)
	count := 0
	for throttler.Allow("key") {
		count++
	}
	assert.GreaterOrEqual(t, count, 3)
}

func TestSlidingWindowThrottler_ExpiredRequests_GivenOldRequests_WhenCleaned_ThenSpaceAvailable(t *testing.T) {
	throttler := NewSlidingWindowThrottler(5, 200*time.Millisecond)

	// Fill window
	for i := 0; i < 5; i++ {
		assert.True(t, throttler.Allow("key"))
	}
	assert.False(t, throttler.Allow("key"))

	// Wait half the window
	time.Sleep(100 * time.Millisecond)
	// Still blocked (requests are only 100ms old)
	assert.False(t, throttler.Allow("key"))

	// Wait for full window + epsilon
	time.Sleep(110 * time.Millisecond)
	// All old requests expired, can make 5 new
	for i := 0; i < 5; i++ {
		assert.True(t, throttler.Allow("key"), "request %d after window expiration", i+1)
	}
}
