package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/vedadb/vapim/pkg/models"
)

// ThrottleLevel represents the level at which throttling is applied.
type ThrottleLevel string

const (
	LevelGlobal     ThrottleLevel = "Global"
	LevelTenant     ThrottleLevel = "Tenant"
	LevelAPI        ThrottleLevel = "API"
	LevelResource   ThrottleLevel = "Resource"
	LevelApplication ThrottleLevel = "Application"
	LevelUser       ThrottleLevel = "User"
)

// ThrottlingEngine is the core throttling engine implementing token bucket,
// sliding window, and fixed window rate limiting algorithms.
type ThrottlingEngine struct {
	store       ThrottleStore
	logger      *slog.Logger
	mu          sync.RWMutex
	localCache  map[string]*tokenBucket // in-memory token buckets for fast path
	burstStates map[string]*burstState  // burst control state
	tiers       map[string]*TierConfig
}

// tokenBucket represents a token bucket for rate limiting.
type tokenBucket struct {
	key          string
	tokens       float64
	capacity     float64
	refillRate   float64   // tokens per second
	lastRefill   time.Time
	mu           sync.Mutex
}

// burstState tracks burst allowance and recovery.
type burstState struct {
	key           string
	maxBurst      int64
	currentBurst  int64
	used          int64
	lastUsed      time.Time
	recoveryRate  float64 // burst capacity recovered per second
	mu            sync.Mutex
}

// TierConfig defines rate limits for a subscription tier.
type TierConfig struct {
	Name            string
	RequestsPerMin  int64
	RequestsPerHour int64
	RequestsPerDay  int64
	BurstAllowance  int64
	QuotaUnit       string
}

// defaultTierConfig returns the built-in tier definitions.
func defaultTierConfig() map[string]*TierConfig {
	return map[string]*TierConfig{
		"Bronze": {
			Name:            "Bronze",
			RequestsPerMin:  20,
			RequestsPerHour: 1000,
			RequestsPerDay:  10000,
			BurstAllowance:  5,
			QuotaUnit:       "requests",
		},
		"Silver": {
			Name:            "Silver",
			RequestsPerMin:  100,
			RequestsPerHour: 5000,
			RequestsPerDay:  50000,
			BurstAllowance:  20,
			QuotaUnit:       "requests",
		},
		"Gold": {
			Name:            "Gold",
			RequestsPerMin:  500,
			RequestsPerHour: 25000,
			RequestsPerDay:  250000,
			BurstAllowance:  100,
			QuotaUnit:       "requests",
		},
		"Unlimited": {
			Name:            "Unlimited",
			RequestsPerMin:  math.MaxInt64,
			RequestsPerHour: math.MaxInt64,
			RequestsPerDay:  math.MaxInt64,
			BurstAllowance:  math.MaxInt64,
			QuotaUnit:       "requests",
		},
	}
}

// NewThrottlingEngine creates a new throttling engine.
func NewThrottlingEngine(store ThrottleStore, logger *slog.Logger) *ThrottlingEngine {
	if logger == nil {
		logger = slog.Default()
	}

	engine := &ThrottlingEngine{
		store:       store,
		logger:      logger.With("component", "throttle-engine"),
		localCache:  make(map[string]*tokenBucket),
		burstStates: make(map[string]*burstState),
		tiers:       defaultTierConfig(),
	}

	// Start background cleanup goroutine
	go engine.cleanupLoop()

	return engine
}

// Evaluate checks if a request should be throttled based on all applicable policies.
func (e *ThrottlingEngine) Evaluate(ctx context.Context, req *models.ThrottleCheckRequest) *models.ThrottleResult {
	result := &models.ThrottleResult{
		Throttled:   false,
		Allowed:     true,
		Timestamp:   time.Now().UTC(),
	}

	// Check in order of priority: Global -> Tenant -> API -> Resource -> Application -> User
	checks := []struct {
		level  ThrottleLevel
		check  func(context.Context, *models.ThrottleCheckRequest) *models.ThrottleResult
	}{
		{LevelGlobal, e.checkGlobalThrottle},
		{LevelTenant, e.checkTenantThrottle},
		{LevelAPI, e.checkAPIThrottle},
		{LevelResource, e.checkResourceThrottle},
		{LevelApplication, e.checkApplicationThrottle},
		{LevelUser, e.checkUserThrottle},
	}

	for _, check := range checks {
		r := check.check(ctx, req)
		if r.Throttled {
			r.Level = string(check.level)
			r.Timestamp = time.Now().UTC()
			return r
		}
	}

	return result
}

// checkGlobalThrottle checks the global rate limit across all traffic.
func (e *ThrottlingEngine) checkGlobalThrottle(ctx context.Context, req *models.ThrottleCheckRequest) *models.ThrottleResult {
	// Global limit: 10,000 requests per minute across the entire system
	key := "throttle:global:rpm"
	return e.checkFixedWindow(ctx, key, 10000, time.Minute)
}

// checkTenantThrottle checks per-tenant rate limits.
func (e *ThrottlingEngine) checkTenantThrottle(ctx context.Context, req *models.ThrottleCheckRequest) *models.ThrottleResult {
	if req.Tenant == "" {
		req.Tenant = "carbon.super"
	}
	key := fmt.Sprintf("throttle:tenant:%s:rpm", req.Tenant)
	return e.checkFixedWindow(ctx, key, 5000, time.Minute)
}

// checkAPIThrottle checks per-API rate limits.
func (e *ThrottlingEngine) checkAPIThrottle(ctx context.Context, req *models.ThrottleCheckRequest) *models.ThrottleResult {
	if req.APIID == "" {
		return &models.ThrottleResult{Throttled: false}
	}

	// Use token bucket for API-level throttling (allows bursts)
	key := fmt.Sprintf("throttle:api:%s:rpm", req.APIID)
	limit := int64(2000) // default API limit
	if req.APILimitPerMin > 0 {
		limit = req.APILimitPerMin
	}

	return e.checkTokenBucket(ctx, key, limit, time.Minute)
}

// checkResourceThrottle checks per-resource (endpoint + method) rate limits.
func (e *ThrottlingEngine) checkResourceThrottle(ctx context.Context, req *models.ThrottleCheckRequest) *models.ThrottleResult {
	if req.APIID == "" || req.ResourcePath == "" {
		return &models.ThrottleResult{Throttled: false}
	}

	key := fmt.Sprintf("throttle:resource:%s:%s:%s:rpm", req.APIID, req.ResourcePath, req.HTTPMethod)
	limit := int64(1000)
	if req.ResourceLimitPerMin > 0 {
		limit = req.ResourceLimitPerMin
	}

	return e.checkTokenBucket(ctx, key, limit, time.Minute)
}

// checkApplicationThrottle checks per-application rate limits based on tier.
func (e *ThrottlingEngine) checkApplicationThrottle(ctx context.Context, req *models.ThrottleCheckRequest) *models.ThrottleResult {
	if req.AppID == "" {
		return &models.ThrottleResult{Throttled: false}
	}

	tier := req.Tier
	if tier == "" {
		tier = "Unlimited"
	}

	tierConfig, ok := e.tiers[tier]
	if !ok {
		tierConfig = e.tiers["Unlimited"]
	}

	// Application per-minute limit
	key := fmt.Sprintf("throttle:app:%s:rpm", req.AppID)
	result := e.checkTokenBucket(ctx, key, tierConfig.RequestsPerMin, time.Minute)
	if result.Throttled {
		return result
	}

	// Application per-hour limit
	keyHour := fmt.Sprintf("throttle:app:%s:rph", req.AppID)
	result = e.checkSlidingWindow(ctx, keyHour, tierConfig.RequestsPerHour, time.Hour)
	if result.Throttled {
		return result
	}

	// Application per-day limit
	keyDay := fmt.Sprintf("throttle:app:%s:rpd", req.AppID)
	result = e.checkFixedWindow(ctx, keyDay, tierConfig.RequestsPerDay, 24*time.Hour)
	if result.Throttled {
		return result
	}

	return &models.ThrottleResult{Throttled: false}
}

// checkUserThrottle checks per-user rate limits.
func (e *ThrottlingEngine) checkUserThrottle(ctx context.Context, req *models.ThrottleCheckRequest) *models.ThrottleResult {
	if req.UserID == "" {
		return &models.ThrottleResult{Throttled: false}
	}

	key := fmt.Sprintf("throttle:user:%s:rpm", req.UserID)
	return e.checkTokenBucket(ctx, key, 200, time.Minute)
}

// --- Rate Limiting Algorithms ---

// checkTokenBucket uses the token bucket algorithm for rate limiting.
// It supports bursts up to the bucket capacity.
func (e *ThrottlingEngine) checkTokenBucket(ctx context.Context, key string, limit int64, window time.Duration) *models.ThrottleResult {
	bucket := e.getOrCreateBucket(key, limit, window)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(bucket.lastRefill).Seconds()
	bucket.tokens = math.Min(bucket.capacity, bucket.tokens+elapsed*bucket.refillRate)
	bucket.lastRefill = now

	if bucket.tokens >= 1 {
		bucket.tokens--

		// Sync to persistent store periodically
		e.syncCounter(ctx, key, 1)

		return &models.ThrottleResult{
			Throttled:        false,
			Remaining:        int64(bucket.tokens),
			Limit:            int64(bucket.capacity),
			ResetTime:        now.Add(time.Duration((1.0/bucket.refillRate)*float64(time.Second))),
		}
	}

	// Calculate retry after
	retryAfter := time.Duration((1.0 - bucket.tokens) / bucket.refillRate * float64(time.Second))
	return &models.ThrottleResult{
		Throttled:        true,
		Level:            string(LevelAPI),
		Limit:            int64(bucket.capacity),
		Remaining:        0,
		RetryAfter:       retryAfter,
		ResetTime:        now.Add(retryAfter),
	}
}

// checkFixedWindow uses a fixed time window counter for rate limiting.
func (e *ThrottlingEngine) checkFixedWindow(ctx context.Context, key string, limit int64, window time.Duration) *models.ThrottleResult {
	count, err := e.store.IncrementCounter(ctx, key, window)
	if err != nil {
		e.logger.Error("failed to increment counter", "key", key, "error", err)
		// Fail open on counter errors to avoid blocking all traffic
		return &models.ThrottleResult{Throttled: false}
	}

	if count > limit {
		// Calculate window reset time
		resetTime := time.Now().Truncate(window).Add(window)
		retryAfter := time.Until(resetTime)

		return &models.ThrottleResult{
			Throttled:        true,
			Limit:            limit,
			Remaining:        0,
			RetryAfter:       retryAfter,
			ResetTime:        resetTime,
		}
	}

	return &models.ThrottleResult{
		Throttled:        false,
		Limit:            limit,
		Remaining:        limit - count,
		ResetTime:        time.Now().Truncate(window).Add(window),
	}
}

// checkSlidingWindow uses a sliding window counter for rate limiting.
// It combines the current window count with a weighted portion of the previous window.
func (e *ThrottlingEngine) checkSlidingWindow(ctx context.Context, key string, limit int64, window time.Duration) *models.ThrottleResult {
	now := time.Now()
	currentWindow := now.Truncate(window)
	previousWindow := currentWindow.Add(-window)

	currentKey := fmt.Sprintf("%s:%d", key, currentWindow.Unix())
	previousKey := fmt.Sprintf("%s:%d", key, previousWindow.Unix())

	currentCount, _ := e.store.GetCounter(ctx, currentKey)
	previousCount, _ := e.store.GetCounter(ctx, previousKey)

	// Weight the previous window based on how much time has elapsed
	elapsedRatio := float64(now.Sub(currentWindow)) / float64(window)
	estimatedCount := int64(float64(previousCount)*(1.0-elapsedRatio)) + currentCount

	if estimatedCount >= limit {
		resetTime := currentWindow.Add(window)
		return &models.ThrottleResult{
			Throttled:        true,
			Limit:            limit,
			Remaining:        0,
			RetryAfter:       time.Until(resetTime),
			ResetTime:        resetTime,
		}
	}

	// Increment current window
	newCount, err := e.store.IncrementCounter(ctx, currentKey, 2*window)
	if err != nil {
		e.logger.Error("failed to increment sliding window counter", "key", currentKey, "error", err)
		return &models.ThrottleResult{Throttled: false}
	}
	_ = newCount

	remaining := limit - estimatedCount - 1
	if remaining < 0 {
		remaining = 0
	}

	return &models.ThrottleResult{
		Throttled:        false,
		Limit:            limit,
		Remaining:        remaining,
		ResetTime:        currentWindow.Add(window),
	}
}

// --- Burst Control ---

// AllowBurst checks if a burst request is allowed and manages burst recovery.
func (e *ThrottlingEngine) AllowBurst(ctx context.Context, key string, burstSize int64) bool {
	state := e.getOrCreateBurstState(key, burstSize)

	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(state.lastUsed).Seconds()

	// Gradual recovery of burst capacity
	recovered := int64(elapsed * state.recoveryRate)
	if recovered > 0 {
		state.currentBurst = minInt64(state.maxBurst, state.currentBurst+recovered)
		state.used = maxInt64(0, state.used-recovered)
	}

	if state.currentBurst > 0 {
		state.currentBurst--
		state.used++
		state.lastUsed = now
		return true
	}

	return false
}

// GetBurstStatus returns the current burst state for a key.
func (e *ThrottlingEngine) GetBurstStatus(ctx context.Context, key string) *models.BurstStatus {
	state := e.getOrCreateBurstState(key, 0)

	state.mu.Lock()
	defer state.mu.Unlock()

	return &models.BurstStatus{
		Key:           state.key,
		MaxBurst:      state.maxBurst,
		CurrentBurst:  state.currentBurst,
		Used:          state.used,
		LastUsed:      state.lastUsed,
		RecoveryRate:  state.recoveryRate,
	}
}

// --- Status ---

// GetStatus returns the current throttle status for a key.
func (e *ThrottlingEngine) GetStatus(ctx context.Context, key string) (*models.ThrottleStatus, error) {
	count, err := e.store.GetCounter(ctx, key)
	if err != nil {
		return nil, err
	}

	return &models.ThrottleStatus{
		Key:       key,
		Count:     count,
		Timestamp: time.Now().UTC(),
	}, nil
}

// --- Helpers ---

func (e *ThrottlingEngine) getOrCreateBucket(key string, limit int64, window time.Duration) *tokenBucket {
	e.mu.RLock()
	bucket, ok := e.localCache[key]
	e.mu.RUnlock()

	if ok {
		return bucket
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	bucket, ok = e.localCache[key]
	if ok {
		return bucket
	}

	capacity := float64(limit)
	refillRate := capacity / window.Seconds()

	bucket = &tokenBucket{
		key:        key,
		tokens:     capacity,
		capacity:   capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
	e.localCache[key] = bucket
	return bucket
}

func (e *ThrottlingEngine) getOrCreateBurstState(key string, burstSize int64) *burstState {
	e.mu.RLock()
	state, ok := e.burstStates[key]
	e.mu.RUnlock()

	if ok {
		return state
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	state, ok = e.burstStates[key]
	if ok {
		return state
	}

	maxBurst := burstSize
	if maxBurst == 0 {
		maxBurst = 10 // default
	}

	state = &burstState{
		key:          key,
		maxBurst:     maxBurst,
		currentBurst: maxBurst,
		recoveryRate: float64(maxBurst) / 60.0, // recover full burst in 60 seconds
		lastUsed:     time.Now(),
	}
	e.burstStates[key] = state
	return state
}

func (e *ThrottlingEngine) syncCounter(ctx context.Context, key string, delta int64) {
	// Fire-and-forget counter sync to persistent store
	// The exact sync frequency is controlled by the store implementation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := e.store.IncrementCounter(ctx, key, time.Minute); err != nil {
			e.logger.Debug("counter sync failed", "key", key, "error", err)
		}
	}()
}

// cleanupLoop periodically removes stale entries from local caches.
func (e *ThrottlingEngine) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		e.mu.Lock()
		now := time.Now()

		// Clean up stale token buckets (inactive for > 10 minutes)
		for key, bucket := range e.localCache {
			bucket.mu.Lock()
			if now.Sub(bucket.lastRefill) > 10*time.Minute {
				delete(e.localCache, key)
			}
			bucket.mu.Unlock()
		}

		// Clean up stale burst states (inactive for > 10 minutes)
		for key, state := range e.burstStates {
			state.mu.Lock()
			if now.Sub(state.lastUsed) > 10*time.Minute {
				delete(e.burstStates, key)
			}
			state.mu.Unlock()
		}

		e.mu.Unlock()

		e.logger.Debug("throttle engine cache cleanup completed",
			"buckets", len(e.localCache),
			"burst_states", len(e.burstStates),
		)
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
