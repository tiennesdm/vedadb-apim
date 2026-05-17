// Package mock provides a comprehensive API mocking engine for the VedaDB API Manager.
// It supports registering mock responses, auto-generating mocks from resource definitions,
// and serving mock responses with configurable delays and status codes.
package mock

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MockResponse represents a single mock response for an API resource.
type MockResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string]string   `json:"headers"`
	Body       interface{}         `json:"body"`
	Delay      time.Duration       `json:"delay"`
	BodyRaw    []byte              `json:"body_raw,omitempty"`    // Pre-serialized body bytes
	Format     string              `json:"format,omitempty"`     // json, xml, text
	Template   string              `json:"template,omitempty"`   // Template string for dynamic responses
}

// MockKey uniquely identifies a mock response.
type MockKey struct {
	APIID  string
	Method string
	Path   string
}

// String returns a string representation of the mock key.
func (k MockKey) String() string {
	return fmt.Sprintf("%s|%s|%s", k.APIID, k.Method, k.Path)
}

// MockEngine defines the interface for the mock response engine.
type MockEngine interface {
	// RegisterMock registers a mock response for an API resource.
	RegisterMock(apiID, method, path string, response MockResponse) error
	// GetMockResponse retrieves a mock response for a resource.
	GetMockResponse(apiID, method, path string) (*MockResponse, bool)
	// AutoGenerate generates mock responses from an API's resource definitions.
	AutoGenerate(apiID string, resources []ResourceDef) error
	// ListMocks returns all registered mocks for an API.
	ListMocks(apiID string) ([]*MockResponse, error)
	// DeleteMock removes a mock response.
	DeleteMock(apiID, method, path string) error
	// DeleteAllMocks removes all mocks for an API.
	DeleteAllMocks(apiID string) error
	// SetGlobalDelay sets a global delay for all mock responses.
	SetGlobalDelay(delay time.Duration)
	// SetGlobalHeaders sets global headers for all mock responses.
	SetGlobalHeaders(headers map[string]string)
}

// ResourceDef represents a resource definition for auto-generation.
type ResourceDef struct {
	Path        string            `json:"path"`
	Method      string            `json:"method"`
	Produces    []string          `json:"produces"`
	Consumes    []string          `json:"consumes"`
	Parameters  []ParameterDef    `json:"parameters"`
	Responses   map[string]ResponseDef `json:"responses"`
	AuthType    string            `json:"auth_type"`
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

// InMemoryMockEngine is an in-memory implementation of MockEngine.
type InMemoryMockEngine struct {
	mocks         map[string]*MockResponse // key -> *MockResponse
	globalDelay   time.Duration
	globalHeaders map[string]string
	mu            sync.RWMutex
	bodyCache     map[string][]byte // key -> serialized body
}

// NewInMemoryMockEngine creates a new in-memory mock engine.
func NewInMemoryMockEngine() *InMemoryMockEngine {
	return &InMemoryMockEngine{
		mocks:         make(map[string]*MockResponse),
		globalHeaders: make(map[string]string),
		bodyCache:     make(map[string][]byte),
	}
}

// RegisterMock registers a mock response.
func (e *InMemoryMockEngine) RegisterMock(apiID, method, path string, response MockResponse) error {
	if apiID == "" {
		return fmt.Errorf("apiID is required")
	}
	if method == "" {
		return fmt.Errorf("method is required")
	}
	if path == "" {
		return fmt.Errorf("path is required")
	}

	key := MockKey{APIID: apiID, Method: strings.ToUpper(method), Path: path}.String()

	// Pre-serialize body for performance
	if response.Body != nil && response.BodyRaw == nil {
		bodyBytes, err := json.Marshal(response.Body)
		if err == nil {
			response.BodyRaw = bodyBytes
		}
	}

	// Set default headers
	if response.Headers == nil {
		response.Headers = make(map[string]string)
	}
	if response.Format == "" {
		response.Format = "json"
	}
	if response.StatusCode == 0 {
		response.StatusCode = 200
	}
	// Set Content-Type based on format
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

	e.mu.Lock()
	defer e.mu.Unlock()

	e.mocks[key] = &response
	if response.BodyRaw != nil {
		e.bodyCache[key] = response.BodyRaw
	}

	return nil
}

// GetMockResponse retrieves a mock response.
func (e *InMemoryMockEngine) GetMockResponse(apiID, method, path string) (*MockResponse, bool) {
	key := MockKey{APIID: apiID, Method: strings.ToUpper(method), Path: path}.String()

	e.mu.RLock()
	defer e.mu.RUnlock()

	resp, ok := e.mocks[key]
	if !ok {
		return nil, false
	}

	// Return a copy
	respCopy := *resp
	respCopy.Headers = make(map[string]string, len(resp.Headers)+len(e.globalHeaders))
	for k, v := range resp.Headers {
		respCopy.Headers[k] = v
	}
	// Merge global headers (global take precedence)
	for k, v := range e.globalHeaders {
		respCopy.Headers[k] = v
	}
	respCopy.Delay = resp.Delay + e.globalDelay

	if respCopy.BodyRaw == nil && e.bodyCache[key] != nil {
		respCopy.BodyRaw = e.bodyCache[key]
	}

	return &respCopy, true
}

// AutoGenerate generates mock responses from resource definitions.
func (e *InMemoryMockEngine) AutoGenerate(apiID string, resources []ResourceDef) error {
	if apiID == "" {
		return fmt.Errorf("apiID is required")
	}

	generator := NewMockDataGenerator()

	for _, resource := range resources {
		// Generate default success response (200 for GET/PUT/PATCH, 201 for POST, 204 for DELETE)
		statusCode := 200
		switch strings.ToUpper(resource.Method) {
		case "POST":
			statusCode = 201
		case "DELETE":
			statusCode = 204
		case "PATCH":
			statusCode = 200
		}

		// Generate body from response schema or parameter types
		var body interface{}
		format := "json"

		if len(resource.Responses) > 0 {
			// Use the lowest status code response schema
			for code, respDef := range resource.Responses {
				codeNum := 0
				fmt.Sscanf(code, "%d", &codeNum)
				if codeNum > 0 && (codeNum < statusCode || statusCode == 204) {
					statusCode = codeNum
				}
				if respDef.Schema != nil {
					body = generator.GenerateFromSchema(respDef.Schema)
					if len(resource.Produces) > 0 && strings.Contains(resource.Produces[0], "xml") {
						format = "xml"
					}
					break
				}
			}
		}

		// Fallback: generate from parameters
		if body == nil {
			body = generator.GenerateFromParameters(resource.Parameters)
		}

		mock := MockResponse{
			StatusCode: statusCode,
			Headers:    make(map[string]string),
			Body:       body,
			Format:     format,
		}

		if err := e.RegisterMock(apiID, resource.Method, resource.Path, mock); err != nil {
			return fmt.Errorf("failed to register mock for %s %s: %w", resource.Method, resource.Path, err)
		}
	}

	return nil
}

// ListMocks returns all mocks for a given API.
func (e *InMemoryMockEngine) ListMocks(apiID string) ([]*MockResponse, error) {
	prefix := apiID + "|"

	e.mu.RLock()
	defer e.mu.RUnlock()

	var mocks []*MockResponse
	for key, mock := range e.mocks {
		if strings.HasPrefix(key, prefix) {
			mCopy := *mock
			mocks = append(mocks, &mCopy)
		}
	}

	return mocks, nil
}

// DeleteMock removes a specific mock.
func (e *InMemoryMockEngine) DeleteMock(apiID, method, path string) error {
	key := MockKey{APIID: apiID, Method: strings.ToUpper(method), Path: path}.String()

	e.mu.Lock()
	defer e.mu.Unlock()

	delete(e.mocks, key)
	delete(e.bodyCache, key)

	return nil
}

// DeleteAllMocks removes all mocks for an API.
func (e *InMemoryMockEngine) DeleteAllMocks(apiID string) error {
	prefix := apiID + "|"

	e.mu.Lock()
	defer e.mu.Unlock()

	for key := range e.mocks {
		if strings.HasPrefix(key, prefix) {
			delete(e.mocks, key)
			delete(e.bodyCache, key)
		}
	}

	return nil
}

// SetGlobalDelay sets a global delay for all responses.
func (e *InMemoryMockEngine) SetGlobalDelay(delay time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.globalDelay = delay
}

// SetGlobalHeaders sets global headers.
func (e *InMemoryMockEngine) SetGlobalHeaders(headers map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.globalHeaders = headers
}

// Stats returns statistics about the mock engine.
func (e *InMemoryMockEngine) Stats() map[string]interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	apiCounts := make(map[string]int)
	for key := range e.mocks {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) > 0 {
			apiCounts[parts[0]]++
		}
	}

	return map[string]interface{}{
		"total_mocks": len(e.mocks),
		"api_count":   len(apiCounts),
		"api_breakdown": apiCounts,
	}
}

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
		StatusCode: r.StatusCode,
		Body:       r.Body,
		BodyRaw:    append([]byte(nil), r.BodyRaw...),
		Delay:      r.Delay,
		Format:     r.Format,
		Template:   r.Template,
		Headers:    make(map[string]string, len(r.Headers)),
	}

	for k, v := range r.Headers {
		clone.Headers[k] = v
	}

	return clone
}

// DefaultMockStore is the default global mock engine instance.
var DefaultMockStore = NewInMemoryMockEngine()

// RegisterMock is a convenience function for the default engine.
func RegisterMock(apiID, method, path string, response MockResponse) error {
	return DefaultMockStore.RegisterMock(apiID, method, path, response)
}

// GetMockResponse is a convenience function for the default engine.
func GetMockResponse(apiID, method, path string) (*MockResponse, bool) {
	return DefaultMockStore.GetMockResponse(apiID, method, path)
}

// AutoGenerate is a convenience function for the default engine.
func AutoGenerate(apiID string, resources []ResourceDef) error {
	return DefaultMockStore.AutoGenerate(apiID, resources)
}
