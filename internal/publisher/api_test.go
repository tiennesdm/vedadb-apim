package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// API Publisher Implementation
// ============================================================================

// API represents an API definition
type API struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	BaseURL     string            `json:"base_url"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	Owner       string            `json:"owner"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Store defines the interface for API persistence
type Store interface {
	Create(ctx context.Context, key string, value interface{}) error
	Get(ctx context.Context, key string, dest interface{}) error
	Update(ctx context.Context, key string, value interface{}) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, page, pageSize int) (*ListResult, error)
	Search(ctx context.Context, query string, filters map[string]string, page, pageSize int) (*ListResult, error)
}

// ListResult holds paginated results
type ListResult struct {
	Items      []json.RawMessage `json:"items"`
	Total      int               `json:"total"`
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
	TotalPages int               `json:"total_pages"`
}

// MockStore is a mock implementation of Store for testing
type MockStore struct {
	data   map[string][]byte
	mu     sync.RWMutex
	errors map[string]error
}

// NewMockStore creates a new mock store
func NewMockStore() *MockStore {
	return &MockStore{
		data:   make(map[string][]byte),
		errors: make(map[string]error),
	}
}

func (m *MockStore) SetError(op string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[op] = err
}

func (m *MockStore) getError(op string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.errors[op]
}

func (m *MockStore) Create(ctx context.Context, key string, value interface{}) error {
	if err := m.getError("create"); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, _ := json.Marshal(value)
	m.data[key] = data
	return nil
}

func (m *MockStore) Get(ctx context.Context, key string, dest interface{}) error {
	if err := m.getError("get"); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.data[key]
	if !ok {
		return errors.New("not found")
	}
	return json.Unmarshal(data, dest)
}

func (m *MockStore) Update(ctx context.Context, key string, value interface{}) error {
	if err := m.getError("update"); err != nil {
		return err
	}
	return m.Create(ctx, key, value)
}

func (m *MockStore) Delete(ctx context.Context, key string) error {
	if err := m.getError("delete"); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *MockStore) List(ctx context.Context, page, pageSize int) (*ListResult, error) {
	if err := m.getError("list"); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	items := make([]json.RawMessage, 0, len(m.data))
	for _, data := range m.data {
		items = append(items, data)
	}

	return &ListResult{
		Items:    items,
		Total:    len(items),
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (m *MockStore) Search(ctx context.Context, query string, filters map[string]string, page, pageSize int) (*ListResult, error) {
	if err := m.getError("search"); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []json.RawMessage
	for _, data := range m.data {
		var api API
		json.Unmarshal(data, &api)

		// Apply query filter
		if query != "" {
			match := strings.Contains(strings.ToLower(api.Name), strings.ToLower(query)) ||
				strings.Contains(strings.ToLower(api.Description), strings.ToLower(query)) ||
				strings.Contains(strings.ToLower(api.ID), strings.ToLower(query))
			if !match {
				continue
			}
		}

		// Apply field filters
		match := true
		for field, value := range filters {
			switch field {
			case "type":
				if api.Type != value {
					match = false
				}
			case "status":
				if api.Status != value {
					match = false
				}
			case "owner":
				if api.Owner != value {
					match = false
				}
			}
		}
		if !match {
			continue
		}

		items = append(items, data)
	}

	return &ListResult{
		Items:    items,
		Total:    len(items),
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// APIManager handles API CRUD operations
type APIManager struct {
	store Store
}

// NewAPIManager creates a new API manager
func NewAPIManager(store Store) *APIManager {
	return &APIManager{store: store}
}

// CreateAPI creates a new API
func (m *APIManager) CreateAPI(ctx context.Context, api *API) error {
	if api.ID == "" {
		return errors.New("API ID is required")
	}
	if api.Name == "" {
		return errors.New("API name is required")
	}
	if api.Version == "" {
		api.Version = "1.0.0"
	}
	if api.Status == "" {
		api.Status = "draft"
	}
	if api.Type == "" {
		api.Type = "REST"
	}

	api.CreatedAt = time.Now()
	api.UpdatedAt = time.Now()

	return m.store.Create(ctx, "api:"+api.ID, api)
}

// GetAPI retrieves an API by ID
func (m *APIManager) GetAPI(ctx context.Context, id string) (*API, error) {
	if id == "" {
		return nil, errors.New("API ID is required")
	}

	var api API
	if err := m.store.Get(ctx, "api:"+id, &api); err != nil {
		return nil, fmt.Errorf("API not found: %w", err)
	}
	return &api, nil
}

// ListAPIs returns a paginated list of APIs
func (m *APIManager) ListAPIs(ctx context.Context, page, pageSize int) (*ListResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	return m.store.List(ctx, page, pageSize)
}

// SearchAPIs searches for APIs
func (m *APIManager) SearchAPIs(ctx context.Context, query string, filters map[string]string, page, pageSize int) (*ListResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	return m.store.Search(ctx, query, filters, page, pageSize)
}

// UpdateAPI updates an existing API
func (m *APIManager) UpdateAPI(ctx context.Context, id string, updates *API) error {
	if id == "" {
		return errors.New("API ID is required")
	}

	existing, err := m.GetAPI(ctx, id)
	if err != nil {
		return err
	}

	// Apply updates
	if updates.Name != "" {
		existing.Name = updates.Name
	}
	if updates.Description != "" {
		existing.Description = updates.Description
	}
	if updates.Version != "" {
		existing.Version = updates.Version
	}
	if updates.BaseURL != "" {
		existing.BaseURL = updates.BaseURL
	}
	if updates.Status != "" {
		existing.Status = updates.Status
	}
	if len(updates.Tags) > 0 {
		existing.Tags = updates.Tags
	}
	if len(updates.Metadata) > 0 {
		existing.Metadata = updates.Metadata
	}
	if updates.Owner != "" {
		existing.Owner = updates.Owner
	}

	existing.UpdatedAt = time.Now()

	return m.store.Update(ctx, "api:"+id, existing)
}

// DeleteAPI deletes an API by ID
func (m *APIManager) DeleteAPI(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("API ID is required")
	}

	// Verify it exists
	if _, err := m.GetAPI(ctx, id); err != nil {
		return err
	}

	return m.store.Delete(ctx, "api:"+id)
}

// ============================================================================
// TESTS
// ============================================================================

func TestAPIManager_CreateAPI_GivenValidAPI_WhenCreated_ThenStored(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)

	tests := []struct {
		name string
		api  *API
	}{
		{
			name: "minimal API",
			api: &API{
				ID:   "api-1",
				Name: "Test API",
			},
		},
		{
			name: "full API",
			api: &API{
				ID:          "api-2",
				Name:        "Full API",
				Description: "A comprehensive API",
				Version:     "2.0.0",
				BaseURL:     "https://api.example.com",
				Type:        "GraphQL",
				Status:      "published",
				Owner:       "team-a",
				Tags:        []string{"v1", "public"},
				Metadata:    map[string]string{"env": "production"},
			},
		},
		{
			name: "API with defaults",
			api: &API{
				ID:   "api-3",
				Name: "Default API",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := manager.CreateAPI(ctx, tt.api)
			require.NoError(t, err)

			// Verify it was stored
			var stored API
			err = store.Get(ctx, "api:"+tt.api.ID, &stored)
			require.NoError(t, err)
			assert.Equal(t, tt.api.ID, stored.ID)
			assert.Equal(t, tt.api.Name, stored.Name)
			assert.NotZero(t, stored.CreatedAt)
			assert.NotZero(t, stored.UpdatedAt)
			assert.False(t, stored.CreatedAt.IsZero())
		})
	}
}

func TestAPIManager_CreateAPI_GivenDefaults_WhenCreated_ThenSetsDefaults(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	api := &API{ID: "api-1", Name: "Test API"}
	err := manager.CreateAPI(ctx, api)
	require.NoError(t, err)

	stored, err := manager.GetAPI(ctx, "api-1")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", stored.Version)
	assert.Equal(t, "draft", stored.Status)
	assert.Equal(t, "REST", stored.Type)
}

func TestAPIManager_CreateAPI_GivenInvalidInput_WhenCreated_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	tests := []struct {
		name   string
		api    *API
		errMsg string
	}{
		{
			name:   "empty id",
			api:    &API{Name: "Test"},
			errMsg: "API ID is required",
		},
		{
			name:   "empty name",
			api:    &API{ID: "api-1"},
			errMsg: "API name is required",
		},
		{
			name:   "both empty",
			api:    &API{},
			errMsg: "API ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.CreateAPI(ctx, tt.api)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestAPIManager_CreateAPI_GivenStoreError_WhenCreated_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	store.SetError("create", errors.New("connection lost"))
	manager := NewAPIManager(store)
	ctx := context.Background()

	api := &API{ID: "api-1", Name: "Test API"}
	err := manager.CreateAPI(ctx, api)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection lost")
}

func TestAPIManager_GetAPI_GivenExistingAPI_WhenRetrieved_ThenReturnsAPI(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create API
	api := &API{
		ID:          "api-1",
		Name:        "Test API",
		Description: "Test Description",
		Version:     "1.0.0",
		BaseURL:     "https://api.example.com",
		Type:        "REST",
		Status:      "published",
		Owner:       "team-a",
		Tags:        []string{"v1", "public"},
	}
	err := manager.CreateAPI(ctx, api)
	require.NoError(t, err)

	// Retrieve
	retrieved, err := manager.GetAPI(ctx, "api-1")
	require.NoError(t, err)
	assert.Equal(t, "api-1", retrieved.ID)
	assert.Equal(t, "Test API", retrieved.Name)
	assert.Equal(t, "Test Description", retrieved.Description)
	assert.Equal(t, "1.0.0", retrieved.Version)
	assert.Equal(t, "https://api.example.com", retrieved.BaseURL)
	assert.Equal(t, "REST", retrieved.Type)
	assert.Equal(t, "published", retrieved.Status)
	assert.Equal(t, "team-a", retrieved.Owner)
	assert.Equal(t, []string{"v1", "public"}, retrieved.Tags)
}

func TestAPIManager_GetAPI_GivenNonExistent_WhenRetrieved_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	_, err := manager.GetAPI(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIManager_GetAPI_GivenEmptyID_WhenRetrieved_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	_, err := manager.GetAPI(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestAPIManager_GetAPI_GivenStoreError_WhenRetrieved_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	store.SetError("get", errors.New("timeout"))
	manager := NewAPIManager(store)
	ctx := context.Background()

	_, err := manager.GetAPI(ctx, "api-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestAPIManager_ListAPIs_GivenMultipleAPIs_WhenListed_ThenReturnsPaginated(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create 5 APIs
	for i := 1; i <= 5; i++ {
		api := &API{
			ID:   fmt.Sprintf("api-%d", i),
			Name: fmt.Sprintf("Test API %d", i),
		}
		err := manager.CreateAPI(ctx, api)
		require.NoError(t, err)
	}

	result, err := manager.ListAPIs(ctx, 1, 10)
	require.NoError(t, err)
	assert.Len(t, result.Items, 5)
	assert.Equal(t, 5, result.Total)
	assert.Equal(t, 1, result.Page)
	assert.Equal(t, 10, result.PageSize)
}

func TestAPIManager_ListAPIs_GivenEmptyStore_WhenListed_ThenReturnsEmpty(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	result, err := manager.ListAPIs(ctx, 1, 10)
	require.NoError(t, err)
	assert.Len(t, result.Items, 0)
	assert.Equal(t, 0, result.Total)
}

func TestAPIManager_ListAPIs_GivenInvalidPagination_WhenListed_ThenUsesDefaults(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	result, err := manager.ListAPIs(ctx, -1, -5)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Page)
	assert.Equal(t, 10, result.PageSize)
}

func TestAPIManager_ListAPIs_GivenStoreError_WhenListed_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	store.SetError("list", errors.New("database error"))
	manager := NewAPIManager(store)
	ctx := context.Background()

	_, err := manager.ListAPIs(ctx, 1, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database error")
}

func TestAPIManager_SearchAPIs_GivenQuery_WhenSearched_ThenReturnsMatchingResults(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create APIs
	apis := []*API{
		{ID: "api-1", Name: "Payment API", Description: "Handles payments", Type: "REST", Status: "published", Owner: "team-a"},
		{ID: "api-2", Name: "User Service", Description: "Manages users", Type: "GraphQL", Status: "draft", Owner: "team-b"},
		{ID: "api-3", Name: "Payment Gateway", Description: "Payment processing", Type: "REST", Status: "published", Owner: "team-a"},
		{ID: "api-4", Name: "Order API", Description: "Order management", Type: "REST", Status: "draft", Owner: "team-c"},
	}

	for _, api := range apis {
		err := manager.CreateAPI(ctx, api)
		require.NoError(t, err)
	}

	tests := []struct {
		name          string
		query         string
		filters       map[string]string
		expectedCount int
	}{
		{
			name:          "search by name",
			query:         "Payment",
			filters:       nil,
			expectedCount: 2,
		},
		{
			name:          "search by description",
			query:         "management",
			filters:       nil,
			expectedCount: 1,
		},
		{
			name:          "filter by type",
			query:         "",
			filters:       map[string]string{"type": "REST"},
			expectedCount: 3,
		},
		{
			name:          "filter by status",
			query:         "",
			filters:       map[string]string{"status": "published"},
			expectedCount: 2,
		},
		{
			name:          "filter by owner",
			query:         "",
			filters:       map[string]string{"owner": "team-a"},
			expectedCount: 2,
		},
		{
			name:          "query and filter combined",
			query:         "Payment",
			filters:       map[string]string{"type": "REST"},
			expectedCount: 1,
		},
		{
			name:          "empty query returns all",
			query:         "",
			filters:       nil,
			expectedCount: 4,
		},
		{
			name:          "no matches",
			query:         "NonExistent",
			filters:       nil,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := manager.SearchAPIs(ctx, tt.query, tt.filters, 1, 10)
			require.NoError(t, err)
			assert.Len(t, result.Items, tt.expectedCount)
		})
	}
}

func TestAPIManager_SearchAPIs_GivenStoreError_WhenSearched_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	store.SetError("search", errors.New("search failed"))
	manager := NewAPIManager(store)
	ctx := context.Background()

	_, err := manager.SearchAPIs(ctx, "test", nil, 1, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "search failed")
}

func TestAPIManager_UpdateAPI_GivenExistingAPI_WhenUpdated_ThenChangesApplied(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create original
	original := &API{
		ID:          "api-1",
		Name:        "Original Name",
		Description: "Original Description",
		Version:     "1.0.0",
		BaseURL:     "https://old.example.com",
		Status:      "draft",
		Owner:       "old-owner",
		Tags:        []string{"old"},
		Metadata:    map[string]string{"env": "dev"},
	}
	err := manager.CreateAPI(ctx, original)
	require.NoError(t, err)

	originalCreatedAt := original.CreatedAt

	// Update
	updates := &API{
		Name:        "Updated Name",
		Description: "Updated Description",
		Version:     "2.0.0",
		BaseURL:     "https://new.example.com",
		Status:      "published",
		Owner:       "new-owner",
		Tags:        []string{"new", "updated"},
		Metadata:    map[string]string{"env": "prod"},
	}

	err = manager.UpdateAPI(ctx, "api-1", updates)
	require.NoError(t, err)

	// Verify
	updated, err := manager.GetAPI(ctx, "api-1")
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, "Updated Description", updated.Description)
	assert.Equal(t, "2.0.0", updated.Version)
	assert.Equal(t, "https://new.example.com", updated.BaseURL)
	assert.Equal(t, "published", updated.Status)
	assert.Equal(t, "new-owner", updated.Owner)
	assert.Equal(t, []string{"new", "updated"}, updated.Tags)
	assert.Equal(t, map[string]string{"env": "prod"}, updated.Metadata)
	assert.Equal(t, originalCreatedAt, updated.CreatedAt) // CreatedAt should not change
	assert.True(t, updated.UpdatedAt.After(originalCreatedAt))
}

func TestAPIManager_UpdateAPI_GivenPartialUpdate_WhenUpdated_ThenOnlySpecifiedFieldsChange(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create original
	original := &API{
		ID:          "api-1",
		Name:        "Original Name",
		Description: "Original Description",
		Version:     "1.0.0",
		BaseURL:     "https://example.com",
		Status:      "draft",
	}
	err := manager.CreateAPI(ctx, original)
	require.NoError(t, err)

	// Update only name
	updates := &API{Name: "Updated Name"}
	err = manager.UpdateAPI(ctx, "api-1", updates)
	require.NoError(t, err)

	// Verify
	updated, err := manager.GetAPI(ctx, "api-1")
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", updated.Name)
	assert.Equal(t, "Original Description", updated.Description) // Unchanged
	assert.Equal(t, "1.0.0", updated.Version)                     // Unchanged
}

func TestAPIManager_UpdateAPI_GivenNonExistent_WhenUpdated_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	err := manager.UpdateAPI(ctx, "non-existent", &API{Name: "Update"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIManager_UpdateAPI_GivenEmptyID_WhenUpdated_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	err := manager.UpdateAPI(ctx, "", &API{Name: "Update"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestAPIManager_UpdateAPI_GivenStoreError_WhenUpdated_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create first
	err := manager.CreateAPI(ctx, &API{ID: "api-1", Name: "Test"})
	require.NoError(t, err)

	// Then set error on update
	store.SetError("update", errors.New("write failed"))

	err = manager.UpdateAPI(ctx, "api-1", &API{Name: "Updated"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")
}

func TestAPIManager_DeleteAPI_GivenExistingAPI_WhenDeleted_ThenRemoved(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create
	err := manager.CreateAPI(ctx, &API{ID: "api-1", Name: "Test API"})
	require.NoError(t, err)

	// Verify exists
	_, err = manager.GetAPI(ctx, "api-1")
	require.NoError(t, err)

	// Delete
	err = manager.DeleteAPI(ctx, "api-1")
	require.NoError(t, err)

	// Verify gone
	_, err = manager.GetAPI(ctx, "api-1")
	assert.Error(t, err)
}

func TestAPIManager_DeleteAPI_GivenNonExistent_WhenDeleted_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	err := manager.DeleteAPI(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIManager_DeleteAPI_GivenEmptyID_WhenDeleted_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	err := manager.DeleteAPI(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestAPIManager_DeleteAPI_GivenStoreError_WhenDeleted_ThenReturnsError(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	// Create first
	err := manager.CreateAPI(ctx, &API{ID: "api-1", Name: "Test"})
	require.NoError(t, err)

	// Set error on delete
	store.SetError("delete", errors.New("delete failed"))

	err = manager.DeleteAPI(ctx, "api-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

func TestAPIManager_ConcurrentAccess_GivenMultipleGoroutines_WhenOperating_ThenNoRace(t *testing.T) {
	store := NewMockStore()
	manager := NewAPIManager(store)
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(4)

	// Concurrent creates
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			api := &API{
				ID:   fmt.Sprintf("concurrent-api-%d", i),
				Name: fmt.Sprintf("API %d", i),
			}
			manager.CreateAPI(ctx, api)
		}
	}()

	// Concurrent reads
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			manager.GetAPI(ctx, fmt.Sprintf("concurrent-api-%d", i))
		}
	}()

	// Concurrent updates
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			manager.UpdateAPI(ctx, fmt.Sprintf("concurrent-api-%d", i), &API{Name: fmt.Sprintf("Updated %d", i)})
		}
	}()

	// Concurrent lists
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			manager.ListAPIs(ctx, 1, 10)
		}
	}()

	wg.Wait()
}
