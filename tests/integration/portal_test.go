package integration

import (
	"bytes"
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
// Portal Integration Test Infrastructure
// ============================================================================

// PortalServer simulates the developer portal
type PortalServer struct {
	Server          *httptest.Server
	catalog         []CatalogItem
	applications    map[string]Application
	subscriptions   map[string]Subscription
	mu              sync.RWMutex
}

// CatalogItem represents an API in the catalog
type CatalogItem struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	Owner       string            `json:"owner"`
	Tags        []string          `json:"tags"`
	Metadata    map[string]string `json:"metadata"`
	Rating      float64           `json:"rating"`
	Subscribers int               `json:"subscribers"`
	CreatedAt   time.Time         `json:"created_at"`
}

// Application represents a developer application
type Application struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Owner       string    `json:"owner"`
	Status      string    `json:"status"`
	APIKeys     []APIKey  `json:"api_keys"`
	CreatedAt   time.Time `json:"created_at"`
}

// APIKey represents an application API key
type APIKey struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Status string `json:"status"`
}

// Subscription represents an API subscription
type Subscription struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	APIID         string    `json:"api_id"`
	APIName       string    `json:"api_name"`
	Plan          string    `json:"plan"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// NewPortalServer creates a new portal test server
func NewPortalServer() *PortalServer {
	ps := &PortalServer{
		catalog:       make([]CatalogItem, 0),
		applications:  make(map[string]Application),
		subscriptions: make(map[string]Subscription),
	}

	// Seed catalog
	ps.catalog = append(ps.catalog, CatalogItem{
		ID: "api-1", Name: "Payment API", Description: "Handles payment processing",
		Version: "1.0.0", Type: "REST", Status: "published", Owner: "team-payments",
		Tags: []string{"finance", "payments"}, Rating: 4.5, Subscribers: 150,
		CreatedAt: time.Now().Add(-30 * 24 * time.Hour),
	})
	ps.catalog = append(ps.catalog, CatalogItem{
		ID: "api-2", Name: "User Service", Description: "User management and authentication",
		Version: "2.1.0", Type: "GraphQL", Status: "published", Owner: "team-auth",
		Tags: []string{"users", "auth"}, Rating: 4.8, Subscribers: 300,
		CreatedAt: time.Now().Add(-60 * 24 * time.Hour),
	})
	ps.catalog = append(ps.catalog, CatalogItem{
		ID: "api-3", Name: "Inventory API", Description: "Product inventory management",
		Version: "1.5.0", Type: "REST", Status: "published", Owner: "team-inventory",
		Tags: []string{"inventory", "products"}, Rating: 4.2, Subscribers: 75,
		CreatedAt: time.Now().Add(-15 * 24 * time.Hour),
	})
	ps.catalog = append(ps.catalog, CatalogItem{
		ID: "api-4", Name: "Notification API", Description: "Push and email notifications",
		Version: "3.0.0", Type: "REST", Status: "deprecated", Owner: "team-notify",
		Tags: []string{"notifications", "messaging"}, Rating: 3.8, Subscribers: 50,
		CreatedAt: time.Now().Add(-90 * 24 * time.Hour),
	})

	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/catalog" && r.Method == "GET":
			ps.handleCatalog(w, r)
		case strings.HasPrefix(r.URL.Path, "/catalog/") && r.Method == "GET":
			ps.handleCatalogItem(w, r, strings.TrimPrefix(r.URL.Path, "/catalog/"))
		case r.URL.Path == "/applications" && r.Method == "POST":
			ps.handleCreateApplication(w, r)
		case r.URL.Path == "/applications" && r.Method == "GET":
			ps.handleListApplications(w, r)
		case strings.HasPrefix(r.URL.Path, "/applications/") && r.Method == "GET":
			ps.handleGetApplication(w, r, strings.TrimPrefix(r.URL.Path, "/applications/"))
		case strings.HasPrefix(r.URL.Path, "/applications/") && r.Method == "PUT":
			ps.handleUpdateApplication(w, r, strings.TrimPrefix(r.URL.Path, "/applications/"))
		case strings.HasPrefix(r.URL.Path, "/applications/") && r.Method == "DELETE":
			ps.handleDeleteApplication(w, r, strings.TrimPrefix(r.URL.Path, "/applications/"))
		case r.URL.Path == "/subscriptions" && r.Method == "POST":
			ps.handleCreateSubscription(w, r)
		case r.URL.Path == "/subscriptions" && r.Method == "GET":
			ps.handleListSubscriptions(w, r)
		case strings.HasPrefix(r.URL.Path, "/subscriptions/") && r.Method == "DELETE":
			ps.handleDeleteSubscription(w, r, strings.TrimPrefix(r.URL.Path, "/subscriptions/"))
		case strings.HasPrefix(r.URL.Path, "/applications/") && strings.HasSuffix(r.URL.Path, "/keys") && r.Method == "POST":
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/applications/"), "/")
			ps.handleGenerateKey(w, r, parts[0])
		case r.URL.Path == "/search" && r.Method == "GET":
			ps.handleSearch(w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	return ps
}

func (ps *PortalServer) Close() {
	ps.Server.Close()
}

// ============================================================================
// Handlers
// ============================================================================

func (ps *PortalServer) handleCatalog(w http.ResponseWriter, r *http.Request) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	// Filter by type if specified
	apiType := r.URL.Query().Get("type")
	var items []CatalogItem
	for _, item := range ps.catalog {
		if item.Status == "published" {
			if apiType == "" || item.Type == apiType {
				items = append(items, item)
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

func (ps *PortalServer) handleCatalogItem(w http.ResponseWriter, r *http.Request, id string) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	for _, item := range ps.catalog {
		if item.ID == id {
			json.NewEncoder(w).Encode(item)
			return
		}
	}
	http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
}

func (ps *PortalServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(r.URL.Query().Get("q"))
	apiType := r.URL.Query().Get("type")

	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var items []CatalogItem
	for _, item := range ps.catalog {
		if item.Status != "published" {
			continue
		}
		if apiType != "" && item.Type != apiType {
			continue
		}
		if query == "" ||
			strings.Contains(strings.ToLower(item.Name), query) ||
			strings.Contains(strings.ToLower(item.Description), query) ||
			strings.Contains(strings.ToLower(item.ID), query) {
			items = append(items, item)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

func (ps *PortalServer) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Owner       string `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Owner == "" {
		http.Error(w, `{"error":"name and owner required"}`, http.StatusBadRequest)
		return
	}

	app := Application{
		ID:          fmt.Sprintf("app-%d", time.Now().UnixNano()),
		Name:        req.Name,
		Description: req.Description,
		Owner:       req.Owner,
		Status:      "active",
		CreatedAt:   time.Now(),
		APIKeys:     make([]APIKey, 0),
	}

	ps.mu.Lock()
	ps.applications[app.ID] = app
	ps.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(app)
}

func (ps *PortalServer) handleListApplications(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("owner")

	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var items []Application
	for _, app := range ps.applications {
		if owner == "" || app.Owner == owner {
			items = append(items, app)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

func (ps *PortalServer) handleGetApplication(w http.ResponseWriter, r *http.Request, id string) {
	ps.mu.RLock()
	app, ok := ps.applications[id]
	ps.mu.RUnlock()

	if !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(app)
}

func (ps *PortalServer) handleUpdateApplication(w http.ResponseWriter, r *http.Request, id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	app, ok := ps.applications[id]
	if !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Name != "" {
		app.Name = req.Name
	}
	if req.Description != "" {
		app.Description = req.Description
	}

	ps.applications[id] = app
	json.NewEncoder(w).Encode(app)
}

func (ps *PortalServer) handleDeleteApplication(w http.ResponseWriter, r *http.Request, id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if _, ok := ps.applications[id]; !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	delete(ps.applications, id)

	// Delete associated subscriptions
	for subID, sub := range ps.subscriptions {
		if sub.ApplicationID == id {
			delete(ps.subscriptions, subID)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (ps *PortalServer) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApplicationID string `json:"application_id"`
		APIID         string `json:"api_id"`
		Plan          string `json:"plan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Verify application exists
	ps.mu.RLock()
	_, appOk := ps.applications[req.ApplicationID]
	ps.mu.RUnlock()
	if !appOk {
		http.Error(w, `{"error":"application not found"}`, http.StatusBadRequest)
		return
	}

	// Find API name
	var apiName string
	ps.mu.RLock()
	for _, item := range ps.catalog {
		if item.ID == req.APIID {
			apiName = item.Name
			break
		}
	}
	ps.mu.RUnlock()

	if apiName == "" {
		http.Error(w, `{"error":"API not found"}`, http.StatusBadRequest)
		return
	}

	sub := Subscription{
		ID:            fmt.Sprintf("sub-%d", time.Now().UnixNano()),
		ApplicationID: req.ApplicationID,
		APIID:         req.APIID,
		APIName:       apiName,
		Plan:          req.Plan,
		Status:        "active",
		CreatedAt:     time.Now(),
	}

	ps.mu.Lock()
	ps.subscriptions[sub.ID] = sub
	ps.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(sub)
}

func (ps *PortalServer) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("application_id")

	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var items []Subscription
	for _, sub := range ps.subscriptions {
		if appID == "" || sub.ApplicationID == appID {
			items = append(items, sub)
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": len(items),
	})
}

func (ps *PortalServer) handleDeleteSubscription(w http.ResponseWriter, r *http.Request, id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if _, ok := ps.subscriptions[id]; !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	delete(ps.subscriptions, id)
	w.WriteHeader(http.StatusNoContent)
}

func (ps *PortalServer) handleGenerateKey(w http.ResponseWriter, r *http.Request, appID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	app, ok := ps.applications[appID]
	if !ok {
		http.Error(w, `{"error":"application not found"}`, http.StatusNotFound)
		return
	}

	key := APIKey{
		ID:     fmt.Sprintf("key-%d", time.Now().UnixNano()),
		Key:    fmt.Sprintf("vapim_%d", time.Now().UnixNano()),
		Status: "active",
	}
	app.APIKeys = append(app.APIKeys, key)
	ps.applications[appID] = app

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"api_key": key.Key,
		"id":      key.ID,
		"status":  key.Status,
	})
}

// ============================================================================
// TESTS
// ============================================================================

func TestPortal_Catalog_GivenPublishedAPIs_WhenBrowsed_ThenReturnsList(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, err := http.Get(portal.Server.URL + "/catalog")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	items := result["items"].([]interface{})
	assert.Equal(t, 3, len(items), "should only return published APIs")

	// Deprecated API should not be in catalog
	for _, item := range items {
		itemMap := item.(map[string]interface{})
		assert.NotEqual(t, "deprecated", itemMap["status"])
	}
}

func TestPortal_Catalog_GivenTypeFilter_WhenFiltered_ThenReturnsMatchingType(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, err := http.Get(portal.Server.URL + "/catalog?type=GraphQL")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	items := result["items"].([]interface{})
	assert.Equal(t, 1, len(items))
	assert.Equal(t, "User Service", items[0].(map[string]interface{})["name"])
}

func TestPortal_Catalog_GivenUnknownType_WhenFiltered_ThenReturnsEmpty(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, err := http.Get(portal.Server.URL + "/catalog?type=gRPC")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, 0, int(result["total"].(float64)))
}

func TestPortal_CatalogItem_GivenExistingAPI_WhenRetrieved_ThenReturnsDetails(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, err := http.Get(portal.Server.URL + "/catalog/api-1")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var item CatalogItem
	json.NewDecoder(resp.Body).Decode(&item)
	assert.Equal(t, "api-1", item.ID)
	assert.Equal(t, "Payment API", item.Name)
	assert.Equal(t, "Handles payment processing", item.Description)
	assert.Equal(t, "REST", item.Type)
	assert.Equal(t, 4.5, item.Rating)
	assert.Equal(t, 150, item.Subscribers)
}

func TestPortal_CatalogItem_GivenNonExistentAPI_WhenRetrieved_ThenReturns404(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, err := http.Get(portal.Server.URL + "/catalog/non-existent")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPortal_Search_GivenQuery_WhenSearched_ThenReturnsMatchingResults(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	tests := []struct {
		name         string
		query        string
		expectedCount int
		expectName   string
	}{
		{"search by name", "payment", 1, "Payment API"},
		{"search by description", "authentication", 1, "User Service"},
		{"search by partial match", "api", 3, ""},
		{"search case insensitive", "PAYMENT", 1, "Payment API"},
		{"search no match", "nonexistent", 0, ""},
		{"empty query returns all", "", 3, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(portal.Server.URL + "/search?q=" + tt.query)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var result map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&result)
			items := result["items"].([]interface{})
			assert.Equal(t, tt.expectedCount, len(items))
			if tt.expectName != "" && len(items) > 0 {
				assert.Equal(t, tt.expectName, items[0].(map[string]interface{})["name"])
			}
		})
	}
}

func TestPortal_Search_GivenTypeAndQuery_WhenSearched_ThenReturnsFilteredResults(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, err := http.Get(portal.Server.URL + "/search?q=api&type=REST")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	items := result["items"].([]interface{})

	// Should only return REST APIs matching "api"
	for _, item := range items {
		itemMap := item.(map[string]interface{})
		assert.Equal(t, "REST", itemMap["type"])
	}
}

// ============================================================================
// Application Management Tests
// ============================================================================

func TestPortal_CreateApplication_GivenValidData_WhenCreated_ThenReturnsApplication(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	reqBody := map[string]interface{}{
		"name":        "My App",
		"description": "My test application",
		"owner":       "user-1",
	}
	body, _ := json.Marshal(reqBody)

	resp, err := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	assert.NotEmpty(t, app.ID)
	assert.Equal(t, "My App", app.Name)
	assert.Equal(t, "My test application", app.Description)
	assert.Equal(t, "user-1", app.Owner)
	assert.Equal(t, "active", app.Status)
}

func TestPortal_CreateApplication_GivenMissingFields_WhenCreated_ThenReturnsError(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{"missing name", map[string]interface{}{"owner": "user-1"}},
		{"missing owner", map[string]interface{}{"name": "My App"}},
		{"empty body", map[string]interface{}{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			resp, err := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestPortal_ListApplications_GivenExistingApps_WhenListed_ThenReturnsList(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create apps
	for i := 0; i < 3; i++ {
		reqBody := map[string]interface{}{
			"name":  fmt.Sprintf("App %d", i),
			"owner": "user-1",
		}
		body, _ := json.Marshal(reqBody)
		http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	}

	// Create app for different owner
	reqBody := map[string]interface{}{"name": "Other App", "owner": "user-2"}
	body, _ := json.Marshal(reqBody)
	http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))

	// List all
	resp, err := http.Get(portal.Server.URL + "/applications")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, 4, int(result["total"].(float64)))

	// Filter by owner
	resp, err = http.Get(portal.Server.URL + "/applications?owner=user-1")
	require.NoError(t, err)
	defer resp.Body.Close()

	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, 3, int(result["total"].(float64)))
}

func TestPortal_GetApplication_GivenExistingApp_WhenRetrieved_ThenReturnsApp(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create
	reqBody := map[string]interface{}{"name": "Test App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var created Application
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Get
	resp, err := http.Get(portal.Server.URL + "/applications/" + created.ID)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	assert.Equal(t, created.ID, app.ID)
	assert.Equal(t, "Test App", app.Name)
}

func TestPortal_GetApplication_GivenNonExistent_WhenRetrieved_ThenReturns404(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, err := http.Get(portal.Server.URL + "/applications/non-existent")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPortal_UpdateApplication_GivenExistingApp_WhenUpdated_ThenChangesApplied(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create
	reqBody := map[string]interface{}{"name": "Original", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var created Application
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Update
	updateBody := map[string]interface{}{"name": "Updated", "description": "New description"}
	body, _ = json.Marshal(updateBody)
	req, _ := http.NewRequest("PUT", portal.Server.URL+"/applications/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify
	resp, _ = http.Get(portal.Server.URL + "/applications/" + created.ID)
	var updated Application
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()
	assert.Equal(t, "Updated", updated.Name)
	assert.Equal(t, "New description", updated.Description)
}

func TestPortal_DeleteApplication_GivenExistingApp_WhenDeleted_ThenRemoved(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create
	reqBody := map[string]interface{}{"name": "To Delete", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var created Application
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Delete
	req, _ := http.NewRequest("DELETE", portal.Server.URL+"/applications/"+created.ID, nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify
	resp, _ = http.Get(portal.Server.URL + "/applications/" + created.ID)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPortal_DeleteApplication_GivenNonExistent_WhenDeleted_ThenReturns404(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	req, _ := http.NewRequest("DELETE", portal.Server.URL+"/applications/non-existent", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ============================================================================
// Subscription Tests
// ============================================================================

func TestPortal_Subscribe_GivenValidData_WhenSubscribed_ThenReturnsSubscription(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create application
	reqBody := map[string]interface{}{"name": "My App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()

	// Subscribe
	subBody := map[string]interface{}{
		"application_id": app.ID,
		"api_id":         "api-1",
		"plan":           "premium",
	}
	body, _ = json.Marshal(subBody)
	resp, err := http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var sub Subscription
	json.NewDecoder(resp.Body).Decode(&sub)
	assert.NotEmpty(t, sub.ID)
	assert.Equal(t, app.ID, sub.ApplicationID)
	assert.Equal(t, "api-1", sub.APIID)
	assert.Equal(t, "Payment API", sub.APIName)
	assert.Equal(t, "premium", sub.Plan)
	assert.Equal(t, "active", sub.Status)
}

func TestPortal_Subscribe_GivenInvalidApplication_WhenSubscribed_ThenReturnsError(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	subBody := map[string]interface{}{
		"application_id": "non-existent",
		"api_id":         "api-1",
		"plan":           "premium",
	}
	body, _ := json.Marshal(subBody)
	resp, _ := http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPortal_Subscribe_GivenInvalidAPI_WhenSubscribed_ThenReturnsError(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create app
	reqBody := map[string]interface{}{"name": "My App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()

	// Subscribe to non-existent API
	subBody := map[string]interface{}{
		"application_id": app.ID,
		"api_id":         "non-existent-api",
		"plan":           "premium",
	}
	body, _ = json.Marshal(subBody)
	resp, _ = http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPortal_ListSubscriptions_GivenExistingSubs_WhenListed_ThenReturnsList(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create app
	reqBody := map[string]interface{}{"name": "My App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()

	// Subscribe to multiple APIs
	for _, apiID := range []string{"api-1", "api-2", "api-3"} {
		subBody := map[string]interface{}{
			"application_id": app.ID,
			"api_id":         apiID,
			"plan":           "basic",
		}
		body, _ := json.Marshal(subBody)
		http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))
	}

	// List subscriptions for app
	resp, err := http.Get(portal.Server.URL + "/subscriptions?application_id=" + app.ID)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, 3, int(result["total"].(float64)))
}

func TestPortal_DeleteSubscription_GivenExistingSub_WhenDeleted_ThenRemoved(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create app and subscribe
	reqBody := map[string]interface{}{"name": "My App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()

	subBody := map[string]interface{}{
		"application_id": app.ID,
		"api_id":         "api-1",
		"plan":           "premium",
	}
	body, _ = json.Marshal(subBody)
	resp, _ = http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))
	var sub Subscription
	json.NewDecoder(resp.Body).Decode(&sub)
	resp.Body.Close()

	// Delete
	req, _ := http.NewRequest("DELETE", portal.Server.URL+"/subscriptions/"+sub.ID, nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestPortal_GenerateKey_GivenApplication_WhenGenerated_ThenReturnsKey(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create app
	reqBody := map[string]interface{}{"name": "My App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()

	// Generate key
	resp, err := http.Post(portal.Server.URL+"/applications/"+app.ID+"/keys", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.NotEmpty(t, result["api_key"])
	assert.True(t, strings.HasPrefix(result["api_key"].(string), "vapim_"))
	assert.Equal(t, "active", result["status"])
}

func TestPortal_GenerateKey_GivenNonExistentApp_WhenGenerated_ThenReturns404(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, _ := http.Post(portal.Server.URL+"/applications/non-existent/keys", "application/json", nil)
	resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ============================================================================
// Full Portal Workflow Test
// ============================================================================

func TestPortal_FullWorkflow_GivenSubscriber_WhenCompleteFlow_ThenSuccess(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	client := &http.Client{Timeout: 10 * time.Second}

	// Step 1: Browse catalog
	resp, err := http.Get(portal.Server.URL + "/catalog")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Step 2: Search for APIs
	resp, err = http.Get(portal.Server.URL + "/search?q=payment")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Step 3: Get API details
	resp, err = http.Get(portal.Server.URL + "/catalog/api-1")
	require.NoError(t, err)
	var apiDetail CatalogItem
	json.NewDecoder(resp.Body).Decode(&apiDetail)
	resp.Body.Close()
	assert.Equal(t, "Payment API", apiDetail.Name)

	// Step 4: Create application
	reqBody := map[string]interface{}{
		"name":        "Payment Integration",
		"description": "Integration with payment API",
		"owner":       "subscriber-1",
	}
	body, _ := json.Marshal(reqBody)
	resp, err = http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.NotEmpty(t, app.ID)

	// Step 5: Subscribe to API
	subBody := map[string]interface{}{
		"application_id": app.ID,
		"api_id":         "api-1",
		"plan":           "premium",
	}
	body, _ = json.Marshal(subBody)
	resp, err = http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var sub Subscription
	json.NewDecoder(resp.Body).Decode(&sub)
	resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "active", sub.Status)

	// Step 6: Generate API key
	resp, err = http.Post(portal.Server.URL+"/applications/"+app.ID+"/keys", "application/json", nil)
	require.NoError(t, err)
	var keyResult map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&keyResult)
	resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.NotEmpty(t, keyResult["api_key"])

	// Step 7: List subscriptions
	resp, err = http.Get(portal.Server.URL + "/subscriptions?application_id=" + app.ID)
	require.NoError(t, err)
	var subList map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&subList)
	resp.Body.Close()
	assert.Equal(t, 1, int(subList["total"].(float64)))

	// Step 8: Delete subscription
	req, _ := http.NewRequest("DELETE", portal.Server.URL+"/subscriptions/"+sub.ID, nil)
	resp, _ = client.Do(req)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify subscription is gone
	resp, _ = http.Get(portal.Server.URL + "/subscriptions?application_id=" + app.ID)
	json.NewDecoder(resp.Body).Decode(&subList)
	resp.Body.Close()
	assert.Equal(t, 0, int(subList["total"].(float64)))

	// Step 9: Delete application
	req, _ = http.NewRequest("DELETE", portal.Server.URL+"/applications/"+app.ID, nil)
	resp, _ = client.Do(req)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify application is gone
	resp, _ = http.Get(portal.Server.URL + "/applications/" + app.ID)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ============================================================================
// Concurrent Tests
// ============================================================================

func TestPortal_Concurrent_GivenMultipleSubscribers_WhenOperating_ThenNoRace(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	const numSubscribers = 20
	var wg sync.WaitGroup
	wg.Add(numSubscribers)

	for i := 0; i < numSubscribers; i++ {
		go func(id int) {
			defer wg.Done()

			// Create app
			reqBody := map[string]interface{}{
				"name":  fmt.Sprintf("App %d", id),
				"owner": fmt.Sprintf("user-%d", id),
			}
			body, _ := json.Marshal(reqBody)
			resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
			resp.Body.Close()

			// Subscribe
			subBody := map[string]interface{}{
				"application_id": fmt.Sprintf("app-%d", id),
				"api_id":         fmt.Sprintf("api-%d", id%4+1),
				"plan":           "basic",
			}
			body, _ = json.Marshal(subBody)
			resp, _ = http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))
			resp.Body.Close()

			// Browse catalog
			resp, _ = http.Get(portal.Server.URL + "/catalog")
			resp.Body.Close()
		}(i)
	}

	wg.Wait()
}

// ============================================================================
// Edge Case Tests
// ============================================================================

func TestPortal_EmptyCatalog_GivenNoPublishedAPIs_WhenBrowsed_ThenReturnsEmpty(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Remove all published items by marking as deleted (simulated)
	portal.mu.Lock()
	portal.catalog = portal.catalog[:0] // Clear catalog
	portal.mu.Unlock()

	resp, err := http.Get(portal.Server.URL + "/catalog")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, 0, int(result["total"].(float64)))
}

func TestPortal_InvalidJSON_GivenMalformedBody_WhenCreatingApp_ThenReturnsError(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", strings.NewReader(`{invalid json`))
	resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPortal_DeleteAppWithSubscriptions_GivenAppWithSubs_WhenDeleted_ThenSubsAlsoDeleted(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create app
	reqBody := map[string]interface{}{"name": "My App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()

	// Subscribe
	subBody := map[string]interface{}{
		"application_id": app.ID,
		"api_id":         "api-1",
		"plan":           "premium",
	}
	body, _ = json.Marshal(subBody)
	http.Post(portal.Server.URL+"/subscriptions", "application/json", bytes.NewReader(body))

	// Verify subscription exists
	resp, _ = http.Get(portal.Server.URL + "/subscriptions?application_id=" + app.ID)
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	assert.Equal(t, 1, int(result["total"].(float64)))

	// Delete app
	req, _ := http.NewRequest("DELETE", portal.Server.URL+"/applications/"+app.ID, nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	// Verify subscriptions are also deleted
	resp, _ = http.Get(portal.Server.URL + "/subscriptions?application_id=" + app.ID)
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	assert.Equal(t, 0, int(result["total"].(float64)))
}

func TestPortal_GenerateMultipleKeys_GivenApp_WhenMultipleGenerated_ThenAllStored(t *testing.T) {
	portal := NewPortalServer()
	defer portal.Close()

	// Create app
	reqBody := map[string]interface{}{"name": "My App", "owner": "user-1"}
	body, _ := json.Marshal(reqBody)
	resp, _ := http.Post(portal.Server.URL+"/applications", "application/json", bytes.NewReader(body))
	var app Application
	json.NewDecoder(resp.Body).Decode(&app)
	resp.Body.Close()

	// Generate 5 keys
	for i := 0; i < 5; i++ {
		resp, _ := http.Post(portal.Server.URL+"/applications/"+app.ID+"/keys", "application/json", nil)
		resp.Body.Close()
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	// Verify all keys stored
	resp, _ = http.Get(portal.Server.URL + "/applications/" + app.ID)
	var result Application
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	assert.Len(t, result.APIKeys, 5)
}
