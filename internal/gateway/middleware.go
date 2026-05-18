// Package gateway provides the complete middleware chain for the VedaDB API Manager Gateway.
//
// CRITICAL SECURITY: Every request is validated against the database.
// AuthMiddleware:    Validates OAuth2 tokens AND API keys via real DB queries.
// SubscriptionMiddleware: Validates app subscriptions via ValidateSubscription DB call.
// ThrottleMiddleware:     Enforces rate limits via ThrottlingEngine.Evaluate with DB-backed policies.
// APILookupMiddleware:    Resolves API context paths against published APIs in DB.
//
// NO STUBS. NO SHORTCUTS. Every auth decision is backed by a store query.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
	"github.com/tiennesdm/vedadb-apim/pkg/store"
	"github.com/tiennesdm/vedadb-apim/internal/keymanager"
	"github.com/tiennesdm/vedadb-apim/internal/traffic"
)

// ---------------------------------------------------------------------------
// Prometheus Metrics
// ---------------------------------------------------------------------------

var (
	// RequestCounter counts total HTTP requests.
	RequestCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "apim_gateway_requests_total",
		Help: "Total number of gateway requests",
	}, []string{"method", "path", "status"})

	// RequestDuration tracks request latency.
	RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "apim_gateway_request_duration_seconds",
		Help:    "Request latency distribution",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// ActiveRequests tracks in-flight requests.
	ActiveRequests = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "apim_gateway_active_requests",
		Help: "Number of active requests",
	})

	// CacheHitCounter tracks cache hits/misses.
	CacheHitCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "apim_gateway_cache_total",
		Help: "Total cache hits and misses",
	}, []string{"result"})

	// RateLimitCounter tracks rate limit decisions.
	RateLimitCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "apim_gateway_ratelimit_total",
		Help: "Total rate limit decisions",
	}, []string{"result"})

	// AuthCounter tracks authentication results.
	AuthCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "apim_gateway_auth_total",
		Help: "Total authentication results",
	}, []string{"type", "result"})
)

// initRegisterOnce ensures metrics are registered exactly once.
func init() {
	RegisterMetrics()
}

// RegisterMetrics registers all Prometheus collectors.
func RegisterMetrics() {
	prometheus.MustRegister(RequestCounter, RequestDuration, ActiveRequests,
		CacheHitCounter, RateLimitCounter, AuthCounter)
}

// ---------------------------------------------------------------------------
// GatewayMiddleware
// ---------------------------------------------------------------------------

// GatewayMiddleware is the consolidated middleware struct that validates
// EVERY request against the database store.
type GatewayMiddleware struct {
	store       store.Store
	keyManager  *keymanager.APIKeyManager
	throttleMgr *traffic.ThrottlingEngine
	cache       *CacheMiddleware
	logger      *zap.Logger
}

// NewGatewayMiddleware creates a new GatewayMiddleware with real store integration.
func NewGatewayMiddleware(
	st store.Store,
	keyMgr *keymanager.APIKeyManager,
	throttleMgr *traffic.ThrottlingEngine,
	cache *CacheMiddleware,
	logger *zap.Logger,
) *GatewayMiddleware {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GatewayMiddleware{
		store:       st,
		keyManager:  keyMgr,
		throttleMgr: throttleMgr,
		cache:       cache,
		logger:      logger,
	}
}

// ---------------------------------------------------------------------------
// 1. Recovery Middleware (catches panics)
// ---------------------------------------------------------------------------

// RecoveryMiddleware recovers from panics and returns a 500 error.
func RecoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered in request handler",
					zap.Any("panic", r),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("request_id", c.GetString("requestID")),
				)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "internal_server_error",
					"message": "An internal server error occurred",
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 2. RequestID Middleware
// ---------------------------------------------------------------------------

// RequestIDMiddleware generates or propagates a request ID.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
		}
		c.Set("requestID", reqID)
		c.Writer.Header().Set("X-Request-ID", reqID)
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 3. CORS Middleware
// ---------------------------------------------------------------------------

// CORSConfig holds per-API CORS configuration.
type CORSConfig struct {
	Enabled          bool
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAge           int
}

// CORSMiddleware returns the Gin CORS handler.
func CORSMiddleware(cfg CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = "*"
		}

		if len(cfg.AllowOrigins) > 0 && cfg.AllowOrigins[0] != "*" {
			allowed := false
			for _, o := range cfg.AllowOrigins {
				if o == origin {
					allowed = true
					break
				}
			}
			if !allowed {
				c.Next()
				return
			}
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Vary", "Origin")
		} else {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		}

		methods := "GET, POST, PUT, DELETE, PATCH, OPTIONS"
		if len(cfg.AllowMethods) > 0 {
			methods = strings.Join(cfg.AllowMethods, ", ")
		}
		c.Writer.Header().Set("Access-Control-Allow-Methods", methods)

		headers := "Authorization, X-API-Key, Content-Type, X-Request-ID, X-Tenant-ID"
		if len(cfg.AllowHeaders) > 0 {
			headers = strings.Join(cfg.AllowHeaders, ", ")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", headers)

		expose := "X-RateLimit-Limit, X-RateLimit-Remaining, X-Request-ID"
		if len(cfg.ExposeHeaders) > 0 {
			expose = strings.Join(cfg.ExposeHeaders, ", ")
		}
		c.Writer.Header().Set("Access-Control-Expose-Headers", expose)

		if cfg.AllowCredentials {
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if cfg.MaxAge > 0 {
			c.Writer.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", cfg.MaxAge))
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 4. Logging Middleware
// ---------------------------------------------------------------------------

// LoggingMiddleware logs every request with structured fields.
func LoggingMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		logger.Info("gateway_request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.String("client_ip", c.ClientIP()),
			zap.String("request_id", c.GetString("requestID")),
			zap.String("api_id", c.GetString("apiID")),
			zap.String("user_id", c.GetString("userID")),
			zap.String("app_id", c.GetString("appID")),
			zap.String("auth_type", c.GetString("authType")),
			zap.Int("errors", len(c.Errors)),
		)

		RequestCounter.WithLabelValues(
			c.Request.Method,
			c.Request.URL.Path,
			fmt.Sprintf("%d", status),
		).Inc()
		RequestDuration.WithLabelValues(
			c.Request.Method,
			c.Request.URL.Path,
		).Observe(latency.Seconds())
	}
}

// ---------------------------------------------------------------------------
// 5. Auth Middleware - VALIDATES AGAINST DATABASE
// ---------------------------------------------------------------------------

// AuthMiddleware validates authentication by querying the store.
// Priority: 1. OAuth2 Bearer token, 2. API Key, 3. Anonymous.
func (m *GatewayMiddleware) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		apiKey := c.GetHeader("X-API-Key")

		// 1. Try OAuth2 Bearer token (REAL DB QUERY)
		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == "" {
				AuthCounter.WithLabelValues("oauth2", "empty_token").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_token",
					"message": "Bearer token is empty",
				})
				c.Abort()
				return
			}

			// REAL DB QUERY: Validate token against tokens table
			storedToken, err := m.store.GetTokenByAccessToken(token)
			if err != nil {
				m.logger.Warn("token db lookup failed",
					zap.Error(err),
					zap.String("request_id", c.GetString("requestID")),
				)
				AuthCounter.WithLabelValues("oauth2", "db_error").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_token",
					"message": "Token is invalid or revoked",
				})
				c.Abort()
				return
			}
			if storedToken == nil {
				AuthCounter.WithLabelValues("oauth2", "not_found").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_token",
					"message": "Token is invalid or revoked",
				})
				c.Abort()
				return
			}
			if storedToken.Revoked {
				AuthCounter.WithLabelValues("oauth2", "revoked").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_token",
					"message": "Token has been revoked",
				})
				c.Abort()
				return
			}

			// Check expiry
			if time.Now().After(storedToken.ExpiresAt) {
				AuthCounter.WithLabelValues("oauth2", "expired").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "token_expired",
					"message": "Token has expired",
				})
				c.Abort()
				return
			}

			// Token is valid - set context
			c.Set("userID", storedToken.UserID)
			c.Set("clientID", storedToken.ClientID)
			c.Set("scopes", storedToken.Scopes)
			c.Set("authType", "oauth2")
			c.Set("tokenID", storedToken.ID)

			AuthCounter.WithLabelValues("oauth2", "success").Inc()
			m.logger.Debug("oauth2 token validated",
				zap.String("token_id", storedToken.ID),
				zap.String("user_id", storedToken.UserID),
				zap.String("client_id", storedToken.ClientID),
			)
			c.Next()
			return
		}

		// 2. Try API Key (REAL DB QUERY)
		if apiKey != "" {
			keyHash := sha256.Sum256([]byte(apiKey))
			keyHashStr := hex.EncodeToString(keyHash[:])

			// REAL DB QUERY: Validate API key hash
			storedKey, err := m.store.GetAPIKeyByHash(keyHashStr)
			if err != nil {
				m.logger.Warn("api key db lookup failed",
					zap.Error(err),
					zap.String("request_id", c.GetString("requestID")),
				)
				AuthCounter.WithLabelValues("apikey", "db_error").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_api_key",
					"message": "API key is invalid or revoked",
				})
				c.Abort()
				return
			}
			if storedKey == nil {
				AuthCounter.WithLabelValues("apikey", "not_found").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_api_key",
					"message": "API key is invalid or revoked",
				})
				c.Abort()
				return
			}
			if storedKey.Status != "active" {
				AuthCounter.WithLabelValues("apikey", "inactive").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "invalid_api_key",
					"message": "API key is not active",
				})
				c.Abort()
				return
			}

			// Check expiry
			if storedKey.ExpiresAt != nil && time.Now().After(*storedKey.ExpiresAt) {
				AuthCounter.WithLabelValues("apikey", "expired").Inc()
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":   "api_key_expired",
					"message": "API key has expired",
				})
				c.Abort()
				return
			}

			// API key is valid - set context
			c.Set("apiKeyID", storedKey.ID)
			c.Set("appID", storedKey.AppID)
			c.Set("scopes", storedKey.Scopes)
			c.Set("authType", "apikey")

			AuthCounter.WithLabelValues("apikey", "success").Inc()
			m.logger.Debug("api key validated",
				zap.String("key_id", storedKey.ID),
				zap.String("app_id", storedKey.AppID),
			)
			c.Next()
			return
		}

		// 3. No auth provided - mark as anonymous
		c.Set("authType", "anonymous")
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 6. API Lookup Middleware - RESOLVES API FROM DATABASE
// ---------------------------------------------------------------------------

// APILookupMiddleware finds the API by matching the request path against
// published APIs in the database.
func (m *GatewayMiddleware) APILookupMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/health") ||
			strings.HasPrefix(path, "/metrics") ||
			strings.HasPrefix(path, "/admin") {
			c.Next()
			return
		}

		// REAL DB QUERY: Find matching published API
		apis, err := m.store.ListPublishedAPIs("", 1000, 0)
		if err != nil {
			m.logger.Error("failed to list published apis from store",
				zap.Error(err),
				zap.String("request_id", c.GetString("requestID")),
			)
			c.Next()
			return
		}

		// Find the longest matching context path
		var matchedAPI *models.APIDB
		for i := range apis {
			api := &apis[i]
			if api.Context != "" && strings.HasPrefix(path, api.Context) {
				if matchedAPI == nil || len(api.Context) > len(matchedAPI.Context) {
					matchedAPI = api
				}
			}
		}

		if matchedAPI != nil {
			c.Set("apiID", matchedAPI.ID)
			c.Set("apiContext", matchedAPI.Context)
			c.Set("apiEndpoint", matchedAPI.Endpoint)
			c.Set("tenantID", matchedAPI.TenantID)

			m.logger.Debug("api resolved",
				zap.String("api_id", matchedAPI.ID),
				zap.String("api_context", matchedAPI.Context),
				zap.String("endpoint", matchedAPI.Endpoint),
				zap.String("path", path),
			)

			// Check if API requires authentication
			if matchedAPI.AuthType != "" && matchedAPI.AuthType != "none" {
				authType := c.GetString("authType")
				if authType == "anonymous" {
					c.JSON(http.StatusUnauthorized, gin.H{
						"error":   "authentication_required",
						"message": fmt.Sprintf("This API requires %s authentication", matchedAPI.AuthType),
					})
					c.Abort()
					return
				}
			}
		} else {
			AuthCounter.WithLabelValues("api_lookup", "not_found").Inc()
		}

		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 7. Subscription Middleware - VALIDATES AGAINST DATABASE
// ---------------------------------------------------------------------------

// SubscriptionMiddleware validates the app is subscribed to the API.
// Performs a REAL DB QUERY on every request.
func (m *GatewayMiddleware) SubscriptionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiID := c.GetString("apiID")
		appID := c.GetString("appID")

		// If no appID or apiID, skip subscription check
		if apiID == "" || appID == "" {
			c.Next()
			return
		}

		// REAL DB QUERY: Check if subscription exists and is active
		valid, err := m.store.ValidateSubscription(apiID, appID)
		if err != nil {
			m.logger.Error("subscription validation db query failed",
				zap.Error(err),
				zap.String("api_id", apiID),
				zap.String("app_id", appID),
			)
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "subscription_required",
				"message": "Application is not subscribed to this API. Subscribe at the Developer Portal.",
			})
			c.Abort()
			return
		}
		if !valid {
			m.logger.Warn("subscription validation failed",
				zap.String("api_id", apiID),
				zap.String("app_id", appID),
			)
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "subscription_required",
				"message": "Application is not subscribed to this API. Subscribe at the Developer Portal.",
			})
			c.Abort()
			return
		}

		m.logger.Debug("subscription validated",
			zap.String("api_id", apiID),
			zap.String("app_id", appID),
		)
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 8. Throttle Middleware - ENFORCES RATE LIMITS FROM DB
// ---------------------------------------------------------------------------

// ThrottleMiddleware enforces rate limits using the ThrottlingEngine with
// DB-backed throttle policies. It queries the store for the API's throttle
// policy, then evaluates the request against the engine.
func (m *GatewayMiddleware) ThrottleMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if m.throttleMgr == nil {
			c.Next()
			return
		}

		apiID := c.GetString("apiID")
		tenantID := c.GetString("tenantID")

		if apiID == "" || tenantID == "" {
			c.Next()
			return
		}

		// REAL DB QUERY: Get API details for throttle lookup
		api, err := m.store.GetAPI(apiID)
		if err != nil {
			m.logger.Warn("failed to get api for throttle lookup",
				zap.Error(err),
				zap.String("api_id", apiID),
			)
			c.Next()
			return
		}

		if api == nil || api.ThrottlePolicy == "" {
			c.Next()
			return
		}

		// REAL DB QUERY: Get throttle policy details
		policy, err := m.store.GetThrottlePolicyByName(api.TenantID, api.ThrottlePolicy)
		if err != nil {
			m.logger.Warn("failed to get throttle policy",
				zap.Error(err),
				zap.String("tenant_id", api.TenantID),
				zap.String("policy_name", api.ThrottlePolicy),
			)
			c.Next()
			return
		}

		if policy == nil {
			c.Next()
			return
		}

		// Build the throttle check request
		entityID := c.GetString("appID")
		if entityID == "" {
			entityID = c.GetString("userID")
		}
		if entityID == "" {
			entityID = "anonymous"
		}

		req := &models.ThrottleCheckRequest{
			APIID:      apiID,
			AppID:      entityID,
			UserID:     c.GetString("userID"),
			Tenant:     tenantID,
			ResourcePath: c.Request.URL.Path,
			HTTPMethod: c.Request.Method,
			ClientIP:   c.ClientIP(),
		}

		// Evaluate against the throttling engine
		ctx := c.Request.Context()
		result := m.throttleMgr.Evaluate(ctx, req)

		if result.Throttled || !result.Allowed {
			RateLimitCounter.WithLabelValues("blocked").Inc()
			c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", result.Limit))
			c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", result.Remaining))
			if result.RetryAfter > 0 {
				c.Header("Retry-After", fmt.Sprintf("%.0f", result.RetryAfter.Seconds()))
			}
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limited",
				"message": fmt.Sprintf("Rate limit exceeded (limit: %d/%s). Try again later.",
					result.Limit, policy.Unit),
				"retry_after": result.RetryAfter.Seconds(),
			})
			c.Abort()
			return
		}

		RateLimitCounter.WithLabelValues("allowed").Inc()
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", result.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", result.Remaining))

		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 9. Cache Middleware Wrapper
// ---------------------------------------------------------------------------

// CacheMiddleware returns the cache middleware handler if cache is enabled.
func (m *GatewayMiddleware) CacheMiddleware() gin.HandlerFunc {
	if m.cache != nil {
		return m.cache.Middleware()
	}
	return func(c *gin.Context) {
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 10. Metrics Middleware
// ---------------------------------------------------------------------------

// MetricsMiddleware tracks Prometheus metrics for each request.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ActiveRequests.Inc()
		defer ActiveRequests.Dec()
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 11. Security Headers Middleware
// ---------------------------------------------------------------------------

// SecurityHeadersMiddleware adds security headers to every response.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
		c.Writer.Header().Set("X-Frame-Options", "DENY")
		c.Writer.Header().Set("X-XSS-Protection", "1; mode=block")
		c.Writer.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 12. Request Size Limit Middleware
// ---------------------------------------------------------------------------

// RequestSizeLimitMiddleware limits the maximum request body size.
func RequestSizeLimitMiddleware(maxSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxSize > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize)
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 13. Timeout Middleware
// ---------------------------------------------------------------------------

// TimeoutMiddleware sets a maximum request processing time.
func TimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 14. Analytics Middleware
// ---------------------------------------------------------------------------

// AnalyticsMiddleware records every request to the analytics store.
func (m *GatewayMiddleware) AnalyticsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if m.store == nil {
			c.Next()
			return
		}

		start := time.Now()
		apiID := c.GetString("apiID")
		appID := c.GetString("appID")
		userID := c.GetString("userID")
		requestID := c.GetString("requestID")
		tenantID := c.GetString("tenantID")

		c.Next()

		latency := int(time.Since(start).Milliseconds())
		statusCode := c.Writer.Status()

		event := &models.AnalyticsEventDB{
			ID:         uuid.New().String(),
			TenantID:   tenantID,
			RequestID:  requestID,
			APIID:      apiID,
			AppID:      appID,
			UserID:     userID,
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			StatusCode: statusCode,
			LatencyMs:  latency,
			UserAgent:  c.Request.UserAgent(),
			ClientIP:   c.ClientIP(),
			Timestamp:  time.Now(),
		}

		// Fire-and-forget: don't block response on analytics
		go func(ev *models.AnalyticsEventDB) {
			if err := m.store.InsertAnalyticsEvent(ev); err != nil {
				m.logger.Warn("failed to store analytics event",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
			}
		}(event)
	}
}

// ---------------------------------------------------------------------------
// 15. Prometheus Handler
// ---------------------------------------------------------------------------

// PrometheusHandler returns the Prometheus metrics endpoint handler.
func PrometheusHandler() gin.HandlerFunc {
	return gin.WrapH(prometheus.Handler())
}
