// Package mock provides a comprehensive API mocking engine for the VedaDB API Manager.
// It persists mock responses in the api_mocks table via VedaDB and supports
// registering, retrieving, auto-generating, and listing mock responses.
package mock

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vedadb/vapim/pkg/models"
	"github.com/vedadb/vapim/pkg/store"
)

// ---------------------------------------------------------------------------
// Mock Models
// ---------------------------------------------------------------------------

// MockResponse represents a single mock response for an API resource.
type MockResponse struct {
	ID         string            `json:"id"`
	APIID      string            `json:"api_id"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       interface{}       `json:"body"`
	BodyRaw    []byte            `json:"body_raw,omitempty"`
	Format     string            `json:"format,omitempty"`
	Template   string            `json:"template,omitempty"`
	Delay      time.Duration     `json:"delay"`
	Status     string            `json:"status"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// ResourceDef represents a resource definition for auto-generation.
type ResourceDef struct {
	Path       string                 `json:"path"`
	Method     string                 `json:"method"`
	Produces   []string               `json:"produces"`
	Consumes   []string               `json:"consumes"`
	Parameters []ParameterDef         `json:"parameters"`
	Responses  map[string]ResponseDef `json:"responses"`
	AuthType   string                 `json:"auth_type"`
}

// ParameterDef represents a parameter in a resource definition.
type ParameterDef struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// ResponseDef represents a response in a resource definition.
type ResponseDef struct {
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema"`
	Headers     map[string]string      `json:"headers"`
}

// ---------------------------------------------------------------------------
// MockEngine
// ---------------------------------------------------------------------------

// MockEngine defines the interface for the mock response engine.
type MockEngine interface {
	RegisterMock(apiID, method, path string, response *MockResponse) error
	GetMockResponse(apiID, method, path string) (*MockResponse, bool)
	AutoGenerate(apiID string) error
	ListMocks(apiID string) ([]*MockResponse, error)
	DeleteMock(apiID, method, path string) error
	DeleteAllMocks(apiID string) error
	SetGlobalDelay(delay time.Duration)
	SetGlobalHeaders(headers map[string]string)
}

// DBMockEngine is a DB-backed implementation of MockEngine.
type DBMockEngine struct {
	store         store.Store
	globalDelay   time.Duration
	globalHeaders map[string]string
	mu            sync.RWMutex
}

// NewDBMockEngine creates a new DB-backed mock engine.
func NewDBMockEngine(store store.Store) *DBMockEngine {
	return &DBMockEngine{
		store:         store,
		globalHeaders: make(map[string]string),
	}
}

// RegisterMock registers a mock response in the database.
func (e *DBMockEngine) RegisterMock(apiID, method, path string, response *MockResponse) error {
	if apiID == "" {
		return fmt.Errorf("apiID is required")
	}
	if method == "" {
		return fmt.Errorf("method is required")
	}
	if path == "" {
		return fmt.Errorf("path is required")
	}

	method = strings.ToUpper(method)

	// Pre-serialize body for storage
	var bodyStr string
	if response.Body != nil {
		bodyBytes, err := json.Marshal(response.Body)
		if err == nil {
			bodyStr = string(bodyBytes)
			response.BodyRaw = bodyBytes
		}
	}

	// Set defaults
	if response.StatusCode == 0 {
		response.StatusCode = 200
	}
	if response.Format == "" {
		response.Format = "json"
	}
	if response.Headers == nil {
		response.Headers = make(map[string]string)
	}
	contentType := "application/json"
	switch response.Format {
	case "xml":
		contentType = "application/xml"
	case "text":
		contentType = "text/plain"
	}
	if response.Headers["Content-Type"] == "" {
		response.Headers["Content-Type"] = contentType
	}
	response.Headers["X-Mock-Response"] = "true"
	response.Headers["X-Mock-Engine"] = "VAPIM-Mock/2.0"

	// Serialize headers
	headersJSON, _ := json.Marshal(response.Headers)

	m := &MockResponse{
		ID:         uuid.New().String(),
		APIID:      apiID,
		Method:     method,
		Path:       path,
		StatusCode: response.StatusCode,
		Headers:    response.Headers,
		Body:       response.Body,
		BodyRaw:    response.BodyRaw,
		Format:     response.Format,
		Template:   response.Template,
		Delay:      response.Delay,
		Status:     "active",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	// Convert to DB model and insert via store
	dbMock := &models.APIMockDB{
		ID:         m.ID,
		APIID:      m.APIID,
		Method:     m.Method,
		Path:       m.Path,
		StatusCode: m.StatusCode,
		Headers:    string(headersJSON),
		Body:       bodyStr,
		DelayMS:    int(m.Delay.Milliseconds()),
		Status:     m.Status,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}

	_ = e.store.CreateMock(dbMock)

	// If duplicate key, update existing
	existing, err := e.store.GetMock(apiID, method, path)
	if err == nil && existing != nil {
		dbMock.ID = existing.ID
		dbMock.CreatedAt = existing.CreatedAt
		dbMock.UpdatedAt = time.Now().UTC()
		_ = e.store.UpdateMock(dbMock)
	}

	_ = headersJSON
	_ = bodyStr
	return nil
}

// GetMockResponse retrieves a mock response from the database.
func (e *DBMockEngine) GetMockResponse(apiID, method, path string) (*MockResponse, bool) {
	method = strings.ToUpper(method)

	e.mu.RLock()
	globalDelay := e.globalDelay
	globalHeaders := make(map[string]string, len(e.globalHeaders))
	for k, v := range e.globalHeaders {
		globalHeaders[k] = v
	}
	e.mu.RUnlock()

	dbMock, err := e.store.GetMock(apiID, method, path)
	if err != nil {
		return nil, false
	}
	if dbMock == nil || dbMock.Status != "active" {
		return nil, false
	}

	// Deserialize headers
	var headers map[string]string
	if dbMock.Headers != "" {
		_ = json.Unmarshal([]byte(dbMock.Headers), &headers)
	}
	if headers == nil {
		headers = make(map[string]string)
	}

	// Deserialize body
	var body interface{}
	var bodyRaw []byte
	if dbMock.Body != "" {
		bodyRaw = []byte(dbMock.Body)
		_ = json.Unmarshal(bodyRaw, &body)
	}

	// Merge global headers (global take precedence)
	mergedHeaders := make(map[string]string, len(headers)+len(globalHeaders))
	for k, v := range headers {
		mergedHeaders[k] = v
	}
	for k, v := range globalHeaders {
		mergedHeaders[k] = v
	}

	resp := &MockResponse{
		ID:         dbMock.ID,
		APIID:      dbMock.APIID,
		Method:     dbMock.Method,
		Path:       dbMock.Path,
		StatusCode: dbMock.StatusCode,
		Headers:    mergedHeaders,
		Body:       body,
		BodyRaw:    bodyRaw,
		Delay:      time.Duration(dbMock.DelayMS)*time.Millisecond + globalDelay,
		Status:     dbMock.Status,
		CreatedAt:  dbMock.CreatedAt,
		UpdatedAt:  dbMock.UpdatedAt,
	}

	return resp, true
}

// AutoGenerate generates mock responses from an API's resource definitions in the DB.
func (e *DBMockEngine) AutoGenerate(apiID string) error {
	if apiID == "" {
		return fmt.Errorf("apiID is required")
	}

	resources, err := e.store.GetResourcesByAPI(apiID)
	if err != nil {
		return fmt.Errorf("failed to get api resources: %w", err)
	}

	generator := NewMockDataGenerator()

	for _, resource := range resources {
		// Determine default status code from method
		statusCode := 200
		switch strings.ToUpper(resource.Method) {
		case "POST":
			statusCode = 201
		case "DELETE":
			statusCode = 204
		}

		// Generate body
		var body interface{}
		format := "json"

		// Try to build a response from schema hints
		body = generator.GenerateFromParameters([]ParameterDef{
			{Name: "id", Type: "string", In: "path"},
			{Name: "data", Type: "object", In: "body"},
		})

		// Override from resource description if it looks like a known pattern
		if strings.Contains(resource.Description, "list") || strings.Contains(resource.Path, "/") {
			body = map[string]interface{}{
				"data":    generator.GenerateArray(3),
				"total":   3,
				"page":    1,
				"perPage": 10,
			}
		}
		if statusCode == 204 {
			body = nil
		}

		headers := map[string]string{
			"Content-Type": "application/json",
		}

		mock := &MockResponse{
			StatusCode: statusCode,
			Headers:    headers,
			Body:       body,
			Format:     format,
			Delay:      time.Duration(rand.Intn(50)) * time.Millisecond,
		}

		if err := e.RegisterMock(apiID, resource.Method, resource.Path, mock); err != nil {
			return fmt.Errorf("failed to register auto-generated mock for %s %s: %w", resource.Method, resource.Path, err)
		}
	}

	return nil
}

// ListMocks returns all mocks for a given API from the database.
func (e *DBMockEngine) ListMocks(apiID string) ([]*MockResponse, error) {
	if apiID == "" {
		return nil, fmt.Errorf("apiID is required")
	}

	dbMocks, err := e.store.ListMocks(apiID)
	if err != nil {
		return nil, fmt.Errorf("failed to list mocks: %w", err)
	}

	mocks := make([]*MockResponse, 0, len(dbMocks))
	for _, dbMock := range dbMocks {
		var headers map[string]string
		if dbMock.Headers != "" {
			_ = json.Unmarshal([]byte(dbMock.Headers), &headers)
		}
		var body interface{}
		var bodyRaw []byte
		if dbMock.Body != "" {
			bodyRaw = []byte(dbMock.Body)
			_ = json.Unmarshal(bodyRaw, &body)
		}
		mocks = append(mocks, &MockResponse{
			ID:         dbMock.ID,
			APIID:      dbMock.APIID,
			Method:     dbMock.Method,
			Path:       dbMock.Path,
			StatusCode: dbMock.StatusCode,
			Headers:    headers,
			Body:       body,
			BodyRaw:    bodyRaw,
			Delay:      time.Duration(dbMock.DelayMS) * time.Millisecond,
			Status:     dbMock.Status,
			CreatedAt:  dbMock.CreatedAt,
			UpdatedAt:  dbMock.UpdatedAt,
		})
	}
	return mocks, nil
}

// DeleteMock removes a specific mock from the database.
func (e *DBMockEngine) DeleteMock(apiID, method, path string) error {
	method = strings.ToUpper(method)

	mock, err := e.store.GetMock(apiID, method, path)
	if err != nil {
		return fmt.Errorf("mock not found: %w", err)
	}
	if mock == nil {
		return fmt.Errorf("mock not found for %s %s %s", apiID, method, path)
	}

	return e.store.DeleteMock(mock.ID)
}

// DeleteAllMocks removes all mocks for an API from the database.
func (e *DBMockEngine) DeleteAllMocks(apiID string) error {
	return e.store.DeleteAllMocksForAPI(apiID)
}

// SetGlobalDelay sets a global delay for all responses.
func (e *DBMockEngine) SetGlobalDelay(delay time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.globalDelay = delay
}

// SetGlobalHeaders sets global headers.
func (e *DBMockEngine) SetGlobalHeaders(headers map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.globalHeaders = headers
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// MatchPath attempts to match a request path against a mock path pattern.
// Supports path parameters in the form {param} and * wildcards.
func MatchPath(pattern, path string) (bool, map[string]string) {
	patternParts := strings.Split(filepath.Clean(pattern), "/")
	pathParts := strings.Split(filepath.Clean(path), "/")

	if len(patternParts) != len(pathParts) {
		return false, nil
	}

	params := make(map[string]string)
	for i, pp := range patternParts {
		if strings.HasPrefix(pp, "{") && strings.HasSuffix(pp, "}") {
			paramName := pp[1 : len(pp)-1]
			params[paramName] = pathParts[i]
		} else if pp == "*" {
			params["*"] = pathParts[i]
		} else if pp != pathParts[i] {
			return false, nil
		}
	}

	return true, params
}

// CloneMockResponse creates a deep copy of a MockResponse.
func CloneMockResponse(r *MockResponse) *MockResponse {
	if r == nil {
		return nil
	}

	clone := &MockResponse{
		ID:         r.ID,
		APIID:      r.APIID,
		Method:     r.Method,
		Path:       r.Path,
		StatusCode: r.StatusCode,
		Body:       r.Body,
		BodyRaw:    append([]byte(nil), r.BodyRaw...),
		Delay:      r.Delay,
		Format:     r.Format,
		Template:   r.Template,
		Status:     r.Status,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
		Headers:    make(map[string]string, len(r.Headers)),
	}

	for k, v := range r.Headers {
		clone.Headers[k] = v
	}

	return clone
}

// ---------------------------------------------------------------------------
// MockDataGenerator generates realistic mock data from schemas.
// ---------------------------------------------------------------------------

// MockDataGenerator generates realistic mock response data.
type MockDataGenerator struct {
	rng *rand.Rand
	mu  sync.Mutex
}

// NewMockDataGenerator creates a new mock data generator.
func NewMockDataGenerator() *MockDataGenerator {
	return &MockDataGenerator{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// GenerateFromSchema generates mock data from a JSON schema.
func (g *MockDataGenerator) GenerateFromSchema(schema map[string]interface{}) interface{} {
	if schema == nil {
		return g.generateDefaultObject()
	}

	schemaType, _ := schema["type"].(string)
	switch schemaType {
	case "object":
		return g.generateObject(schema)
	case "array":
		return g.GenerateArray(3)
	case "string":
		return g.randomString()
	case "integer", "number":
		return g.rng.Intn(1000)
	case "boolean":
		return g.rng.Intn(2) == 0
	default:
		return g.generateDefaultObject()
	}
}

// GenerateFromParameters generates mock data from parameter definitions.
func (g *MockDataGenerator) GenerateFromParameters(params []ParameterDef) interface{} {
	result := make(map[string]interface{})
	for _, p := range params {
		switch p.Type {
		case "string":
			result[p.Name] = g.randomString()
		case "integer", "number":
			result[p.Name] = g.rng.Intn(1000)
		case "boolean":
			result[p.Name] = g.rng.Intn(2) == 0
		case "object":
			result[p.Name] = g.generateDefaultObject()
		case "array":
			result[p.Name] = g.GenerateArray(3)
		default:
			result[p.Name] = g.randomString()
		}
	}
	return result
}

// GenerateArray generates an array of mock objects.
func (g *MockDataGenerator) GenerateArray(size int) []interface{} {
	arr := make([]interface{}, size)
	for i := 0; i < size; i++ {
		arr[i] = g.generateDefaultObject()
	}
	return arr
}

func (g *MockDataGenerator) generateObject(schema map[string]interface{}) interface{} {
	result := make(map[string]interface{})
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return g.generateDefaultObject()
	}
	for key, prop := range props {
		if propMap, ok := prop.(map[string]interface{}); ok {
			result[key] = g.GenerateFromSchema(propMap)
		} else {
			result[key] = g.randomString()
		}
	}
	return result
}

func (g *MockDataGenerator) generateDefaultObject() map[string]interface{} {
	g.mu.Lock()
	defer g.mu.Unlock()

	return map[string]interface{}{
		"id":        strconv.Itoa(g.rng.Intn(100000)),
		"name":      g.randomString(),
		"status":    []string{"active", "pending", "completed"}[g.rng.Intn(3)],
		"createdAt": time.Now().UTC().Add(-time.Duration(g.rng.Intn(1000)) * time.Hour).Format(time.RFC3339),
		"updatedAt": time.Now().UTC().Add(-time.Duration(g.rng.Intn(100)) * time.Hour).Format(time.RFC3339),
	}
}

func (g *MockDataGenerator) randomString() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	adjectives := []string{"quick", "lazy", "sleepy", "noisy", "hungry", "bright", "dark", "calm"}
	nouns := []string{"fox", "dog", "cat", "bird", "fish", "wolf", "bear", "deer"}
	return fmt.Sprintf("%s-%s-%d",
		adjectives[g.rng.Intn(len(adjectives))],
		nouns[g.rng.Intn(len(nouns))],
		g.rng.Intn(10000))
}

// ---------------------------------------------------------------------------
// Global convenience functions (delegates to a default engine instance)
// ---------------------------------------------------------------------------

// SetDefaultMockEngine sets the global default mock engine.
func SetDefaultMockEngine(engine MockEngine) {
	defaultEngineMu.Lock()
	defer defaultEngineMu.Unlock()
	defaultEngine = engine
}

var (
	defaultEngine   MockEngine
	defaultEngineMu sync.RWMutex
)

func getDefault() MockEngine {
	defaultEngineMu.RLock()
	defer defaultEngineMu.RUnlock()
	return defaultEngine
}

// RegisterMock registers a mock via the default engine.
func RegisterMock(apiID, method, path string, response *MockResponse) error {
	if e := getDefault(); e != nil {
		return e.RegisterMock(apiID, method, path, response)
	}
	return fmt.Errorf("no default mock engine configured")
}

// GetMockResponse retrieves a mock via the default engine.
func GetMockResponse(apiID, method, path string) (*MockResponse, bool) {
	if e := getDefault(); e != nil {
		return e.GetMockResponse(apiID, method, path)
	}
	return nil, false
}

// AutoGenerate generates mocks via the default engine.
func AutoGenerate(apiID string) error {
	if e := getDefault(); e != nil {
		return e.AutoGenerate(apiID)
	}
	return fmt.Errorf("no default mock engine configured")
}
