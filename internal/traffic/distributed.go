package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// DistributedRateLimiter implements cluster-aware rate limiting using
// VedaDB as the shared counter store. It provides consistent throttling
// across multiple gateway nodes.
type DistributedRateLimiter struct {
	store       ThrottleStore
	logger      *slog.Logger
	mu          sync.RWMutex
	localCache  map[string]*localCounter // fast-path local counters
	eventCh     chan *models.CounterEvent
	batchSize   int
	flushInterval time.Duration
}

// localCounter tracks a counter value locally for fast-path decisions.
type localCounter struct {
	key        string
	value      int64
	limit      int64
	window     time.Duration
	windowStart time.Time
	lastSync   time.Time
	mu         sync.Mutex
}

// NewDistributedRateLimiter creates a new distributed rate limiter.
func NewDistributedRateLimiter(store ThrottleStore, logger *slog.Logger) *DistributedRateLimiter {
	if logger == nil {
		logger = slog.Default()
	}

	drl := &DistributedRateLimiter{
		store:         store,
		logger:        logger.With("component", "distributed-limiter"),
		localCache:    make(map[string]*localCounter),
		eventCh:       make(chan *models.CounterEvent, 10000),
		batchSize:     100,
		flushInterval: 1 * time.Second,
	}

	// Start background event processor
	go drl.eventProcessor()

	// Start periodic counter sync
	go drl.syncLoop()

	return drl
}

// Allow checks if a request is allowed under the distributed rate limit.
// It uses a fast local cache with periodic sync to VedaDB for consistency.
func (drl *DistributedRateLimiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (*models.DistributedLimitResult, error) {
	counter := drl.getOrCreateCounter(key, limit, window)

	counter.mu.Lock()
	defer counter.mu.Unlock()

	now := time.Now()

	// Check if we need to reset the window
	if now.Sub(counter.windowStart) >= window {
		counter.value = 0
		counter.windowStart = now.Truncate(window)
	}

	// Fast path: check local counter
	if counter.value >= counter.limit {
		// Quota exceeded locally - sync with store to be sure
		storeCount, err := drl.store.GetCounter(ctx, key)
		if err != nil {
			drl.logger.Error("failed to sync counter with store", "key", key, "error", err)
			// On error, allow the request to avoid blocking traffic
			counter.value++
			return &models.DistributedLimitResult{
				Allowed:     true,
				Limit:       limit,
				Remaining:   limit - counter.value,
				ResetTime:   counter.windowStart.Add(window),
				Synced:      false,
			}, nil
		}

		if storeCount >= limit {
			// Confirmed: quota exceeded globally
			resetTime := counter.windowStart.Add(window)
			retryAfter := time.Until(resetTime)
			if retryAfter < 0 {
				retryAfter = 0
			}

			return &models.DistributedLimitResult{
				Allowed:     false,
				Limit:       limit,
				Remaining:   0,
				ResetTime:   resetTime,
				RetryAfter:  retryAfter,
				Synced:      true,
			}, nil
		}

		// Store count is lower - local cache was stale
		counter.value = storeCount
	}

	// Allow the request
	counter.value++

	// Publish counter event for cluster sync
	drl.publishEvent(&models.CounterEvent{
		Key:       key,
		Delta:     1,
		Timestamp: now,
	})

	return &models.DistributedLimitResult{
		Allowed:     true,
		Limit:       limit,
		Remaining:   limit - counter.value,
		ResetTime:   counter.windowStart.Add(window),
		Synced:      false,
	}, nil
}

// SyncCounter forces a sync of the local counter with the distributed store.
func (drl *DistributedRateLimiter) SyncCounter(ctx context.Context, key string) (int64, error) {
	counter := drl.getOrCreateCounter(key, 0, time.Minute)

	counter.mu.Lock()
	localValue := counter.value
	counter.mu.Unlock()

	// Sync to store
	storeValue, err := drl.store.IncrementCounter(ctx, key, counter.window)
	if err != nil {
		return localValue, fmt.Errorf("failed to sync counter %s: %w", key, err)
	}

	// Update local cache
	counter.mu.Lock()
	counter.value = storeValue
	counter.lastSync = time.Now()
	counter.mu.Unlock()

	return storeValue, nil
}

// ResetCounter resets both local and distributed counters.
func (drl *DistributedRateLimiter) ResetCounter(ctx context.Context, key string) error {
	counter := drl.getOrCreateCounter(key, 0, time.Minute)

	counter.mu.Lock()
	counter.value = 0
	counter.windowStart = time.Now()
	counter.mu.Unlock()

	return drl.store.ResetCounter(ctx, key)
}

// GetClusterUsage returns the aggregated usage across all cluster nodes.
func (drl *DistributedRateLimiter) GetClusterUsage(ctx context.Context, key string) (*models.ClusterUsage, error) {
	storeValue, err := drl.store.GetCounter(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster usage: %w", err)
	}

	counter := drl.getOrCreateCounter(key, 0, time.Minute)
	counter.mu.Lock()
	localValue := counter.value
	counter.mu.Unlock()

	return &models.ClusterUsage{
		Key:         key,
		StoreValue:  storeValue,
		LocalValue:  localValue,
		Total:       storeValue + localValue,
		LastSync:    counter.lastSync,
		WindowStart: counter.windowStart,
	}, nil
}

// --- Counter Synchronization ---

// syncLoop periodically syncs local counters with the distributed store.
func (drl *DistributedRateLimiter) syncLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		drl.syncAllCounters(ctx)
		cancel()
	}
}

func (drl *DistributedRateLimiter) syncAllCounters(ctx context.Context) {
	drl.mu.RLock()
	counters := make([]*localCounter, 0, len(drl.localCache))
	for _, c := range drl.localCache {
		counters = append(counters, c)
	}
	drl.mu.RUnlock()

	for _, counter := range counters {
		counter.mu.Lock()
		key := counter.key
		counter.mu.Unlock()

		if _, err := drl.SyncCounter(ctx, key); err != nil {
			drl.logger.Debug("counter sync failed", "key", key, "error", err)
		}
	}
}

// --- Event Publishing ---

// publishEvent queues a counter event for cluster-wide propagation.
func (drl *DistributedRateLimiter) publishEvent(event *models.CounterEvent) {
	select {
	case drl.eventCh <- event:
		// Event queued successfully
	default:
		// Channel full - drop event (counter will be synced eventually)
		drl.logger.Debug("event channel full, dropping event", "key", event.Key)
	}
}

// eventProcessor batches and publishes counter events to VedaDB.
func (drl *DistributedRateLimiter) eventProcessor() {
	ticker := time.NewTicker(drl.flushInterval)
	defer ticker.Stop()

	batch := make([]*models.CounterEvent, 0, drl.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := drl.flushBatch(ctx, batch); err != nil {
			drl.logger.Error("failed to flush counter events", "count", len(batch), "error", err)
		} else {
			drl.logger.Debug("flushed counter events", "count", len(batch))
		}
		cancel()

		batch = batch[:0]
	}

	for {
		select {
		case event := <-drl.eventCh:
			batch = append(batch, event)
			if len(batch) >= drl.batchSize {
				flush()
			}

		case <-ticker.C:
			flush()
		}
	}
}

func (drl *DistributedRateLimiter) flushBatch(ctx context.Context, events []*models.CounterEvent) error {
	if len(events) == 0 {
		return nil
	}

	// Aggregate deltas by key
	aggregated := make(map[string]int64)
	for _, event := range events {
		aggregated[event.Key] += event.Delta
	}

	// Publish aggregated increments to store
	for key, delta := range aggregated {
		event := &models.CounterEvent{
			Key:       key,
			Delta:     delta,
			Timestamp: time.Now(),
		}
		if err := drl.store.PublishCounterEvent(ctx, event); err != nil {
			drl.logger.Error("failed to publish counter event", "key", key, "error", err)
		}
	}

	return nil
}

// --- Helpers ---

func (drl *DistributedRateLimiter) getOrCreateCounter(key string, limit int64, window time.Duration) *localCounter {
	drl.mu.RLock()
	counter, ok := drl.localCache[key]
	drl.mu.RUnlock()

	if ok {
		return counter
	}

	drl.mu.Lock()
	defer drl.mu.Unlock()

	// Double-check
	counter, ok = drl.localCache[key]
	if ok {
		return counter
	}

	now := time.Now()
	counter = &localCounter{
		key:         key,
		limit:       limit,
		window:      window,
		windowStart: now.Truncate(window),
		lastSync:    now,
	}
	drl.localCache[key] = counter
	return counter
}

// --- Cluster Coordination ---

// ClusterSync performs a full synchronization of all local counters with
// the distributed store. This should be called during node startup and
// periodically for consistency.
func (drl *DistributedRateLimiter) ClusterSync(ctx context.Context) error {
	drl.logger.Info("starting cluster sync")

	drl.mu.Lock()
	defer drl.mu.Unlock()

	for key, counter := range drl.localCache {
		counter.mu.Lock()

		storeValue, err := drl.store.GetCounter(ctx, key)
		if err != nil {
			drl.logger.Error("cluster sync failed for counter", "key", key, "error", err)
			counter.mu.Unlock()
			continue
		}

		counter.value = storeValue
		counter.lastSync = time.Now()
		counter.mu.Unlock()
	}

	drl.logger.Info("cluster sync completed", "counters", len(drl.localCache))
	return nil
}

// LocalCacheSize returns the number of counters in the local cache.
func (drl *DistributedRateLimiter) LocalCacheSize() int {
	drl.mu.RLock()
	defer drl.mu.RUnlock()
	return len(drl.localCache)
}

// SetFlushInterval configures the event flush interval.
func (drl *DistributedRateLimiter) SetFlushInterval(d time.Duration) {
	drl.flushInterval = d
}

// SetBatchSize configures the event batch size.
func (drl *DistributedRateLimiter) SetBatchSize(size int) {
	drl.batchSize = size
}
