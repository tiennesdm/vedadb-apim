// Package traffic implements the Traffic Manager for VedaDB API Manager.
// It provides advanced throttling, quota management, and rate limiting policies.
package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vedadb/vapim/pkg/models"
)

// Config holds the Traffic Manager server configuration.
type Config struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	AuthToken       string // Admin API key for traffic manager endpoints
}

// DefaultConfig returns sensible defaults for the traffic manager.
func DefaultConfig() Config {
	return Config{
		Addr:            ":9090",
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    10 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}
}

// ThrottleStore defines the persistence interface for throttle state.
type ThrottleStore interface {
	// Counter operations
	IncrementCounter(ctx context.Context, key string, window time.Duration) (int64, error)
	GetCounter(ctx context.Context, key string) (int64, error)
	ResetCounter(ctx context.Context, key string) error
	SetCounter(ctx context.Context, key string, value int64, ttl time.Duration) error

	// Policy storage
	GetPolicy(ctx context.Context, policyID string) (*models.ThrottlePolicy, error)
	ListPolicies(ctx context.Context, policyType string) ([]models.ThrottlePolicy, error)
	SavePolicy(ctx context.Context, policy *models.ThrottlePolicy) error
	DeletePolicy(ctx context.Context, policyID string) error

	// Quota operations
	GetQuotaUsage(ctx context.Context, quotaKey string) (*models.QuotaUsage, error)
	SetQuotaUsage(ctx context.Context, usage *models.QuotaUsage) error
	IncrementQuotaUsage(ctx context.Context, quotaKey string, amount int64) (int64, error)
	ResetQuotaUsage(ctx context.Context, quotaKey string) error

	// Event publishing for distributed sync
	PublishCounterEvent(ctx context.Context, event *models.CounterEvent) error
}

// Server is the Traffic Manager HTTP server.
type Server struct {
	config    Config
	router    *gin.Engine
	store     ThrottleStore
	engine    *ThrottlingEngine
	quotaMgr  *QuotaManager
	logger    *slog.Logger
	http      *http.Server
	analytics AnalyticsCollector
}

// AnalyticsCollector is the interface for publishing throttle analytics events.
type AnalyticsCollector interface {
	CollectThrottleEvent(ctx context.Context, event *models.ThrottleEvent) error
}

// NewServer creates a new Traffic Manager server.
func NewServer(cfg Config, store ThrottleStore, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	s := &Server{
		config: cfg,
		store:  store,
		logger: logger.With("component", "traffic-manager"),
	}

	// Initialize subsystems
	s.engine = NewThrottlingEngine(store, logger)
	s.quotaMgr = NewQuotaManager(store, logger)

	return s
}

// SetAnalyticsCollector injects the analytics collector for throttle events.
func (s *Server) SetAnalyticsCollector(ac AnalyticsCollector) {
	s.analytics = ac
}

// BuildRouter sets up all routes.
func (s *Server) BuildRouter() *gin.Engine {
	if gin.Mode() == gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.requestLogger())
	r.Use(s.adminAuth())

	// Health
	r.GET("/health", s.handleHealth)
	r.GET("/ready", s.handleReadiness)

	// Throttle status (for gateway integration)
	r.POST("/api/traffic/v1/throttle/check", s.handleThrottleCheck)
	r.GET("/api/traffic/v1/throttle/status/:key", s.handleGetThrottleStatus)

	// Counter management
	r.GET("/api/traffic/v1/counters/:key", s.handleGetCounter)
	r.POST("/api/traffic/v1/counters/:key/increment", s.handleIncrementCounter)
	r.DELETE("/api/traffic/v1/counters/:key", s.handleResetCounter)

	// Policy management
	r.GET("/api/traffic/v1/policies", s.handleListPolicies)
	r.POST("/api/traffic/v1/policies", s.handleCreatePolicy)
	r.GET("/api/traffic/v1/policies/:policyID", s.handleGetPolicy)
	r.PUT("/api/traffic/v1/policies/:policyID", s.handleUpdatePolicy)
	r.DELETE("/api/traffic/v1/policies/:policyID", s.handleDeletePolicy)

	// Quota management
	r.GET("/api/traffic/v1/quotas/:quotaKey", s.handleGetQuotaUsage)
	r.POST("/api/traffic/v1/quotas/:quotaKey/reset", s.handleResetQuota)
	r.GET("/api/traffic/v1/quotas/tier/:tierName", s.handleGetTierQuota)

	// Burst control
	r.POST("/api/traffic/v1/burst/allow", s.handleBurstAllow)
	r.GET("/api/traffic/v1/burst/status/:key", s.handleBurstStatus)

	s.router = r
	return r
}

// Run starts the traffic manager server.
func (s *Server) Run() error {
	if s.router == nil {
		s.BuildRouter()
	}

	s.http = &http.Server{
		Addr:         s.config.Addr,
		Handler:      s.router,
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
		IdleTimeout:  s.config.IdleTimeout,
		MaxHeaderBytes: 1 << 20,
	}

	s.logger.Info("traffic manager starting", "addr", s.config.Addr)

	go s.handleShutdown()

	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.logger.Error("traffic manager server error", "error", err)
		return fmt.Errorf("traffic manager failed: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("traffic manager shutting down")
	if s.http != nil {
		return s.http.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleShutdown() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	sig := <-sigCh
	s.logger.Info("received shutdown signal", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		s.logger.Error("graceful shutdown failed", "error", err)
	} else {
		s.logger.Info("traffic manager stopped gracefully")
	}
}

// Engine returns the throttling engine for direct integration.
func (s *Server) Engine() *ThrottlingEngine {
	return s.engine
}

// QuotaManager returns the quota manager for direct integration.
func (s *Server) QuotaManager() *QuotaManager {
	return s.quotaMgr
}

// --- Middleware ---

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		s.logger.Debug("traffic request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

func (s *Server) adminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Health endpoints are public
		if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/ready" {
			c.Next()
			return
		}

		// Throttle check is called by gateway with service token
		if c.Request.URL.Path == "/api/traffic/v1/throttle/check" {
			c.Next()
			return
		}

		// Admin endpoints require auth token
		token := c.GetHeader("X-Admin-Token")
		if token == "" {
			token = extractBearerToken(c)
		}

		if s.config.AuthToken != "" && token != s.config.AuthToken {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error:  "unauthorized",
				Code:   "AUTH_REQUIRED",
				Status: http.StatusUnauthorized,
			})
			return
		}
		c.Next()
	}
}

// --- Handlers ---

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "vedadb-apim-traffic-manager",
		"time":    time.Now().UTC(),
	})
}

func (s *Server) handleReadiness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ready",
		"service": "vedadb-apim-traffic-manager",
		"time":    time.Now().UTC(),
	})
}

// handleThrottleCheck evaluates a throttle request from the gateway.
func (s *Server) handleThrottleCheck(c *gin.Context) {
	var req models.ThrottleCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:  "invalid request: " + err.Error(),
			Code:   "INVALID_REQUEST",
			Status: http.StatusBadRequest,
		})
		return
	}

	result := s.engine.Evaluate(c.Request.Context(), &req)

	// Publish analytics event if throttled
	if result.Throttled && s.analytics != nil {
		event := &models.ThrottleEvent{
			APIID:       req.APIID,
			AppID:       req.AppID,
			UserID:      req.UserID,
			Tier:        req.Tier,
			Level:       result.Level,
			PolicyID:    result.PolicyID,
			Timestamp:   time.Now().UTC(),
			RetryAfter:  result.RetryAfter,
		}
		go s.analytics.CollectThrottleEvent(c.Request.Context(), event)
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) handleGetThrottleStatus(c *gin.Context) {
	key := c.Param("key")
	status, err := s.engine.GetStatus(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to get throttle status: " + err.Error(),
			Code:   "STATUS_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusOK, status)
}

func (s *Server) handleGetCounter(c *gin.Context) {
	key := c.Param("key")
	val, err := s.store.GetCounter(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to get counter: " + err.Error(),
			Code:   "COUNTER_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": val})
}

func (s *Server) handleIncrementCounter(c *gin.Context) {
	key := c.Param("key")
	var req struct {
		Window string `json:"window"`
	}
	c.ShouldBindJSON(&req)

	window := time.Minute
	if req.Window != "" {
		if d, err := time.ParseDuration(req.Window); err == nil {
			window = d
		}
	}

	val, err := s.store.IncrementCounter(c.Request.Context(), key, window)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to increment counter: " + err.Error(),
			Code:   "COUNTER_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"key": key, "value": val})
}

func (s *Server) handleResetCounter(c *gin.Context) {
	key := c.Param("key")
	if err := s.store.ResetCounter(c.Request.Context(), key); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to reset counter: " + err.Error(),
			Code:   "COUNTER_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (s *Server) handleListPolicies(c *gin.Context) {
	policyType := c.Query("type")
	policies, err := s.store.ListPolicies(c.Request.Context(), policyType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to list policies: " + err.Error(),
			Code:   "POLICY_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusOK, policies)
}

func (s *Server) handleCreatePolicy(c *gin.Context) {
	var policy models.ThrottlePolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:  "invalid request: " + err.Error(),
			Code:   "INVALID_REQUEST",
			Status: http.StatusBadRequest,
		})
		return
	}
	policy.CreatedAt = time.Now().UTC()
	policy.UpdatedAt = time.Now().UTC()

	if err := s.store.SavePolicy(c.Request.Context(), &policy); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to create policy: " + err.Error(),
			Code:   "POLICY_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusCreated, policy)
}

func (s *Server) handleGetPolicy(c *gin.Context) {
	policyID := c.Param("policyID")
	policy, err := s.store.GetPolicy(c.Request.Context(), policyID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:  "policy not found",
			Code:   "POLICY_NOT_FOUND",
			Status: http.StatusNotFound,
		})
		return
	}
	c.JSON(http.StatusOK, policy)
}

func (s *Server) handleUpdatePolicy(c *gin.Context) {
	policyID := c.Param("policyID")
	var policy models.ThrottlePolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:  "invalid request: " + err.Error(),
			Code:   "INVALID_REQUEST",
			Status: http.StatusBadRequest,
		})
		return
	}
	policy.ID = policyID
	policy.UpdatedAt = time.Now().UTC()

	if err := s.store.SavePolicy(c.Request.Context(), &policy); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to update policy: " + err.Error(),
			Code:   "POLICY_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusOK, policy)
}

func (s *Server) handleDeletePolicy(c *gin.Context) {
	policyID := c.Param("policyID")
	if err := s.store.DeletePolicy(c.Request.Context(), policyID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to delete policy: " + err.Error(),
			Code:   "POLICY_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (s *Server) handleGetQuotaUsage(c *gin.Context) {
	quotaKey := c.Param("quotaKey")
	usage, err := s.quotaMgr.GetUsage(c.Request.Context(), quotaKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to get quota usage: " + err.Error(),
			Code:   "QUOTA_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusOK, usage)
}

func (s *Server) handleResetQuota(c *gin.Context) {
	quotaKey := c.Param("quotaKey")
	if err := s.quotaMgr.ResetUsage(c.Request.Context(), quotaKey); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  "failed to reset quota: " + err.Error(),
			Code:   "QUOTA_ERROR",
			Status: http.StatusInternalServerError,
		})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (s *Server) handleGetTierQuota(c *gin.Context) {
	tierName := c.Param("tierName")
	quota := s.quotaMgr.GetTierDefinition(tierName)
	if quota == nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:  "tier not found",
			Code:   "TIER_NOT_FOUND",
			Status: http.StatusNotFound,
		})
		return
	}
	c.JSON(http.StatusOK, quota)
}

func (s *Server) handleBurstAllow(c *gin.Context) {
	var req models.BurstRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:  "invalid request: " + err.Error(),
			Code:   "INVALID_REQUEST",
			Status: http.StatusBadRequest,
		})
		return
	}

	allowed := s.engine.AllowBurst(c.Request.Context(), req.Key, req.BurstSize)
	c.JSON(http.StatusOK, gin.H{
		"allowed": allowed,
		"key":     req.Key,
	})
}

func (s *Server) handleBurstStatus(c *gin.Context) {
	key := c.Param("key")
	status := s.engine.GetBurstStatus(c.Request.Context(), key)
	c.JSON(http.StatusOK, status)
}

func extractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}
