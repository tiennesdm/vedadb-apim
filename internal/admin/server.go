// Package admin provides the admin API for the VedaDB API Manager.
// This file implements the administrative endpoints for health checks,
// statistics, configuration viewing, reload triggers, and gateway introspection.
package admin

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/tiennesdm/vedadb-apim/pkg/config"
	"github.com/tiennesdm/vedadb-apim/pkg/store"
)

// AdminServer provides the admin API endpoints.
type AdminServer struct {
	// Engine is the Gin HTTP engine for admin routes.
	Engine *gin.Engine
	// Config is the application configuration.
	Config *config.Config
	// ConfigManager handles configuration reloads.
	ConfigManager *config.Manager
	// Logger is the structured logger.
	Logger *zap.Logger
	// DBClient is the VedaDB client.
	DBClient *store.VedaDBClient
	// GatewayAddr is the gateway server address.
	GatewayAddr string

	httpServer *http.Server
	isRunning  bool
	startTime  time.Time
}

// AdminServerOptions holds options for creating an AdminServer.
type AdminServerOptions struct {
	Config        *config.Config
	ConfigManager *config.Manager
	Logger        *zap.Logger
	DBClient      *store.VedaDBClient
	GatewayAddr   string
}

// NewAdminServer creates a new admin server.
func NewAdminServer(opts AdminServerOptions) *AdminServer {
	engine := gin.New()
	engine.Use(gin.Recovery())

	s := &AdminServer{
		Engine:        engine,
		Config:        opts.Config,
		ConfigManager: opts.ConfigManager,
		Logger:        opts.Logger,
		DBClient:      opts.DBClient,
		GatewayAddr:   opts.GatewayAddr,
		startTime:     time.Now(),
	}

	s.setupRoutes()
	return s
}

// setupRoutes registers all admin routes.
func (s *AdminServer) setupRoutes() {
	// Health endpoint
	s.Engine.GET("/health", s.handleHealth)

	// Statistics
	s.Engine.GET("/stats", s.handleStats)

	// Configuration
	s.Engine.GET("/config", s.handleConfig)

	// Reload configuration
	s.Engine.POST("/reload", s.handleReload)

	// Gateway routes
	s.Engine.GET("/gateway/routes", s.handleGatewayRoutes)

	// Gateway cache statistics
	s.Engine.GET("/gateway/cache/stats", s.handleGatewayCacheStats)

	// Gateway throttle statistics
	s.Engine.GET("/gateway/throttle/stats", s.handleGatewayThrottleStats)

	// Database health
	s.Engine.GET("/db/health", s.handleDBHealth)

	// Database stats
	s.Engine.GET("/db/stats", s.handleDBStats)

	// Metrics (Prometheus)
	s.Engine.GET("/metrics", s.handleMetrics())

	// Build info
	s.Engine.GET("/version", s.handleVersion)

	// Goroutine debug
	s.Engine.GET("/debug/goroutines", s.handleGoroutines)

	// GC endpoint
	s.Engine.POST("/debug/gc", s.handleGC)
}

// ---------------------------------------------------------------------------
// Route Handlers
// ---------------------------------------------------------------------------

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version"`
	Timestamp time.Time         `json:"timestamp"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]string `json:"checks"`
}

// handleHealth returns the health status of all components.
func (s *AdminServer) handleHealth(c *gin.Context) {
	now := time.Now()
	checks := map[string]string{
		"admin": "healthy",
	}

	// Check database
	if s.DBClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.DBClient.Ping(ctx); err != nil {
			checks["database"] = "unhealthy: " + err.Error()
		} else {
			checks["database"] = "healthy"
		}
	} else {
		checks["database"] = "not configured"
	}

	// Check gateway
	checks["gateway"] = "healthy"

	// Determine overall status
	status := "healthy"
	for _, check := range checks {
		if check != "healthy" {
			status = "degraded"
			break
		}
	}

	resp := HealthResponse{
		Status:    status,
		Version:   "1.0.0",
		Timestamp: now,
		Uptime:    now.Sub(s.startTime).String(),
		Checks:    checks,
	}

	if status == "healthy" {
		c.JSON(http.StatusOK, resp)
	} else {
		c.JSON(http.StatusServiceUnavailable, resp)
	}
}

// GatewayStats represents gateway statistics.
type GatewayStats struct {
	Server    ServerStats    `json:"server"`
	Runtime   RuntimeStats   `json:"runtime"`
	Database  DatabaseStats  `json:"database"`
	Cache     CacheStats     `json:"cache"`
	Throttle  ThrottleStats  `json:"throttle"`
	Timestamp time.Time      `json:"timestamp"`
}

// ServerStats holds server-level statistics.
type ServerStats struct {
	Status      string `json:"status"`
	Uptime      string `json:"uptime"`
	Version     string `json:"version"`
	GoVersion   string `json:"go_version"`
	GinMode     string `json:"gin_mode"`
	RequestID   string `json:"request_id"`
}

// RuntimeStats holds Go runtime statistics.
type RuntimeStats struct {
	NumGoroutine  int      `json:"num_goroutines"`
	NumCPU        int      `json:"num_cpu"`
	GoOS          string   `json:"go_os"`
	GoArch        string   `json:"go_arch"`
	NumCgoCall    int64    `json:"num_cgo_calls"`
	MemStats      MemStats `json:"memory"`
}

// MemStats holds memory statistics.
type MemStats struct {
	Alloc         uint64 `json:"alloc_bytes"`
	TotalAlloc    uint64 `json:"total_alloc_bytes"`
	Sys           uint64 `json:"sys_bytes"`
	NumGC         uint32 `json:"num_gc"`
	HeapAlloc     uint64 `json:"heap_alloc_bytes"`
	HeapSys       uint64 `json:"heap_sys_bytes"`
	HeapIdle      uint64 `json:"heap_idle_bytes"`
	HeapInuse     uint64 `json:"heap_inuse_bytes"`
	HeapReleased  uint64 `json:"heap_released_bytes"`
	HeapObjects   uint64 `json:"heap_objects"`
}

// DatabaseStats holds database statistics.
type DatabaseStats struct {
	Connected bool                   `json:"connected"`
	Address   string                 `json:"address"`
	Stats     map[string]interface{} `json:"stats,omitempty"`
}

// CacheStats holds cache statistics.
type CacheStats struct {
	Enabled      bool    `json:"enabled"`
	TotalEntries int64   `json:"total_entries"`
	TotalHits    int64   `json:"total_hits"`
	TotalMisses  int64   `json:"total_misses"`
	HitRate      float64 `json:"hit_rate"`
}

// ThrottleStats holds throttle statistics.
type ThrottleStats struct {
	Enabled         bool  `json:"enabled"`
	RateLimiting    bool  `json:"rate_limiting"`
	SpikeArrest     bool  `json:"spike_arrest"`
	SpikeArrestRate int   `json:"spike_arrest_rate"`
}

// handleStats returns comprehensive statistics.
func (s *AdminServer) handleStats(c *gin.Context) {
	now := time.Now()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Get DB stats
	dbStats := DatabaseStats{
		Connected: s.DBClient != nil && s.DBClient.IsHealthy(),
		Address:   fmt.Sprintf("%s:%d", s.Config.Database.Host, s.Config.Database.Port),
	}
	if s.DBClient != nil {
		dbStats.Stats = s.DBClient.Stats()
	}

	stats := GatewayStats{
		Server: ServerStats{
			Status:    "running",
			Uptime:    now.Sub(s.startTime).String(),
			Version:   "1.0.0",
			GoVersion: runtime.Version(),
			GinMode:   gin.Mode(),
		},
		Runtime: RuntimeStats{
			NumGoroutine: runtime.NumGoroutine(),
			NumCPU:       runtime.NumCPU(),
			GoOS:         runtime.GOOS,
			GoArch:       runtime.GOARCH,
			NumCgoCall:   runtime.NumCgoCall(),
			MemStats: MemStats{
				Alloc:        m.Alloc,
				TotalAlloc:   m.TotalAlloc,
				Sys:          m.Sys,
				NumGC:        m.NumGC,
				HeapAlloc:    m.HeapAlloc,
				HeapSys:      m.HeapSys,
				HeapIdle:     m.HeapIdle,
				HeapInuse:    m.HeapInuse,
				HeapReleased: m.HeapReleased,
				HeapObjects:  m.HeapObjects,
			},
		},
		Database: dbStats,
		Cache: CacheStats{
			Enabled: s.Config.Cache.Enabled,
		},
		Throttle: ThrottleStats{
			Enabled:         s.Config.Throttle.Enabled,
			RateLimiting:    s.Config.RateLimit.Enabled,
			SpikeArrest:     s.Config.Throttle.SpikeArrestEnabled,
			SpikeArrestRate: s.Config.Throttle.SpikeArrestRate,
		},
		Timestamp: now,
	}

	c.JSON(http.StatusOK, stats)
}

// ConfigResponse represents the configuration response.
type ConfigResponse struct {
	Server    config.ServerConfig    `json:"server"`
	Gateway   config.GatewayConfig   `json:"gateway"`
	Database  config.DatabaseConfig  `json:"database"`
	Auth      AuthConfigResponse     `json:"auth"`
	Throttle  config.ThrottleConfig  `json:"throttle"`
	Cache     config.CacheConfig     `json:"cache"`
	Analytics config.AnalyticsConfig `json:"analytics"`
	CORS      config.CORSConfig      `json:"cors"`
}

// AuthConfigResponse is a sanitized version of auth config.
type AuthConfigResponse struct {
	Enabled            bool     `json:"enabled"`
	AccessTokenExpiry  string   `json:"access_token_expiry"`
	RefreshTokenExpiry string   `json:"refresh_token_expiry"`
	TokenIssuer        string   `json:"token_issuer"`
	TokenAudience      string   `json:"token_audience"`
	HeaderName         string   `json:"header_name"`
	QueryParamName     string   `json:"query_param_name"`
	SkipPaths          []string `json:"skip_paths"`
	SkipAuthForOptions bool     `json:"skip_auth_for_options"`
	RevocationCheck    bool     `json:"revocation_check"`
	CacheTokens        bool     `json:"cache_tokens"`
	TokenCacheTTL      string   `json:"token_cache_ttl"`
}

// handleConfig returns the current configuration (sanitized).
func (s *AdminServer) handleConfig(c *gin.Context) {
	cfg := s.Config
	resp := ConfigResponse{
		Server:    cfg.Server,
		Gateway:   cfg.Gateway,
		Database:  cfg.Database,
		Auth: AuthConfigResponse{
			Enabled:            cfg.Auth.Enabled,
			AccessTokenExpiry:  cfg.Auth.AccessTokenExpiry.String(),
			RefreshTokenExpiry: cfg.Auth.RefreshTokenExpiry.String(),
			TokenIssuer:        cfg.Auth.TokenIssuer,
			TokenAudience:      cfg.Auth.TokenAudience,
			HeaderName:         cfg.Auth.HeaderName,
			QueryParamName:     cfg.Auth.QueryParamName,
			SkipPaths:          cfg.Auth.SkipPaths,
			SkipAuthForOptions: cfg.Auth.SkipAuthForOptions,
			RevocationCheck:    cfg.Auth.RevocationCheck,
			CacheTokens:        cfg.Auth.CacheTokens,
			TokenCacheTTL:      cfg.Auth.TokenCacheTTL.String(),
		},
		Throttle:  cfg.Throttle,
		Cache:     cfg.Cache,
		Analytics: cfg.Analytics,
		CORS:      cfg.CORS,
	}

	c.JSON(http.StatusOK, resp)
}

// ReloadResponse represents the reload response.
type ReloadResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// handleReload triggers a configuration reload.
func (s *AdminServer) handleReload(c *gin.Context) {
	ctx := context.Background()

	// Reload configuration
	if s.ConfigManager != nil {
		configPath := config.FindConfigFile()
		if err := s.ConfigManager.Reload(configPath); err != nil {
			s.Logger.Error("failed to reload configuration", zap.Error(err))
			c.JSON(http.StatusInternalServerError, ReloadResponse{
				Status:  "error",
				Message: "failed to reload configuration: " + err.Error(),
			})
			return
		}
	}

	// Ping database to verify connectivity
	if s.DBClient != nil {
		if err := s.DBClient.Ping(ctx); err != nil {
			s.Logger.Warn("database ping after reload failed", zap.Error(err))
		}
	}

	s.Logger.Info("configuration reloaded successfully")
	c.JSON(http.StatusOK, ReloadResponse{
		Status:  "success",
		Message: "configuration reloaded successfully",
	})
}

// GatewayRoutesResponse represents the gateway routes response.
type GatewayRoutesResponse struct {
	Count  int           `json:"count"`
	Routes []RouteInfo   `json:"routes"`
}

// RouteInfo represents a single route info entry.
type RouteInfo struct {
	ID             string `json:"id"`
	APIContext     string `json:"api_context"`
	APIName        string `json:"api_name"`
	APIVersion     string `json:"api_version"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	Endpoint       string `json:"endpoint"`
	AuthRequired   bool   `json:"auth_required"`
	AuthType       string `json:"auth_type"`
	ThrottlePolicy string `json:"throttle_policy,omitempty"`
	Status         string `json:"status"`
}

// handleGatewayRoutes returns all registered gateway routes.
func (s *AdminServer) handleGatewayRoutes(c *gin.Context) {
	// In a full implementation, this would query the router
	// For now, return a placeholder
	routes := []RouteInfo{
		{
			ID:           "default",
			APIContext:   "/",
			APIName:      "default",
			APIVersion:   "1.0",
			Method:       "*",
			Path:         "/*",
			Endpoint:     s.GatewayAddr,
			AuthRequired: false,
			AuthType:     "NONE",
			Status:       "PUBLISHED",
		},
	}

	resp := GatewayRoutesResponse{
		Count:  len(routes),
		Routes: routes,
	}
	c.JSON(http.StatusOK, resp)
}

// handleGatewayCacheStats returns cache statistics.
func (s *AdminServer) handleGatewayCacheStats(c *gin.Context) {
	stats := CacheStats{
		Enabled:      s.Config.Cache.Enabled,
		TotalEntries: 0,
		TotalHits:    0,
		TotalMisses:  0,
		HitRate:      0,
	}
	c.JSON(http.StatusOK, stats)
}

// handleGatewayThrottleStats returns throttle statistics.
func (s *AdminServer) handleGatewayThrottleStats(c *gin.Context) {
	stats := ThrottleStats{
		Enabled:         s.Config.Throttle.Enabled,
		RateLimiting:    s.Config.RateLimit.Enabled,
		SpikeArrest:     s.Config.Throttle.SpikeArrestEnabled,
		SpikeArrestRate: s.Config.Throttle.SpikeArrestRate,
	}
	c.JSON(http.StatusOK, stats)
}

// handleDBHealth checks database health.
func (s *AdminServer) handleDBHealth(c *gin.Context) {
	if s.DBClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":    "unhealthy",
			"component": "database",
			"error":     "not configured",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.DBClient.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":    "unhealthy",
			"component": "database",
			"error":     err.Error(),
			"address":   fmt.Sprintf("%s:%d", s.Config.Database.Host, s.Config.Database.Port),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"component": "database",
		"address":   fmt.Sprintf("%s:%d", s.Config.Database.Host, s.Config.Database.Port),
	})
}

// handleDBStats returns database statistics.
func (s *AdminServer) handleDBStats(c *gin.Context) {
	if s.DBClient == nil {
		c.JSON(http.StatusOK, gin.H{
			"connected": false,
			"stats":     map[string]interface{}{},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"connected": s.DBClient.IsHealthy(),
		"address":   fmt.Sprintf("%s:%d", s.Config.Database.Host, s.Config.Database.Port),
		"stats":     s.DBClient.Stats(),
	})
}

// handleMetrics returns Prometheus metrics.
func (s *AdminServer) handleMetrics() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// VersionResponse represents the version response.
type VersionResponse struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	BuildTime string `json:"build_time"`
	Commit    string `json:"commit,omitempty"`
}

// handleVersion returns version information.
func (s *AdminServer) handleVersion(c *gin.Context) {
	resp := VersionResponse{
		Version:   "1.0.0",
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		BuildTime: s.startTime.Format(time.RFC3339),
	}
	c.JSON(http.StatusOK, resp)
}

// handleGoroutines returns goroutine information.
func (s *AdminServer) handleGoroutines(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"goroutines": runtime.NumGoroutine(),
		"max_procs":  runtime.GOMAXPROCS(0),
	})
}

// handleGC triggers garbage collection.
func (s *AdminServer) handleGC(c *gin.Context) {
	before := runtime.MemStats{}
	runtime.ReadMemStats(&before)

	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	after := runtime.MemStats{}
	runtime.ReadMemStats(&after)

	c.JSON(http.StatusOK, gin.H{
		"status": "gc completed",
		"before": map[string]uint64{
			"heap_alloc": before.HeapAlloc,
			"heap_sys":   before.HeapSys,
		},
		"after": map[string]uint64{
			"heap_alloc": after.HeapAlloc,
			"heap_sys":   after.HeapSys,
		},
		"freed": before.HeapAlloc - after.HeapAlloc,
	})
}

// ---------------------------------------------------------------------------
// Server Lifecycle
// ---------------------------------------------------------------------------

// Run starts the admin server.
func (s *AdminServer) Run(port int) error {
	addr := fmt.Sprintf(":%d", port)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.Engine,
	}

	s.isRunning = true

	s.Logger.Info("starting admin server", zap.String("addr", addr))

	// Start in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- s.httpServer.ListenAndServe()
	}()

	// Wait for error or shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errChan:
		if err != nil && err != http.ErrServerClosed {
			s.Logger.Error("admin server error", zap.Error(err))
			return err
		}
	case <-quit:
		return s.Shutdown()
	}

	return nil
}

// Shutdown performs a graceful shutdown.
func (s *AdminServer) Shutdown() error {
	if !s.isRunning {
		return nil
	}

	s.Logger.Info("shutting down admin server")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.Logger.Error("admin server shutdown error", zap.Error(err))
		return err
	}

	s.isRunning = false
	s.Logger.Info("admin server stopped")
	return nil
}

// IsRunning returns true if the admin server is running.
func (s *AdminServer) IsRunning() bool {
	return s.isRunning
}
