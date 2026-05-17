// Package gateway provides API request routing functionality for the VedaDB API Manager.
// This file implements route matching by API context + resource path, dynamic route
// loading from VedaDB, route priority, and wildcard pattern matching.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// Route defines a single API route in the gateway.
type Route struct {
	// ID is the unique route identifier.
	ID uuid.UUID `json:"id"`
	// APIID is the parent API ID.
	APIID uuid.UUID `json:"api_id"`
	// APIContext is the API base context path (e.g., /v1/store).
	APIContext string `json:"api_context"`
	// APIName is the human-readable API name.
	APIName string `json:"api_name"`
	// APIVersion is the API version.
	APIVersion string `json:"api_version"`
	// Endpoint is the backend service endpoint URL.
	Endpoint string `json:"endpoint"`
	// Method is the HTTP method (GET, POST, PUT, DELETE, PATCH, etc.).
	Method string `json:"method"`
	// Path is the resource path pattern (supports wildcards like /products/{id}).
	Path string `json:"path"`
	// AuthRequired indicates whether authentication is required.
	AuthRequired bool `json:"auth_required"`
	// AuthType specifies the authentication type.
	AuthType models.AuthType `json:"auth_type"`
	// ThrottlePolicy is the name of the throttle policy to apply.
	ThrottlePolicy string `json:"throttle_policy,omitempty"`
	// Scope is the required OAuth scope.
	Scope string `json:"scope,omitempty"`
	// TransformPolicy is the name of the transform policy.
	TransformPolicy string `json:"transform_policy,omitempty"`
	// CachingEnabled enables response caching for this route.
	CachingEnabled bool `json:"caching_enabled"`
	// CacheTTL is the cache time-to-live for this route.
	CacheTTL time.Duration `json:"cache_ttl,omitempty"`
	// Priority determines route matching order (higher = matched first).
	Priority int `json:"priority"`
	// Status is the route status.
	Status string `json:"status"`
	// CreatedAt is when the route was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the route was last updated.
	UpdatedAt time.Time `json:"updated_at"`

	// compiledPattern is the compiled regex for path matching.
	compiledPattern *regexp.Regexp
	// pathParamNames are the named path parameter names.
	pathParamNames []string
}

// CompilePattern compiles the path pattern into a regex for efficient matching.
func (r *Route) CompilePattern() error {
	pattern, paramNames := pathToRegex(r.Path)
	re, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		return fmt.Errorf("invalid path pattern %q: %w", r.Path, err)
	}
	r.compiledPattern = re
	r.pathParamNames = paramNames
	return nil
}

// Match checks if the given method and path match this route.
func (r *Route) Match(method, path string) (bool, map[string]string) {
	if r.Status != "PUBLISHED" && r.Status != "" {
		return false, nil
	}

	// Check method match
	if r.Method != method && r.Method != "*" {
		return false, nil
	}

	// Check path match
	if r.compiledPattern == nil {
		if err := r.CompilePattern(); err != nil {
			return false, nil
		}
	}

	matches := r.compiledPattern.FindStringSubmatch(path)
	if matches == nil {
		return false, nil
	}

	// Extract path parameters
	params := make(map[string]string)
	for i, name := range r.pathParamNames {
		if i+1 < len(matches) {
			params[name] = matches[i+1]
		}
	}

	return true, params
}

// pathToRegex converts a path pattern like /products/{id} to a regex pattern.
func pathToRegex(pattern string) (string, []string) {
	var paramNames []string
	// Escape special regex characters except {}
	re := regexp.MustCompile(`\{(\w+)\}`)

	result := re.ReplaceAllStringFunc(pattern, func(match string) string {
		// Extract parameter name
		name := match[1 : len(match)-1]
		paramNames = append(paramNames, name)
		return `([^/]+)`
	})

	// Handle wildcard ** for deep paths
	result = strings.ReplaceAll(result, `/**`, `(?:/.*)?`)
	result = strings.ReplaceAll(result, `/*`, `(?:/[^/]*)?`)

	return result, paramNames
}

// ---------------------------------------------------------------------------
// Route Store
// ---------------------------------------------------------------------------

// RouteStore defines the interface for route storage and retrieval.
type RouteStore interface {
	// GetRoutes returns all active routes.
	GetRoutes(ctx context.Context) ([]*Route, error)
	// GetRouteByID returns a route by ID.
	GetRouteByID(ctx context.Context, id uuid.UUID) (*Route, error)
	// GetRoutesByAPI returns all routes for a specific API.
	GetRoutesByAPI(ctx context.Context, apiID uuid.UUID) ([]*Route, error)
	// WatchRoutes watches for route changes.
	WatchRoutes(ctx context.Context, callback func([]*Route)) error
}

// ---------------------------------------------------------------------------
// Dynamic Router
// ---------------------------------------------------------------------------

// Router implements dynamic API request routing.
type Router struct {
	routes     []*Route
	mu         sync.RWMutex
	store      RouteStore
	reloadInterval time.Duration
	stopCh     chan struct{}
}

// NewRouter creates a new dynamic router.
func NewRouter(store RouteStore) *Router {
	return &Router{
		routes:         make([]*Route, 0),
		store:          store,
		reloadInterval: 30 * time.Second,
		stopCh:         make(chan struct{}),
	}
}

// WithReloadInterval sets the route reload interval.
func (r *Router) WithReloadInterval(d time.Duration) *Router {
	r.reloadInterval = d
	return r
}

// LoadRoutes loads routes from the store.
func (r *Router) LoadRoutes(ctx context.Context) error {
	routes, err := r.store.GetRoutes(ctx)
	if err != nil {
		return fmt.Errorf("failed to load routes: %w", err)
	}

	// Compile patterns for all routes
	for _, route := range routes {
		if err := route.CompilePattern(); err != nil {
			// Log error but continue loading other routes
			continue
		}
	}

	r.mu.Lock()
	r.routes = routes
	// Sort by priority (highest first)
	sort.Slice(r.routes, func(i, j int) bool {
		return r.routes[i].Priority > r.routes[j].Priority
	})
	r.mu.Unlock()

	return nil
}

// Start begins the background route refresh.
func (r *Router) Start(ctx context.Context) error {
	// Initial load
	if err := r.LoadRoutes(ctx); err != nil {
		return err
	}

	// Start periodic refresh
	go r.refreshLoop(ctx)

	return nil
}

// Stop stops the background route refresh.
func (r *Router) Stop() {
	close(r.stopCh)
}

// refreshLoop periodically refreshes routes from the store.
func (r *Router) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(r.reloadInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			if err := r.LoadRoutes(ctx); err != nil {
				// Log error but don't stop
			}
		}
	}
}

// Match finds a matching route for the given method and path.
func (r *Router) Match(method, path string) (*Route, map[string]string) {
	r.mu.RLock()
	routes := r.routes
	r.mu.RUnlock()

	for _, route := range routes {
		matched, params := route.Match(method, path)
		if matched {
			return route, params
		}
	}
	return nil, nil
}

// GetRoutes returns all loaded routes.
func (r *Router) GetRoutes() []*Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	routes := make([]*Route, len(r.routes))
	copy(routes, r.routes)
	return routes
}

// GetRouteCount returns the number of loaded routes.
func (r *Router) GetRouteCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.routes)
}

// ---------------------------------------------------------------------------
// RouteStore Implementation
// ---------------------------------------------------------------------------

// VedaDBRouteStore implements RouteStore using VedaDB.
type VedaDBRouteStore struct {
	client VedaDBRouteClient
}

// VedaDBRouteClient defines the interface needed from VedaDB for routes.
type VedaDBRouteClient interface {
	List(ctx context.Context, namespace string) ([]string, error)
	Get(ctx context.Context, namespace, key string, dest interface{}) error
}

// NewVedaDBRouteStore creates a new VedaDB-backed route store.
func NewVedaDBRouteStore(client VedaDBRouteClient) *VedaDBRouteStore {
	return &VedaDBRouteStore{
		client: client,
	}
}

// GetRoutes returns all active routes from VedaDB.
func (s *VedaDBRouteStore) GetRoutes(ctx context.Context) ([]*Route, error) {
	keys, err := s.client.List(ctx, "routes")
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}

	routes := make([]*Route, 0, len(keys))
	for _, key := range keys {
		var route Route
		if err := s.client.Get(ctx, "routes", key, &route); err != nil {
			continue
		}
		if err := route.CompilePattern(); err != nil {
			continue
		}
		routes = append(routes, &route)
	}

	return routes, nil
}

// GetRouteByID returns a route by ID.
func (s *VedaDBRouteStore) GetRouteByID(ctx context.Context, id uuid.UUID) (*Route, error) {
	var route Route
	if err := s.client.Get(ctx, "routes", id.String(), &route); err != nil {
		return nil, fmt.Errorf("get route %s: %w", id, err)
	}
	if err := route.CompilePattern(); err != nil {
		return nil, err
	}
	return &route, nil
}

// GetRoutesByAPI returns all routes for a specific API.
func (s *VedaDBRouteStore) GetRoutesByAPI(ctx context.Context, apiID uuid.UUID) ([]*Route, error) {
	allRoutes, err := s.GetRoutes(ctx)
	if err != nil {
		return nil, err
	}

	routes := make([]*Route, 0)
	for _, route := range allRoutes {
		if route.APIID == apiID {
			routes = append(routes, route)
		}
	}
	return routes, nil
}

// WatchRoutes watches for route changes (stub - would use pub/sub in production).
func (s *VedaDBRouteStore) WatchRoutes(ctx context.Context, callback func([]*Route)) error {
	// In a full implementation, this would use VedaDB's pub/sub or watch mechanism
	// For now, poll periodically
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			routes, err := s.GetRoutes(ctx)
			if err != nil {
				continue
			}
			callback(routes)
		}
	}
}

// ---------------------------------------------------------------------------
// Gin Router Integration
// ---------------------------------------------------------------------------

// RegisterRoutes registers gateway routes with the Gin engine.
func RegisterRoutes(router *Router, engine *gin.Engine, proxyHandler gin.HandlerFunc) {
	engine.NoRoute(func(c *gin.Context) {
		method := c.Request.Method
		path := c.Request.URL.Path

		// Find matching route
		route, params := router.Match(method, path)
		if route == nil {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "no route found for " + method + " " + path,
				"code":    "APIM.ROUTE_NOT_FOUND",
				"message": "the requested API or resource does not exist",
			})
			return
		}

		// Store route info in context
		c.Set("route_id", route.ID.String())
		c.Set("api_id", route.APIID.String())
		c.Set("api_context", route.APIContext)
		c.Set("api_name", route.APIName)
		c.Set("api_version", route.APIVersion)
		c.Set("backend_endpoint", route.Endpoint)
		c.Set("auth_required", route.AuthRequired)
		c.Set("auth_type", string(route.AuthType))
		c.Set("throttle_policy", route.ThrottlePolicy)
		c.Set("required_scope", route.Scope)
		c.Set("caching_enabled", route.CachingEnabled)
		c.Set("cache_ttl", route.CacheTTL)
		c.Set("path_params", params)
		c.Set("resource_path", route.Path)
		c.Set("http_method", route.Method)

		// Continue to proxy
		proxyHandler(c)
	})
}

// ---------------------------------------------------------------------------
// Route Builder
// ---------------------------------------------------------------------------

// RouteBuilder provides a fluent API for building routes.
type RouteBuilder struct {
	route *Route
}

// NewRouteBuilder creates a new route builder.
func NewRouteBuilder() *RouteBuilder {
	return &RouteBuilder{
		route: &Route{
			ID:         uuid.New(),
			Status:     "PUBLISHED",
			Priority:   0,
			AuthType:   models.AuthTypeOAuth2,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		},
	}
}

// ForAPI sets the API context for the route.
func (b *RouteBuilder) ForAPI(apiID uuid.UUID, apiContext, apiName, apiVersion string) *RouteBuilder {
	b.route.APIID = apiID
	b.route.APIContext = apiContext
	b.route.APIName = apiName
	b.route.APIVersion = apiVersion
	return b
}

// ToEndpoint sets the backend endpoint.
func (b *RouteBuilder) ToEndpoint(endpoint string) *RouteBuilder {
	b.route.Endpoint = endpoint
	return b
}

// WithMethod sets the HTTP method.
func (b *RouteBuilder) WithMethod(method string) *RouteBuilder {
	b.route.Method = strings.ToUpper(method)
	return b
}

// WithPath sets the path pattern.
func (b *RouteBuilder) WithPath(path string) *RouteBuilder {
	b.route.Path = path
	return b
}

// WithAuth sets the authentication requirements.
func (b *RouteBuilder) WithAuth(required bool, authType models.AuthType) *RouteBuilder {
	b.route.AuthRequired = required
	b.route.AuthType = authType
	return b
}

// WithThrottle sets the throttle policy.
func (b *RouteBuilder) WithThrottle(policy string) *RouteBuilder {
	b.route.ThrottlePolicy = policy
	return b
}

// WithScope sets the required OAuth scope.
func (b *RouteBuilder) WithScope(scope string) *RouteBuilder {
	b.route.Scope = scope
	return b
}

// WithCaching enables response caching.
func (b *RouteBuilder) WithCaching(enabled bool, ttl time.Duration) *RouteBuilder {
	b.route.CachingEnabled = enabled
	b.route.CacheTTL = ttl
	return b
}

// WithPriority sets the route priority.
func (b *RouteBuilder) WithPriority(priority int) *RouteBuilder {
	b.route.Priority = priority
	return b
}

// Build returns the built route.
func (b *RouteBuilder) Build() (*Route, error) {
	if b.route.APIContext == "" {
		return nil, fmt.Errorf("API context is required")
	}
	if b.route.Endpoint == "" {
		return nil, fmt.Errorf("backend endpoint is required")
	}
	if b.route.Method == "" {
		b.route.Method = "*"
	}
	if b.route.Path == "" {
		b.route.Path = "/*"
	}
	if err := b.route.CompilePattern(); err != nil {
		return nil, err
	}
	return b.route, nil
}

// ---------------------------------------------------------------------------
// Route Statistics
// ---------------------------------------------------------------------------

// RouterStats contains router statistics.
type RouterStats struct {
	TotalRoutes   int            `json:"total_routes"`
	ActiveRoutes  int            `json:"active_routes"`
	APIs          map[string]int `json:"apis"`
	Methods       map[string]int `json:"methods"`
	LastReload    time.Time      `json:"last_reload"`
	AvgBuildTime  int64          `json:"avg_build_time_ms"`
}

// GetStats returns router statistics.
func (r *Router) GetStats() RouterStats {
	r.mu.RLock()
	routes := r.routes
	r.mu.RUnlock()

	stats := RouterStats{
		TotalRoutes:  len(routes),
		ActiveRoutes: 0,
		APIs:         make(map[string]int),
		Methods:      make(map[string]int),
	}

	for _, route := range routes {
		if route.Status == "PUBLISHED" || route.Status == "" {
			stats.ActiveRoutes++
		}
		stats.APIs[route.APIContext]++
		stats.Methods[route.Method]++
	}

	return stats
}
