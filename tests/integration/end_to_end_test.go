package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// End-to-End Test Suite
// ============================================================================

// E2EContext holds the state for end-to-end tests
type E2EContext struct {
	PublisherToken   string
	SubscriberToken  string
	APIID            string
	ApplicationID    string
	SubscriptionID   string
	APIKey           string
	GatewayServer    *httptest.Server
	PublisherServer  *httptest.Server
	PortalServer     *httptest.Server
	AnalyticsServer  *httptest.Server
	mu               sync.Mutex
}

// NewE2EContext creates a new end-to-end test context
func NewE2EContext() *E2EContext {
	return &E2EContext{
		PublisherToken:  "publisher_token_123",
		SubscriberToken: "subscriber_token_456",
	}
}

// Setup initializes all servers for the E2E test
func (ctx *E2EContext) Setup(t *testing.T) {
	// Publisher server
	ctx.PublisherServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth check
		if r.Header.Get("Authorization") != "Bearer "+ctx.PublisherToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		path := r.URL.Path
		method := r.Method

		switch {
		case path == "/apis" && method == "POST":
			ctx.handleCreateAPI(w, r)
		case path == "/apis" && method == "GET":
			ctx.handleListAPIs(w, r)
		case strings.HasPrefix(path, "/apis/") && method == "GET":
			ctx.handleGetAPI(w, r, strings.TrimPrefix(path, "/apis/"))
		case strings.HasPrefix(path, "/apis/") && method == "DELETE":
			ctx.handleDeleteAPI(w, r, strings.TrimPrefix(path, "/apis/"))
		case strings.HasPrefix(path, "/apis/") && method == "PUT":
			ctx.handleUpdateAPI(w, r, strings.TrimPrefix(path, "/apis/"))
		case strings.HasPrefix(path, "/apis/") && method == "PATCH":
			ctx.handlePublishAPI(w, r, strings.TrimPrefix(path, "/apis/"))
		default:
			http.NotFound(w, r)
		}
	}))

	// Portal server
	ctx.PortalServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		method := r.Method

		switch {
		case path == "/catalog" && method == "GET":
			ctx.handleCatalog(w, r)
		case path == "/applications" && method == "POST":
			ctx.handleCreateApplication(w, r)
		case path == "/applications" && method == "GET":
			ctx.handleListApplications(w, r)
		case strings.HasPrefix(path, "/applications/") && method == "GET":
			ctx.handleGetApplication(w, r, strings.TrimPrefix(path, "/applications/"))
		case strings.HasPrefix(path, "/applications/") && method == "DELETE":
			ctx.handleDeleteApplication(w, r, strings.TrimPrefix(path, "/applications/"))
		case path == "/subscriptions" && method == "POST":
			ctx.handleCreateSubscription(w, r)
		case path == "/subscriptions" && method == "GET":
			ctx.handleListSubscriptions(w, r)
		case strings.HasPrefix(path, "/subscriptions/") && method == "DELETE":
			ctx.handleDeleteSubscription(w, r, strings.TrimPrefix(path, "/subscriptions/"))
		case path == "/keys/generate" && method == "POST":
			ctx.handleGenerateKey(w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	// Gateway server
	ctx.GatewayServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			http.Error(w, `{"error":"missing API key"}`, http.StatusUnauthorized)
			return
		}

		// Check if API key is valid
		ctx.mu.Lock()
		valid := ctx.APIKey != "" && apiKey == ctx.APIKey
		ctx.mu.Unlock()

		if !valid {
			http.Error(w, `{"error":"invalid API key"}`, http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"path":    r.URL.Path,
			"method":  r.Method,
			"message": "API call successful",
			"api_id":  ctx.APIID,
			"time":    time.Now().Unix(),
		})
	}))

	// Analytics server
	ctx.AnalyticsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+ctx.PublisherToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total_calls":   10,
			"successful":    8,
			"failed":        2,
			"avg_latency_ms": 45,
			"api_id":        ctx.APIID,
			"period":        "last_24h",
		})
	}))
}

func (ctx *E2EContext) Teardown() {
	if ctx.PublisherServer != nil {
		ctx.PublisherServer.Close()
	}
	if ctx.PortalServer != nil {
		ctx.PortalServer.Close()
	}
	if ctx.GatewayServer != nil {
		ctx.GatewayServer.Close()
	}
	if ctx.AnalyticsServer != nil {
		ctx.AnalyticsServer.Close()
	}
}

// ============================================================================
// Handler Methods
// ============================================================================

func (ctx *E2EContext) handleCreateAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Version     string            `json:"version"`
		BaseURL     string            `json:"base_url"`
		Type        string            `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	ctx.mu.Lock()
	ctx.APIID = fmt.Sprintf("api-%d", time.Now().Unix())
	apiID := ctx.APIID
	ctx.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          apiID,
		"name":        req.Name,
		"description": req.Description,
		"version":     req.Version,
		"base_url":    req.BaseURL,
		"type":        req.Type,
		"status":      "draft",
		"created_at":  time.Now().Format(time.RFC3339),
	})
}

func (ctx *E2EContext) handleGetAPI(w http.ResponseWriter, r *http.Request, id string) {
	ctx.mu.Lock()
	apiID := ctx.APIID
	ctx.mu.Unlock()

	if id != apiID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     apiID,
		"name":   "Test API",
		"status": "published",
	})
}

func (ctx *E2EContext) handleListAPIs(w http.ResponseWriter, r *http.Request) {
	ctx.mu.Lock()
	apiID := ctx.APIID
	ctx.mu.Unlock()

	var apis []map[string]interface{}
	if apiID != "" {
		apis = append(apis, map[string]interface{}{
			"id":     apiID,
			"name":   "Test API",
			"status": "published",
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": apis,
		"total": len(apis),
	})
}

func (ctx *E2EContext) handleDeleteAPI(w http.ResponseWriter, r *http.Request, id string) {
	ctx.mu.Lock()
	apiID := ctx.APIID
	ctx.mu.Unlock()

	if id != apiID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	ctx.mu.Lock()
	ctx.APIID = ""
	ctx.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (ctx *E2EContext) handleUpdateAPI(w http.ResponseWriter, r *http.Request, id string) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "status": "updated"})
}

func (ctx *E2EContext) handlePublishAPI(w http.ResponseWriter, r *http.Request, id string) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     id,
		"status": "published",
	})
}

func (ctx *E2EContext) handleCatalog(w http.ResponseWriter, r *http.Request) {
	ctx.mu.Lock()
	apiID := ctx.APIID
	ctx.mu.Unlock()

	var items []map[string]interface{}
	if apiID != "" {
		items = append(items, map[string]interface{}{
			"id":          apiID,
			"name":        "Test API",
			"description": "A test API",
			"version":     "1.0.0",
			"type":        "REST",
			"status":      "published",
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

func (ctx *E2EContext) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	ctx.mu.Lock()
	ctx.ApplicationID = fmt.Sprintf("app-%d", time.Now().Unix())
	appID := ctx.ApplicationID
	ctx.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     appID,
		"name":   req.Name,
		"status": "active",
	})
}

func (ctx *E2EContext) handleListApplications(w http.ResponseWriter, r *http.Request) {
	ctx.mu.Lock()
	appID := ctx.ApplicationID
	ctx.mu.Unlock()

	var items []map[string]interface{}
	if appID != "" {
		items = append(items, map[string]interface{}{
			"id":   appID,
			"name": "Test Application",
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

func (ctx *E2EContext) handleGetApplication(w http.ResponseWriter, r *http.Request, id string) {
	ctx.mu.Lock()
	appID := ctx.ApplicationID
	ctx.mu.Unlock()

	if id != appID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":   appID,
		"name": "Test Application",
	})
}

func (ctx *E2EContext) handleDeleteApplication(w http.ResponseWriter, r *http.Request, id string) {
	ctx.mu.Lock()
	appID := ctx.ApplicationID
	ctx.mu.Unlock()

	if id != appID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	ctx.mu.Lock()
	ctx.ApplicationID = ""
	ctx.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (ctx *E2EContext) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIID string `json:"api_id"`
		Plan  string `json:"plan"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	ctx.mu.Lock()
	ctx.SubscriptionID = fmt.Sprintf("sub-%d", time.Now().Unix())
	subID := ctx.SubscriptionID
	ctx.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":            subID,
		"api_id":        req.APIID,
		"plan":          req.Plan,
		"status":        "active",
	})
}

func (ctx *E2EContext) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx.mu.Lock()
	subID := ctx.SubscriptionID
	ctx.mu.Unlock()

	var items []map[string]interface{}
	if subID != "" {
		items = append(items, map[string]interface{}{"id": subID})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

func (ctx *E2EContext) handleDeleteSubscription(w http.ResponseWriter, r *http.Request, id string) {
	ctx.mu.Lock()
	subID := ctx.SubscriptionID
	ctx.mu.Unlock()

	if id != subID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	ctx.mu.Lock()
	ctx.SubscriptionID = ""
	ctx.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (ctx *E2EContext) handleGenerateKey(w http.ResponseWriter, r *http.Request) {
	ctx.mu.Lock()
	ctx.APIKey = fmt.Sprintf("vapim_%d", time.Now().Unix())
	key := ctx.APIKey
	ctx.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"api_key": key,
		"status":  "active",
	})
}

// ============================================================================
// Test Cases - Complete API Lifecycle
// ============================================================================

func TestE2E_CompleteLifecycle_GivenNewAPI_WhenFullLifecycle_ThenSuccess(t *testing.T) {
	ctx := NewE2EContext()
	ctx.Setup(t)
	defer ctx.Teardown()

	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Create API as publisher
	t.Run("Step 1: Create API", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"name":        "Payment API",
			"description": "Handles payment processing",
			"version":     "1.0.0",
			"base_url":    "https://payments.example.com",
			"type":        "REST",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", ctx.PublisherServer.URL+"/apis", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ctx.PublisherToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.NotEmpty(t, result["id"])
		assert.Equal(t, "Payment API", result["name"])
		assert.Equal(t, "draft", result["status"])

		ctx.mu.Lock()
		ctx.APIID = result["id"].(string)
		ctx.mu.Unlock()
	})

	// Step 2: Publish API
	t.Run("Step 2: Publish API", func(t *testing.T) {
		ctx.mu.Lock()
		apiID := ctx.APIID
		ctx.mu.Unlock()
		require.NotEmpty(t, apiID)

		reqBody := map[string]interface{}{"status": "published"}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/apis/%s", ctx.PublisherServer.URL, apiID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ctx.PublisherToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.Equal(t, "published", result["status"])
	})

	// Step 3: Browse APIs as subscriber
	t.Run("Step 3: Browse APIs", func(t *testing.T) {
		resp, err := http.Get(ctx.PortalServer.URL + "/catalog")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		items := result["items"].([]interface{})
		assert.GreaterOrEqual(t, len(items), 1)
	})

	// Step 4: Create application
	t.Run("Step 4: Create Application", func(t *testing.T) {
		reqBody := map[string]interface{}{"name": "My Payment App"}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", ctx.PortalServer.URL+"/applications", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ctx.SubscriberToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.NotEmpty(t, result["id"])
		assert.Equal(t, "active", result["status"])

		ctx.mu.Lock()
		ctx.ApplicationID = result["id"].(string)
		ctx.mu.Unlock()
	})

	// Step 5: Subscribe to API
	t.Run("Step 5: Subscribe to API", func(t *testing.T) {
		ctx.mu.Lock()
		apiID := ctx.APIID
		ctx.mu.Unlock()
		require.NotEmpty(t, apiID)

		reqBody := map[string]interface{}{
			"api_id": apiID,
			"plan":   "premium",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", ctx.PortalServer.URL+"/subscriptions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ctx.SubscriberToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.NotEmpty(t, result["id"])
		assert.Equal(t, "active", result["status"])

		ctx.mu.Lock()
		ctx.SubscriptionID = result["id"].(string)
		ctx.mu.Unlock()
	})

	// Step 6: Generate keys
	t.Run("Step 6: Generate Keys", func(t *testing.T) {
		reqBody := map[string]interface{}{"application_id": ctx.ApplicationID}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", ctx.PortalServer.URL+"/keys/generate", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ctx.SubscriberToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.NotEmpty(t, result["api_key"])
		assert.True(t, strings.HasPrefix(result["api_key"].(string), "vapim_"))

		ctx.mu.Lock()
		ctx.APIKey = result["api_key"].(string)
		ctx.mu.Unlock()
	})

	// Step 7: Call API through gateway
	t.Run("Step 7: Call API through Gateway", func(t *testing.T) {
		ctx.mu.Lock()
		apiKey := ctx.APIKey
		ctx.mu.Unlock()
		require.NotEmpty(t, apiKey)

		req, _ := http.NewRequest("GET", ctx.GatewayServer.URL+"/api/v1/payments", nil)
		req.Header.Set("X-API-Key", apiKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.Equal(t, "API call successful", result["message"])
		assert.Equal(t, "/api/v1/payments", result["path"])
	})

	// Step 8: Check analytics
	t.Run("Step 8: Check Analytics", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ctx.AnalyticsServer.URL+"/analytics?period=24h", nil)
		req.Header.Set("Authorization", "Bearer "+ctx.PublisherToken)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.GreaterOrEqual(t, result["total_calls"], float64(0))
		assert.Contains(t, result, "avg_latency_ms")
	})

	// Step 9: Unsubscribe
	t.Run("Step 9: Unsubscribe", func(t *testing.T) {
		ctx.mu.Lock()
		subID := ctx.SubscriptionID
		ctx.mu.Unlock()
		require.NotEmpty(t, subID)

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/subscriptions/%s", ctx.PortalServer.URL, subID), nil)
		req.Header.Set("Authorization", "Bearer "+ctx.SubscriberToken)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		ctx.mu.Lock()
		ctx.SubscriptionID = ""
		ctx.mu.Unlock()
	})

	// Step 10: Delete API
	t.Run("Step 10: Delete API", func(t *testing.T) {
		ctx.mu.Lock()
		apiID := ctx.APIID
		ctx.mu.Unlock()
		require.NotEmpty(t, apiID)

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/apis/%s", ctx.PublisherServer.URL, apiID), nil)
		req.Header.Set("Authorization", "Bearer "+ctx.PublisherToken)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		ctx.mu.Lock()
		ctx.APIID = ""
		ctx.mu.Unlock()
	})
}

func TestE2E_InvalidAuth_GivenWrongToken_WhenOperation_ThenUnauthorized(t *testing.T) {
	ctx := NewE2EContext()
	ctx.Setup(t)
	defer ctx.Teardown()

	client := &http.Client{Timeout: 10 * time.Second}

	tests := []struct {
		name    string
		method  string
		url     string
		body    string
		token   string
		expect  int
	}{
		{
			name:   "wrong publisher token",
			method: "POST",
			url:    "/apis",
			body:   `{"name":"Test"}`,
			token:  "wrong-token",
			expect: http.StatusUnauthorized,
		},
		{
			name:   "empty token",
			method: "POST",
			url:    "/apis",
			body:   `{"name":"Test"}`,
			token:  "",
			expect: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}

			req, _ := http.NewRequest(tt.method, ctx.PublisherServer.URL+tt.url, body)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expect, resp.StatusCode)
		})
	}
}

func TestE2E_ParallelWorkflows_GivenMultipleAPIs_WhenLifecycle_ThenAllSucceed(t *testing.T) {
	ctx := NewE2EContext()
	ctx.Setup(t)
	defer ctx.Teardown()

	client := &http.Client{Timeout: 10 * time.Second}

	const numAPIs = 5
	var wg sync.WaitGroup
	wg.Add(numAPIs)

	for i := 0; i < numAPIs; i++ {
		go func(idx int) {
			defer wg.Done()

			// Create API
			reqBody := map[string]interface{}{
				"name":     fmt.Sprintf("Parallel API %d", idx),
				"version":  "1.0.0",
				"base_url": fmt.Sprintf("https://api%d.example.com", idx),
				"type":     "REST",
			}
			body, _ := json.Marshal(reqBody)

			req, _ := http.NewRequest("POST", ctx.PublisherServer.URL+"/apis", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+ctx.PublisherToken)
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, http.StatusCreated, resp.StatusCode)
		}(i)
	}

	wg.Wait()

	// Verify all APIs are listed
	resp, err := http.Get(ctx.PublisherServer.URL + "/apis")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.GreaterOrEqual(t, int(result["total"].(float64)), numAPIs)
}
