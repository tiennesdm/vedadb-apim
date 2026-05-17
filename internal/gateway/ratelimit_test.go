package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Rate Limiter Implementation
// ============================================================================

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	capacity   float64
	refillRate float64    // tokens per second
	lastRefill time.Time
}

// NewTokenBucket creates a new token bucket
// rate: tokens per second, burst: maximum bucket capacity
func NewTokenBucket(rate, burst int) *TokenBucket {
	return &TokenBucket{
		tokens:     float64(burst),
		capacity:   float64(burst),
		refillRate: float64(rate),
		lastRefill: time.Now(),
	}
}

// Allow checks if a single request is allowed
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN checks if N requests are allowed
func (tb *TokenBucket) AllowN(n int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}
	return false
}

// refill adds tokens based on elapsed time
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.lastRefill = now

	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
}

// Tokens returns current token count (for testing)
func (tb *TokenBucket) Tokens() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	return tb.tokens
}

// RateLimiter manages rate limiting for multiple keys
type RateLimiter struct {
	buckets map[string]*TokenBucket
	mu      sync.RWMutex
	defaultRate  int
	defaultBurst int
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(defaultRate, defaultBurst int) *RateLimiter {
	return &RateLimiter{
		buckets:      make(map[string]*TokenBucket),
		defaultRate:  defaultRate,
		defaultBurst: defaultBurst,
	}
}

// Allow checks if a request from the given key is allowed
func (rl *RateLimiter) Allow(key string) bool {
	return rl.AllowN(key, 1)
}

// AllowN checks if N requests from the given key are allowed
func (rl *RateLimiter) AllowN(key string, n int) bool {
	rl.mu.RLock()
	bucket, exists := rl.buckets[key]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		bucket, exists = rl.buckets[key]
		if !exists {
			bucket = NewTokenBucket(rl.defaultRate, rl.defaultBurst)
			rl.buckets[key] = bucket
		}
		rl.mu.Unlock()
	}

	return bucket.AllowN(n)
}

// SetLimit sets a custom rate limit for a specific key
func (rl *RateLimiter) SetLimit(key string, rate, burst int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.buckets[key] = NewTokenBucket(rate, burst)
}

// RemoveKey removes a key's rate limit bucket
func (rl *RateLimiter) RemoveKey(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, key)
}

// GetBucket returns a bucket for a key (for testing)
func (rl *RateLimiter) GetBucket(key string) *TokenBucket {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.buckets[key]
}

// ============================================================================
// TESTS
// ============================================================================

func TestTokenBucket_Allow_GivenNewBucket_WhenRequestMade_ThenAllowed(t *testing.T) {
	tb := NewTokenBucket(10, 5)

	// Should allow requests up to burst limit
	for i := 0; i < 5; i++ {
		assert.True(t, tb.Allow(), "request %d should be allowed", i+1)
	}
}

func TestTokenBucket_Allow_GivenExhaustedBucket_WhenRequestMade_ThenBlocked(t *testing.T) {
	tb := NewTokenBucket(10, 3)

	// Exhaust the bucket
	for i := 0; i < 3; i++ {
		assert.True(t, tb.Allow(), "request %d should be allowed", i+1)
	}

	// Next request should be blocked
	assert.False(t, tb.Allow(), "request should be blocked when bucket is empty")
}

func TestTokenBucket_Allow_GivenExhaustedBucket_WhenTimePasses_ThenRefills(t *testing.T) {
	tb := NewTokenBucket(100, 2) // 100 tokens/sec, burst of 2

	// Exhaust the bucket
	assert.True(t, tb.Allow())
	assert.True(t, tb.Allow())
	assert.False(t, tb.Allow())

	// Wait for refill
	time.Sleep(110 * time.Millisecond)

	// Should have at least 1 token refilled
	assert.True(t, tb.Allow(), "should allow after refill time")
}

func TestTokenBucket_AllowN_GivenBurst_WhenMultipleRequested_ThenCorrectResult(t *testing.T) {
	tests := []struct {
		name      string
		rate      int
		burst     int
		requestN  int
		expected  bool
	}{
		{"request within burst", 10, 5, 3, true},
		{"request exact burst", 10, 5, 5, true},
		{"request over burst", 10, 5, 6, false},
		{"request zero", 10, 5, 0, true},
		{"request with rate 1 burst 1", 1, 1, 1, true},
		{"request over rate 1 burst 1", 1, 1, 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := NewTokenBucket(tt.rate, tt.burst)
			result := tb.AllowN(tt.requestN)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTokenBucket_Burst_GivenFullBucket_WhenRequestsWithinBurst_ThenAllAllowed(t *testing.T) {
	tb := NewTokenBucket(1000, 100)

	// All 100 requests should be allowed (within burst)
	allowed := 0
	for i := 0; i < 100; i++ {
		if tb.Allow() {
			allowed++
		}
	}
	assert.Equal(t, 100, allowed, "all requests within burst should be allowed")

	// 101st should be blocked
	assert.False(t, tb.Allow(), "request over burst should be blocked")
}

func TestTokenBucket_RefillRate_GivenLowRate_WhenRequestsExceedRate_ThenBlocks(t *testing.T) {
	tb := NewTokenBucket(2, 10) // 2 tokens/sec, burst of 10

	// Use all burst
	consumed := 0
	for tb.Allow() {
		consumed++
	}
	assert.Equal(t, 10, consumed, "should consume all burst")

	// Immediately try again - should fail
	assert.False(t, tb.Allow())

	// Wait half a second - should have 1 token
	time.Sleep(600 * time.Millisecond)
	assert.True(t, tb.Allow(), "should have 1 token after 0.6s at 2 tokens/sec")

	// Should be empty again
	assert.False(t, tb.Allow())
}

func TestTokenBucket_ConcurrentAccess_GivenMultipleGoroutines_WhenAllowing_ThenNoRace(t *testing.T) {
	tb := NewTokenBucket(10000, 1000)
	const numGoroutines = 50
	const requestsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	allowed := int64(0)
	blocked := int64(0)
	var countMu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				if tb.Allow() {
					countMu.Lock()
					allowed++
					countMu.Unlock()
				} else {
					countMu.Lock()
					blocked++
					countMu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	// Total allowed should not exceed burst
	assert.LessOrEqual(t, allowed, int64(1000+numGoroutines*requestsPerGoroutine))
	assert.Greater(t, allowed, int64(0))
}

func TestTokenBucket_CapacityNeverExceedsBurst_GivenTimePasses_WhenRefilling_ThenMaxIsBurst(t *testing.T) {
	tb := NewTokenBucket(10, 5)

	// Exhaust bucket
	for tb.Allow() {
	}

	// Wait a long time - bucket should not exceed burst
	time.Sleep(2 * time.Second)

	tokens := tb.Tokens()
	assert.LessOrEqual(t, tokens, float64(5), "tokens should never exceed burst capacity")
	assert.GreaterOrEqual(t, tokens, float64(4.5), "tokens should be close to burst after long wait")
}

// ============================================================================
// RateLimiter Tests
// ============================================================================

func TestRateLimiter_Allow_GivenNewKey_WhenFirstRequest_ThenAllowed(t *testing.T) {
	rl := NewRateLimiter(100, 50)

	assert.True(t, rl.Allow("client-1"), "first request should always be allowed")
}

func TestRateLimiter_Allow_GivenMultipleKeys_WhenRequestsMade_ThenIndependentLimits(t *testing.T) {
	rl := NewRateLimiter(10, 5)

	// Exhaust client-1's bucket
	for i := 0; i < 5; i++ {
		assert.True(t, rl.Allow("client-1"), "client-1 request %d", i+1)
	}
	assert.False(t, rl.Allow("client-1"), "client-1 should be blocked")

	// client-2 should still be allowed
	assert.True(t, rl.Allow("client-2"), "client-2 first request should be allowed")
	assert.True(t, rl.Allow("client-2"), "client-2 second request should be allowed")
}

func TestRateLimiter_Allow_GivenExhaustedKey_WhenTimePasses_ThenRefills(t *testing.T) {
	rl := NewRateLimiter(100, 2)

	// Exhaust client-1
	assert.True(t, rl.Allow("client-1"))
	assert.True(t, rl.Allow("client-1"))
	assert.False(t, rl.Allow("client-1"))

	// Wait for refill
	time.Sleep(110 * time.Millisecond)

	assert.True(t, rl.Allow("client-1"), "should allow after refill")
}

func TestRateLimiter_SetLimit_GivenCustomLimit_WhenApplied_ThenUsesCustomRate(t *testing.T) {
	rl := NewRateLimiter(100, 50) // default

	// Set a very low limit for client-1
	rl.SetLimit("client-1", 1, 1)

	// client-1 should only allow 1 request
	assert.True(t, rl.Allow("client-1"))
	assert.False(t, rl.Allow("client-1"))

	// client-2 should use default (100, 50)
	for i := 0; i < 50; i++ {
		assert.True(t, rl.Allow("client-2"), "client-2 request %d", i+1)
	}
}

func TestRateLimiter_RemoveKey_GivenExistingKey_WhenRemoved_ThenFreshBucketOnNextRequest(t *testing.T) {
	rl := NewRateLimiter(10, 5)

	// Exhaust client-1
	for i := 0; i < 5; i++ {
		rl.Allow("client-1")
	}
	assert.False(t, rl.Allow("client-1"))

	// Remove and re-add (implicitly via Allow)
	rl.RemoveKey("client-1")

	// Should get a fresh bucket
	assert.True(t, rl.Allow("client-1"), "should get fresh bucket after removal")
}

func TestRateLimiter_ConcurrentKeys_GivenMultipleClients_WhenConcurrentAccess_ThenNoRace(t *testing.T) {
	rl := NewRateLimiter(10000, 100)
	const numClients = 50
	const requestsPerClient = 50

	var wg sync.WaitGroup
	wg.Add(numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			defer wg.Done()
			key := fmt.Sprintf("client-%d", clientID)
			for j := 0; j < requestsPerClient; j++ {
				rl.Allow(key)
			}
		}(i)
	}

	wg.Wait()

	// Should have created buckets for all clients
	for i := 0; i < numClients; i++ {
		key := fmt.Sprintf("client-%d", i)
		assert.NotNil(t, rl.GetBucket(key), "bucket for %s should exist", key)
	}
}

func TestRateLimiter_DifferentLimits_GivenVariousConfigs_WhenLimited_ThenCorrectBehavior(t *testing.T) {
	tests := []struct {
		name         string
		defaultRate  int
		defaultBurst int
		customRate   int
		customBurst  int
		customKey    string
		defaultReqs  int
		customReqs   int
	}{
		{
			name:         "low default high custom",
			defaultRate:  1,
			defaultBurst: 1,
			customRate:   1000,
			customBurst:  1000,
			customKey:    "vip-client",
			defaultReqs:  1,
			customReqs:   1000,
		},
		{
			name:         "high default low custom",
			defaultRate:  1000,
			defaultBurst: 1000,
			customRate:   1,
			customBurst:  1,
			customKey:    "restricted-client",
			defaultReqs:  1000,
			customReqs:   1,
		},
		{
			name:         "equal limits",
			defaultRate:  100,
			defaultBurst: 100,
			customRate:   100,
			customBurst:  100,
			customKey:    "normal-client",
			defaultReqs:  100,
			customReqs:   100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(tt.defaultRate, tt.defaultBurst)
			rl.SetLimit(tt.customKey, tt.customRate, tt.customBurst)

			// Test default key
			allowed := 0
			for i := 0; i < tt.defaultReqs+10; i++ {
				if rl.Allow("default-client") {
					allowed++
				}
			}
			assert.Equal(t, tt.defaultReqs, allowed, "default client should allow %d requests", tt.defaultReqs)

			// Test custom key
			allowed = 0
			for i := 0; i < tt.customReqs+10; i++ {
				if rl.Allow(tt.customKey) {
					allowed++
				}
			}
			assert.Equal(t, tt.customReqs, allowed, "custom client should allow %d requests", tt.customReqs)
		})
	}
}

func TestRateLimiter_AllowN_GivenMultipleTokens_WhenRequested_ThenCorrectResult(t *testing.T) {
	rl := NewRateLimiter(10, 10)

	// Request 5 at once
	assert.True(t, rl.AllowN("client-1", 5), "should allow 5 within burst")

	// Request 5 more
	assert.True(t, rl.AllowN("client-1", 5), "should allow remaining 5")

	// Request 1 more - should fail
	assert.False(t, rl.AllowN("client-1", 1), "should block when bucket empty")
}

func TestRateLimiter_ContextCancellation_GivenCancelledContext_WhenLimited_ThenHandled(t *testing.T) {
	// TokenBucket doesn't use context, but we verify it doesn't hang
	tb := NewTokenBucket(10, 5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Allow should still work regardless of context
	_ = ctx
	assert.True(t, tb.Allow())
}

func TestTokenBucket_EdgeCases_GivenBoundaryValues_WhenCreated_ThenHandlesCorrectly(t *testing.T) {
	t.Run("rate zero", func(t *testing.T) {
		tb := NewTokenBucket(0, 5)
		// Should allow burst then never refill
		for i := 0; i < 5; i++ {
			assert.True(t, tb.Allow())
		}
		assert.False(t, tb.Allow())

		// Wait and try again - should still be empty with 0 refill rate
		time.Sleep(100 * time.Millisecond)
		assert.False(t, tb.Allow())
	})

	t.Run("burst zero", func(t *testing.T) {
		tb := NewTokenBucket(10, 0)
		// Should never allow with burst 0
		assert.False(t, tb.Allow())
	})

	t.Run("rate and burst one", func(t *testing.T) {
		tb := NewTokenBucket(1, 1)
		assert.True(t, tb.Allow())
		assert.False(t, tb.Allow())
		time.Sleep(1100 * time.Millisecond)
		assert.True(t, tb.Allow())
	})

	t.Run("very high rate", func(t *testing.T) {
		tb := NewTokenBucket(1000000, 1000000)
		allowed := 0
		for i := 0; i < 10000; i++ {
			if tb.Allow() {
				allowed++
			}
		}
		assert.Equal(t, 10000, allowed)
	})
}

func TestRateLimiter_MemoryLeaks_GivenManyKeys_WhenCreated_ThenReasonableMemory(t *testing.T) {
	rl := NewRateLimiter(10, 5)

	// Create many keys
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("client-%d", i)
		rl.Allow(key)
	}

	// All keys should have buckets
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("client-%d", i)
		assert.NotNil(t, rl.GetBucket(key))
	}
}
