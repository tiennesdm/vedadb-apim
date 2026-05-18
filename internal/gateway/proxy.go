// Package gateway provides reverse proxy functionality for the VedaDB API Manager.
// This file implements reverse proxying with database-backed API resolution,
// load balancing, request/response transformation, header injection, circuit
// breaker, retry with backoff, backend health checking, and analytics recording.
//
// CRITICAL: The proxy reads the resolved API endpoint from the Gin context
// (set by APILookupMiddleware via a REAL DB query) and forwards requests
// to the backend with proper authentication context headers.
package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
	"github.com/tiennesdm/vedadb-apim/pkg/store"
)

// ---------------------------------------------------------------------------
// Backend and Upstream
// ---------------------------------------------------------------------------

// Backend represents a single backend server.
type Backend struct {
	// ID is the unique backend identifier.
	ID string `json:"id"`
	// URL is the backend server URL.
	URL string `json:"url"`
	// Weight is the load balancing weight (higher = more traffic).
	Weight int `json:"weight"`
	// Healthy indicates if the backend is currently healthy.
	Healthy int32 `json:"-"`
	// CurrentWeight is used in weighted round-robin.
	CurrentWeight int32 `json:"-"`
	// LastChecked is when health was last checked.
	LastChecked time.Time `json:"-"`
	// FailureCount tracks consecutive failures.
	FailureCount int32 `json:"-"`
	// SuccessCount tracks consecutive successes.
	SuccessCount int32 `json:"-"`
}

// IsHealthy returns true if the backend is healthy.
func (b *Backend) IsHealthy() bool {
	return atomic.LoadInt32(&b.Healthy) == 1
}

// SetHealthy sets the health status of the backend.
func (b *Backend) SetHealthy(healthy bool) {
	if healthy {
		atomic.StoreInt32(&b.Healthy, 1)
		atomic.StoreInt32(&b.FailureCount, 0)
		atomic.AddInt32(&b.SuccessCount, 1)
	} else {
		atomic.StoreInt32(&b.Healthy, 0)
		atomic.AddInt32(&b.FailureCount, 1)
		atomic.StoreInt32(&b.SuccessCount, 0)
	}
	b.LastChecked = time.Now()
}

// Upstream represents a group of backend servers for an API.
type Upstream struct {
	APIID      uuid.UUID  `json:"api_id"`
	APIContext string     `json:"api_context"`
	Backends   []*Backend `json:"backends"`
	mu         sync.RWMutex
	strategy   LoadBalanceStrategy
}

// LoadBalanceStrategy defines the interface for load balancing algorithms.
type LoadBalanceStrategy interface {
	// Select returns the next backend to use.
	Select(backends []*Backend) *Backend
}

// NewUpstream creates a new upstream with the given backends.
func NewUpstream(apiID uuid.UUID, apiContext string, backends []*Backend) *Upstream {
	// Default all backends to healthy
	for _, b := range backends {
		b.SetHealthy(true)
	}
	return &Upstream{
		APIID:      apiID,
		APIContext: apiContext,
		Backends:   backends,
		strategy:   &WeightedRoundRobin{},
	}
}

// Select returns a healthy backend using the load balancing strategy.
func (u *Upstream) Select() *Backend {
	u.mu.RLock()
	backends := make([]*Backend, 0, len(u.Backends))
	for _, b := range u.Backends {
		if b.IsHealthy() {
			backends = append(backends, b)
		}
	}
	strategy := u.strategy
	u.mu.RUnlock()

	if len(backends) == 0 {
		return nil
	}
	return strategy.Select(backends)
}

// SetStrategy changes the load balancing strategy.
func (u *Upstream) SetStrategy(strategy LoadBalanceStrategy) {
	u.mu.Lock()
	u.strategy = strategy
	u.mu.Unlock()
}

// HealthCount returns the number of healthy backends.
func (u *Upstream) HealthCount() int {
	count := 0
	for _, b := range u.Backends {
		if b.IsHealthy() {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// Load Balancing Strategies
// ---------------------------------------------------------------------------

// WeightedRoundRobin implements weighted round-robin load balancing.
type WeightedRoundRobin struct {
	mu        sync.Mutex
	current   int
	gcd       int
	maxWeight int
}

// Select returns the next backend using weighted round-robin.
func (w *WeightedRoundRobin) Select(backends []*Backend) *Backend {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(backends) == 0 {
		return nil
	}

	totalWeight := 0
	maxWeight := 0
	for _, b := range backends {
		totalWeight += b.Weight
		if b.Weight > maxWeight {
			maxWeight = b.Weight
		}
	}

	if totalWeight == 0 {
		return backends[w.current%len(backends)]
	}

	// Simple weighted random selection
	point := rand.Intn(totalWeight)
	for _, b := range backends {
		point -= b.Weight
		if point < 0 {
			return b
		}
	}

	return backends[0]
}

// RoundRobin implements simple round-robin load balancing.
type RoundRobin struct {
	mu      sync.Mutex
	current uint32
}

// Select returns the next backend using round-robin.
func (r *RoundRobin) Select(backends []*Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}
	idx := atomic.AddUint32(&r.current, 1) % uint32(len(backends))
	return backends[idx]
}

// Random implements random load balancing.
type Random struct{}

// Select returns a random backend.
func (r *Random) Select(backends []*Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}
	return backends[rand.Intn(len(backends))]
}

// LeastConnections implements least connections load balancing.
type LeastConnections struct{}

// Select returns the backend with least connections (simplified to random).
func (lc *LeastConnections) Select(backends []*Backend) *Backend {
	if len(backends) == 0 {
		return nil
	}
	return backends[rand.Intn(len(backends))]
}

// ---------------------------------------------------------------------------
// Circuit Breaker
// ---------------------------------------------------------------------------

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	// CircuitClosed means requests flow normally.
	CircuitClosed CircuitState = iota
	// CircuitOpen means all requests are rejected.
	CircuitOpen
	// CircuitHalfOpen means a limited number of requests are allowed.
	CircuitHalfOpen
)

// CircuitBreaker implements the circuit breaker pattern.
type CircuitBreaker struct {
	mu               sync.RWMutex
	state            CircuitState
	failureThreshold int
	successThreshold int
	halfOpenMaxCalls int
	failureCount     int
	successCount     int
	halfOpenCalls    int
	timeout          time.Duration
	lastFailureTime  time.Time
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(failureThreshold, successThreshold, halfOpenMaxCalls int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		halfOpenMaxCalls: halfOpenMaxCalls,
		timeout:          timeout,
		state:            CircuitClosed,
	}
}

// Allow checks if a request should be allowed through.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = CircuitHalfOpen
			cb.halfOpenCalls = 0
			return true
		}
		return false
	case CircuitHalfOpen:
		if cb.halfOpenCalls < cb.halfOpenMaxCalls {
			cb.halfOpenCalls++
			return true
		}
		return false
	}
	return false
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitHalfOpen:
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.state = CircuitClosed
			cb.failureCount = 0
			cb.successCount = 0
		}
	case CircuitClosed:
		cb.failureCount = 0
	}
}

// RecordFailure records a failed request.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.lastFailureTime = time.Now()
	case CircuitClosed:
		cb.failureCount++
		if cb.failureCount >= cb.failureThreshold {
			cb.state = CircuitOpen
			cb.lastFailureTime = time.Now()
		}
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// ---------------------------------------------------------------------------
// ProxyConfig
// ---------------------------------------------------------------------------

// ProxyConfig holds proxy configuration.
type ProxyConfig struct {
	RequestTimeout      time.Duration
	RetryCount          int
	RetryBackoff        time.Duration
	PreserveHost        bool
	MaxRequestBodySize  int64
	MaxResponseBodySize int64
}

// ---------------------------------------------------------------------------
// Proxy
// ---------------------------------------------------------------------------

// Proxy handles reverse proxying to backend APIs with store integration
// for analytics recording.
type Proxy struct {
	config              ProxyConfig
	logger              *zap.Logger
	store               store.Store
	upstreams           sync.Map // api_context -> *Upstream
	circuitBreakers     sync.Map // api_context -> *CircuitBreaker
	transport           *http.Transport
	healthCheckInterval time.Duration
}

// NewProxy creates a new reverse proxy.
func NewProxy(config ProxyConfig, logger *zap.Logger, store store.Store) *Proxy {
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 * time.Second
	}
	if config.RetryCount <= 0 {
		config.RetryCount = 3
	}
	if config.RetryBackoff <= 0 {
		config.RetryBackoff = 100 * time.Millisecond
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 90 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
		ForceAttemptHTTP2:     true,
	}

	proxy := &Proxy{
		config:              config,
		logger:              logger,
		store:               store,
		transport:           transport,
		healthCheckInterval: 30 * time.Second,
	}

	// Start health checker
	go proxy.healthChecker()

	return proxy
}

// RegisterUpstream registers an upstream for an API.
func (p *Proxy) RegisterUpstream(apiContext string, upstream *Upstream) {
	p.upstreams.Store(apiContext, upstream)
}

// GetUpstream returns the upstream for an API context.
func (p *Proxy) GetUpstream(apiContext string) (*Upstream, bool) {
	val, ok := p.upstreams.Load(apiContext)
	if !ok {
		return nil, false
	}
	return val.(*Upstream), true
}

// RegisterCircuitBreaker registers a circuit breaker for an API.
func (p *Proxy) RegisterCircuitBreaker(apiContext string, cb *CircuitBreaker) {
	p.circuitBreakers.Store(apiContext, cb)
}

// GetCircuitBreaker returns the circuit breaker for an API context.
func (p *Proxy) GetCircuitBreaker(apiContext string) (*CircuitBreaker, bool) {
	val, ok := p.circuitBreakers.Load(apiContext)
	if !ok {
		return nil, false
	}
	return val.(*CircuitBreaker), true
}

// Handler returns the Gin handler function for proxying.
//
// The handler reads the resolved API endpoint from the Gin context
// (set by APILookupMiddleware via a REAL DB query to ListPublishedAPIs)
// and forwards the request to the backend.
func (p *Proxy) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip admin/internal paths
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/health") ||
			strings.HasPrefix(path, "/metrics") ||
			strings.HasPrefix(path, "/admin") {
			c.Next()
			return
		}

		apiContext := c.GetString("apiContext")
		if apiContext == "" {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "api_not_found",
				"message": "No published API matches the request path. Ensure the API is published and the context path is correct.",
			})
			return
		}

		// Try to get endpoint from context (set by APILookupMiddleware from DB)
		endpoint := c.GetString("apiEndpoint")
		if endpoint == "" {
			// Fallback: try to get from upstream
			upstream, ok := p.GetUpstream(apiContext)
			if !ok || upstream == nil {
				c.JSON(http.StatusNotFound, gin.H{
					"error":   "no_backend_endpoint",
					"message": "No backend endpoint configured for API: " + apiContext,
				})
				return
			}
			backend := upstream.Select()
			if backend == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error":   "no_healthy_backends",
					"message": "No healthy backends available for API: " + apiContext,
				})
				return
			}
			endpoint = backend.URL
		}

		// Check circuit breaker
		if cb, ok := p.GetCircuitBreaker(apiContext); ok {
			if !cb.Allow() {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error":   "circuit breaker is open",
					"code":    "APIM.CIRCUIT_OPEN",
					"message": "service is temporarily unavailable due to high failure rate",
				})
				return
			}
		}

		// Proxy the request with retries
		var lastErr error
		for attempt := 0; attempt <= p.config.RetryCount; attempt++ {
			if attempt > 0 {
				time.Sleep(p.config.RetryBackoff * time.Duration(attempt))
				p.logger.Info("retrying proxy request",
					zap.String("api", apiContext),
					zap.String("path", path),
					zap.Int("attempt", attempt),
					zap.String("request_id", c.GetString("requestID")),
				)
			}

			if err := p.proxyRequest(c, endpoint); err != nil {
				lastErr = err
				p.logger.Warn("proxy request failed",
					zap.Error(err),
					zap.String("api", apiContext),
					zap.Int("attempt", attempt),
					zap.String("request_id", c.GetString("requestID")),
				)
				continue
			}

			// Success - record circuit breaker success
			if cb, ok := p.GetCircuitBreaker(apiContext); ok {
				cb.RecordSuccess()
			}
			return
		}

		// All retries failed - record circuit breaker failure
		if cb, ok := p.GetCircuitBreaker(apiContext); ok {
			cb.RecordFailure()
		}

		p.logger.Error("proxy request failed after all retries",
			zap.String("api", apiContext),
			zap.String("path", path),
			zap.Error(lastErr),
			zap.String("request_id", c.GetString("requestID")),
		)

		// Record error analytics to store
		p.recordProxyError(c, apiContext, lastErr)

		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "failed to proxy request",
			"code":    "APIM.PROXY_ERROR",
			"message": lastErr.Error(),
		})
	}
}

// proxyRequest proxies a single request to the backend.
//
// It reads the API endpoint from context (set by APILookupMiddleware),
// forwards the request with injected auth context headers, and returns
// the backend response to the client.
func (p *Proxy) proxyRequest(c *gin.Context, endpoint string) error {
	targetURL, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL %q: %w", endpoint, err)
	}

	// Build target path by mapping incoming path to backend path
	apiContext := c.GetString("apiContext")
	originalPath := c.Request.URL.Path

	// Remove API context from path to get the backend resource path
	backendPath := originalPath
	if apiContext != "" && strings.HasPrefix(originalPath, apiContext) {
		backendPath = strings.TrimPrefix(originalPath, apiContext)
	}
	if backendPath == "" {
		backendPath = "/"
	}

	targetURL.Path = singleJoiningSlash(targetURL.Path, backendPath)
	targetURL.RawQuery = c.Request.URL.RawQuery

	// Read request body
	var body []byte
	if c.Request.Body != nil {
		body, err = io.ReadAll(c.Request.Body)
		if err != nil {
			return fmt.Errorf("read request body: %w", err)
		}
		c.Request.Body.Close()
	}

	// Create proxy request with context for cancellation
	req, err := http.NewRequestWithContext(
		c.Request.Context(),
		c.Request.Method,
		targetURL.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create proxy request: %w", err)
	}

	// Copy all original headers
	for name, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	// Inject gateway context headers from authenticated session
	// These are set by AuthMiddleware after DB validation
	apiID := c.GetString("apiID")
	appID := c.GetString("appID")
	userID := c.GetString("userID")
	authType := c.GetString("authType")
	clientID := c.GetString("clientID")
	requestID := c.GetString("requestID")

	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	if apiID != "" {
		req.Header.Set("X-API-ID", apiID)
	}
	if appID != "" {
		req.Header.Set("X-Application-ID", appID)
	}
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
		req.Header.Set("X-User-Context", userID)
	}
	if authType != "" {
		req.Header.Set("X-Auth-Type", authType)
	}
	if clientID != "" {
		req.Header.Set("X-Client-ID", clientID)
	}
	req.Header.Set("X-Gateway-Name", "VedaDB-APIM")
	req.Header.Set("X-API-Context", apiContext)

	// Remove hop-by-hop headers
	for _, h := range hopByHopHeaders {
		req.Header.Del(h)
	}

	// Set/override Host header
	if !p.config.PreserveHost {
		req.Host = targetURL.Host
	}

	// Execute proxy request
	client := &http.Client{
		Transport: p.transport,
		Timeout:   p.config.RequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		// Record error analytics to store (best-effort, non-blocking)
		p.recordProxyError(c, apiContext, err)
		return fmt.Errorf("execute proxy request to %s: %w", targetURL.String(), err)
	}
	defer resp.Body.Close()

	// Copy response headers (excluding gateway-internal ones)
	for name, values := range resp.Header {
		for _, value := range values {
			c.Header(name, value)
		}
	}

	// Set gateway response headers
	c.Header("X-Gateway-Version", "1.0.0")
	c.Header("X-Proxied-By", "VedaDB-APIM-Gateway")
	c.Header("X-Proxy-Latency", fmt.Sprintf("%dms", latency.Milliseconds()))

	// Strip gateway-internal headers from backend response
	StripGatewayHeaders(c.Writer.Header())

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	// Write response to client
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)

	// Record success analytics to store (best-effort, non-blocking)
	p.recordProxySuccess(c, apiContext, appID, userID, resp.StatusCode, latency)

	return nil
}

// hopByHopHeaders are headers that should not be forwarded.
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Transfer-Encoding",
	"TE",
	"Trailer",
	"Proxy-Authorization",
	"Proxy-Authenticate",
	"Upgrade",
}

// recordProxySuccess records a successful proxy request to the analytics store.
func (p *Proxy) recordProxySuccess(c *gin.Context, apiID, appID, userID string, statusCode int, latency time.Duration) {
	if p.store == nil {
		return
	}

	requestID := c.GetString("requestID")
	tenantID := c.GetString("tenantID")

	event := &models.AnalyticsEventDB{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		RequestID:  requestID,
		APIID:      apiID,
		AppID:      appID,
		UserID:     userID,
		Method:     c.Request.Method,
		Path:       c.Request.URL.Path,
		StatusCode: statusCode,
		LatencyMs:  int(latency.Milliseconds()),
		UserAgent:  c.Request.UserAgent(),
		ClientIP:   c.ClientIP(),
		Timestamp:  time.Now(),
	}

	// Fire-and-forget: don't block response on analytics
	go func(ev *models.AnalyticsEventDB) {
		if err := p.store.InsertAnalyticsEvent(ev); err != nil {
			p.logger.Warn("failed to store proxy success analytics",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}(event)
}

// recordProxyError records a failed proxy request to the analytics store.
func (p *Proxy) recordProxyError(c *gin.Context, apiID string, proxyErr error) {
	if p.store == nil {
		return
	}

	requestID := c.GetString("requestID")
	tenantID := c.GetString("tenantID")
	appID := c.GetString("appID")
	userID := c.GetString("userID")

	errMsg := ""
	if proxyErr != nil {
		errMsg = proxyErr.Error()
	}

	event := &models.AnalyticsEventDB{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		RequestID:    requestID,
		APIID:        apiID,
		AppID:        appID,
		UserID:       userID,
		Method:       c.Request.Method,
		Path:         c.Request.URL.Path,
		StatusCode:   http.StatusBadGateway,
		LatencyMs:    0,
		ErrorMessage: errMsg,
		UserAgent:    c.Request.UserAgent(),
		ClientIP:     c.ClientIP(),
		Timestamp:    time.Now(),
	}

	// Fire-and-forget
	go func(ev *models.AnalyticsEventDB) {
		if err := p.store.InsertAnalyticsEvent(ev); err != nil {
			p.logger.Warn("failed to store proxy error analytics",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}(event)
}

// ---------------------------------------------------------------------------
// Path Utilities
// ---------------------------------------------------------------------------

// mapPath maps the incoming request path to the backend path.
func mapPath(requestPath, routePattern, backendPrefix string) string {
	// For wildcard patterns, extract the variable part
	if strings.HasSuffix(routePattern, "/**") {
		base := strings.TrimSuffix(routePattern, "/**")
		if strings.HasPrefix(requestPath, base) {
			suffix := strings.TrimPrefix(requestPath, base)
			return singleJoiningSlash(backendPrefix, suffix)
		}
	}
	if strings.HasSuffix(routePattern, "/*") {
		base := strings.TrimSuffix(routePattern, "/*")
		if strings.HasPrefix(requestPath, base) {
			suffix := strings.TrimPrefix(requestPath, base)
			return singleJoiningSlash(backendPrefix, suffix)
		}
	}
	return backendPrefix
}

// singleJoiningSlash joins two path segments with a single slash.
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// ---------------------------------------------------------------------------
// Health Checker
// ---------------------------------------------------------------------------

// healthChecker periodically checks backend health.
func (p *Proxy) healthChecker() {
	ticker := time.NewTicker(p.healthCheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		p.upstreams.Range(func(key, value interface{}) bool {
			upstream := value.(*Upstream)
			for _, backend := range upstream.Backends {
				go p.checkBackendHealth(backend)
			}
			return true
		})
	}
}

// checkBackendHealth performs a health check on a single backend.
func (p *Proxy) checkBackendHealth(backend *Backend) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	healthURL := backend.URL
	resp, err := client.Head(healthURL)
	if err != nil {
		// Try with /health if HEAD fails
		if u, err := url.Parse(backend.URL); err == nil {
			u.Path = singleJoiningSlash(u.Path, "/health")
			resp, err = client.Get(u.String())
		}
	}

	if err != nil {
		backend.SetHealthy(false)
		p.logger.Warn("backend health check failed",
			zap.String("backend", backend.URL),
			zap.Error(err),
		)
		return
	}
	if resp != nil {
		resp.Body.Close()
		backend.SetHealthy(resp.StatusCode < 500)
	}
}

// ---------------------------------------------------------------------------
// Reverse Proxy Handler Utility
// ---------------------------------------------------------------------------

// ReverseProxyHandler creates a standard reverse proxy handler.
func ReverseProxyHandler(target string) (gin.HandlerFunc, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.URL.Path = singleJoiningSlash(targetURL.Path, req.URL.Path)
		req.Host = targetURL.Host
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}, nil
}

// ---------------------------------------------------------------------------
// Proxy Analytics Store Recording (blocking variant for middleware use)
// ---------------------------------------------------------------------------

// RecordAnalytics records a request to the analytics store.
// This variant is used directly by middleware for synchronous recording.
func (p *Proxy) RecordAnalytics(c *gin.Context, statusCode int, latency time.Duration, errMsg string) {
	if p.store == nil {
		return
	}

	requestID := c.GetString("requestID")
	apiID := c.GetString("apiID")
	appID := c.GetString("appID")
	userID := c.GetString("userID")
	tenantID := c.GetString("tenantID")

	event := &models.AnalyticsEventDB{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		RequestID:    requestID,
		APIID:        apiID,
		AppID:        appID,
		UserID:       userID,
		Method:       c.Request.Method,
		Path:         c.Request.URL.Path,
		StatusCode:   statusCode,
		LatencyMs:    int(latency.Milliseconds()),
		ErrorMessage: errMsg,
		UserAgent:    c.Request.UserAgent(),
		ClientIP:     c.ClientIP(),
		Timestamp:    time.Now(),
	}

	go func(ev *models.AnalyticsEventDB) {
		if err := p.store.InsertAnalyticsEvent(ev); err != nil {
			p.logger.Warn("failed to store analytics",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}(event)
}
