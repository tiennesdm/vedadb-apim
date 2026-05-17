package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// maxTryItBodySize limits the request/response body size in the try-it console.
const maxTryItBodySize int64 = 1 << 20 // 1 MB

// handleTryIt executes a test API call on behalf of the user and returns
// the full request/response details for the Try-it console.
//
// Request body:
//   - method: HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS)
//   - url: target URL
//   - headers: map of headers to send
//   - body: request body (string or object)
//   - auth_type: "none", "basic", "bearer", "api_key"
//   - auth_token: authorization token
//   - timeout_ms: request timeout in milliseconds (default 30000)
func (s *Server) handleTryIt(c *gin.Context) {
	var req models.TryItRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid request body: " + err.Error(),
			Code:      "INVALID_REQUEST",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Validate URL
	if req.URL == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "url is required",
			Code:      "VALIDATION_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid URL: " + err.Error(),
			Code:      "INVALID_URL",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Only allow http and https
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

	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bodyReader)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "failed to build request: " + err.Error(),
			Code:      "BUILD_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Apply headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Apply authentication
	applyAuth(httpReq, req.AuthType, req.AuthToken)

	// Ensure Accept and Content-Type are set
	if httpReq.Header.Get("Accept") == "" {
		httpReq.Header.Set("Accept", "*/*")
	}

	// Capture request details for response
	reqDetails := models.RequestDetails{
		Method:  method,
		URL:     req.URL,
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

	// Execute request
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil // follow redirects
		},
	}

	start := time.Now()
	httpResp, err := client.Do(httpReq)
	latency := time.Since(start)

	if err != nil {
		s.logger.Warn("try-it request failed", "url", req.URL, "error", err, "request_id", c.GetString("request_id"))
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
		StatusCode: httpResp.StatusCode,
		Status:     httpResp.Status,
		Headers:    flattenHeaders(httpResp.Header),
		Body:       respBody,
		BodySize:   int64(len(bodyBytes)),
	}

	c.JSON(http.StatusOK, models.TryItResponse{
		Success:        true,
		LatencyMs:      latency.Milliseconds(),
		Request:        reqDetails,
		Response:       respDetails,
		RequestID:      c.GetString("request_id"),
	})
}

// handleTryItProxy is a CORS proxy for the try-it console that forwards
// requests to APIs that don't allow cross-origin requests from the portal.
//
// Request body:
//   - target_url: URL to proxy to
//   - method: HTTP method
//   - headers: headers to forward
//   - body: request body
//   - auth_type: authentication type
//   - auth_token: authentication token
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
		// Don't forward host header
		if strings.EqualFold(key, "Host") {
			continue
		}
		httpReq.Header.Set(key, value)
	}

	applyAuth(httpReq, req.AuthType, req.AuthToken)

	// Add CORS headers to indicate proxy
	httpReq.Header.Set("X-Forwarded-By", "vedadb-apim-tryit-proxy")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // don't follow redirects in proxy
		},
	}

	start := time.Now()
	httpResp, err := client.Do(httpReq)
	latency := time.Since(start)

	if err != nil {
		c.JSON(http.StatusOK, models.TryItResponse{
			Success:        false,
			Error:          err.Error(),
			LatencyMs:      latency.Milliseconds(),
			RequestID:      c.GetString("request_id"),
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
		LatencyMs: latency.Milliseconds(),
		Request: models.RequestDetails{
			Method:  method,
			URL:     req.TargetURL,
			Headers: flattenHeaders(httpReq.Header),
		},
		Response: models.ResponseDetails{
			StatusCode: httpResp.StatusCode,
			Status:     httpResp.Status,
			Headers:    flattenHeaders(httpResp.Header),
			Body:       respBody,
			BodySize:   int64(len(bodyBytes)),
		},
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
		// Support both header and query param styles
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
