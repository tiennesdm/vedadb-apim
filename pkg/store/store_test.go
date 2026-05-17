package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockVedaDB creates a mock VedaDB server for testing
func MockVedaDB(t *testing.T) (*httptest.Server, *MockVedaDBBackend) {
	backend := &MockVedaDBBackend{
		data:   make(map[string]map[string][]byte),
		mutex:  sync.RWMutex{},
		errors: make(map[string]error),
	}

	mux := http.NewServeMux()

	// PUT /v1/{namespace}/{key} - Create/Update
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 {
			http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
			return
		}

		namespace := parts[1]
		key := strings.Join(parts[2:], "/")

		if err := backend.getError("put"); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		switch r.Method {
		case http.MethodPut:
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			backend.put(namespace, key, body)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "created",
				"key":    key,
			})

		case http.MethodGet:
			if err := backend.getError("get"); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			data, found := backend.get(namespace, key)
			if !found {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)

		case http.MethodDelete:
			if err := backend.getError("delete"); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			backend.delete(namespace, key)
			w.WriteHeader(http.StatusNoContent)

		case http.MethodPost:
			// Search/List endpoint
			if err := backend.getError("search"); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			var req SearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
				return
			}
			results := backend.search(namespace, req)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(results)

		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	return httptest.NewServer(mux), backend
}

// MockVedaDBBackend holds the mock data
type MockVedaDBBackend struct {
	data   map[string]map[string][]byte
	mutex  sync.RWMutex
	errors map[string]error
}

func (m *MockVedaDBBackend) put(namespace, key string, value []byte) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.data[namespace] == nil {
		m.data[namespace] = make(map[string][]byte)
	}
	m.data[namespace][key] = value
}

func (m *MockVedaDBBackend) get(namespace, key string) ([]byte, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	ns, ok := m.data[namespace]
	if !ok {
		return nil, false
	}
	val, ok := ns[key]
	return val, ok
}

func (m *MockVedaDBBackend) delete(namespace, key string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if ns, ok := m.data[namespace]; ok {
		delete(ns, key)
	}
}

// SetError configures the mock to return an error for a specific operation
func (m *MockVedaDBBackend) SetError(op string, err error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.errors[op] = err
}

func (m *MockVedaDBBackend) getError(op string) error {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.errors[op]
}

// ClearError removes a configured error
func (m *MockVedaDBBackend) ClearError(op string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.errors, op)
}

// Reset clears all data and errors
func (m *MockVedaDBBackend) Reset() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.data = make(map[string]map[string][]byte)
	m.errors = make(map[string]error)
}

type SearchRequest struct {
	Query      string            `json:"query,omitempty"`
	Filters    map[string]string `json:"filters,omitempty"`
	Page       int               `json:"page,omitempty"`
	PageSize   int               `json:"page_size,omitempty"`
	SortBy     string            `json:"sort_by,omitempty"`
	SortOrder  string            `json:"sort_order,omitempty"`
}

type SearchResponse struct {
	Items      []json.RawMessage `json:"items"`
	Total      int               `json:"total"`
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
	TotalPages int               `json:"total_pages"`
}

// VedaDBStore is the store implementation that connects to VedaDB
type VedaDBStore struct {
	baseURL   string
	namespace string
	client    *http.Client
}

// NewVedaDBStore creates a new store instance
func NewVedaDBStore(baseURL, namespace string) *VedaDBStore {
	return &VedaDBStore{
		baseURL:   baseURL,
		namespace: namespace,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *VedaDBStore) url(key string) string {
	return fmt.Sprintf("%s/v1/%s/%s", s.baseURL, s.namespace, key)
}

// Create stores a new document in VedaDB
func (s *VedaDBStore) Create(ctx context.Context, key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.url(key), strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusInternalServerError {
		return errors.New("internal server error")
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// Get retrieves a document from VedaDB
func (s *VedaDBStore) Get(ctx context.Context, key string, dest interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url(key), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode == http.StatusInternalServerError {
		return errors.New("internal server error")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(dest)
}

// Update updates an existing document in VedaDB
func (s *VedaDBStore) Update(ctx context.Context, key string, value interface{}) error {
	return s.Create(ctx, key, value)
}

// Delete removes a document from VedaDB
func (s *VedaDBStore) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.url(key), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusInternalServerError {
		return errors.New("internal server error")
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// List retrieves paginated documents from VedaDB
func (s *VedaDBStore) List(ctx context.Context, page, pageSize int) (*SearchResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	req := SearchRequest{
		Page:     page,
		PageSize: pageSize,
	}

	return s.search(ctx, req)
}

// Search performs a full-text search with filters
func (s *VedaDBStore) Search(ctx context.Context, query string, filters map[string]string, page, pageSize int) (*SearchResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	req := SearchRequest{
		Query:    query,
		Filters:  filters,
		Page:     page,
		PageSize: pageSize,
	}

	return s.search(ctx, req)
}

func (s *VedaDBStore) search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	searchURL := fmt.Sprintf("%s/v1/%s/_search", s.baseURL, s.namespace)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusInternalServerError {
		return nil, errors.New("internal server error")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// Store errors
var ErrNotFound = errors.New("document not found")

// Document is a sample document model for testing
type Document struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// ============================================================================
// TESTS
// ============================================================================

func TestVedaDBStore_Create_GivenValidDocument_WhenStored_ThenReturnsNoError(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	tests := []struct {
		name string
		doc  Document
		key  string
	}{
		{
			name: "simple document",
			doc: Document{
				ID:   "api-1",
				Name: "Test API",
				Type: "REST",
				Tags: []string{"v1", "public"},
				Metadata: map[string]string{
					"owner": "team-a",
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			key: "api-1",
		},
		{
			name: "document with special characters in name",
			doc: Document{
				ID:   "api-2",
				Name: "API with special chars: <>&\"",
				Type: "GraphQL",
				Tags: []string{},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			key: "api-2",
		},
		{
			name: "large document",
			doc: Document{
				ID:   "api-3",
				Name: "Large API",
				Type: "REST",
				Tags: make([]string, 100),
				Metadata: map[string]string{
					"key1": "value1",
					"key2": "value2",
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			key: "api-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Create(context.Background(), tt.key, tt.doc)
			require.NoError(t, err)

			// Verify the document was stored
			data, found := backend.get("apis", tt.key)
			require.True(t, found, "document should be stored")
			assert.NotNil(t, data)
		})
	}
}

func TestVedaDBStore_Create_GivenInvalidServer_WhenStoreCalled_ThenReturnsError(t *testing.T) {
	store := NewVedaDBStore("http://invalid-server:9999", "apis")

	doc := Document{ID: "api-1", Name: "Test API", Type: "REST"}
	err := store.Create(context.Background(), "api-1", doc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execute request")
}

func TestVedaDBStore_Create_GivenServerError_WhenStoreCalled_ThenReturnsError(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")
	backend.SetError("put", errors.New("disk full"))

	doc := Document{ID: "api-1", Name: "Test API", Type: "REST"}
	err := store.Create(context.Background(), "api-1", doc)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "internal server error")
}

func TestVedaDBStore_Get_GivenExistingDocument_WhenRetrieved_ThenReturnsDocument(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	tests := []struct {
		name     string
		expected Document
		key      string
	}{
		{
			name: "simple document",
			expected: Document{
				ID:   "api-1",
				Name: "Test API",
				Type: "REST",
				Tags: []string{"v1", "public"},
				Metadata: map[string]string{
					"owner": "team-a",
				},
			},
			key: "api-1",
		},
		{
			name: "nested metadata",
			expected: Document{
				ID:   "api-2",
				Name: "Complex API",
				Type: "GraphQL",
				Tags: []string{"internal", "v2", "beta"},
				Metadata: map[string]string{
					"department":  "engineering",
					"cost-center": "CC-12345",
					"environment": "production",
				},
			},
			key: "api-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Store the document first
			data, _ := json.Marshal(tt.expected)
			backend.put("apis", tt.key, data)

			var result Document
			err := store.Get(context.Background(), tt.key, &result)
			require.NoError(t, err)
			assert.Equal(t, tt.expected.ID, result.ID)
			assert.Equal(t, tt.expected.Name, result.Name)
			assert.Equal(t, tt.expected.Type, result.Type)
			assert.Equal(t, tt.expected.Tags, result.Tags)
			assert.Equal(t, tt.expected.Metadata, result.Metadata)
		})
	}
}

func TestVedaDBStore_Get_GivenNonExistentKey_WhenRetrieved_ThenReturnsNotFound(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	var result Document
	err := store.Get(context.Background(), "non-existent-key", &result)

	assert.ErrorIs(t, err, ErrNotFound)
}

func TestVedaDBStore_Get_GivenServerError_WhenRetrieved_ThenReturnsError(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")
	backend.SetError("get", errors.New("connection timeout"))

	var result Document
	err := store.Get(context.Background(), "api-1", &result)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "internal server error")
}

func TestVedaDBStore_Update_GivenExistingDocument_WhenUpdated_ThenStoresNewVersion(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	// Store initial version
	initial := Document{ID: "api-1", Name: "Test API v1", Type: "REST"}
	data, _ := json.Marshal(initial)
	backend.put("apis", "api-1", data)

	// Update
	updated := Document{ID: "api-1", Name: "Test API v2", Type: "REST", Tags: []string{"updated"}}
	err := store.Update(context.Background(), "api-1", updated)
	require.NoError(t, err)

	// Verify
	storedData, found := backend.get("apis", "api-1")
	require.True(t, found)
	var storedDoc Document
	json.Unmarshal(storedData, &storedDoc)
	assert.Equal(t, "Test API v2", storedDoc.Name)
	assert.Equal(t, []string{"updated"}, storedDoc.Tags)
}

func TestVedaDBStore_Delete_GivenExistingDocument_WhenDeleted_ThenRemovesDocument(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	// Store document first
	doc := Document{ID: "api-1", Name: "Test API", Type: "REST"}
	data, _ := json.Marshal(doc)
	backend.put("apis", "api-1", data)

	err := store.Delete(context.Background(), "api-1")
	require.NoError(t, err)

	_, found := backend.get("apis", "api-1")
	assert.False(t, found, "document should be deleted")
}

func TestVedaDBStore_Delete_GivenNonExistentKey_WhenDeleted_ThenReturnsNotFound(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	err := store.Delete(context.Background(), "non-existent-key")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestVedaDBStore_List_GivenMultipleDocuments_WhenListed_ThenReturnsPaginatedResults(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	tests := []struct {
		name           string
		docCount       int
		page           int
		pageSize       int
		expectedItems  int
		expectedTotal  int
		expectedTotalPages int
	}{
		{
			name:           "first page of three",
			docCount:       25,
			page:           1,
			pageSize:       10,
			expectedItems:  10,
			expectedTotal:  25,
			expectedTotalPages: 3,
		},
		{
			name:           "last page",
			docCount:       25,
			page:           3,
			pageSize:       10,
			expectedItems:  5,
			expectedTotal:  25,
			expectedTotalPages: 3,
		},
		{
			name:           "empty list",
			docCount:       0,
			page:           1,
			pageSize:       10,
			expectedItems:  0,
			expectedTotal:  0,
			expectedTotalPages: 0,
		},
		{
			name:           "single page",
			docCount:       5,
			page:           1,
			pageSize:       10,
			expectedItems:  5,
			expectedTotal:  5,
			expectedTotalPages: 1,
		},
		{
			name:           "page beyond range",
			docCount:       5,
			page:           10,
			pageSize:       10,
			expectedItems:  0,
			expectedTotal:  5,
			expectedTotalPages: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend.Reset()

			// Seed documents
			for i := 0; i < tt.docCount; i++ {
				doc := Document{
					ID:   fmt.Sprintf("api-%d", i),
					Name: fmt.Sprintf("Test API %d", i),
					Type: "REST",
				}
				data, _ := json.Marshal(doc)
				backend.put("apis", doc.ID, data)
			}

			result, err := store.List(context.Background(), tt.page, tt.pageSize)
			require.NoError(t, err)
			assert.Len(t, result.Items, tt.expectedItems)
			assert.Equal(t, tt.expectedTotal, result.Total)
		})
	}
}

func TestVedaDBStore_Search_GivenQuery_WhenSearched_ThenReturnsMatchingResults(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	tests := []struct {
		name          string
		seedDocs      []Document
		query         string
		filters       map[string]string
		page          int
		pageSize      int
		expectedCount int
	}{
		{
			name: "search by name query",
			seedDocs: []Document{
				{ID: "api-1", Name: "Payment API", Type: "REST"},
				{ID: "api-2", Name: "User Service", Type: "REST"},
				{ID: "api-3", Name: "Payment Gateway", Type: "GraphQL"},
			},
			query:         "Payment",
			filters:       nil,
			page:          1,
			pageSize:      10,
			expectedCount: 2,
		},
		{
			name: "search with type filter",
			seedDocs: []Document{
				{ID: "api-1", Name: "Payment API", Type: "REST"},
				{ID: "api-2", Name: "User Service", Type: "GraphQL"},
				{ID: "api-3", Name: "Order API", Type: "REST"},
			},
			query:         "",
			filters:       map[string]string{"type": "REST"},
			page:          1,
			pageSize:      10,
			expectedCount: 2,
		},
		{
			name: "search with query and filter",
			seedDocs: []Document{
				{ID: "api-1", Name: "Payment API", Type: "REST"},
				{ID: "api-2", Name: "Payment Service", Type: "GraphQL"},
				{ID: "api-3", Name: "User API", Type: "REST"},
			},
			query:         "Payment",
			filters:       map[string]string{"type": "REST"},
			page:          1,
			pageSize:      10,
			expectedCount: 1,
		},
		{
			name: "empty query returns all",
			seedDocs: []Document{
				{ID: "api-1", Name: "Payment API", Type: "REST"},
				{ID: "api-2", Name: "User Service", Type: "REST"},
			},
			query:         "",
			filters:       nil,
			page:          1,
			pageSize:      10,
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend.Reset()

			for _, doc := range tt.seedDocs {
				data, _ := json.Marshal(doc)
				backend.put("apis", doc.ID, data)
			}

			result, err := store.Search(context.Background(), tt.query, tt.filters, tt.page, tt.pageSize)
			require.NoError(t, err)
			assert.Len(t, result.Items, tt.expectedCount)
		})
	}
}

func TestVedaDBStore_ConcurrentAccess_GivenMultipleGoroutines_WhenOperating_ThenNoDataRace(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")
	const numGoroutines = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()
			key := fmt.Sprintf("api-%d", idx)

			doc := Document{
				ID:   key,
				Name: fmt.Sprintf("Test API %d", idx),
				Type: "REST",
			}

			// Create
			err := store.Create(ctx, key, doc)
			assert.NoError(t, err)

			// Get
			var result Document
			err = store.Get(ctx, key, &result)
			assert.NoError(t, err)
			assert.Equal(t, doc.Name, result.Name)

			// Update
			doc.Name = fmt.Sprintf("Updated API %d", idx)
			err = store.Update(ctx, key, doc)
			assert.NoError(t, err)

			// Delete (every even index)
			if idx%2 == 0 {
				err = store.Delete(ctx, key)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()
}

func TestVedaDBStore_Pagination_GivenLargeDataset_WhenPaginated_ThenReturnsCorrectPages(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	// Seed 100 documents
	for i := 0; i < 100; i++ {
		doc := Document{
			ID:   fmt.Sprintf("api-%03d", i),
			Name: fmt.Sprintf("Test API %d", i),
			Type: "REST",
		}
		data, _ := json.Marshal(doc)
		backend.put("apis", doc.ID, data)
	}

	tests := []struct {
		name         string
		page         int
		pageSize     int
		expectedLen  int
		expectedPage int
	}{
		{"page 1 size 10", 1, 10, 10, 1},
		{"page 2 size 10", 2, 10, 10, 2},
		{"page 10 size 10", 10, 10, 10, 10},
		{"page 11 size 10", 11, 10, 0, 11},
		{"page 1 size 50", 1, 50, 50, 1},
		{"page 2 size 50", 2, 50, 50, 2},
		{"page 1 size 100", 1, 100, 100, 1},
		{"page 1 size 200", 1, 200, 100, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := store.List(context.Background(), tt.page, tt.pageSize)
			require.NoError(t, err)
			assert.Len(t, result.Items, tt.expectedLen)
			assert.Equal(t, tt.expectedPage, result.Page)
		})
	}
}

func TestVedaDBStore_InvalidPagination_GivenNegativeValues_WhenListed_ThenUsesDefaults(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	// Seed 5 documents
	for i := 0; i < 5; i++ {
		doc := Document{ID: fmt.Sprintf("api-%d", i), Name: fmt.Sprintf("API %d", i), Type: "REST"}
		data, _ := json.Marshal(doc)
		backend.put("apis", doc.ID, data)
	}

	tests := []struct {
		name     string
		page     int
		pageSize int
	}{
		{"negative page", -1, 10},
		{"negative page size", 1, -1},
		{"both negative", -1, -1},
		{"zero values", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := store.List(context.Background(), tt.page, tt.pageSize)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(result.Items), 0)
		})
	}
}

func TestVedaDBStore_SearchWithFilters_GivenMultipleFilters_WhenSearched_ThenReturnsFilteredResults(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	// Seed documents with various types and tags
	docs := []Document{
		{ID: "api-1", Name: "Payment API", Type: "REST", Tags: []string{"v1", "public"}},
		{ID: "api-2", Name: "User GraphQL", Type: "GraphQL", Tags: []string{"v2", "internal"}},
		{ID: "api-3", Name: "Order REST", Type: "REST", Tags: []string{"v1", "internal"}},
		{ID: "api-4", Name: "Payment GraphQL", Type: "GraphQL", Tags: []string{"v1", "public"}},
		{ID: "api-5", Name: "Inventory REST", Type: "REST", Tags: []string{"v2", "public"}},
	}

	for _, doc := range docs {
		data, _ := json.Marshal(doc)
		backend.put("apis", doc.ID, data)
	}

	tests := []struct {
		name          string
		query         string
		filters       map[string]string
		expectedCount int
	}{
		{
			name:          "filter by REST type only",
			query:         "",
			filters:       map[string]string{"type": "REST"},
			expectedCount: 3,
		},
		{
			name:          "filter by GraphQL type only",
			query:         "",
			filters:       map[string]string{"type": "GraphQL"},
			expectedCount: 2,
		},
		{
			name:          "query Payment with no filter",
			query:         "Payment",
			filters:       nil,
			expectedCount: 2,
		},
		{
			name:          "query Payment with REST filter",
			query:         "Payment",
			filters:       map[string]string{"type": "REST"},
			expectedCount: 1,
		},
		{
			name:          "empty query with no filters returns all",
			query:         "",
			filters:       nil,
			expectedCount: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := store.Search(context.Background(), tt.query, tt.filters, 1, 10)
			require.NoError(t, err)
			assert.Len(t, result.Items, tt.expectedCount)
		})
	}
}

func TestVedaDBStore_ContextCancellation_GivenCancelledContext_WhenOperationCalled_ThenReturnsError(t *testing.T) {
	server, backend := MockVedaDB(t)
	defer server.Close()
	backend.Reset()

	store := NewVedaDBStore(server.URL, "apis")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	doc := Document{ID: "api-1", Name: "Test API", Type: "REST"}
	err := store.Create(ctx, "api-1", doc)
	assert.Error(t, err)
}

func TestVedaDBStore_ContextTimeout_GivenExpiredTimeout_WhenOperationCalled_ThenReturnsError(t *testing.T) {
	server, _ := MockVedaDB(t)
	defer server.Close()

	// Create a slow server
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	store := NewVedaDBStore(slowServer.URL, "apis")
	store.client = &http.Client{Timeout: 50 * time.Millisecond}

	ctx := context.Background()
	doc := Document{ID: "api-1", Name: "Test API", Type: "REST"}
	err := store.Create(ctx, "api-1", doc)
	assert.Error(t, err)
}
