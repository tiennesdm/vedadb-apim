package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Integration Test Infrastructure
// ============================================================================

// MockBackend simulates a backend service
type MockBackend struct {
	Server      *httptest.Server
	RequestCount int64
	mu          sync.Mutex
	Responses   map[string]mockResponse
}

type mockResponse struct {
	StatusCode int
	Body       string
	Headers    map[string]string
}

// NewMockBackend creates a new mock backend
func NewMockBackend() *MockBackend {
	mb := &MockBackend{
		Responses: make(map[string]mockResponse),
	}
	mb.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&mb.RequestCount, 1)

		// Check if there's a predefined response
		key := r.Method + " " + r.URL.Path
		mb.mu.Lock()
		resp, exists := mb.Responses[key]
		mb.mu.Unlock()

		if exists {
			for k, v := range resp.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(resp.StatusCode)
			w.Write([]byte(resp.Body))
			return
		}

		// Default response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"path":    r.URL.Path,
			"method":  r.Method,
			"message": "OK from backend",
		})
	}))
	return mb
}

func (mb *MockBackend) SetResponse(method, path string, status int, body string, headers map[string]string) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.Responses[method+" "+path] = mockResponse{StatusCode: status, Body: body, Headers: headers}
}

// GatewayServer simulates the API gateway
type GatewayServer struct {
	Server        *httptest.Server
	BackendURL    string
	Cache         map[string]cacheEntry
	CacheMu       sync.RWMutex
	RateLimiter   *testRateLimiter
	AuthHandler   http.HandlerFunc
	Routes        []Route
	mu            sync.RWMutex
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

type Route struct {
	ID       string
	Method   string
	Path     string
	Target   string
	Priority int
}

type testRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]int
	capacity int
}

func newTestRateLimiter(capacity int) *testRateLimiter {
	return &testRateLimiter{
		buckets:  make(map[string]int),
		capacity: capacity,
	}
}

func (rl *testRateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	count := rl.buckets[key]
	if count >= rl.capacity {
		return false
	}
	rl.buckets[key] = count + 1
	return true
}

// NewGatewayServer creates a new gateway server for testing
func NewGatewayServer(backendURL string) *GatewayServer {
	gw := &GatewayServer{
		BackendURL:  backendURL,
		Cache:       make(map[string]cacheEntry),
		RateLimiter: newTestRateLimiter(100),
		AuthHandler: defaultAuthHandler,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", gw.proxyHandler)
	gw.Server = httptest.NewServer(mux)
	return gw
}

func defaultAuthHandler(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") && !strings.HasPrefix(authHeader, "ApiKey ") {
		http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (gw *GatewayServer) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Auth check
	if r.URL.Path != "/health" {
		rr := httptest.NewRecorder()
		gw.AuthHandler(rr, r)
		if rr.Code != http.StatusOK {
			for k, v := range rr.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rr.Code)
			w.Write(rr.Body.Bytes())
			return
		}
	}

	// 2. Rate limiting
	clientKey := r.Header.Get("X-API-Key")
	if clientKey == "" {
		clientKey = r.RemoteAddr
	}
	if !gw.RateLimiter.Allow(clientKey) {
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// 3. Cache check
	cacheKey := r.Method + ":" + r.URL.Path
	gw.CacheMu.RLock()
	entry, found := gw.Cache[cacheKey]
	gw.CacheMu.RUnlock()
	if found && time.Now().Before(entry.expiresAt) {
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(entry.data)
		return
	}

	// 4. Proxy to backend
	backendURL := gw.BackendURL + r.URL.Path
	if r.URL.RawQuery != "" {
		backendURL += "?" + r.URL.RawQuery
	}

	body, _ := io.ReadAll(r.Body)
	req, err := http.NewRequest(r.Method, backendURL, strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, `{"error":"proxy error"}`, http.StatusInternalServerError)
		return
	}

	// Copy headers
	for name, values := range r.Header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, `{"error":"backend unavailable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// 5. Cache successful GET responses
	if r.Method == "GET" && resp.StatusCode == 200 {
		gw.CacheMu.Lock()
		gw.Cache[cacheKey] = cacheEntry{data: respBody, expiresAt: time.Now().Add(5 * time.Minute)}
		gw.CacheMu.Unlock()
	}

	w.Header().Set("X-Cache", "MISS")
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// ============================================================================
// TESTS
// ============================================================================

func TestGateway_RequestRouting_GivenValidRoute_WhenRequested_ThenProxiesToBackend(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		expectCode int
	}{
		{"GET request", "GET", "/api/users", "", http.StatusOK},
		{"POST request", "POST", "/api/users", `{"name":"John"}`, http.StatusOK},
		{"PUT request", "PUT", "/api/users/1", `{"name":"Jane"}`, http.StatusOK},
		{"DELETE request", "DELETE", "/api/users/1", "", http.StatusOK},
		{"path with params", "GET", "/api/users/123/posts", "", http.StatusOK},
		{"nested path", "GET", "/api/v1/organizations/456/teams/789/members", "", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}

			req, err := http.NewRequest(tt.method, gateway.Server.URL+tt.path, body)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer valid-token")
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expectCode, resp.StatusCode)

			respBody, _ := io.ReadAll(resp.Body)
			assert.Contains(t, string(respBody), tt.path)
		})
	}
}

func TestGateway_AuthMiddleware_GivenMissingAuth_WhenRequested_ThenReturns401(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	// No auth header
	resp, err := http.Get(gateway.Server.URL + "/api/users")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestGateway_AuthMiddleware_GivenInvalidAuth_WhenRequested_ThenReturns401(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	tests := []struct {
		name  string
		auth  string
	}{
		{"empty auth", ""},
		{"invalid format", "Basic abc123"},
		{"just token", "abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
}

func TestGateway_AuthMiddleware_GivenValidAuth_WhenRequested_ThenAllowed(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	tests := []struct {
		name  string
		auth  string
	}{
		{"Bearer token", "Bearer valid-token-123"},
		{"API Key", "ApiKey vapim_abc123def456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
			req.Header.Set("Authorization", tt.auth)

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

func TestGateway_RateLimiting_GivenManyRequests_WhenLimitReached_ThenReturns429(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	gateway.RateLimiter = newTestRateLimiter(5) // Very small limit
	defer gateway.Server.Close()

	// Make 5 requests that should succeed
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-API-Key", "client-1")

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "request %d should succeed", i+1)
	}

	// 6th request should be rate limited
	req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-API-Key", "client-1")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "rate limit exceeded")
}

func TestGateway_RateLimiting_GivenDifferentClients_WhenLimited_ThenIndependent(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	gateway.RateLimiter = newTestRateLimiter(3)
	defer gateway.Server.Close()

	// Client 1 uses all their quota
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-API-Key", "client-1")
		http.DefaultClient.Do(req)
	}

	// Client 1 should be blocked
	req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-API-Key", "client-1")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)

	// Client 2 should still be able to make requests
	req, _ = http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-API-Key", "client-2")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGateway_Caching_GivenGETRequest_WhenRepeated_ThenCacheHit(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	// Set a specific response
	backend.SetResponse("GET", "/api/users", 200, `{"cached": true, "data": [1, 2, 3]}`, map[string]string{"Content-Type": "application/json"})

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	// First request - should be cache miss
	req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body1, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "MISS", resp.Header.Get("X-Cache"))
	assert.Equal(t, `{"cached": true, "data": [1, 2, 3]}`, string(body1))

	// Change backend response
	backend.SetResponse("GET", "/api/users", 200, `{"cached": false, "data": [4, 5, 6]}`, map[string]string{"Content-Type": "application/json"})

	// Second request - should be cache hit (old data)
	req, _ = http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer token")

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "HIT", resp.Header.Get("X-Cache"))
	assert.Equal(t, `{"cached": true, "data": [1, 2, 3]}`, string(body2))
}

func TestGateway_Caching_GivenNonGETRequest_WhenMade_ThenNotCached(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	// POST request should not be cached
	req, _ := http.NewRequest("POST", gateway.Server.URL+"/api/users", strings.NewReader(`{"name":"John"}`))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("X-Cache")) // No cache header for non-GET
}

func TestGateway_Caching_GivenErrorResponse_WhenCached_ThenNotCached(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	// Set error response
	backend.SetResponse("GET", "/api/error", 500, `{"error": "internal server error"}`, map[string]string{"Content-Type": "application/json"})

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/error", nil)
	req.Header.Set("Authorization", "Bearer token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "MISS", resp.Header.Get("X-Cache"))

	// Verify not in cache
	gateway.CacheMu.RLock()
	_, found := gateway.Cache["GET:/api/error"]
	gateway.CacheMu.RUnlock()
	assert.False(t, found, "error responses should not be cached")
}

func TestGateway_HealthEndpoint_GivenNoAuth_WhenRequested_ThenAllowed(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	// Health endpoint should not require auth
	resp, err := http.Get(gateway.Server.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGateway_ProxyHeaders_GivenRequestWithHeaders_WhenProxied_ThenHeadersForwarded(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	// Track received headers
	var receivedHeaders http.Header
	backend.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
	})

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Request-ID", "abc-123")
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotNil(t, receivedHeaders)
	assert.Equal(t, "abc-123", receivedHeaders.Get("X-Request-ID"))
	assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Header"))
	assert.Equal(t, "application/json", receivedHeaders.Get("Accept"))
}

func TestGateway_BackendUnavailable_GivenDownstreamFailure_WhenRequested_ThenReturns502(t *testing.T) {
	// Use a non-existent backend
	gateway := NewGatewayServer("http://localhost:59999")
	defer gateway.Server.Close()

	req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/users", nil)
	req.Header.Set("Authorization", "Bearer token")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "backend unavailable")
}

func TestGateway_ConcurrentRequests_GivenMultipleClients_WhenOperating_ThenNoRace(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	const numClients = 20
	const requestsPerClient = 20

	var wg sync.WaitGroup
	wg.Add(numClients)

	for i := 0; i < numClients; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < requestsPerClient; j++ {
				method := "GET"
				if j%4 == 1 {
					method = "POST"
				} else if j%4 == 2 {
					method = "PUT"
				} else if j%4 == 3 {
					method = "DELETE"
				}

				req, _ := http.NewRequest(method, gateway.Server.URL+fmt.Sprintf("/api/resource%d", j), nil)
				req.Header.Set("Authorization", "Bearer token")
				req.Header.Set("X-API-Key", fmt.Sprintf("client-%d", id))

				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}(i)
	}

	wg.Wait()

	// Backend should have received requests
	assert.GreaterOrEqual(t, atomic.LoadInt64(&backend.RequestCount), int64(1))
}

func TestGateway_QueryParams_GivenURLWithQuery_WhenProxied_ThenParamsForwarded(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	var receivedQuery string
	backend.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"query": receivedQuery})
	})

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	resp, err := http.Get(gateway.Server.URL + "/api/search?q=test&limit=10&offset=0")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "q=test&limit=10&offset=0", receivedQuery)
}

func TestGateway_RequestBody_GivenPOSTWithBody_WhenProxied_ThenBodyForwarded(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	var receivedBody string
	backend.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"received": receivedBody})
	})

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	body := `{"name":"Test API","version":"1.0.0"}`
	req, _ := http.NewRequest("POST", gateway.Server.URL+"/api/apis", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, body, receivedBody)
}

func TestGateway_ResponseHeaders_GivenBackendHeaders_WhenReturned_ThenForwarded(t *testing.T) {
	backend := NewMockBackend()
	defer backend.Server.Close()

	backend.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Response", "custom-value")
		w.Header().Set("X-Request-ID", "resp-123")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"OK"}`))
	})

	gateway := NewGatewayServer(backend.Server.URL)
	defer gateway.Server.Close()

	req, _ := http.NewRequest("GET", gateway.Server.URL+"/api/test", nil)
	req.Header.Set("Authorization", "Bearer token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "custom-value", resp.Header.Get("X-Custom-Response"))
}
