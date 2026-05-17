// Package gateway provides the core API gateway functionality including rate limiting.
// This file implements the token bucket rate limiter with support for per-API,
// per-application, per-user, and distributed rate limiting backed by VedaDB.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter defines the interface for rate limiting implementations.
type RateLimiter interface {
	// Allow checks if a request is allowed under the given key and returns rate limit info.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (*RateLimitInfo, error)
	// AllowWithBurst checks if a request is allowed with a burst capacity.
	AllowWithBurst(ctx context.Context, key string, limit int, burst int, window time.Duration) (*RateLimitInfo, error)
	// Reset resets the rate limiter for the given key.
	Reset(ctx context.Context, key string) error
}

// RateLimitInfo contains rate limiting information returned to the caller.
type RateLimitInfo struct {
	// Allowed indicates whether the request is allowed.
	Allowed bool `json:"allowed"`
	// Limit is the maximum number of requests allowed in the window.
	Limit int `json:"limit"`
	// Remaining is the number of requests remaining in the current window.
	Remaining int `json:"remaining"`
	// ResetAt is the Unix timestamp when the rate limit window resets.
	ResetAt int64 `json:"reset_at"`
	// RetryAfter is the number of seconds to wait before retrying (only when not allowed).
	RetryAfter int64 `json:"retry_after,omitempty"`
}

// TokenBucket implements a token bucket rate limiter.
type TokenBucket struct {
	// tokens is the current number of available tokens.
	tokens float64
	// lastRefill is the last time the bucket was refilled.
	lastRefill time.Time
	// capacity is the maximum number of tokens (burst capacity).
	capacity float64
	// rate is the rate of token generation per second.
	rate float64
	mu   sync.Mutex
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

// refill adds tokens based on elapsed time.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.lastRefill = now
}

// LocalRateLimiter implements rate limiting using in-memory token buckets.
type LocalRateLimiter struct {
	buckets sync.Map // key -> *tokenBucketEntry
}

type tokenBucketEntry struct {
	bucket    *TokenBucket
	window    time.Duration
	limit     int
	resetAt   int64
	mu        sync.Mutex
}

// NewLocalRateLimiter creates a new local rate limiter.
func NewLocalRateLimiter() *LocalRateLimiter {
	rl := &LocalRateLimiter{}
	// Start cleanup goroutine to remove stale buckets
	go rl.cleanup()
	return rl
}

// Allow checks if a request is allowed under the given key.
func (r *LocalRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (*RateLimitInfo, error) {
	return r.AllowWithBurst(ctx, key, limit, limit, window)
}

// AllowWithBurst checks if a request is allowed with a burst capacity.
func (r *LocalRateLimiter) AllowWithBurst(ctx context.Context, key string, limit int, burst int, window time.Duration) (*RateLimitInfo, error) {
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

	// Reset bucket if window has changed
	currentWindowStart := now.Truncate(window).Add(window).Unix()
	if currentWindowStart > entry.resetAt {
		entry.bucket = NewTokenBucket(rate, float64(burst))
		entry.resetAt = currentWindowStart
	}

	allowed := entry.bucket.Allow()
	remaining := int(entry.bucket.tokens)

	info := &RateLimitInfo{
		Allowed:  allowed,
		Limit:    limit,
		Remaining: remaining,
		ResetAt:  entry.resetAt,
	}

	if !allowed {
		info.RetryAfter = entry.resetAt - now.Unix()
		if info.RetryAfter < 1 {
			info.RetryAfter = 1
		}
	}

	return info, nil
}

// Reset resets the rate limiter for the given key.
func (r *LocalRateLimiter) Reset(ctx context.Context, key string) error {
	r.buckets.Delete(key)
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
// Gateway Rate Limiting Integration
// ---------------------------------------------------------------------------

// RateLimitKeys defines the context keys for rate limit information.
const (
	RateLimitInfoKey = "rate_limit_info"
)

// RateLimitConfig holds rate limiting configuration for the gateway.
type RateLimitConfig struct {
	// Enabled enables/disables rate limiting.
	Enabled bool
	// DefaultLimit is the default request limit per window.
	DefaultLimit int
	// DefaultWindow is the default rate limit window.
	DefaultWindow time.Duration
	// HeaderLimitName is the response header for the limit.
	HeaderLimitName string
	// HeaderRemainingName is the response header for remaining requests.
	HeaderRemainingName string
	// HeaderResetName is the response header for reset timestamp.
	HeaderResetName string
	// HeaderRetryAfterName is the response header for retry after.
	HeaderRetryAfterName string
	// Distributed enables distributed rate limiting via VedaDB.
	Distributed bool
	// PerAPILimits defines limits per API context.
	PerAPILimits map[string]APILimitConfig
	// PerAppLimits defines limits per application tier.
	PerAppLimits map[string]int
	// PerUserLimits defines limits per user role.
	PerUserLimits map[string]int
}

// APILimitConfig defines rate limits for a specific API.
type APILimitConfig struct {
	Limit    int
	Burst    int
	Window   time.Duration
	APILimit int
	AppLimit int
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
			"Bronze":   100,
			"Silver":   500,
			"Gold":     2000,
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

		info, err := m.limiter.AllowWithBurst(c.Request.Context(), key, limit, burst, window)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "rate limiter error",
			})
			return
		}

		// Set rate limit headers
		c.Header(m.config.HeaderLimitName, strconv.Itoa(info.Limit))
		c.Header(m.config.HeaderRemainingName, strconv.Itoa(info.Remaining))
		c.Header(m.config.HeaderResetName, strconv.FormatInt(info.ResetAt, 10))

		if !info.Allowed {
			c.Header(m.config.HeaderRetryAfterName, strconv.FormatInt(info.RetryAfter, 10))
			c.Header("X-RateLimit-Reason", "quota exceeded")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":        "rate limit exceeded",
				"limit":        info.Limit,
				"remaining":    info.Remaining,
				"reset_at":     info.ResetAt,
				"retry_after":  info.RetryAfter,
			})
			return
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

	// Build composite key: api:app:user:ip
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

	return fmt.Sprintf("%s:%s:%s", apiContext, appID, userID)
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
// Distributed Rate Limiter (VedaDB-backed)
// ---------------------------------------------------------------------------

// DistributedRateLimiter implements rate limiting backed by VedaDB for
// distributed gateway deployments where multiple instances share rate limits.
type DistributedRateLimiter struct {
	client VedaDBRateClient
	config RateLimitConfig
}

// VedaDBRateClient defines the interface needed from VedaDB for rate limiting.
type VedaDBRateClient interface {
	Get(ctx context.Context, namespace, key string, dest interface{}) error
	Set(ctx context.Context, namespace, key string, value interface{}) error
}

// rateLimitEntry represents a stored rate limit counter in VedaDB.
type rateLimitEntry struct {
	Count     int       `json:"count"`
	ResetAt   int64     `json:"reset_at"`
	Limit     int       `json:"limit"`
	Burst     int       `json:"burst"`
	Window    int64     `json:"window"` // nanoseconds
	UpdatedAt time.Time `json:"updated_at"`
}

// NewDistributedRateLimiter creates a new distributed rate limiter.
func NewDistributedRateLimiter(client VedaDBRateClient, config RateLimitConfig) *DistributedRateLimiter {
	return &DistributedRateLimiter{
		client: client,
		config: config,
	}
}

// Allow checks if a request is allowed in the distributed rate limiter.
func (d *DistributedRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (*RateLimitInfo, error) {
	return d.AllowWithBurst(ctx, key, limit, limit, window)
}

// AllowWithBurst checks if a request is allowed with burst capacity (distributed).
func (d *DistributedRateLimiter) AllowWithBurst(ctx context.Context, key string, limit int, burst int, window time.Duration) (*RateLimitInfo, error) {
	namespace := "ratelimit"
	now := time.Now()
	windowEnd := now.Truncate(window).Add(window).Unix()

	var entry rateLimitEntry
	err := d.client.Get(ctx, namespace, key, &entry)
	if err != nil {
		// Key doesn't exist, create new entry
		entry = rateLimitEntry{
			Count:     1,
			ResetAt:   windowEnd,
			Limit:     limit,
			Burst:     burst,
			Window:    window.Nanoseconds(),
			UpdatedAt: now,
		}
		if err := d.client.Set(ctx, namespace, key, entry); err != nil {
			return nil, fmt.Errorf("failed to create rate limit entry: %w", err)
		}
		return &RateLimitInfo{
			Allowed:   true,
			Limit:     limit,
			Remaining: burst - 1,
			ResetAt:   windowEnd,
		}, nil
	}

	// Check if window has expired
	if now.Unix() >= entry.ResetAt {
		// Reset for new window
		entry = rateLimitEntry{
			Count:     1,
			ResetAt:   windowEnd,
			Limit:     limit,
			Burst:     burst,
			Window:    window.Nanoseconds(),
			UpdatedAt: now,
		}
		if err := d.client.Set(ctx, namespace, key, entry); err != nil {
			return nil, fmt.Errorf("failed to reset rate limit entry: %w", err)
		}
		return &RateLimitInfo{
			Allowed:   true,
			Limit:     limit,
			Remaining: burst - 1,
			ResetAt:   windowEnd,
		}, nil
	}

	// Check if over limit
	if entry.Count >= burst {
		return &RateLimitInfo{
			Allowed:    false,
			Limit:      limit,
			Remaining:  0,
			ResetAt:    entry.ResetAt,
			RetryAfter: entry.ResetAt - now.Unix(),
		}, nil
	}

	// Increment counter
	entry.Count++
	entry.UpdatedAt = now
	if err := d.client.Set(ctx, namespace, key, entry); err != nil {
		return nil, fmt.Errorf("failed to update rate limit entry: %w", err)
	}

	return &RateLimitInfo{
		Allowed:   true,
		Limit:     limit,
		Remaining: burst - entry.Count,
		ResetAt:   entry.ResetAt,
	}, nil
}

// Reset resets the rate limit for a key.
func (d *DistributedRateLimiter) Reset(ctx context.Context, key string) error {
	// In distributed mode, we store a reset marker
	return d.client.Set(ctx, "ratelimit", key, rateLimitEntry{
		Count:     0,
		ResetAt:   time.Now().Unix(),
		UpdatedAt: time.Now(),
	})
}

// ---------------------------------------------------------------------------
// Spike Arrest (Token Drip Rate Limiter)
// ---------------------------------------------------------------------------

// SpikeArrest implements a token drip rate limiter to prevent traffic spikes.
type SpikeArrest struct {
	// ratePerSecond is the maximum requests per second.
	ratePerSecond float64
	// bucket is the token bucket.
	bucket *TokenBucket
	mu     sync.Mutex
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
				"error":   "request throttled due to traffic spike",
				"policy":  "spike_arrest",
			})
			return
		}
		c.Next()
	}
}
