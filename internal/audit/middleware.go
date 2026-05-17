// Package audit provides Gin middleware for automatic request audit logging.
package audit

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MiddlewareConfig configures the audit middleware behavior.
type MiddlewareConfig struct {
	// Logger is the audit logger instance.
	Logger AuditLogger
	// ExcludePaths is a list of path prefixes to skip audit logging for.
	ExcludePaths []string
	// LogRequestBody if true, captures request bodies (be careful with sensitive data).
	LogRequestBody bool
	// LogResponseBody if true, captures response bodies.
	LogResponseBody bool
	// SensitiveFields are field names whose values will be redacted from logs.
	SensitiveFields []string
	// SkipHealthCheck if true, skips logging for health check endpoints.
	SkipHealthCheck bool
	// MaxBodySize limits the number of bytes captured from request/response bodies.
	MaxBodySize int64
}

// DefaultMiddlewareConfig returns a sensible default configuration.
func DefaultMiddlewareConfig(logger AuditLogger) MiddlewareConfig {
	return MiddlewareConfig{
		Logger:          logger,
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

// Middleware returns a Gin middleware that logs every HTTP request as an audit event.
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
		// Check exact exclusions
		if excludeMap[path] {
			return true
		}
		// Check prefix exclusions
		for prefix := range excludeMap {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
		// Check health check paths
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
				// Restore the body for downstream handlers
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				// Redact sensitive fields
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
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()

		// Extract user info from context
		var userID, username string
		if uid, exists := c.Get("user_id"); exists {
			userID, _ = uid.(string)
		}
		if un, exists := c.Get("username"); exists {
			username, _ = un.(string)
		}

		// Build audit details
		details := map[string]interface{}{
			"method":      method,
			"path":        path,
			"status":      status,
			"duration_ms": elapsed.Milliseconds(),
			"user_agent":  userAgent,
		}

		if requestBody != "" {
			details["request_body"] = requestBody
		}

		if cfg.LogResponseBody && wrapped != nil {
			respBody := wrapped.body.String()
			if len(respBody) > int(cfg.MaxBodySize) {
				respBody = respBody[:cfg.MaxBodySize]
			}
			details["response_body"] = redactSensitiveFields(respBody, sensitiveMap)
		}

		// Add errors from Gin context if any
		if len(c.Errors) > 0 {
			details["errors"] = c.Errors.Errors()
		}

		// Determine action based on HTTP method and path
		action := mapHTTPMethodToAction(method, status)
		resourceType := "HTTP_REQUEST"
		resourceID := path

		// Build context with user info for the logger
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, "user_id", userID)
		ctx = context.WithValue(ctx, "username", username)
		ctx = context.WithValue(ctx, "client_ip", clientIP)

		// Log asynchronously via goroutine for non-blocking operation
		go cfg.Logger.Log(ctx, action, resourceType, resourceID, details)
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
// This is a best-effort approach for JSON-like payloads.
func redactSensitiveFields(input string, sensitiveFields map[string]bool) string {
	// Simple string-based redaction for common patterns like "password": "secret123"
	result := input
	for field := range sensitiveFields {
		// Match patterns like "fieldname": "value" or 'fieldname': 'value'
		// This is a basic approach; for production, use proper JSON parsing
		patterns := []string{
			`"` + field + `"\s*:\s*"[^"]*"`,
			`"` + field + `"\s*:\s*'[^']*'`,
			`'` + field + `'\s*:\s*"[^"]*"`,
			`'` + field + `'\s*:\s*'[^']*'`,
		}
		for _, pattern := range patterns {
			idx := 0
			for {
				foundIdx := strings.Index(result[idx:], `"`+field+`"`)
				if foundIdx == -1 {
					break
				}
				foundIdx += idx
				// Find the colon and value
				colonIdx := strings.Index(result[foundIdx:], ":")
				if colonIdx == -1 {
					break
				}
				colonIdx += foundIdx
				// Find the next quote after colon
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
	}
	return result
}
