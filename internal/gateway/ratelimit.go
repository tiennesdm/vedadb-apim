// Package gateway provides the core API gateway functionality including rate limiting.
// This file implements both a local token-bucket rate limiter and a DB-backed
// distributed rate limiter for multi-node gateway deployments.
package gateway

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vedadb/vapim/pkg/store"
)

// ---------------------------------------------------------------------------
// Rate Limiter Interface
// ---------------------------------------------------------------------------

// RateLimiter defines the interface for rate limiting implementations.
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error)
	AllowWithBurst(ctx context.Context, key string, limit, burst int, window time.Duration) (bool, int, int, error)
	Reset(ctx context.Context, key string) error
}

// RateLimitInfo contains rate limiting information returned to the caller.
type RateLimitInfo struct {
	Allowed    bool  `json:"allowed"`
	Limit      int   `json:"limit"`
	Remaining  int   `json:"remaining"`
	ResetAt    int64 `json:"reset_at"`
	RetryAfter int64 `json:"retry_after,omitempty"`
}

// ---------------------------------------------------------------------------
// Local Token Bucket (fast path)
// ---------------------------------------------------------------------------

// TokenBucket implements a token bucket rate limiter.
type TokenBucket struct {
	tokens     float64
	lastRefill time.Time
	capacity   float64
	rate       float64
	mu         sync.Mutex
}

// NewTokenBucket creates a new token bucket with the given rate and burst capacity.
func NewTokenBucket(rate, burst float64) *TokenBucket {
	return &TokenBucket{
		tokens:     burst,
		lastRefill: time.Now(),
		capacity:   burst,
		rate:       rate,
	}
}

// Allow checks if a token is available and consumes it.
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// Remaining returns the current number of available tokens.
func (tb *TokenBucket) Remaining() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	return int(math.Floor(tb.tokens))
}

// refill adds tokens based on elapsed time.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens = math.Min(tb.capacity, tb.tokens+elapsed*tb.rate)
	tb.lastRefill = now
}

// ---------------------------------------------------------------------------
// DB-Backed Distributed Rate Limiter
// ---------------------------------------------------------------------------

// ThrottleStore defines the store methods needed for distributed rate limiting.
type ThrottleStore interface {
	store.Store
}

// bucketState holds local fast-path state for a rate limit key.
type bucketState struct {
	tokens     float64
	lastRefill time.Time
	window     time.Time
}

// DBRateLimiter implements a hybrid local-cache + DB-backed rate limiter.
// The local cache provides sub-microsecond checks; the DB provides
// distributed consistency across gateway instances.
type DBRateLimiter struct {
	store      ThrottleStore
	localCache map[string]*bucketState
	mu         sync.RWMutex
}

// NewDBRateLimiter creates a new DB-backed rate limiter with local fast-path caching.
func NewDBRateLimiter(store ThrottleStore) *DBRateLimiter {
	rl := &DBRateLimiter{
		store:      store,
		localCache: make(map[string]*bucketState),
	}
	// Start stale entry cleanup
	go rl.cleanup()
	return rl
}

// Allow checks if a request is allowed under the given key (no burst).
func (r *DBRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	allowed, remaining, _, err := r.AllowWithBurst(ctx, key, limit, limit, window)
	return allowed, remaining, err
}

// AllowWithBurst checks if a request is allowed with a burst capacity.
// It first checks the local cache (fast path), then falls back to DB
// for distributed consistency.
func (r *DBRateLimiter) AllowWithBurst(ctx context.Context, key string, limit, burst int, window time.Duration) (bool, int, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	windowStart := now.Truncate(window)

	state, exists := r.localCache[key]
	if !exists || state.window.Before(windowStart) {
		// New window or key doesn't exist locally
		state = &bucketState{
			tokens:     float64(burst),
			lastRefill: now,
			window:     windowStart,
		}
		r.localCache[key] = state
	}

	// Refill tokens based on elapsed time since last check
	elapsed := now.Sub(state.lastRefill).Seconds()
	ratePerSec := float64(limit) / window.Seconds()
	state.tokens = math.Min(float64(burst), state.tokens+elapsed*ratePerSec)
	state.lastRefill = now

	if state.tokens >= 1 {
		state.tokens--
		remaining := int(math.Floor(state.tokens))
		resetAt := int(windowStart.Add(window).Unix())
		return true, remaining, resetAt, nil
	}

	// Local cache says rate limited, but check DB for distributed consistency
	// (another instance may have decremented the shared counter)
	dbCount, err := r.store.GetThrottleCounter(key, windowStart)
	if err != nil {
		// On DB error, fall back to local decision (deny)
		return false, 0, int(windowStart.Add(window).Unix()), nil
	}

	if dbCount < limit {
		if err := r.store.IncrementThrottleCounter(key, windowStart); err != nil {
			return false, 0, int(windowStart.Add(window).Unix()), nil
		}
		newCount := dbCount + 1
		// Update local state to match DB reality
		state.tokens = math.Max(0, float64(burst)-float64(newCount))
		state.lastRefill = now
		remaining := limit - newCount
		if remaining < 0 {
			remaining = 0
		}
		return true, remaining, int(windowStart.Add(window).Unix()), nil
	}

	return false, 0, int(windowStart.Add(window).Unix()), nil
}

// Reset resets the rate limiter for the given key in both local cache and DB.
func (r *DBRateLimiter) Reset(ctx context.Context, key string) error {
	r.mu.Lock()
	delete(r.localCache, key)
	r.mu.Unlock()
	_ = ctx
	return r.store.ResetThrottleCounter(key)
}

// cleanup periodically removes stale token buckets older than 10 minutes.
func (r *DBRateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		r.mu.Lock()
		for key, state := range r.localCache {
			if state.window.Before(cutoff) {
				delete(r.localCache, key)
			}
		}
		r.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// Local-Only Rate Limiter (single-node deployments)
// ---------------------------------------------------------------------------

// LocalRateLimiter implements pure in-memory rate limiting for single-node deployments.
type LocalRateLimiter struct {
	buckets sync.Map // key -> *tokenBucketEntry
}

type tokenBucketEntry struct {
	bucket  *TokenBucket
	window  time.Duration
	limit   int
	resetAt int64
	mu      sync.Mutex
}

// NewLocalRateLimiter creates a new local-only rate limiter.
func NewLocalRateLimiter() *LocalRateLimiter {
	rl := &LocalRateLimiter{}
	go rl.cleanup()
	return rl
}

// Allow checks if a request is allowed under the given key.
func (r *LocalRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	allowed, remaining, _, err := r.AllowWithBurst(ctx, key, limit, limit, window)
	return allowed, remaining, err
}

// AllowWithBurst checks if a request is allowed with a burst capacity.
func (r *LocalRateLimiter) AllowWithBurst(ctx context.Context, key string, limit int, burst int, window time.Duration) (bool, int, int, error) {
	rate := float64(limit) / window.Seconds()
	now := time.Now()
	windowStart := now.Truncate(window).Add(window).Unix()

	entryRaw, _ := r.buckets.LoadOrStore(key, &tokenBucketEntry{
		bucket:  NewTokenBucket(rate, float64(burst)),
		window:  window,
		limit:   limit,
		resetAt: windowStart,
	})
	entry := entryRaw.(*tokenBucketEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	currentWindowStart := now.Truncate(window).Add(window).Unix()
	if currentWindowStart > entry.resetAt {
		entry.bucket = NewTokenBucket(rate, float64(burst))
		entry.resetAt = currentWindowStart
	}

	allowed := entry.bucket.Allow()
	remaining := int(entry.bucket.tokens)
	_ = ctx
	return allowed, remaining, int(entry.resetAt), nil
}

// Reset resets the rate limit for a key.
func (r *LocalRateLimiter) Reset(ctx context.Context, key string) error {
	r.buckets.Delete(key)
	_ = ctx
	return nil
}

// cleanup periodically removes stale token buckets.
func (r *LocalRateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().Unix()
		r.buckets.Range(func(key, value interface{}) bool {
			entry := value.(*tokenBucketEntry)
			if now > entry.resetAt+300 { // 5 minutes past window
				r.buckets.Delete(key)
			}
			return true
		})
	}
}

// ---------------------------------------------------------------------------
// Gateway Middleware
// ---------------------------------------------------------------------------

const (
	RateLimitInfoKey = "rate_limit_info"
)

// RateLimitConfig holds rate limiting configuration for the gateway.
type RateLimitConfig struct {
	Enabled              bool
	DefaultLimit         int
	DefaultWindow        time.Duration
	HeaderLimitName      string
	HeaderRemainingName  string
	HeaderResetName      string
	HeaderRetryAfterName string
	Distributed          bool
	PerAPILimits         map[string]APILimitConfig
	PerAppLimits         map[string]int
	PerUserLimits        map[string]int
}

// APILimitConfig defines rate limits for a specific API.
type APILimitConfig struct {
	Limit     int
	Burst     int
	Window    time.Duration
	APILimit  int
	AppLimit  int
	UserLimit int
}

// RateLimiterMiddleware is the Gin middleware for rate limiting.
type RateLimiterMiddleware struct {
	limiter RateLimiter
	config  RateLimitConfig
}

// NewRateLimiterMiddleware creates a new rate limiter middleware.
func NewRateLimiterMiddleware(limiter RateLimiter, config RateLimitConfig) *RateLimiterMiddleware {
	if config.DefaultLimit <= 0 {
		config.DefaultLimit = 100
	}
	if config.DefaultWindow <= 0 {
		config.DefaultWindow = time.Minute
	}
	if config.HeaderLimitName == "" {
		config.HeaderLimitName = "X-RateLimit-Limit"
	}
	if config.HeaderRemainingName == "" {
		config.HeaderRemainingName = "X-RateLimit-Remaining"
	}
	if config.HeaderResetName == "" {
		config.HeaderResetName = "X-RateLimit-Reset"
	}
	if config.HeaderRetryAfterName == "" {
		config.HeaderRetryAfterName = "Retry-After"
	}
	if config.PerAPILimits == nil {
		config.PerAPILimits = make(map[string]APILimitConfig)
	}
	if config.PerAppLimits == nil {
		config.PerAppLimits = map[string]int{
			"Bronze":    100,
			"Silver":    500,
			"Gold":      2000,
			"Unlimited": 100000,
		}
	}
	if config.PerUserLimits == nil {
		config.PerUserLimits = map[string]int{
			"admin":      100000,
			"publisher":  5000,
			"subscriber": 1000,
		}
	}
	return &RateLimiterMiddleware{
		limiter: limiter,
		config:  config,
	}
}

// Middleware returns the Gin handler function for rate limiting.
func (m *RateLimiterMiddleware) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Enabled {
			c.Next()
			return
		}

		key := m.buildKey(c)
		limit, burst, window := m.resolveLimits(c)

		allowed, remaining, resetAt, err := m.limiter.AllowWithBurst(c.Request.Context(), key, limit, burst, window)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "rate limiter error",
			})
			return
		}

		// Set rate limit headers
		c.Header(m.config.HeaderLimitName, strconv.Itoa(limit))
		c.Header(m.config.HeaderRemainingName, strconv.Itoa(remaining))
		c.Header(m.config.HeaderResetName, strconv.FormatInt(int64(resetAt), 10))

		if !allowed {
			retryAfter := int64(resetAt) - time.Now().Unix()
			if retryAfter < 1 {
				retryAfter = 1
			}
			c.Header(m.config.HeaderRetryAfterName, strconv.FormatInt(retryAfter, 10))
			c.Header("X-RateLimit-Reason", "quota exceeded")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"limit":       limit,
				"remaining":   remaining,
				"reset_at":    resetAt,
				"retry_after": retryAfter,
			})
			return
		}

		info := &RateLimitInfo{
			Allowed:   allowed,
			Limit:     limit,
			Remaining: remaining,
			ResetAt:   int64(resetAt),
		}
		c.Set(RateLimitInfoKey, info)
		c.Next()
	}
}

// buildKey constructs the rate limit key from request context.
func (m *RateLimiterMiddleware) buildKey(c *gin.Context) string {
	apiContext := c.GetString("api_context")
	appID := c.GetString("app_id")
	userID := c.GetString("user_id")
	clientIP := c.ClientIP()

	parts := []string{"rl"}
	if apiContext != "" {
		parts = append(parts, apiContext)
	}
	if appID != "" {
		parts = append(parts, "app:"+appID)
	} else {
		parts = append(parts, "ip:"+clientIP)
	}
	if userID != "" {
		parts = append(parts, "user:"+userID)
	}

	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ":"
		}
		result += p
	}
	return result
}

// resolveLimits determines the appropriate rate limits for the request.
func (m *RateLimiterMiddleware) resolveLimits(c *gin.Context) (limit, burst int, window time.Duration) {
	limit = m.config.DefaultLimit
	burst = limit
	window = m.config.DefaultWindow

	// Check API-specific limits
	apiContext := c.GetString("api_context")
	if apiCtx, ok := m.config.PerAPILimits[apiContext]; ok {
		limit = apiCtx.Limit
		burst = apiCtx.Burst
		if apiCtx.Window > 0 {
			window = apiCtx.Window
		}
	}

	// Check application tier limits
	tier := c.GetString("app_tier")
	if tierLimit, ok := m.config.PerAppLimits[tier]; ok && tierLimit < limit {
		limit = tierLimit
		burst = tierLimit
	}

	// Check user role limits
	role := c.GetString("user_role")
	if roleLimit, ok := m.config.PerUserLimits[role]; ok && roleLimit < limit {
		limit = roleLimit
		burst = roleLimit
	}

	return
}

// ---------------------------------------------------------------------------
// Spike Arrest (Token Drip Rate Limiter)
// ---------------------------------------------------------------------------

// SpikeArrest implements a token drip rate limiter to prevent traffic spikes.
type SpikeArrest struct {
	ratePerSecond float64
	bucket        *TokenBucket
}

// NewSpikeArrest creates a new spike arrest limiter.
func NewSpikeArrest(ratePerSecond float64) *SpikeArrest {
	return &SpikeArrest{
		ratePerSecond: ratePerSecond,
		bucket:        NewTokenBucket(ratePerSecond, ratePerSecond),
	}
}

// Allow checks if the request passes spike arrest.
func (sa *SpikeArrest) Allow() bool {
	return sa.bucket.Allow()
}

// ThrottleMiddleware returns a Gin middleware for spike arrest throttling.
func ThrottleMiddleware(ratePerSecond float64) gin.HandlerFunc {
	sa := NewSpikeArrest(ratePerSecond)
	return func(c *gin.Context) {
		if !sa.Allow() {
			c.Header("X-Throttled", "true")
			c.Header("X-Throttle-Reason", "spike_arrest")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":  "request throttled due to traffic spike",
				"policy": "spike_arrest",
			})
			return
		}
		c.Next()
	}
}
