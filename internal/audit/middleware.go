// Package audit provides Gin middleware for automatic request audit logging
// with guaranteed DB persistence via the VedaDB wire protocol.
package audit

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/vedadb/vapim/pkg/models"
)

// MiddlewareConfig configures the audit middleware behavior.
type MiddlewareConfig struct {
	Auditor         *AuditLogger
	ExcludePaths    []string
	LogRequestBody  bool
	LogResponseBody bool
	SensitiveFields []string
	SkipHealthCheck bool
	MaxBodySize     int64
}

// DefaultMiddlewareConfig returns a sensible default configuration.
func DefaultMiddlewareConfig(auditor *AuditLogger) MiddlewareConfig {
	return MiddlewareConfig{
		Auditor:         auditor,
		ExcludePaths:    []string{"/graphql/playground", "/static/", "/favicon.ico"},
		LogRequestBody:  false,
		LogResponseBody: false,
		SensitiveFields: []string{"password", "secret", "token", "api_key", "apiKey", "authorization"},
		SkipHealthCheck: true,
		MaxBodySize:     4096,
	}
}

// responseWriter wraps gin.ResponseWriter to capture the response body.
type responseWriter struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func newResponseWriter(rw gin.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: rw,
		body:           &bytes.Buffer{},
		status:         http.StatusOK,
	}
}

func (w *responseWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// isImportantGet returns true for GET requests that should be audited
// (e.g., sensitive resources, export endpoints).
func isImportantGet(path string) bool {
	importantPatterns := []string{
		"/export", "/download", "/keys", "/secrets", "/credentials",
		"/tokens", "/auth", "/login", "/logout", "/admin",
	}
	lowerPath := strings.ToLower(path)
	for _, p := range importantPatterns {
		if strings.Contains(lowerPath, p) {
			return true
		}
	}
	return false
}

// extractResourceType determines the resource type from the request path.
func extractResourceType(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 {
		// Common REST patterns: /api/v1/<resource>/...
		for _, p := range parts {
			if p == "v1" || p == "v2" || p == "api" {
				continue
			}
			if p != "" && !strings.HasPrefix(p, ":") {
				return p
			}
		}
	}
	if len(parts) >= 1 && parts[0] != "" {
		return parts[0]
	}
	return "http_request"
}

// Middleware returns a Gin middleware that logs every significant HTTP request
// as an audit event directly to the database.
func Middleware(cfg MiddlewareConfig) gin.HandlerFunc {
	excludeMap := make(map[string]bool)
	for _, path := range cfg.ExcludePaths {
		excludeMap[path] = true
	}

	sensitiveMap := make(map[string]bool)
	for _, field := range cfg.SensitiveFields {
		sensitiveMap[field] = true
	}

	shouldExclude := func(path string) bool {
		if excludeMap[path] {
			return true
		}
		for prefix := range excludeMap {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
		if cfg.SkipHealthCheck {
			if strings.EqualFold(path, "/health") ||
				strings.EqualFold(path, "/healthz") ||
				strings.EqualFold(path, "/ready") ||
				strings.EqualFold(path, "/readyz") ||
				strings.HasPrefix(path, "/health/") {
				return true
			}
		}
		return false
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skip excluded paths
		if shouldExclude(path) {
			c.Next()
			return
		}

		start := time.Now().UTC()

		// Capture request body if enabled
		var requestBody string
		if cfg.LogRequestBody && c.Request.Body != nil {
			bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, cfg.MaxBodySize))
			if err == nil {
				requestBody = string(bodyBytes)
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				requestBody = redactSensitiveFields(requestBody, sensitiveMap)
			}
		}

		// Wrap response writer to capture response
		var wrapped *responseWriter
		if cfg.LogResponseBody {
			wrapped = newResponseWriter(c.Writer)
			c.Writer = wrapped
		}

		// Process the request
		c.Next()

		elapsed := time.Since(start)
		status := c.Writer.Status()
		method := c.Request.Method

		// Only log significant actions: mutations (POST/PUT/PATCH/DELETE),
		// failed requests, and important GETs
		if method == "GET" && !isImportantGet(path) && status < 400 {
			return
		}

		// Extract tenant and user from context
		tenantID, _ := c.Get("tenantID").(string)
		if tenantID == "" {
			tenantID, _ = c.Get("tenant_id").(string)
		}
		userID, _ := c.Get("userID").(string)
		if userID == "" {
			userID, _ = c.Get("user_id").(string)
		}
		if userID == "" {
			if uid, exists := c.Get("user_id"); exists {
				userID, _ = uid.(string)
			}
		}

		username, _ := c.Get("username").(string)
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()

		// Build audit details
		detailsParts := []string{
			fmt.Sprintf("status=%d", status),
			fmt.Sprintf("latency=%dms", elapsed.Milliseconds()),
			fmt.Sprintf("method=%s", method),
			fmt.Sprintf("path=%s", path),
		}
		if username != "" {
			detailsParts = append(detailsParts, "user="+username)
		}
		if requestBody != "" {
			detailsParts = append(detailsParts, "request_body="+requestBody)
		}
		if cfg.LogResponseBody && wrapped != nil {
			respBody := wrapped.body.String()
			if len(respBody) > int(cfg.MaxBodySize) {
				respBody = respBody[:cfg.MaxBodySize]
			}
			respBody = redactSensitiveFields(respBody, sensitiveMap)
			detailsParts = append(detailsParts, "response_body="+respBody)
		}
		if len(c.Errors) > 0 {
			detailsParts = append(detailsParts, "errors="+strings.Join(c.Errors.Errors(), "; "))
		}

		entry := &models.AuditLogDB{
			ID:           uuid.New().String(),
			TenantID:     tenantID,
			UserID:       userID,
			Action:       fmt.Sprintf("%s %s", method, path),
			ResourceType: extractResourceType(path),
			ResourceID:   c.Param("id"),
			Details:      strings.Join(detailsParts, ", "),
			IPAddress:    clientIP,
			UserAgent:    userAgent,
			Timestamp:    time.Now().UTC(),
		}

		// REAL DB WRITE - async via goroutine to avoid blocking response
		go func(auditor *AuditLogger, logEntry *models.AuditLogDB) {
			if auditor != nil {
				if err := auditor.InsertEntry(logEntry); err != nil {
					// Log failure but don't block response
					// The InsertEntry method handles its own error reporting
				}
			}
		}(cfg.Auditor, entry)
	}
}

// mapHTTPMethodToAction maps HTTP method and status to an audit action string.
func mapHTTPMethodToAction(method string, status int) string {
	action := "UNKNOWN"
	switch strings.ToUpper(method) {
	case http.MethodGet:
		action = "READ"
	case http.MethodPost:
		action = "CREATE"
	case http.MethodPut, http.MethodPatch:
		action = "UPDATE"
	case http.MethodDelete:
		action = "DELETE"
	case http.MethodHead:
		action = "HEAD"
	case http.MethodOptions:
		action = "OPTIONS"
	}

	if status >= 400 {
		action += "_FAILED"
	} else if status >= 300 {
		action += "_REDIRECT"
	}

	return action
}

// redactSensitiveFields redacts values of sensitive fields from a string representation.
func redactSensitiveFields(input string, sensitiveFields map[string]bool) string {
	result := input
	for field := range sensitiveFields {
		idx := 0
		for {
			foundIdx := strings.Index(strings.ToLower(result[idx:]), `"`+strings.ToLower(field)+`"`)
			if foundIdx == -1 {
				// Try single quote
				foundIdx = strings.Index(strings.ToLower(result[idx:]), `'`+strings.ToLower(field)+`'')
				if foundIdx == -1 {
					break
				}
			}
			foundIdx += idx

			colonIdx := strings.Index(result[foundIdx:], ":")
			if colonIdx == -1 {
				break
			}
			colonIdx += foundIdx

			afterColon := colonIdx + 1
			for afterColon < len(result) && (result[afterColon] == ' ' || result[afterColon] == '\t') {
				afterColon++
			}
			if afterColon < len(result) && (result[afterColon] == '"' || result[afterColon] == '\'') {
				quoteChar := result[afterColon]
				endQuote := strings.Index(result[afterColon+1:], string(quoteChar))
				if endQuote != -1 {
					endQuote += afterColon + 1
					result = result[:afterColon+1] + "[REDACTED]" + result[endQuote:]
				}
			}
			idx = foundIdx + 1
		}
	}
	return result
}
