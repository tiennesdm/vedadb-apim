// Package gateway provides reverse proxy functionality for the VedaDB API Manager.
// This file implements reverse proxying with load balancing, request/response
// transformation, header injection, circuit breaker, retry with backoff, and
// backend health checking.
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
	"github.com/tiennesdm/vedadb-apim/pkg/models"
	"go.uber.org/zap"
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
	APIID     uuid.UUID   `json:"api_id"`
	APIContext string     `json:"api_context"`
	Backends  []*Backend  `json:"backends"`
	mu        sync.RWMutex
	strategy  LoadBalanceStrategy
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
	mu       sync.Mutex
	current  int
	gcd      int
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
// Reverse Proxy
// ---------------------------------------------------------------------------

// ProxyConfig holds proxy configuration.
type ProxyConfig struct {
	RequestTimeout    time.Duration
	RetryCount        int
	RetryBackoff      time.Duration
	PreserveHost      bool
	MaxRequestBodySize int64
	MaxResponseBodySize int64
}

// Proxy handles reverse proxying to backend APIs.
type Proxy struct {
	config            ProxyConfig
	logger            *zap.Logger
	upstreams         sync.Map // api_context -> *Upstream
	circuitBreakers   sync.Map // api_context -> *CircuitBreaker
	transport         *http.Transport
	healthCheckInterval time.Duration
}

// NewProxy creates a new reverse proxy.
func NewProxy(config ProxyConfig, logger *zap.Logger) *Proxy {
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
func (p *Proxy) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiContext := c.GetString("api_context")
		if apiContext == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "no API context found",
			})
			return
		}

		endpoint := c.GetString("backend_endpoint")
		if endpoint == "" {
			// Try to get from upstream
			upstream, ok := p.GetUpstream(apiContext)
			if !ok || upstream == nil {
				c.JSON(http.StatusNotFound, gin.H{
					"error": "no backend endpoint configured for API: " + apiContext,
				})
				return
			}
			backend := upstream.Select()
			if backend == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": "no healthy backends available for API: " + apiContext,
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
				p.logger.Info("retrying request",
					zap.String("api", apiContext),
					zap.String("path", c.Request.URL.Path),
					zap.Int("attempt", attempt),
				)
			}

			if err := p.proxyRequest(c, endpoint); err != nil {
				lastErr = err
				continue
			}

			// Success
			if cb, ok := p.GetCircuitBreaker(apiContext); ok {
				cb.RecordSuccess()
			}
			return
		}

		// All retries failed
		if cb, ok := p.GetCircuitBreaker(apiContext); ok {
			cb.RecordFailure()
		}

		p.logger.Error("proxy request failed after retries",
			zap.String("api", apiContext),
			zap.String("path", c.Request.URL.Path),
			zap.Error(lastErr),
		)

		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "failed to proxy request",
			"code":    "APIM.PROXY_ERROR",
			"message": lastErr.Error(),
		})
	}
}

// proxyRequest proxies a single request to the backend.
func (p *Proxy) proxyRequest(c *gin.Context, endpoint string) error {
	targetURL, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	// Build target path
	apiContext := c.GetString("api_context")
	resourcePath := c.GetString("resource_path")
	originalPath := c.Request.URL.Path

	// Map incoming path to backend path
	backendPath := originalPath
	if apiContext != "" && strings.HasPrefix(originalPath, apiContext) {
		// Remove API context from path
		backendPath = strings.TrimPrefix(originalPath, apiContext)
		if resourcePath != "" && resourcePath != "/*" {
			// Map resource path to backend
			backendPath = mapPath(originalPath, apiContext+resourcePath, targetURL.Path)
		}
	}

	if backendPath == "" {
		backendPath = "/"
	}

	targetURL.Path = singleJoiningSlash(targetURL.Path, backendPath)
	targetURL.RawQuery = c.Request.URL.RawQuery

	// Create proxy request
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	c.Request.Body.Close()

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create proxy request: %w", err)
	}

	// Copy headers
	for name, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	// Inject gateway headers
	InjectHeaders(c, c.GetString("api_id"), c.GetString("app_id"), c.GetString("user_id"))

	if !p.config.PreserveHost {
		req.Host = targetURL.Host
	}

	// Execute request
	client := &http.Client{
		Transport: p.transport,
		Timeout:   p.config.RequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("execute proxy request: %w", err)
	}
	defer resp.Body.Close()

	// Copy response headers
	for name, values := range resp.Header {
		for _, value := range values {
			c.Header(name, value)
		}
	}

	// Set gateway headers
	c.Header("X-Gateway-Version", "1.0.0")

	// Copy response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
	return nil
}

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

	// Try GET on root path, fallback to HEAD
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
