// Package gateway provides the middleware chain for the VedaDB API Manager.
// This file implements authentication, rate limiting, throttling, caching,
// logging, metrics, and transform middleware for the API Gateway.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	apimerrors "github.com/tiennesdm/vedadb-apim/pkg/errors"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// ---------------------------------------------------------------------------
// RequestID Middleware
// ---------------------------------------------------------------------------

// RequestIDMiddleware generates and attaches a unique request ID to each request.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// CORS Middleware
// ---------------------------------------------------------------------------

// CORSMiddleware returns a Gin middleware for handling CORS.
func CORSMiddleware(config CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.Enabled {
			c.Next()
			return
		}

		origin := c.GetHeader("Origin")
		allowedOrigin := ""

		for _, o := range config.AllowOrigins {
			if o == "*" || o == origin {
				allowedOrigin = o
				break
			}
		}

		if allowedOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
		}
		if config.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		if len(config.ExposeHeaders) > 0 {
			c.Header("Access-Control-Expose-Headers", strings.Join(config.ExposeHeaders, ", "))
		}

		// Handle preflight
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Methods", strings.Join(config.AllowMethods, ", "))
			c.Header("Access-Control-Allow-Headers", strings.Join(config.AllowHeaders, ", "))
			if config.MaxAge > 0 {
				c.Header("Access-Control-Max-Age", fmt.Sprintf("%d", config.MaxAge))
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// CORSConfig defines CORS middleware configuration.
type CORSConfig struct {
	Enabled          bool
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAge           int
}

// ---------------------------------------------------------------------------
// Authentication Middleware
// ---------------------------------------------------------------------------

// AuthMiddlewareConfig holds authentication middleware configuration.
type AuthMiddlewareConfig struct {
	Enabled            bool
	JWTSecret          string
	AccessTokenExpiry  time.Duration
	TokenIssuer        string
	TokenAudience      string
	HeaderName         string
	QueryParamName     string
	SkipPaths          []string
	SkipAuthForOptions bool
	RevocationCheck    bool
	TokenCacheTTL      time.Duration
}

// AuthMiddleware is the Gin middleware for authentication.
type AuthMiddleware struct {
	config AuthMiddlewareConfig
	logger *zap.Logger
}

// NewAuthMiddleware creates a new authentication middleware.
func NewAuthMiddleware(config AuthMiddlewareConfig, logger *zap.Logger) *AuthMiddleware {
	if config.HeaderName == "" {
		config.HeaderName = "Authorization"
	}
	if config.QueryParamName == "" {
		config.QueryParamName = "access_token"
	}
	return &AuthMiddleware{
		config: config,
		logger: logger,
	}
}

// Middleware returns the Gin handler function for authentication.
func (m *AuthMiddleware) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Enabled {
			c.Next()
			return
		}

		// Skip auth for OPTIONS requests
		if m.config.SkipAuthForOptions && c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// Check skip paths
		for _, path := range m.config.SkipPaths {
			if c.Request.URL.Path == path {
				c.Next()
				return
			}
		}

		// Check if auth is required for this route
		authRequired, exists := c.Get("auth_required")
		if exists && !authRequired.(bool) {
			c.Next()
			return
		}

		// Extract token
		token, err := m.extractToken(c)
		if err != nil {
			m.logger.Warn("failed to extract token", zap.String("path", c.Request.URL.Path), zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, apimerrors.NewErrorResponse(apimerrors.ErrUnauthorized))
			return
		}

		// Validate token
		claims, err := m.validateToken(token)
		if err != nil {
			m.logger.Warn("token validation failed", zap.String("path", c.Request.URL.Path), zap.Error(err))
			if strings.Contains(err.Error(), "expired") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, apimerrors.NewErrorResponse(apimerrors.ErrTokenExpired))
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, apimerrors.NewErrorResponse(apimerrors.ErrInvalidToken))
			return
		}

		// Store claims in context
		c.Set("user_id", claims.Subject)
		c.Set("username", claims.Username)
		c.Set("email", claims.Email)
		c.Set("user_role", claims.Role)
		c.Set("tenant", claims.Tenant)
		c.Set("scopes", claims.Scopes)
		c.Set("client_id", claims.ClientID)
		c.Set("app_id", claims.ApplicationID)
		c.Set("authenticated", true)

		// Check required scope
		if requiredScope := c.GetString("required_scope"); requiredScope != "" {
			if !m.hasScope(claims.Scopes, requiredScope) {
				c.AbortWithStatusJSON(http.StatusForbidden, apimerrors.NewErrorResponse(apimerrors.ErrScopeInsufficient))
				return
			}
		}

		c.Next()
	}
}

// extractToken extracts the bearer token from the request.
func (m *AuthMiddleware) extractToken(c *gin.Context) (string, error) {
	// Try header first
	header := c.GetHeader(m.config.HeaderName)
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return parts[1], nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
	}

	// Try query parameter
	token := c.Query(m.config.QueryParamName)
	if token != "" {
		return token, nil
	}

	// Try API key header
	token = c.GetHeader("X-API-Key")
	if token != "" {
		return token, nil
	}

	return "", fmt.Errorf("no authentication token found")
}

// JWTClaims defines the custom claims structure for VedaDB APIM.
type JWTClaims struct {
	Subject       string   `json:"sub"`
	Username      string   `json:"username,omitempty"`
	Email         string   `json:"email,omitempty"`
	Role          string   `json:"role,omitempty"`
	Tenant        string   `json:"tenant,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	ClientID      string   `json:"client_id,omitempty"`
	ApplicationID string   `json:"application_id,omitempty"`
	Tier          string   `json:"tier,omitempty"`
	jwt.RegisteredClaims
}

// validateToken validates a JWT token and returns the claims.
func (m *AuthMiddleware) validateToken(tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(m.config.JWTSecret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	// Validate issuer
	if m.config.TokenIssuer != "" && claims.Issuer != m.config.TokenIssuer {
		return nil, fmt.Errorf("invalid issuer: %s", claims.Issuer)
	}

	// Validate audience
	if m.config.TokenAudience != "" {
		found := false
		for _, aud := range claims.Audience {
			if aud == m.config.TokenAudience {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("invalid audience")
		}
	}

	return claims, nil
}

// hasScope checks if the scopes contain the required scope.
func (m *AuthMiddleware) hasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required || s == "*" {
			return true
		}
	}
	return false
}

// GenerateJWT generates a new JWT token for the given user.
func GenerateJWT(secret, issuer, audience string, expiry time.Duration, user *models.User, scopes []string) (string, error) {
	now := time.Now()
	claims := JWTClaims{
		Subject:  user.ID.String(),
		Username: user.Username,
		Email:    user.Email,
		Role:     string(user.Role),
		Tenant:   user.Tenant,
		Scopes:   scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			ID:        uuid.New().String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ---------------------------------------------------------------------------
// Subscription Middleware
// ---------------------------------------------------------------------------

// SubscriptionMiddleware validates that a valid subscription exists.
type SubscriptionMiddleware struct {
	logger *zap.Logger
}

// NewSubscriptionMiddleware creates a new subscription middleware.
func NewSubscriptionMiddleware(logger *zap.Logger) *SubscriptionMiddleware {
	return &SubscriptionMiddleware{logger: logger}
}

// Middleware returns the Gin handler function for subscription validation.
func (m *SubscriptionMiddleware) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if subscription is required
		apiContext := c.GetString("api_context")
		if apiContext == "" {
			c.Next()
			return
		}

		// Skip if no auth required
		authRequired, _ := c.Get("auth_required")
		if authRequiredBool, ok := authRequired.(bool); !ok || !authRequiredBool {
			c.Next()
			return
		}

		// Check if user is authenticated
		authenticated, _ := c.Get("authenticated")
		if authBool, ok := authenticated.(bool); !ok || !authBool {
			c.AbortWithStatusJSON(http.StatusForbidden, apimerrors.NewErrorResponse(apimerrors.ErrSubscriptionRequired))
			return
		}

		// In a full implementation, check subscription status in VedaDB
		// For now, we assume valid if authenticated
		appTier := c.GetString("app_tier")
		if appTier == "" {
			appTier = string(models.TierBronze)
			c.Set("app_tier", appTier)
		}

		c.Next()
	}
}

// ---------------------------------------------------------------------------
// Logging Middleware
// ---------------------------------------------------------------------------

// LoggingMiddleware returns a Gin middleware for structured request logging.
func LoggingMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		fields := []zap.Field{
			zap.Time("timestamp", param.TimeStamp),
			zap.String("method", param.Method),
			zap.String("path", param.Path),
			zap.String("query", param.Request.URL.RawQuery),
			zap.Int("status", param.StatusCode),
			zap.Duration("latency", param.Latency),
			zap.String("client_ip", param.ClientIP),
			zap.String("user_agent", param.Request.UserAgent()),
			zap.String("error", param.ErrorMessage),
		}

		// Add request ID if available
		if reqID := param.Request.Header.Get("X-Request-ID"); reqID != "" {
			fields = append(fields, zap.String("request_id", reqID))
		}

		// Log based on status code
		switch {
		case param.StatusCode >= 500:
			logger.Error("gateway request", fields...)
		case param.StatusCode >= 400:
			logger.Warn("gateway request", fields...)
		default:
			logger.Info("gateway request", fields...)
		}

		return ""
	})
}

// GinToZapLogger wraps zap logger for Gin's logger middleware.
func GinToZapLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		requestID := c.GetString("request_id")
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		userAgent := c.Request.UserAgent()
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		fields := []zap.Field{
			zap.String("request_id", requestID),
			zap.Int("status", statusCode),
			zap.Duration("latency", latency),
			zap.String("client_ip", clientIP),
			zap.String("method", method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("user_agent", userAgent),
		}
		if errorMessage != "" {
			fields = append(fields, zap.String("error", errorMessage))
		}

		switch {
		case statusCode >= 500:
			logger.Error("request completed", fields...)
		case statusCode >= 400:
			logger.Warn("request completed", fields...)
		default:
			logger.Info("request completed", fields...)
		}
	}
}

// ---------------------------------------------------------------------------
// Metrics Middleware
// ---------------------------------------------------------------------------

var (
	// RequestCounter counts total HTTP requests.
	RequestCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "apim_requests_total",
			Help: "Total number of HTTP requests processed by the gateway",
		},
		[]string{"method", "path", "status", "api"},
	)

	// RequestDuration tracks request latencies.
	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "apim_request_duration_seconds",
			Help:    "HTTP request latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "api"},
	)

	// ActiveRequests tracks currently active requests.
	ActiveRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "apim_active_requests",
			Help: "Number of requests currently being processed",
		},
	)

	// CacheHitCounter counts cache hits and misses.
	CacheHitCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "apim_cache_total",
			Help: "Cache hit/miss counts",
		},
		[]string{"result", "api"},
	)

	// RateLimitCounter counts rate-limited requests.
	RateLimitCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "apim_rate_limited_total",
			Help: "Number of rate-limited requests",
		},
		[]string{"api", "reason"},
	)

	// AuthCounter counts authentication results.
	AuthCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "apim_auth_total",
			Help: "Authentication result counts",
		},
		[]string{"result"},
	)
)

// RegisterMetrics registers all Prometheus metrics.
func RegisterMetrics() {
	prometheus.MustRegister(RequestCounter)
	prometheus.MustRegister(RequestDuration)
	prometheus.MustRegister(ActiveRequests)
	prometheus.MustRegister(CacheHitCounter)
	prometheus.MustRegister(RateLimitCounter)
	prometheus.MustRegister(AuthCounter)
}

// MetricsMiddleware returns a Gin middleware for Prometheus metrics collection.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ActiveRequests.Inc()

		c.Next()

		ActiveRequests.Dec()
		duration := time.Since(start).Seconds()
		status := fmt.Sprintf("%d", c.Writer.Status())
		apiContext := c.GetString("api_context")
		if apiContext == "" {
			apiContext = "unknown"
		}
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		RequestCounter.WithLabelValues(c.Request.Method, path, status, apiContext).Inc()
		RequestDuration.WithLabelValues(c.Request.Method, path, apiContext).Observe(duration)
	}
}

// PrometheusHandler returns the HTTP handler for Prometheus metrics.
func PrometheusHandler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// ---------------------------------------------------------------------------
// Recovery Middleware
// ---------------------------------------------------------------------------

// RecoveryMiddleware returns a Gin middleware for panic recovery.
func RecoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		logger.Error("panic recovered",
			zap.String("request_id", c.GetString("request_id")),
			zap.String("path", c.Request.URL.Path),
			zap.Any("panic", recovered),
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error":   "internal server error",
			"code":    "APIM.INTERNAL_ERROR",
			"message": "an unexpected error occurred",
		})
	})
}

// ---------------------------------------------------------------------------
// Security Headers Middleware
// ---------------------------------------------------------------------------

// SecurityHeadersMiddleware adds security-related HTTP headers.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// Request Size Limit Middleware
// ---------------------------------------------------------------------------

// RequestSizeLimitMiddleware limits the size of incoming request bodies.
func RequestSizeLimitMiddleware(maxSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxSize > 0 && c.Request.ContentLength > maxSize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error":   "request body too large",
				"code":    "APIM.REQUEST_TOO_LARGE",
				"message": fmt.Sprintf("request body exceeds maximum size of %d bytes", maxSize),
			})
			return
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// Timeout Middleware
// ---------------------------------------------------------------------------

// TimeoutMiddleware adds a timeout to the request context.
func TimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)

		// Use a channel to detect if handler completes before timeout
		finished := make(chan struct{})
		go func() {
			c.Next()
			close(finished)
		}()

		select {
		case <-finished:
			// Request completed normally
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				c.AbortWithStatusJSON(http.StatusGatewayTimeout, apimerrors.NewErrorResponse(apimerrors.ErrGatewayTimeout))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Analytics Middleware
// ---------------------------------------------------------------------------

// AnalyticsMiddlewareConfig holds analytics middleware configuration.
type AnalyticsMiddlewareConfig struct {
	Enabled  bool
	Store    AnalyticsStore
	Logger   *zap.Logger
}

// AnalyticsStore defines the interface for analytics event storage.
type AnalyticsStore interface {
	StoreEvent(event *models.AnalyticsEvent) error
}

// AnalyticsMiddleware collects analytics events for each request.
type AnalyticsMiddleware struct {
	config AnalyticsMiddlewareConfig
}

// NewAnalyticsMiddleware creates a new analytics middleware.
func NewAnalyticsMiddleware(config AnalyticsMiddlewareConfig) *AnalyticsMiddleware {
	return &AnalyticsMiddleware{config: config}
}

// Middleware returns the Gin handler function for analytics collection.
func (m *AnalyticsMiddleware) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Enabled {
			c.Next()
			return
		}

		start := time.Now()
		requestID := c.GetString("request_id")
		apiID := c.GetString("api_id")
		apiName := c.GetString("api_name")
		apiContext := c.GetString("api_context")
		apiVersion := c.GetString("api_version")
		resourcePath := c.GetString("resource_path")
		if resourcePath == "" {
			resourcePath = c.Request.URL.Path
		}
		httpMethod := c.Request.Method
		appID := c.GetString("app_id")
		userID := c.GetString("user_id")
		username := c.GetString("username")
		tenants := c.GetString("tenant")
		sourceIP := c.ClientIP()
		userAgent := c.Request.UserAgent()
		protocol := c.Request.Proto

		// Check cache hit
		cacheStatus := c.GetHeader("X-Cache")
		cacheHit := cacheStatus == "HIT"

		c.Next()

		// Build analytics event
		gatewayLatency := time.Since(start)
		statusCode := c.Writer.Status()

		now := time.Now()
		event := &models.AnalyticsEvent{
			RequestID:      requestID,
			HTTPMethod:     httpMethod,
			ResourcePath:   resourcePath,
			StatusCode:     statusCode,
			GatewayLatency: gatewayLatency.Milliseconds(),
			TotalLatency:   gatewayLatency.Milliseconds(),
			SourceIP:       sourceIP,
			UserAgent:      userAgent,
			Protocol:       protocol,
			CacheHit:       cacheHit,
			Throttled:      c.GetHeader("X-Throttled") == "true",
			Timestamp:      now,
			DayOfWeek:      int(now.Weekday()),
			HourOfDay:      now.Hour(),
		}

		// Parse optional fields
		if apiID != "" {
			event.APIID = uuid.MustParse(apiID)
		}
		event.APIName = apiName
		event.APIContext = apiContext
		event.APIVersion = apiVersion
		if appID != "" {
			event.AppID = uuid.MustParse(appID)
		}
		if userID != "" {
			event.UserID = uuid.MustParse(userID)
		}
		event.Username = username
		event.Tenant = tenants

		// Store event asynchronously
		go func(evt *models.AnalyticsEvent) {
			if m.config.Store != nil {
				if err := m.config.Store.StoreEvent(evt); err != nil {
					m.config.Logger.Warn("failed to store analytics event",
						zap.String("request_id", evt.RequestID),
						zap.Error(err),
					)
				}
			}
		}(event)
	}
}


