// Package mock provides HTTP handlers for serving mock responses via the gateway.
package mock

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MockServerConfig holds configuration for the mock server.
type MockServerConfig struct {
	// DefaultDelay is the default delay for all mock responses.
	DefaultDelay time.Duration
	// DefaultStatusCode is the default status code when no mock is found.
	DefaultStatusCode int
	// EnableCORS enables CORS headers on mock responses.
	EnableCORS bool
	// CorsOrigin is the allowed origin for CORS.
	CorsOrigin string
	// EnableLogging enables request/response logging.
	EnableLogging bool
	// ResponseFormat is the default response format (json, xml, text).
	ResponseFormat string
	// GatewayPrefix is the URL prefix for gateway routes.
	GatewayPrefix string
	// SandboxMode enables sandbox-specific headers.
	SandboxMode bool
}

// DefaultMockServerConfig returns a default configuration.
func DefaultMockServerConfig() *MockServerConfig {
	return &MockServerConfig{
		DefaultDelay:      0,
		DefaultStatusCode: 404,
		EnableCORS:        true,
		CorsOrigin:        "*",
		EnableLogging:     true,
		ResponseFormat:    "json",
		GatewayPrefix:     "/mock",
		SandboxMode:       false,
	}
}

// Server handles mock HTTP requests.
type Server struct {
	engine *InMemoryMockEngine
	config *MockServerConfig
}

// NewServer creates a new mock server.
func NewServer(engine *InMemoryMockEngine, config *MockServerConfig) *Server {
	if engine == nil {
		engine = NewInMemoryMockEngine()
	}
	if config == nil {
		config = DefaultMockServerConfig()
	}
	return &Server{
		engine: engine,
		config: config,
	}
}

// Engine returns the underlying mock engine.
func (s *Server) Engine() *InMemoryMockEngine {
	return s.engine
}

// Config returns the server configuration.
func (s *Server) Config() *MockServerConfig {
	return s.config
}

// ServeHTTP implements http.Handler for direct HTTP serving.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract API ID from path (e.g., /mock/{apiId}/path)
	path := r.URL.Path
	if s.config.GatewayPrefix != "" {
		path = strings.TrimPrefix(path, s.config.GatewayPrefix)
	}
	path = strings.TrimPrefix(path, "/")

	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		s.writeError(w, http.StatusBadRequest, "Missing API ID in path")
		return
	}

	apiID := parts[0]
	resourcePath := "/"
	if len(parts) > 1 {
		resourcePath = "/" + parts[1]
	}

	s.serveMock(w, r, apiID, resourcePath)
}

// GinHandler returns a gin.HandlerFunc for use with Gin router.
func (s *Server) GinHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiID := c.Param("apiId")
		if apiID == "" {
			s.writeGinError(c, http.StatusBadRequest, "Missing API ID")
			return
		}

		// Reconstruct the resource path from the wildcard
		resourcePath := c.Request.URL.Path
		prefix := fmt.Sprintf("/mock/%s", apiID)
		resourcePath = strings.TrimPrefix(resourcePath, prefix)
		if resourcePath == "" {
			resourcePath = "/"
		}

		s.serveGinMock(c, apiID, resourcePath)
	}
}

// RegisterRoutes registers mock server routes with a Gin router.
func (s *Server) RegisterRoutes(router *gin.Engine) {
	// Admin endpoints for mock management
	router.GET("/mock/admin/stats", s.handleAdminStats)
	router.GET("/mock/admin/apis/:apiId/mocks", s.handleListMocks)
	router.POST("/mock/admin/apis/:apiId/mocks", s.handleRegisterMock)
	router.DELETE("/mock/admin/apis/:apiId/mocks", s.handleDeleteAllMocks)
	router.DELETE("/mock/admin/apis/:apiId/mocks/:method/*path", s.handleDeleteMock)

	// Catch-all mock endpoint
	router.Any(fmt.Sprintf("%s/:apiId/*wildcard", s.config.GatewayPrefix), s.GinHandler())
}

func (s *Server) serveMock(w http.ResponseWriter, r *http.Request, apiID, resourcePath string) {
	if s.config.EnableLogging {
		fmt.Printf("[MOCK] %s %s (API: %s, Path: %s)\n", r.Method, r.URL.Path, apiID, resourcePath)
	}

	// Apply delay
	if s.config.DefaultDelay > 0 {
		time.Sleep(s.config.DefaultDelay)
	}

	// Get mock response
	mock, found := s.engine.GetMockResponse(apiID, r.Method, resourcePath)
	if !found {
		// Try wildcard matching
		mock, found = s.findWildcardMock(apiID, r.Method, resourcePath)
	}

	if !found {
		s.writeError(w, s.config.DefaultStatusCode, fmt.Sprintf("No mock found for %s %s on API %s", r.Method, resourcePath, apiID))
		return
	}

	// Apply mock-specific delay
	if mock.Delay > 0 {
		time.Sleep(mock.Delay)
	}

	// Write CORS headers
	if s.config.EnableCORS {
		w.Header().Set("Access-Control-Allow-Origin", s.config.CorsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Write response headers
	for key, value := range mock.Headers {
		w.Header().Set(key, value)
	}

	// Write sandbox headers
	if s.config.SandboxMode {
		w.Header().Set("X-Sandbox-Response", "true")
		w.Header().Set("X-Environment", "sandbox")
	}

	// Write status code
	statusCode := mock.StatusCode
	if statusCode == 0 {
		statusCode = 200
	}

	// Serialize body
	body := mock.BodyRaw
	if body == nil && mock.Body != nil {
		var err error
		body, err = s.serializeBody(mock.Body, mock.Format)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "Failed to serialize response body")
			return
		}
	}

	w.WriteHeader(statusCode)
	if body != nil {
		w.Write(body)
	}

	if s.config.EnableLogging {
		fmt.Printf("[MOCK] Response: %d (%d bytes)\n", statusCode, len(body))
	}
}

func (s *Server) serveGinMock(c *gin.Context, apiID, resourcePath string) {
	if s.config.EnableLogging {
		fmt.Printf("[MOCK] %s %s (API: %s, Path: %s)\n", c.Request.Method, c.Request.URL.Path, apiID, resourcePath)
	}

	// Apply delay
	if s.config.DefaultDelay > 0 {
		time.Sleep(s.config.DefaultDelay)
	}

	// Get mock response
	mock, found := s.engine.GetMockResponse(apiID, c.Request.Method, resourcePath)
	if !found {
		mock, found = s.findWildcardMock(apiID, c.Request.Method, resourcePath)
	}

	if !found {
		c.JSON(s.config.DefaultStatusCode, gin.H{
			"error": fmt.Sprintf("No mock found for %s %s on API %s", c.Request.Method, resourcePath, apiID),
		})
		return
	}

	// Apply mock-specific delay
	if mock.Delay > 0 {
		time.Sleep(mock.Delay)
	}

	// Write CORS headers
	if s.config.EnableCORS {
		c.Header("Access-Control-Allow-Origin", s.config.CorsOrigin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
	}

	if c.Request.Method == "OPTIONS" {
		c.Status(http.StatusNoContent)
		return
	}

	// Write response headers
	for key, value := range mock.Headers {
		c.Header(key, value)
	}

	// Write sandbox headers
	if s.config.SandboxMode {
		c.Header("X-Sandbox-Response", "true")
		c.Header("X-Environment", "sandbox")
	}

	statusCode := mock.StatusCode
	if statusCode == 0 {
		statusCode = 200
	}

	c.Status(statusCode)

	// Serialize and write body
	body := mock.BodyRaw
	if body == nil && mock.Body != nil {
		var err error
		body, err = s.serializeBody(mock.Body, mock.Format)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize response"})
			return
		}
	}

	if body != nil {
		c.Writer.Write(body)
	}

	if s.config.EnableLogging {
		fmt.Printf("[MOCK] Response: %d (%d bytes)\n", statusCode, len(body))
	}
}

func (s *Server) serializeBody(body interface{}, format string) ([]byte, error) {
	switch strings.ToLower(format) {
	case "xml":
		return xml.MarshalIndent(body, "", "  ")
	case "text":
		if str, ok := body.(string); ok {
			return []byte(str), nil
		}
		return []byte(fmt.Sprintf("%v", body)), nil
	case "json", "":
		return json.Marshal(body)
	default:
		return json.Marshal(body)
	}
}

func (s *Server) findWildcardMock(apiID, method, path string) (*MockResponse, bool) {
	// Try matching with path parameter wildcards
	allMocks, err := s.engine.ListMocks(apiID)
	if err != nil {
		return nil, false
	}

	// Get all keys for this API and method
	for _, mock := range allMocks {
		// This is simplified - in production, we'd need to track the original path pattern
		// For now, check if any mock exists with a matching method
		_ = mock
	}

	// TODO: Implement proper wildcard matching
	return nil, false
}

func (s *Server) writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":       "mock_not_found",
		"message":     message,
		"status_code": code,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) writeGinError(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{
		"error":     "mock_not_found",
		"message":   message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// --- Admin handlers ---

func (s *Server) handleAdminStats(c *gin.Context) {
	stats := s.engine.Stats()
	c.JSON(http.StatusOK, stats)
}

func (s *Server) handleListMocks(c *gin.Context) {
	apiID := c.Param("apiId")
	mocks, err := s.engine.ListMocks(apiID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type mockInfo struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string]string   `json:"headers"`
		Format     string              `json:"format"`
		Delay      string              `json:"delay"`
	}

	var result []mockInfo
	for _, m := range mocks {
		result = append(result, mockInfo{
			StatusCode: m.StatusCode,
			Headers:    m.Headers,
			Format:     m.Format,
			Delay:      m.Delay.String(),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id": apiID,
		"count":  len(result),
		"mocks":  result,
	})
}

func (s *Server) handleRegisterMock(c *gin.Context) {
	apiID := c.Param("apiId")

	var req struct {
		Method     string            `json:"method" binding:"required"`
		Path       string            `json:"path" binding:"required"`
		StatusCode int               `json:"status_code"`
		Headers    map[string]string `json:"headers"`
		Body       interface{}       `json:"body"`
		Format     string            `json:"format"`
		Delay      string            `json:"delay"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	delay := time.Duration(0)
	if req.Delay != "" {
		d, err := time.ParseDuration(req.Delay)
		if err == nil {
			delay = d
		}
	}

	mock := MockResponse{
		StatusCode: req.StatusCode,
		Headers:    req.Headers,
		Body:       req.Body,
		Format:     req.Format,
		Delay:      delay,
	}

	if err := s.engine.RegisterMock(apiID, req.Method, req.Path, mock); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Mock registered successfully",
		"api_id":  apiID,
		"method":  req.Method,
		"path":    req.Path,
	})
}

func (s *Server) handleDeleteMock(c *gin.Context) {
	apiID := c.Param("apiId")
	method := c.Param("method")
	path := c.Param("path")

	if err := s.engine.DeleteMock(apiID, method, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Mock deleted",
		"api_id":  apiID,
		"method":  method,
		"path":    path,
	})
}

func (s *Server) handleDeleteAllMocks(c *gin.Context) {
	apiID := c.Param("apiId")

	if err := s.engine.DeleteAllMocks(apiID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "All mocks deleted",
		"api_id":  apiID,
	})
}

// --- Configurable Response ---

// ConfigurableMockHandler allows dynamic configuration of mock responses.
type ConfigurableMockHandler struct {
	engine     *InMemoryMockEngine
	selectors  []ResponseSelector
}

// ResponseSelector selects a response based on request criteria.
type ResponseSelector struct {
	Condition func(r *http.Request) bool
	Response  MockResponse
}

// NewConfigurableMockHandler creates a new configurable handler.
func NewConfigurableMockHandler(engine *InMemoryMockEngine) *ConfigurableMockHandler {
	return &ConfigurableMockHandler{
		engine:    engine,
		selectors: make([]ResponseSelector, 0),
	}
}

// AddSelector adds a response selector.
func (h *ConfigurableMockHandler) AddSelector(selector ResponseSelector) {
	h.selectors = append(h.selectors, selector)
}

// HandleRequest processes a request using configured selectors.
func (h *ConfigurableMockHandler) HandleRequest(w http.ResponseWriter, r *http.Request, apiID, resourcePath string) {
	// Check selectors first
	for _, sel := range h.selectors {
		if sel.Condition(r) {
			h.writeMockResponse(w, &sel.Response)
			return
		}
	}

	// Fall through to engine
	mock, found := h.engine.GetMockResponse(apiID, r.Method, resourcePath)
	if !found {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "no matching mock"})
		return
	}

	h.writeMockResponse(w, mock)
}

func (h *ConfigurableMockHandler) writeMockResponse(w http.ResponseWriter, mock *MockResponse) {
	if mock.Delay > 0 {
		time.Sleep(mock.Delay)
	}

	for key, value := range mock.Headers {
		w.Header().Set(key, value)
	}

	statusCode := mock.StatusCode
	if statusCode == 0 {
		statusCode = 200
	}

	body, _ := json.Marshal(mock.Body)
	w.WriteHeader(statusCode)
	w.Write(body)
}

// --- Utility Functions ---

// ParseDelay parses a duration string with support for shorthand notation.
// Supports: 100ms, 1s, 500ms, 2.5s, random(100ms,500ms)
func ParseDelay(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}

	// Handle random range
	if strings.HasPrefix(s, "random(") && strings.HasSuffix(s, ")") {
		inner := s[7 : len(s)-1]
		parts := strings.Split(inner, ",")
		if len(parts) == 2 {
			min, err1 := time.ParseDuration(strings.TrimSpace(parts[0]))
			max, err2 := time.ParseDuration(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil {
				// Return average for now - actual randomization happens in server
				return (min + max) / 2, nil
			}
		}
	}

	return time.ParseDuration(s)
}

// StatusCodeFromString parses a status code, supporting named status codes.
func StatusCodeFromString(s string) int {
	switch strings.ToLower(s) {
	case "ok":
		return http.StatusOK
	case "created":
		return http.StatusCreated
	case "accepted":
		return http.StatusAccepted
	case "nocontent", "no_content":
		return http.StatusNoContent
	case "badrequest", "bad_request":
		return http.StatusBadRequest
	case "unauthorized":
		return http.StatusUnauthorized
	case "forbidden":
		return http.StatusForbidden
	case "notfound", "not_found":
		return http.StatusNotFound
	case "conflict":
		return http.StatusConflict
	case "internal", "internalerror", "internal_error":
		return http.StatusInternalServerError
	case "serviceunavailable", "service_unavailable":
		return http.StatusServiceUnavailable
	default:
		if code, err := fmt.Sscanf(s, "%d", new(int)); err == nil {
			return code
		}
		return 200
	}
}

// ReadBody reads and returns the request body, restoring it for further reads.
func ReadBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	// Restore body for potential re-reads
	r.Body.Close()
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	return body, nil
}
