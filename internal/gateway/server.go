// Package gateway provides the HTTP server for the VedaDB API Manager Gateway.
// This file implements the Gin-based HTTP server with all middleware chains,
// route registration, health checks, and graceful shutdown support.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/tiennesdm/vedadb-apim/pkg/config"
)

// GatewayServer is the main HTTP server for the API Gateway.
type GatewayServer struct {
	// Engine is the Gin HTTP engine.
	Engine *gin.Engine
	// Config is the gateway configuration.
	Config *config.Config
	// Logger is the structured logger.
	Logger *zap.Logger
	// Router is the dynamic API router.
	Router *Router
	// Proxy is the reverse proxy.
	Proxy *Proxy
	// RateLimiter is the rate limiter middleware.
	RateLimiter *RateLimiterMiddleware
	// Cache is the cache middleware.
	Cache *CacheMiddleware
	// AuthMiddleware is the authentication middleware.
	AuthMiddleware *AuthMiddleware
	// AnalyticsMiddleware is the analytics middleware.
	AnalyticsMiddleware *AnalyticsMiddleware
	// TransformMiddleware is the transform middleware.
	TransformMiddleware *TransformMiddleware
	// SubscriptionMiddleware is the subscription middleware.
	SubscriptionMiddleware *SubscriptionMiddleware

	// HTTP server
	httpServer *http.Server
	// isRunning tracks server state
	isRunning bool
}

// GatewayOptions holds configuration options for the gateway.
type GatewayOptions struct {
	Config      *config.Config
	Logger      *zap.Logger
	Router      *Router
	Proxy       *Proxy
	RateLimiter *RateLimiterMiddleware
	Cache       *CacheMiddleware
	Auth        *AuthMiddleware
	Analytics   *AnalyticsMiddleware
	Transform   *TransformMiddleware
	Subscription *SubscriptionMiddleware
}

// NewGatewayServer creates a new gateway server with all middleware.
func NewGatewayServer(opts GatewayOptions) *GatewayServer {
	cfg := opts.Config

	// Set Gin mode
	if cfg.Log.Level == "debug" || cfg.Log.Level == "DEBUG" {
		gin.SetMode(gin.DebugMode)
	} else if cfg.Log.Level == "test" {
		gin.SetMode(gin.TestMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()

	// Register Prometheus metrics
	RegisterMetrics()

	gw := &GatewayServer{
		Engine:                 engine,
		Config:                 cfg,
		Logger:                 opts.Logger,
		Router:                 opts.Router,
		Proxy:                  opts.Proxy,
		RateLimiter:            opts.RateLimiter,
		Cache:                  opts.Cache,
		AuthMiddleware:         opts.Auth,
		AnalyticsMiddleware:    opts.Analytics,
		TransformMiddleware:    opts.Transform,
		SubscriptionMiddleware: opts.Subscription,
	}

	// Setup middleware chain
	gw.setupMiddleware()

	// Setup routes
	gw.setupRoutes()

	return gw
}

// setupMiddleware configures the middleware chain in order.
func (s *GatewayServer) setupMiddleware() {
	cfg := s.Config

	// 1. Recovery middleware (must be first)
	s.Engine.Use(RecoveryMiddleware(s.Logger))

	// 2. Request ID
	s.Engine.Use(RequestIDMiddleware())

	// 3. CORS
	if cfg.CORS.Enabled {
		corsConfig := CORSConfig{
			Enabled:          cfg.CORS.Enabled,
			AllowOrigins:     cfg.CORS.AllowOrigins,
			AllowMethods:     cfg.CORS.AllowMethods,
			AllowHeaders:     cfg.CORS.AllowHeaders,
			ExposeHeaders:    cfg.CORS.ExposeHeaders,
			AllowCredentials: cfg.CORS.AllowCredentials,
			MaxAge:           cfg.CORS.MaxAge,
		}
		s.Engine.Use(CORSMiddleware(corsConfig))
	}

	// 4. Logging
	s.Engine.Use(LoggingMiddleware(s.Logger))

	// 5. Metrics
	s.Engine.Use(MetricsMiddleware())

	// 6. Security headers
	s.Engine.Use(SecurityHeadersMiddleware())

	// 7. Request size limit
	if cfg.Gateway.MaxRequestBodySize > 0 {
		s.Engine.Use(RequestSizeLimitMiddleware(cfg.Gateway.MaxRequestBodySize))
	}

	// 8. Timeout
	if cfg.Gateway.RequestTimeout > 0 {
		s.Engine.Use(TimeoutMiddleware(cfg.Gateway.RequestTimeout))
	}

	// 9. Spike arrest / throttling
	if cfg.Throttle.SpikeArrestEnabled {
		s.Engine.Use(ThrottleMiddleware(float64(cfg.Throttle.SpikeArrestRate)))
	}

	// 10. Authentication (if configured)
	if s.AuthMiddleware != nil && cfg.Auth.Enabled {
		s.Engine.Use(s.AuthMiddleware.Middleware())
	}

	// 11. Subscription validation
	if s.SubscriptionMiddleware != nil {
		s.Engine.Use(s.SubscriptionMiddleware.Middleware())
	}

	// 12. Rate limiting
	if s.RateLimiter != nil && cfg.RateLimit.Enabled {
		s.Engine.Use(s.RateLimiter.Middleware())
	}

	// 13. Cache
	if s.Cache != nil && cfg.Cache.Enabled {
		s.Engine.Use(s.Cache.Middleware())
	}

	// 14. Transform
	if s.TransformMiddleware != nil {
		s.Engine.Use(s.TransformMiddleware.Middleware())
	}

	// 15. Analytics
	if s.AnalyticsMiddleware != nil && cfg.Analytics.Enabled {
		s.Engine.Use(s.AnalyticsMiddleware.Middleware())
	}
}

// setupRoutes registers all routes with the Gin engine.
func (s *GatewayServer) setupRoutes() {
	// Health check endpoint
	s.Engine.GET("/health", s.handleHealth)

	// Prometheus metrics endpoint
	s.Engine.GET("/metrics", PrometheusHandler())

	// Admin routes group
	admin := s.Engine.Group("/admin")
	{
		admin.GET("/health", s.handleHealth)
		admin.GET("/stats", s.handleStats)
		admin.GET("/config", s.handleConfig)
		admin.POST("/reload", s.handleReload)
		admin.GET("/gateway/routes", s.handleRoutes)
		admin.GET("/gateway/cache/stats", s.handleCacheStats)
		admin.GET("/gateway/throttle/stats", s.handleThrottleStats)
	}

	// Gateway proxy routes (catch-all)
	s.Engine.NoRoute(s.handleProxy)
}

// ---------------------------------------------------------------------------
// Route Handlers
// ---------------------------------------------------------------------------

// handleHealth handles health check requests.
func (s *GatewayServer) handleHealth(c *gin.Context) {
	health := struct {
		Status    string            `json:"status"`
		Version   string            `json:"version"`
		Timestamp time.Time         `json:"timestamp"`
		Uptime    string            `json:"uptime"`
		Checks    map[string]string `json:"checks,omitempty"`
	}{
		Status:    "healthy",
		Version:   "1.0.0",
		Timestamp: time.Now(),
		Uptime:    time.Since(time.Now()).String(),
		Checks: map[string]string{
			"gateway": "ok",
			"server":  "running",
		},
	}
	c.JSON(http.StatusOK, health)
}

// handleStats returns gateway statistics.
func (s *GatewayServer) handleStats(c *gin.Context) {
	stats := struct {
		Server    serverStats    `json:"server"`
		Router    routerStats    `json:"router"`
		Cache     cacheStats     `json:"cache"`
		Throttling throttleStats `json:"throttling"`
		Timestamp time.Time      `json:"timestamp"`
	}{
		Server: serverStats{
			Status:    "running",
			Uptime:    time.Since(time.Now()).String(),
			GoVersion: "1.21",
			GinMode:   gin.Mode(),
		},
		Router: routerStats{
			TotalRoutes:  s.Router.GetRouteCount(),
		},
		Cache: cacheStats{
			Enabled: s.Config.Cache.Enabled,
		},
		Throttling: throttleStats{
			Enabled:         s.Config.Throttle.Enabled,
			RateLimiting:    s.Config.RateLimit.Enabled,
			SpikeArrest:     s.Config.Throttle.SpikeArrestEnabled,
			SpikeArrestRate: s.Config.Throttle.SpikeArrestRate,
		},
		Timestamp: time.Now(),
	}

	// Add cache stats if available
	if s.Cache != nil {
		cacheStats := s.Cache.GetCacheStore().Stats()
		stats.Cache.TotalEntries = cacheStats.TotalEntries
		stats.Cache.TotalHits = cacheStats.TotalHits
		stats.Cache.TotalMisses = cacheStats.TotalMisses
		stats.Cache.HitRate = cacheStats.HitRate
	}

	c.JSON(http.StatusOK, stats)
}

// handleConfig returns current configuration (filtered for security).
func (s *GatewayServer) handleConfig(c *gin.Context) {
	cfg := struct {
		Server    config.ServerConfig   `json:"server"`
		Gateway   config.GatewayConfig  `json:"gateway"`
		Database  config.DatabaseConfig `json:"database"`
		Auth      authConfigSafe        `json:"auth"`
		Throttle  config.ThrottleConfig `json:"throttle"`
		Cache     config.CacheConfig    `json:"cache"`
		Analytics config.AnalyticsConfig `json:"analytics"`
		CORS      config.CORSConfig     `json:"cors"`
	}{
		Server:    s.Config.Server,
		Gateway:   s.Config.Gateway,
		Database:  s.Config.Database,
		Throttle:  s.Config.Throttle,
		Cache:     s.Config.Cache,
		Analytics: s.Config.Analytics,
		CORS:      s.Config.CORS,
	}
	// Mask sensitive auth fields
	cfg.Auth = authConfigSafe{
		Enabled:            s.Config.Auth.Enabled,
		AccessTokenExpiry:  s.Config.Auth.AccessTokenExpiry.String(),
		RefreshTokenExpiry: s.Config.Auth.RefreshTokenExpiry.String(),
		TokenIssuer:        s.Config.Auth.TokenIssuer,
		TokenAudience:      s.Config.Auth.TokenAudience,
		HeaderName:         s.Config.Auth.HeaderName,
		QueryParamName:     s.Config.Auth.QueryParamName,
		SkipAuthForOptions: s.Config.Auth.SkipAuthForOptions,
		RevocationCheck:    s.Config.Auth.RevocationCheck,
		CacheTokens:        s.Config.Auth.CacheTokens,
	}

	c.JSON(http.StatusOK, cfg)
}

// handleReload triggers configuration reload.
func (s *GatewayServer) handleReload(c *gin.Context) {
	// Reload routes
	ctx := context.Background()
	if err := s.Router.LoadRoutes(ctx); err != nil {
		s.Logger.Error("failed to reload routes", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to reload configuration",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "configuration reloaded successfully",
		"routes":  s.Router.GetRouteCount(),
	})
}

// handleRoutes returns all loaded routes.
func (s *GatewayServer) handleRoutes(c *gin.Context) {
	routes := s.Router.GetRoutes()
	c.JSON(http.StatusOK, gin.H{
		"count":  len(routes),
		"routes": routes,
	})
}

// handleCacheStats returns cache statistics.
func (s *GatewayServer) handleCacheStats(c *gin.Context) {
	if s.Cache == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "cache is not enabled",
		})
		return
	}

	stats := s.Cache.GetCacheStore().Stats()
	c.JSON(http.StatusOK, stats)
}

// handleThrottleStats returns throttling statistics.
func (s *GatewayServer) handleThrottleStats(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"enabled":        s.Config.Throttle.Enabled,
		"rate_limiting":  s.Config.RateLimit.Enabled,
		"spike_arrest":   s.Config.Throttle.SpikeArrestEnabled,
		"spike_arrest_rate": s.Config.Throttle.SpikeArrestRate,
		"default_policy": s.Config.Throttle.DefaultPolicy,
		"ip_based":       s.Config.Throttle.IPBasedThrottling,
	})
}

// handleProxy proxies requests to backend APIs.
func (s *GatewayServer) handleProxy(c *gin.Context) {
	if s.Proxy != nil {
		s.Proxy.Handler()(c)
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "proxy is not configured",
		})
	}
}

// ---------------------------------------------------------------------------
// Server Management
// ---------------------------------------------------------------------------

// Run starts the HTTP server and blocks until shutdown.
func (s *GatewayServer) Run() error {
	addr := fmt.Sprintf(":%d", s.Config.Server.Port)

	s.httpServer = &http.Server{
		Addr:           addr,
		Handler:        s.Engine,
		ReadTimeout:    s.Config.Server.ReadTimeout,
		WriteTimeout:   s.Config.Server.WriteTimeout,
		IdleTimeout:    s.Config.Server.IdleTimeout,
		MaxHeaderBytes: s.Config.Server.MaxHeaderBytes,
	}

	s.isRunning = true

	s.Logger.Info("starting gateway server",
		zap.String("addr", addr),
		zap.String("gin_mode", gin.Mode()),
		zap.Int("port", s.Config.Server.Port),
		zap.Int("routes_loaded", s.Router.GetRouteCount()),
	)

	// Start in a goroutine
	errChan := make(chan error, 1)
	go func() {
		if s.Config.Server.TLSEnabled {
			errChan <- s.httpServer.ListenAndServeTLS(
				s.Config.Server.TLSCertFile,
				s.Config.Server.TLSKeyFile,
			)
		} else {
			errChan <- s.httpServer.ListenAndServe()
		}
	}()

	// Wait for shutdown signal or error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	select {
	case err := <-errChan:
		if err != nil && err != http.ErrServerClosed {
			s.Logger.Error("server error", zap.Error(err))
			return err
		}
	case sig := <-quit:
		s.Logger.Info("shutdown signal received", zap.String("signal", sig.String()))
		return s.Shutdown()
	}

	return nil
}

// Shutdown performs a graceful shutdown.
func (s *GatewayServer) Shutdown() error {
	if !s.isRunning {
		return nil
	}

	s.Logger.Info("shutting down gateway server", zap.Duration("timeout", s.Config.Server.ShutdownTimeout))

	ctx, cancel := context.WithTimeout(context.Background(), s.Config.Server.ShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.Logger.Error("server shutdown error", zap.Error(err))
		return err
	}

	s.isRunning = false
	s.Logger.Info("gateway server stopped")
	return nil
}

// IsRunning returns true if the server is running.
func (s *GatewayServer) IsRunning() bool {
	return s.isRunning
}

// ---------------------------------------------------------------------------
// Stats Structs
// ---------------------------------------------------------------------------

type serverStats struct {
	Status    string `json:"status"`
	Uptime    string `json:"uptime"`
	GoVersion string `json:"go_version"`
	GinMode   string `json:"gin_mode"`
}

type routerStats struct {
	TotalRoutes int `json:"total_routes"`
}

type cacheStats struct {
	Enabled      bool    `json:"enabled"`
	TotalEntries int64   `json:"total_entries"`
	TotalHits    int64   `json:"total_hits"`
	TotalMisses  int64   `json:"total_misses"`
	HitRate      float64 `json:"hit_rate"`
}

type throttleStats struct {
	Enabled         bool `json:"enabled"`
	RateLimiting    bool `json:"rate_limiting"`
	SpikeArrest     bool `json:"spike_arrest"`
	SpikeArrestRate int  `json:"spike_arrest_rate"`
}

type authConfigSafe struct {
	Enabled            bool   `json:"enabled"`
	AccessTokenExpiry  string `json:"access_token_expiry"`
	RefreshTokenExpiry string `json:"refresh_token_expiry"`
	TokenIssuer        string `json:"token_issuer"`
	TokenAudience      string `json:"token_audience"`
	HeaderName         string `json:"header_name"`
	QueryParamName     string `json:"query_param_name"`
	SkipAuthForOptions bool   `json:"skip_auth_for_options"`
	RevocationCheck    bool   `json:"revocation_check"`
	CacheTokens        bool   `json:"cache_tokens"`
}

// Collectors returns all Prometheus collectors for registration.
func Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		RequestCounter,
		RequestDuration,
		ActiveRequests,
		CacheHitCounter,
		RateLimitCounter,
		AuthCounter,
	}
}
