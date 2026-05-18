// Package portal implements the Developer Portal HTTP server for VedaDB API Manager.
// This file provides the Try-It console handler that loads API definitions from
// the database and executes real HTTP calls through the gateway.
package portal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vedadb/vapim/pkg/models"
)

// maxTryItBodySize limits the request/response body size in the try-it console.
const maxTryItBodySize int64 = 1 << 20 // 1 MB

// tryItAPIRequest is the enriched request that includes the API ID for DB lookup.
type tryItAPIRequest struct {
	APIID     string            `json:"api_id" binding:"required"`
	Method    string            `json:"method"`
	Path      string            `json:"path" binding:"required"`
	Headers   map[string]string `json:"headers"`
	Body      interface{}       `json:"body"`
	AuthType  string            `json:"auth_type"`  // none, basic, bearer, api_key
	AuthToken string            `json:"auth_token"` // token for bearer/basic
	APIKey    string            `json:"api_key"`    // key for api_key auth
	TimeoutMs int               `json:"timeout_ms"`
}

// handleTryItAPI executes a test API call by first loading the API from the database,
// then building the target URL from the API endpoint + resource path, executing
// the HTTP request, and recording analytics.
func (s *Server) handleTryItAPI(c *gin.Context) {
	var req tryItAPIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid request body: " + err.Error(),
			Code:      "INVALID_REQUEST",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// REAL DB QUERY: Get API by ID
	api, err := s.store.GetAPIDetails(c.Request.Context(), req.APIID)
	if err != nil {
		s.logger.Warn("try-it api not found", "api_id", req.APIID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:     "API not found",
			Code:      "API_NOT_FOUND",
			Status:    http.StatusNotFound,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Validate the API is published
	if api.Status != "PUBLISHED" {
		c.JSON(http.StatusForbidden, models.ErrorResponse{
			Error:     "API is not published and cannot be tested",
			Code:      "API_NOT_PUBLISHED",
			Status:    http.StatusForbidden,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Build target URL from API endpoint + resource path
	targetURL := api.Endpoint + req.Path

	// Validate the URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid target URL: " + err.Error(),
			Code:      "INVALID_URL",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "only http and https URLs are allowed",
			Code:      "INVALID_SCHEME",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Normalize method
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = "GET"
	}
	if !isValidMethod(method) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "unsupported HTTP method: " + method,
			Code:      "INVALID_METHOD",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Build request body
	var bodyReader io.Reader
	if req.Body != nil {
		bodyBytes, err := serializeBody(req.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error:     "invalid body: " + err.Error(),
				Code:      "INVALID_BODY",
				Status:    http.StatusBadRequest,
				RequestID: c.GetString("request_id"),
			})
			return
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Timeout
	timeout := 30 * time.Second
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
		if timeout > 120*time.Second {
			timeout = 120 * time.Second
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "failed to build request: " + err.Error(),
			Code:      "BUILD_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Copy headers from request
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Add auth based on type
	switch strings.ToLower(req.AuthType) {
	case "bearer":
		httpReq.Header.Set("Authorization", "Bearer "+req.AuthToken)
	case "basic":
		httpReq.Header.Set("Authorization", "Basic "+req.AuthToken)
	case "api_key":
		httpReq.Header.Set("X-API-Key", req.APIKey)
	}

	// Ensure Accept header
	if httpReq.Header.Get("Accept") == "" {
		httpReq.Header.Set("Accept", "*/*")
	}

	// Capture request details for response
	reqDetails := models.RequestDetails{
		Method:  method,
		URL:     targetURL,
		Headers: flattenHeaders(httpReq.Header),
	}
	if bodyReader != nil {
		if b, ok := bodyReader.(*bytes.Reader); ok {
			data := make([]byte, b.Len())
			b.Read(data)
			reqDetails.Body = string(data)
			b.Seek(0, io.SeekStart)
		}
	}

	// Execute HTTP request (REAL HTTP CALL)
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	start := time.Now()
	httpResp, err := client.Do(httpReq)
	latency := time.Since(start)

	// Record analytics (async) - REAL DB WRITE
	var userID string
	if uid, exists := c.Get("user_id"); exists {
		userID, _ = uid.(string)
	}
	go func(apiID string, method string, path string, resp *http.Response, callErr error, lat time.Duration, uid string) {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		if callErr != nil {
			statusCode = 0
		}

		event := &models.AnalyticsEventDB{
			ID:         fmt.Sprintf("tryit-%d", time.Now().UnixNano()),
			APIID:      apiID,
			UserID:     uid,
			Method:     method,
			Path:       path,
			StatusCode: statusCode,
			LatencyMs:  int(lat.Milliseconds()),
			Timestamp:  time.Now().UTC(),
		}

		if err := s.store.InsertAnalyticsEvent(event); err != nil {
			s.logger.Warn("failed to record try-it analytics", "error", err, "api_id", apiID)
		}
	}(api.ID, method, req.Path, httpResp, err, latency, userID)

	if err != nil {
		s.logger.Warn("try-it request failed",
			"url", targetURL,
			"error", err,
			"request_id", c.GetString("request_id"),
		)
		c.JSON(http.StatusOK, models.TryItResponse{
			Success:        false,
			Error:          err.Error(),
			LatencyMs:      latency.Milliseconds(),
			Request:        reqDetails,
			RequestID:      c.GetString("request_id"),
		})
		return
	}
	defer httpResp.Body.Close()

	// Read response body (with size limit)
	bodyBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, maxTryItBodySize))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to read response body: " + err.Error(),
			Code:      "READ_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Format response body
	var respBody interface{}
	respBodyStr := string(bodyBytes)
	contentType := httpResp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var jsonBody interface{}
		if err := json.Unmarshal(bodyBytes, &jsonBody); err == nil {
			respBody = jsonBody
		} else {
			respBody = respBodyStr
		}
	} else {
		respBody = respBodyStr
	}

	respDetails := models.ResponseDetails{
		StatusCode: int64(httpResp.StatusCode),
		Status:     httpResp.Status,
		Headers:    flattenHeaders(httpResp.Header),
		Body:       respBody,
		BodySize:   int64(len(bodyBytes)),
	}

	c.JSON(http.StatusOK, models.TryItResponse{
		Success:   true,
		LatencyMs: latency.Milliseconds(),
		Request:   reqDetails,
		Response:  respDetails,
		RequestID: c.GetString("request_id"),
	})
}

// handleTryItProxy is a CORS proxy for the try-it console that forwards
// requests to APIs that don't allow cross-origin requests from the portal.
func (s *Server) handleTryItProxy(c *gin.Context) {
	var req models.TryItProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid request body: " + err.Error(),
			Code:      "INVALID_REQUEST",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Validate target URL
	parsedURL, err := url.Parse(req.TargetURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid target_url",
			Code:      "INVALID_URL",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	method := strings.ToUpper(req.Method)
	if method == "" {
		method = "GET"
	}
	if !isValidMethod(method) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "unsupported HTTP method",
			Code:      "INVALID_METHOD",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Build body
	var bodyReader io.Reader
	if req.Body != nil {
		bodyBytes, _ := serializeBody(req.Body)
		bodyReader = bytes.NewReader(bodyBytes)
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, method, req.TargetURL, bodyReader)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "failed to build proxy request: " + err.Error(),
			Code:      "BUILD_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Forward specified headers
	for key, value := range req.Headers {
		if strings.EqualFold(key, "Host") {
			continue
		}
		httpReq.Header.Set(key, value)
	}

	applyAuth(httpReq, req.AuthType, req.AuthToken)

	// Add proxy indicator header
	httpReq.Header.Set("X-Forwarded-By", "vedadb-apim-tryit-proxy")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	httpResp, err := client.Do(httpReq)
	latency := time.Since(start)

	if err != nil {
		c.JSON(http.StatusOK, models.TryItResponse{
			Success:   false,
			Error:     err.Error(),
			LatencyMs: latency.Milliseconds(),
			RequestID: c.GetString("request_id"),
		})
		return
	}
	defer httpResp.Body.Close()

	// Read response (with limit)
	bodyBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, maxTryItBodySize))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to read proxy response: " + err.Error(),
			Code:      "READ_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Format response body
	var respBody interface{}
	if strings.Contains(httpResp.Header.Get("Content-Type"), "application/json") {
		var jsonBody interface{}
		if err := json.Unmarshal(bodyBytes, &jsonBody); err == nil {
			respBody = jsonBody
		} else {
			respBody = string(bodyBytes)
		}
	} else {
		respBody = string(bodyBytes)
	}

	c.JSON(http.StatusOK, models.TryItResponse{
		Success: true,
		Request: models.RequestDetails{
			Method:  method,
			URL:     req.TargetURL,
			Headers: flattenHeaders(httpReq.Header),
		},
		Response: models.ResponseDetails{
			StatusCode: int64(httpResp.StatusCode),
			Status:     httpResp.Status,
			Headers:    flattenHeaders(httpResp.Header),
			Body:       respBody,
			BodySize:   int64(len(bodyBytes)),
		},
		LatencyMs: latency.Milliseconds(),
		RequestID: c.GetString("request_id"),
	})
}

// --- Helpers ---

func isValidMethod(m string) bool {
	switch m {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		return true
	}
	return false
}

func applyAuth(req *http.Request, authType, token string) {
	switch strings.ToLower(authType) {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+token)
	case "basic":
		req.Header.Set("Authorization", "Basic "+token)
	case "api_key":
		if req.Header.Get("X-API-Key") == "" {
			req.Header.Set("X-API-Key", token)
		}
	}
}

func serializeBody(body interface{}) ([]byte, error) {
	switch v := body.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	default:
		return json.Marshal(body)
	}
}

func flattenHeaders(h http.Header) map[string]string {
	result := make(map[string]string, len(h))
	for key, values := range h {
		result[key] = strings.Join(values, ", ")
	}
	return result
}
