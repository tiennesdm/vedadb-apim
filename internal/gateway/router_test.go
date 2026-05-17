package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Router Implementation
// ============================================================================

// Route represents a single route
type Route struct {
	ID       string
	Method   string
	Path     string
	Target   string
	Priority int // higher = more specific/matched first
	Handler  http.HandlerFunc
}

// Router manages HTTP routes with priority and wildcard matching
type Router struct {
	routes []Route
	mu     sync.RWMutex
}

// NewRouter creates a new router
func NewRouter() *Router {
	return &Router{
		routes: make([]Route, 0),
	}
}

// AddRoute registers a new route
func (r *Router) AddRoute(route Route) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, route)
	r.sortRoutes()
}

// RemoveRoute removes a route by ID
func (r *Router) RemoveRoute(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, route := range r.routes {
		if route.ID == id {
			r.routes = append(r.routes[:i], r.routes[i+1:]...)
			return true
		}
	}
	return false
}

// Match finds the best matching route for a request
func (r *Router) Match(method, path string) (*Route, map[string]string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, route := range r.routes {
		if params, ok := matchRoute(route, method, path); ok {
			// Return a copy
			routeCopy := route
			return &routeCopy, params
		}
	}
	return nil, nil
}

// matchRoute checks if a single route matches
func matchRoute(route Route, method, path string) (map[string]string, bool) {
	// Method check
	if route.Method != "" && route.Method != method {
		return nil, false
	}

	// Path matching
	params := make(map[string]string)
	routeParts := splitPath(route.Path)
	pathParts := splitPath(path)

	if len(routeParts) != len(pathParts) {
		// Check for wildcard at end
		if len(routeParts) > 0 && routeParts[len(routeParts)-1] == "*" {
			if len(pathParts) >= len(routeParts)-1 {
				// Match prefix
				for i := 0; i < len(routeParts)-1; i++ {
					if isParam(routeParts[i]) {
						params[strings.Trim(routeParts[i], "{}")] = pathParts[i]
					} else if routeParts[i] != pathParts[i] {
						return nil, false
					}
				}
				params["*"] = strings.Join(pathParts[len(routeParts)-1:], "/")
				return params, true
			}
		}
		return nil, false
	}

	for i := 0; i < len(routeParts); i++ {
		if isParam(routeParts[i]) {
			params[strings.Trim(routeParts[i], "{}")] = pathParts[i]
		} else if routeParts[i] != pathParts[i] {
			return nil, false
		}
	}

	return params, true
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return []string{}
	}
	return strings.Split(path, "/")
}

func isParam(part string) bool {
	return strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")
}

// sortRoutes sorts routes by priority (highest first), then by specificity
func (r *Router) sortRoutes() {
	// Simple bubble sort for clarity
	for i := 0; i < len(r.routes); i++ {
		for j := i + 1; j < len(r.routes); j++ {
			if shouldComeBefore(r.routes[j], r.routes[i]) {
				r.routes[i], r.routes[j] = r.routes[j], r.routes[i]
			}
		}
	}
}

func shouldComeBefore(a, b Route) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	// More specific (fewer wildcards/params) comes first
	aWildcards := strings.Count(a.Path, "{") + strings.Count(a.Path, "*")
	bWildcards := strings.Count(b.Path, "{") + strings.Count(b.Path, "*")
	if aWildcards != bWildcards {
		return aWildcards < bWildcards
	}
	return len(a.Path) > len(b.Path)
}

// Routes returns a copy of all routes
func (r *Router) Routes() []Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Route, len(r.routes))
	copy(result, r.routes)
	return result
}

// LoadRoutes dynamically loads routes (simulates config reload)
func (r *Router) LoadRoutes(routes []Route) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = make([]Route, len(routes))
	copy(r.routes, routes)
	r.sortRoutes()
}

// ServeHTTP implements http.Handler
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	route, params := r.Match(req.Method, req.URL.Path)
	if route == nil {
		http.NotFound(w, req)
		return
	}
	// Store params in context
	ctx := context.WithValue(req.Context(), "params", params)
	route.Handler.ServeHTTP(w, req.WithContext(ctx))
}

// ============================================================================
// TESTS
// ============================================================================

func TestRouter_Match_GivenExactRoute_WhenMatched_ThenReturnsRoute(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "route-1",
		Method:   "GET",
		Path:     "/api/v1/users",
		Target:   "http://backend:8080",
		Priority: 1,
	})

	route, params := router.Match("GET", "/api/v1/users")
	require.NotNil(t, route)
	assert.Equal(t, "route-1", route.ID)
	assert.Equal(t, "/api/v1/users", route.Path)
	assert.Empty(t, params)
}

func TestRouter_Match_GivenWrongMethod_WhenMatched_ThenReturnsNil(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "route-1",
		Method:   "GET",
		Path:     "/api/v1/users",
		Priority: 1,
	})

	route, _ := router.Match("POST", "/api/v1/users")
	assert.Nil(t, route)
}

func TestRouter_Match_GivenWrongPath_WhenMatched_ThenReturnsNil(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "route-1",
		Method:   "GET",
		Path:     "/api/v1/users",
		Priority: 1,
	})

	route, _ := router.Match("GET", "/api/v1/products")
	assert.Nil(t, route)
}

func TestRouter_Match_GivenParameterizedRoute_WhenMatched_ThenReturnsParams(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "user-detail",
		Method:   "GET",
		Path:     "/api/v1/users/{id}",
		Target:   "http://backend:8080",
		Priority: 1,
	})

	tests := []struct {
		name         string
		path         string
		expectMatch  bool
		expectParams map[string]string
	}{
		{
			name:         "valid user id",
			path:         "/api/v1/users/123",
			expectMatch:  true,
			expectParams: map[string]string{"id": "123"},
		},
		{
			name:         "uuid user id",
			path:         "/api/v1/users/550e8400-e29b-41d4-a716-446655440000",
			expectMatch:  true,
			expectParams: map[string]string{"id": "550e8400-e29b-41d4-a716-446655440000"},
		},
		{
			name:         "wrong path depth",
			path:         "/api/v1/users/123/posts",
			expectMatch:  false,
			expectParams: nil,
		},
		{
			name:         "missing id",
			path:         "/api/v1/users/",
			expectMatch:  false,
			expectParams: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, params := router.Match("GET", tt.path)
			if tt.expectMatch {
				require.NotNil(t, route)
				assert.Equal(t, tt.expectParams, params)
			} else {
				assert.Nil(t, route)
			}
		})
	}
}

func TestRouter_Match_GivenWildcardRoute_WhenMatched_ThenMatchesPrefix(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "api-proxy",
		Method:   "",
		Path:     "/api/v1/*",
		Target:   "http://backend:8080",
		Priority: 1,
	})

	tests := []struct {
		name        string
		path        string
		expectMatch bool
	}{
		{"direct child", "/api/v1/users", true},
		{"nested path", "/api/v1/users/123/posts", true},
		{"single segment", "/api/v1/health", true},
		{"wrong prefix", "/api/v2/users", false},
		{"shorter path", "/api/v1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, params := router.Match("GET", tt.path)
			if tt.expectMatch {
				require.NotNil(t, route, "should match %s", tt.path)
				assert.Equal(t, "api-proxy", route.ID)
				if params != nil {
					assert.Contains(t, params, "*")
				}
			} else {
				assert.Nil(t, route, "should not match %s", tt.path)
			}
		})
	}
}

func TestRouter_Priority_GivenMultipleRoutes_WhenMatched_ThenHighestPriorityWins(t *testing.T) {
	router := NewRouter()

	// Lower priority route (added first)
	router.AddRoute(Route{
		ID:       "generic-users",
		Method:   "GET",
		Path:     "/api/v1/users/{id}",
		Priority: 1,
	})

	// Higher priority route (more specific)
	router.AddRoute(Route{
		ID:       "user-profile",
		Method:   "GET",
		Path:     "/api/v1/users/{id}/profile",
		Priority: 10,
	})

	// Highest priority route (exact match)
	router.AddRoute(Route{
		ID:       "user-me",
		Method:   "GET",
		Path:     "/api/v1/users/me",
		Priority: 100,
	})

	tests := []struct {
		name         string
		path         string
		expectedID   string
		expectParams map[string]string
	}{
		{
			name:       "exact me route wins",
			path:       "/api/v1/users/me",
			expectedID: "user-me",
		},
		{
			name:         "profile route wins over generic",
			path:         "/api/v1/users/123/profile",
			expectedID:   "user-profile",
			expectParams: map[string]string{"id": "123"},
		},
		{
			name:         "generic route for simple id",
			path:         "/api/v1/users/123",
			expectedID:   "generic-users",
			expectParams: map[string]string{"id": "123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, params := router.Match("GET", tt.path)
			require.NotNil(t, route)
			assert.Equal(t, tt.expectedID, route.ID)
			if tt.expectParams != nil {
				assert.Equal(t, tt.expectParams, params)
			}
		})
	}
}

func TestRouter_Priority_GivenSamePriority_WhenMatched_ThenMoreSpecificWins(t *testing.T) {
	router := NewRouter()

	router.AddRoute(Route{
		ID:       "wildcard",
		Method:   "GET",
		Path:     "/api/v1/*",
		Priority: 5,
	})

	router.AddRoute(Route{
		ID:       "exact",
		Method:   "GET",
		Path:     "/api/v1/users",
		Priority: 5,
	})

	route, _ := router.Match("GET", "/api/v1/users")
	require.NotNil(t, route)
	assert.Equal(t, "exact", route.ID, "exact route should win over wildcard at same priority")
}

func TestRouter_DynamicRouteLoading_GivenNewRoutes_WhenLoaded_ThenReplacesAll(t *testing.T) {
	router := NewRouter()

	// Initial routes
	router.AddRoute(Route{ID: "route-1", Method: "GET", Path: "/old/path", Priority: 1})
	router.AddRoute(Route{ID: "route-2", Method: "POST", Path: "/old/path2", Priority: 1})

	// Verify initial
	routes := router.Routes()
	require.Len(t, routes, 2)

	// Dynamic reload
	newRoutes := []Route{
		{ID: "new-route-1", Method: "GET", Path: "/new/path", Priority: 10},
		{ID: "new-route-2", Method: "POST", Path: "/new/path2", Priority: 5},
		{ID: "new-route-3", Method: "DELETE", Path: "/new/path3", Priority: 1},
	}
	router.LoadRoutes(newRoutes)

	routes = router.Routes()
	require.Len(t, routes, 3)

	// Old routes should not exist
	oldRoute, _ := router.Match("GET", "/old/path")
	assert.Nil(t, oldRoute)

	// New routes should work
	newRoute, _ := router.Match("GET", "/new/path")
	require.NotNil(t, newRoute)
	assert.Equal(t, "new-route-1", newRoute.ID)
}

func TestRouter_RemoveRoute_GivenExistingRoute_WhenRemoved_ThenGone(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{ID: "route-1", Method: "GET", Path: "/api/v1/users", Priority: 1})
	router.AddRoute(Route{ID: "route-2", Method: "GET", Path: "/api/v1/products", Priority: 1})

	removed := router.RemoveRoute("route-1")
	assert.True(t, removed)

	routes := router.Routes()
	require.Len(t, routes, 1)
	assert.Equal(t, "route-2", routes[0].ID)

	// route-1 should not match
	route, _ := router.Match("GET", "/api/v1/users")
	assert.Nil(t, route)
}

func TestRouter_RemoveRoute_GivenNonExistent_WhenRemoved_ThenReturnsFalse(t *testing.T) {
	router := NewRouter()
	removed := router.RemoveRoute("non-existent")
	assert.False(t, removed)
}

func TestRouter_MultipleParameters_GivenRouteWithMultipleParams_WhenMatched_ThenReturnsAllParams(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "nested-resource",
		Method:   "GET",
		Path:     "/api/v1/users/{userId}/posts/{postId}",
		Priority: 1,
	})

	route, params := router.Match("GET", "/api/v1/users/123/posts/456")
	require.NotNil(t, route)
	assert.Equal(t, "nested-resource", route.ID)
	assert.Equal(t, "123", params["userId"])
	assert.Equal(t, "456", params["postId"])
}

func TestRouter_ConcurrentAccess_GivenMultipleGoroutines_WhenOperating_ThenNoRace(t *testing.T) {
	router := NewRouter()

	// Seed initial routes
	for i := 0; i < 10; i++ {
		router.AddRoute(Route{
			ID:       fmt.Sprintf("route-%d", i),
			Method:   "GET",
			Path:     fmt.Sprintf("/api/v1/resource%d", i),
			Priority: i,
		})
	}

	var wg sync.WaitGroup
	wg.Add(4)

	// Concurrent reads
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			router.Match("GET", fmt.Sprintf("/api/v1/resource%d", i%10))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			router.Routes()
		}
	}()

	// Concurrent writes
	go func() {
		defer wg.Done()
		for i := 100; i < 200; i++ {
			router.AddRoute(Route{
				ID:       fmt.Sprintf("route-%d", i),
				Method:   "POST",
				Path:     fmt.Sprintf("/api/v1/resource%d", i),
				Priority: i,
			})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			router.RemoveRoute(fmt.Sprintf("route-%d", i))
		}
	}()

	wg.Wait()
}

func TestRouter_EmptyPath_GivenRootPath_WhenMatched_ThenWorks(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "root",
		Method:   "GET",
		Path:     "/",
		Priority: 1,
	})

	route, _ := router.Match("GET", "/")
	require.NotNil(t, route)
	assert.Equal(t, "root", route.ID)
}

func TestRouter_MethodAny_GivenEmptyMethod_WhenMatched_ThenAnyMethodAllowed(t *testing.T) {
	router := NewRouter()
	router.AddRoute(Route{
		ID:       "catch-all",
		Method:   "", // Any method
		Path:     "/health",
		Priority: 1,
	})

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	for _, method := range methods {
		route, _ := router.Match(method, "/health")
		assert.NotNil(t, route, "should match %s", method)
	}
}

func TestRouter_ServeHTTP_GivenMatchingRoute_WhenCalled_ThenExecutesHandler(t *testing.T) {
	router := NewRouter()
	called := false
	router.AddRoute(Route{
		ID:       "test",
		Method:   "GET",
		Path:     "/test",
		Priority: 1,
		Handler: func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		},
	})

	req := requireHTTPRequest(t, "GET", "/test")
	w := &mockResponseWriter{header: make(http.Header)}
	router.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.statusCode)
}

func TestRouter_ServeHTTP_GivenNoMatch_WhenCalled_ThenReturns404(t *testing.T) {
	router := NewRouter()
	req := requireHTTPRequest(t, "GET", "/nonexistent")
	w := &mockResponseWriter{header: make(http.Header)}
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.statusCode)
}

func TestRouter_Routes_GivenMultipleRoutes_WhenRetrieved_ThenReturnsSortedByPriority(t *testing.T) {
	router := NewRouter()

	router.AddRoute(Route{ID: "low", Priority: 1, Path: "/low"})
	router.AddRoute(Route{ID: "high", Priority: 100, Path: "/high"})
	router.AddRoute(Route{ID: "medium", Priority: 50, Path: "/medium"})

	routes := router.Routes()
	require.Len(t, routes, 3)
	assert.Equal(t, "high", routes[0].ID)
	assert.Equal(t, "medium", routes[1].ID)
	assert.Equal(t, "low", routes[2].ID)
}

// ============================================================================
// Test Helpers
// ============================================================================

type mockResponseWriter struct {
	statusCode int
	header     http.Header
	body       []byte
}

func (m *mockResponseWriter) Header() http.Header       { return m.header }
func (m *mockResponseWriter) Write(b []byte) (int, error) { m.body = append(m.body, b...); return len(b), nil }
func (m *mockResponseWriter) WriteHeader(code int)        { m.statusCode = code }

func requireHTTPRequest(t *testing.T, method, path string) *http.Request {
	req, err := http.NewRequest(method, "http://localhost"+path, nil)
	require.NoError(t, err)
	return req
}
