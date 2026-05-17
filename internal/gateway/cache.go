// Package gateway provides response caching functionality for the VedaDB API Manager.
// This file implements an in-memory response cache with TTL, cache key generation,
// invalidation support, and cache statistics.
package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// CachedResponse represents a cached HTTP response.
type CachedResponse struct {
	// StatusCode is the HTTP status code.
	StatusCode int `json:"status_code"`
	// Headers are the response headers.
	Headers map[string]string `json:"headers"`
	// Body is the response body.
	Body []byte `json:"body"`
	// CreatedAt is when the entry was cached.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is when the entry expires.
	ExpiresAt time.Time `json:"expires_at"`
	// CacheKey is the key used to store this entry.
	CacheKey string `json:"cache_key"`
	// APIContext is the API context path.
	APIContext string `json:"api_context"`
	// ResourcePath is the API resource path.
	ResourcePath string `json:"resource_path"`
	// HitCount is the number of cache hits.
	HitCount int64 `json:"hit_count"`
}

// IsExpired returns true if the cache entry has expired.
func (cr *CachedResponse) IsExpired() bool {
	return time.Now().After(cr.ExpiresAt)
}

// CacheStore defines the interface for cache storage backends.
type CacheStore interface {
	// Get retrieves a cached response by key.
	Get(key string) (*CachedResponse, bool)
	// Set stores a response in the cache.
	Set(key string, resp *CachedResponse) error
	// Delete removes a response from the cache.
	Delete(key string) error
	// DeleteByPrefix removes all entries with the given prefix.
	DeleteByPrefix(prefix string) error
	// DeleteByAPI removes all entries for a specific API.
	DeleteByAPI(apiContext string) error
	// Clear removes all entries.
	Clear() error
	// Stats returns cache statistics.
	Stats() CacheStats
}

// CacheStats contains cache performance statistics.
type CacheStats struct {
	// TotalEntries is the current number of entries in the cache.
	TotalEntries int64 `json:"total_entries"`
	// TotalHits is the total number of cache hits.
	TotalHits int64 `json:"total_hits"`
	// TotalMisses is the total number of cache misses.
	TotalMisses int64 `json:"total_misses"`
	// TotalEvictions is the total number of evicted entries.
	TotalEvictions int64 `json:"total_evictions"`
	// CurrentSize is the current memory usage in bytes.
	CurrentSize int64 `json:"current_size_bytes"`
	// MaxSize is the maximum cache size in bytes.
	MaxSize int64 `json:"max_size_bytes"`
	// HitRate is the cache hit rate as a percentage.
	HitRate float64 `json:"hit_rate"`
	// AvgEntrySize is the average entry size in bytes.
	AvgEntrySize int64 `json:"avg_entry_size_bytes"`
}

// ---------------------------------------------------------------------------
// In-Memory Cache Implementation
// ---------------------------------------------------------------------------

// InMemoryCache implements CacheStore using an in-memory map with LRU eviction.
type InMemoryCache struct {
	mu          sync.RWMutex
	entries     map[string]*cacheEntry
	order       *lruList
	maxEntries  int
	maxSize     int64
	currentSize int64

	stats struct {
		totalHits      int64
		totalMisses    int64
		totalEvictions int64
	}
}

// cacheEntry wraps a CachedResponse with LRU list pointers.
type cacheEntry struct {
	key      string
	response *CachedResponse
	size     int64
	elem     *listElem
}

// lruList is a doubly-linked list for LRU tracking.
type lruList struct {
	head *listElem
	tail *listElem
	size int
}

type listElem struct {
	key  string
	next *listElem
	prev *listElem
}

// NewInMemoryCache creates a new in-memory cache.
func NewInMemoryCache(maxEntries int, maxSize int64) *InMemoryCache {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	if maxSize <= 0 {
		maxSize = 100 * 1024 * 1024 // 100 MB
	}
	cache := &InMemoryCache{
		entries:    make(map[string]*cacheEntry),
		order:      &lruList{},
		maxEntries: maxEntries,
		maxSize:    maxSize,
	}
	// Start background cleanup
	go cache.cleanup()
	return cache
}

// Get retrieves a cached response by key.
func (c *InMemoryCache) Get(key string) (*CachedResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		c.stats.totalMisses++
		return nil, false
	}

	if entry.response.IsExpired() {
		c.removeEntry(entry)
		c.stats.totalMisses++
		return nil, false
	}

	// Move to front (most recently used)
	c.order.moveToFront(entry.elem)
	entry.response.HitCount++
	c.stats.totalHits++
	return entry.response, true
}

// Set stores a response in the cache.
func (c *InMemoryCache) Set(key string, resp *CachedResponse) error {
	if resp == nil {
		return fmt.Errorf("cannot cache nil response")
	}

	respSize := int64(len(resp.Body) + 1024) // Approximate overhead

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if entry already exists
	if existing, ok := c.entries[key]; ok {
		c.currentSize -= existing.size
		c.removeElem(existing.elem)
		delete(c.entries, key)
	}

	// Evict entries if needed
	for (len(c.entries) >= c.maxEntries || c.currentSize+respSize > c.maxSize) && c.order.size > 0 {
		c.evictLRU()
	}

	// Create new entry
	elem := c.order.pushFront(key)
	entry := &cacheEntry{
		key:      key,
		response: resp,
		size:     respSize,
		elem:     elem,
	}
	c.entries[key] = entry
	c.currentSize += respSize

	return nil
}

// Delete removes a response from the cache.
func (c *InMemoryCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[key]; ok {
		c.removeEntry(entry)
	}
	return nil
}

// DeleteByPrefix removes all entries with the given prefix.
func (c *InMemoryCache) DeleteByPrefix(prefix string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		if strings.HasPrefix(key, prefix) {
			c.removeEntry(entry)
		}
	}
	return nil
}

// DeleteByAPI removes all entries for a specific API.
func (c *InMemoryCache) DeleteByAPI(apiContext string) error {
	prefix := fmt.Sprintf("cache:%s:", apiContext)
	return c.DeleteByPrefix(prefix)
}

// Clear removes all entries.
func (c *InMemoryCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
	c.order = &lruList{}
	c.currentSize = 0
	return nil
}

// Stats returns cache statistics.
func (c *InMemoryCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.stats.totalHits + c.stats.totalMisses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(c.stats.totalHits) / float64(total) * 100
	}

	avgSize := int64(0)
	if len(c.entries) > 0 {
		avgSize = c.currentSize / int64(len(c.entries))
	}

	return CacheStats{
		TotalEntries:   int64(len(c.entries)),
		TotalHits:      c.stats.totalHits,
		TotalMisses:    c.stats.totalMisses,
		TotalEvictions: c.stats.totalEvictions,
		CurrentSize:    c.currentSize,
		MaxSize:        c.maxSize,
		HitRate:        hitRate,
		AvgEntrySize:   avgSize,
	}
}

// evictLRU removes the least recently used entry.
func (c *InMemoryCache) evictLRU() {
	if c.order.tail == nil {
		return
	}
	key := c.order.tail.key
	if entry, ok := c.entries[key]; ok {
		c.removeEntry(entry)
	}
	c.stats.totalEvictions++
}

// removeEntry removes a specific entry from the cache.
func (c *InMemoryCache) removeEntry(entry *cacheEntry) {
	c.currentSize -= entry.size
	c.removeElem(entry.elem)
	delete(c.entries, entry.key)
}

// removeElem removes a list element.
func (c *InMemoryCache) removeElem(elem *listElem) {
	c.order.remove(elem)
}

// cleanup periodically removes expired entries.
func (c *InMemoryCache) cleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.entries {
			if now.After(entry.response.ExpiresAt) {
				c.currentSize -= entry.size
				c.removeElem(entry.elem)
				delete(c.entries, key)
				c.stats.totalEvictions++
			}
		}
		c.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// LRU List Operations
// ---------------------------------------------------------------------------

func (l *lruList) pushFront(key string) *listElem {
	elem := &listElem{key: key}
	if l.head == nil {
		l.head = elem
		l.tail = elem
	} else {
		elem.next = l.head
		l.head.prev = elem
		l.head = elem
	}
	l.size++
	return elem
}

func (l *lruList) moveToFront(elem *listElem) {
	if elem == nil || elem == l.head {
		return
	}
	l.remove(elem)
	if l.head == nil {
		l.head = elem
		l.tail = elem
		elem.next = nil
		elem.prev = nil
		l.size = 1
		return
	}
	elem.next = l.head
	l.head.prev = elem
	elem.prev = nil
	l.head = elem
	l.size++
}

func (l *lruList) remove(elem *listElem) {
	if elem == nil {
		return
	}
	if elem.prev != nil {
		elem.prev.next = elem.next
	} else {
		l.head = elem.next
	}
	if elem.next != nil {
		elem.next.prev = elem.prev
	} else {
		l.tail = elem.prev
	}
	l.size--
}

// ---------------------------------------------------------------------------
// Cache Key Generation
// ---------------------------------------------------------------------------

// GenerateCacheKey creates a cache key from the request context.
func GenerateCacheKey(apiContext, method, path string, queryParams map[string]string, varyHeaders map[string]string) string {
	var sb strings.Builder
	sb.WriteString("cache:")
	sb.WriteString(apiContext)
	sb.WriteString(":")
	sb.WriteString(method)
	sb.WriteString(":")
	sb.WriteString(path)

	// Add sorted query parameters
	if len(queryParams) > 0 {
		keys := make([]string, 0, len(queryParams))
		for k := range queryParams {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteString("?")
		for i, k := range keys {
			if i > 0 {
				sb.WriteString("&")
			}
			sb.WriteString(k)
			sb.WriteString("=")
			sb.WriteString(queryParams[k])
		}
	}

	// Add vary headers
	if len(varyHeaders) > 0 {
		keys := make([]string, 0, len(varyHeaders))
		for k := range varyHeaders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteString("|h=")
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(k)
			sb.WriteString(":")
			sb.WriteString(varyHeaders[k])
		}
	}

	// Hash the key for consistent length
	h := sha256.New()
	h.Write([]byte(sb.String()))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// ---------------------------------------------------------------------------
// Cache Middleware
// ---------------------------------------------------------------------------

// CacheMiddlewareConfig holds configuration for cache middleware.
type CacheMiddlewareConfig struct {
	Enabled              bool
	DefaultTTL           time.Duration
	MaxEntrySize         int64
	CacheBypassHeader    string
	CacheBypassValue     string
	CacheableMethods     []string
	CacheableStatusCodes []int
	VaryByHeaders        []string
	StaleWhileRevalidate time.Duration
}

// CacheMiddleware is the Gin middleware for response caching.
type CacheMiddleware struct {
	store  CacheStore
	config CacheMiddlewareConfig
}

// NewCacheMiddleware creates a new cache middleware.
func NewCacheMiddleware(store CacheStore, config CacheMiddlewareConfig) *CacheMiddleware {
	if config.DefaultTTL <= 0 {
		config.DefaultTTL = 5 * time.Minute
	}
	if config.MaxEntrySize <= 0 {
		config.MaxEntrySize = 1 << 20 // 1 MB
	}
	if config.CacheBypassHeader == "" {
		config.CacheBypassHeader = "X-Cache-Bypass"
	}
	if config.CacheBypassValue == "" {
		config.CacheBypassValue = "true"
	}
	if len(config.CacheableMethods) == 0 {
		config.CacheableMethods = []string{"GET", "HEAD"}
	}
	if len(config.CacheableStatusCodes) == 0 {
		config.CacheableStatusCodes = []int{200, 201, 204, 301, 302}
	}
	return &CacheMiddleware{
		store:  store,
		config: config,
	}
}

// Middleware returns the Gin handler function for response caching.
func (m *CacheMiddleware) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Enabled {
			c.Next()
			return
		}

		// Check cache bypass header
		if c.GetHeader(m.config.CacheBypassHeader) == m.config.CacheBypassValue {
			c.Header("X-Cache", "BYPASS")
			c.Next()
			return
		}

		// Only cache specific HTTP methods
		if !m.isCacheableMethod(c.Request.Method) {
			c.Next()
			return
		}

		// Generate cache key
		apiContext := c.GetString("api_context")
		if apiContext == "" {
			apiContext = "default"
		}

		varyHeaders := m.extractVaryHeaders(c)
		queryParams := make(map[string]string)
		for k, v := range c.Request.URL.Query() {
			if len(v) > 0 {
				queryParams[k] = v[0]
			}
		}

		cacheKey := GenerateCacheKey(apiContext, c.Request.Method, c.Request.URL.Path, queryParams, varyHeaders)

		// Try to get from cache
		if cached, ok := m.store.Get(cacheKey); ok {
			c.Header("X-Cache", "HIT")
			c.Header("X-Cache-Hits", fmt.Sprintf("%d", cached.HitCount))
			for k, v := range cached.Headers {
				c.Header(k, v)
			}
			c.Data(cached.StatusCode, cached.Headers["Content-Type"], cached.Body)
			c.Abort()
			return
		}

		// Not in cache, proceed with request
		c.Header("X-Cache", "MISS")

		// Capture response
		w := &responseRecorder{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
			headers:        make(http.Header),
			statusCode:     0,
		}
		c.Writer = w
		c.Next()

		// Only cache successful responses
		if !m.isCacheableStatus(w.statusCode) {
			return
		}

		// Check body size
		if int64(w.body.Len()) > m.config.MaxEntrySize {
			c.Header("X-Cache", "SKIP (too large)")
			return
		}

		// Store in cache
		headers := make(map[string]string)
		for k, v := range w.headers {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}

		cached := &CachedResponse{
			StatusCode:   w.statusCode,
			Headers:      headers,
			Body:         w.body.Bytes(),
			CreatedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(m.config.DefaultTTL),
			CacheKey:     cacheKey,
			APIContext:   apiContext,
			ResourcePath: c.Request.URL.Path,
		}

		if err := m.store.Set(cacheKey, cached); err != nil {
			// Log error but don't fail the request
			c.Header("X-Cache-Error", err.Error())
		}
	}
}

// isCacheableMethod checks if an HTTP method should be cached.
func (m *CacheMiddleware) isCacheableMethod(method string) bool {
	for _, mth := range m.config.CacheableMethods {
		if mth == method {
			return true
		}
	}
	return false
}

// isCacheableStatus checks if an HTTP status code should be cached.
func (m *CacheMiddleware) isCacheableStatus(code int) bool {
	for _, sc := range m.config.CacheableStatusCodes {
		if sc == code {
			return true
		}
	}
	return false
}

// extractVaryHeaders extracts headers that should vary the cache key.
func (m *CacheMiddleware) extractVaryHeaders(c *gin.Context) map[string]string {
	result := make(map[string]string)
	for _, h := range m.config.VaryByHeaders {
		if v := c.GetHeader(h); v != "" {
			result[h] = v
		}
	}
	return result
}

// InvalidateCache invalidates cache entries for an API context.
func (m *CacheMiddleware) InvalidateCache(apiContext string) error {
	return m.store.DeleteByAPI(apiContext)
}

// responseRecorder captures the response for caching.
type responseRecorder struct {
	gin.ResponseWriter
	body       *bytes.Buffer
	headers    http.Header
	statusCode int
	written    bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if !r.written {
		r.statusCode = code
		r.written = true
		r.headers = r.ResponseWriter.Header().Clone()
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	r.body.Write(data)
	return r.ResponseWriter.Write(data)
}

func (r *responseRecorder) Header() http.Header {
	return r.ResponseWriter.Header()
}

// GetCacheStore returns the underlying cache store.
func (m *CacheMiddleware) GetCacheStore() CacheStore {
	return m.store
}
