// Package portal implements the Developer Portal HTTP server for VedaDB API Manager.
// It provides API catalog browsing, application management, subscription handling,
// and API testing capabilities for API consumers.
package portal

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// Config holds the Developer Portal server configuration.
type Config struct {
	Addr              string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	TrustedProxies    []string
	CORSOrigins       []string
	CORSMethods       []string
	CORSHeaders       []string
	AllowCredentials  bool
	MaxBodySize       int64
	RequireHTTPS      bool
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() Config {
	return Config{
		Addr:             ":9443",
		ReadTimeout:      15 * time.Second,
		WriteTimeout:     15 * time.Second,
		IdleTimeout:      60 * time.Second,
		ShutdownTimeout:  30 * time.Second,
		CORSOrigins:      []string{"*"},
		CORSMethods:      []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"},
		CORSHeaders:      []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID", "X-Correlation-ID"},
		AllowCredentials: true,
		MaxBodySize:      10 << 20, // 10 MB
		RequireHTTPS:     false,
	}
}

// Store defines the persistence interface required by the portal.
type Store interface {
	// API catalog
	ListPublishedAPIs(ctx context.Context, offset, limit int, filter *models.APIFilter) ([]models.PublishedAPI, int64, error)
	GetAPIDetails(ctx context.Context, apiID string) (*models.API, error)
	SearchAPIs(ctx context.Context, query string, offset, limit int) ([]models.PublishedAPI, int64, error)
	GetAPIRating(ctx context.Context, apiID string) (*models.APIRating, error)
	GetAPIReviews(ctx context.Context, apiID string, offset, limit int) ([]models.APIReview, int64, error)
	GetAPIDocumentation(ctx context.Context, apiID string) ([]models.APIDoc, error)
	GetAPISwagger(ctx context.Context, apiID string) (*models.SwaggerDef, error)

	// Applications
	CreateApplication(ctx context.Context, app *models.Application) error
	GetApplication(ctx context.Context, appID, userID string) (*models.Application, error)
	ListApplications(ctx context.Context, userID string, offset, limit int) ([]models.Application, int64, error)
	UpdateApplication(ctx context.Context, app *models.Application) error
	DeleteApplication(ctx context.Context, appID, userID string) error
	GenerateApplicationKeys(ctx context.Context, appID, userID, keyType, tier string) (*models.ApplicationKeys, error)
	GetApplicationKeys(ctx context.Context, appID, userID string) ([]models.ApplicationKeys, error)
	RegenerateKeys(ctx context.Context, appID, userID, keyType string) (*models.ApplicationKeys, error)

	// Subscriptions
	SubscribeToAPI(ctx context.Context, sub *models.Subscription) error
	UnsubscribeFromAPI(ctx context.Context, subID, userID string) error
	ListSubscriptions(ctx context.Context, userID string, offset, limit int) ([]models.Subscription, int64, error)
	GetSubscription(ctx context.Context, subID, userID string) (*models.Subscription, error)
	ValidateSubscription(ctx context.Context, appID, apiID string) (*models.Subscription, error)

	// Users
	GetUserByToken(ctx context.Context, token string) (*models.User, error)

	// Analytics
	InsertAnalyticsEvent(e *models.AnalyticsEventDB) error
}

// Server is the Developer Portal HTTP server.
type Server struct {
	config Config
	router *gin.Engine
	store  Store
	logger *slog.Logger
	http   *http.Server
}

// NewServer creates a new Developer Portal server.
func NewServer(cfg Config, store Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &Server{
		config: cfg,
		store:  store,
		logger: logger.With("component", "developer-portal"),
	}
}

// BuildRouter sets up all routes and returns the gin.Engine.
func (s *Server) BuildRouter() *gin.Engine {
	if gin.Mode() == gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.requestLogger())
	r.Use(s.correlationID())
	r.Use(s.corsMiddleware())
	r.Use(s.httpsRedirect())

	// Public routes (no authentication required)
	s.registerPublicRoutes(r)

	// Protected routes (subscriber authentication required)
	protected := r.Group("/api/portal/v1")
	protected.Use(s.subscriberAuth())
	s.registerProtectedRoutes(protected)

	s.router = r
	return r
}

// registerPublicRoutes sets up all public (unauthenticated) endpoints.
func (s *Server) registerPublicRoutes(r *gin.Engine) {
	// Health check
	r.GET("/health", s.handleHealth)
	r.GET("/ready", s.handleReadiness)

	// Public API catalog
	r.GET("/api/portal/v1/apis", s.handleListPublishedAPIs)
	r.GET("/api/portal/v1/apis/search", s.handleSearchAPIs)
	r.GET("/api/portal/v1/apis/:apiID", s.handleGetAPIDetails)
	r.GET("/api/portal/v1/apis/:apiID/rating", s.handleGetAPIRating)
	r.GET("/api/portal/v1/apis/:apiID/reviews", s.handleGetAPIReviews)
	r.GET("/api/portal/v1/apis/:apiID/docs", s.handleGetAPIDocumentation)
	r.GET("/api/portal/v1/apis/:apiID/swagger", s.handleGetAPISwagger)

	// Thumbnail / icon
	r.GET("/api/portal/v1/apis/:apiID/thumbnail", s.handleGetAPIThumbnail)
}

// registerProtectedRoutes sets up all authenticated endpoints.
func (s *Server) registerProtectedRoutes(g *gin.RouterGroup) {
	// Applications
	g.POST("/applications", s.handleCreateApplication)
	g.GET("/applications", s.handleListApplications)
	g.GET("/applications/:appID", s.handleGetApplication)
	g.PUT("/applications/:appID", s.handleUpdateApplication)
	g.DELETE("/applications/:appID", s.handleDeleteApplication)
	g.POST("/applications/:appID/keys", s.handleGenerateApplicationKeys)
	g.GET("/applications/:appID/keys", s.handleGetApplicationKeys)
	g.POST("/applications/:appID/keys/:keyType/regenerate", s.handleRegenerateKeys)

	// Subscriptions
	g.POST("/subscriptions", s.handleSubscribeToAPI)
	g.GET("/subscriptions", s.handleListSubscriptions)
	g.GET("/subscriptions/:subID", s.handleGetSubscription)
	g.DELETE("/subscriptions/:subID", s.handleUnsubscribeFromAPI)
	g.GET("/subscriptions/validate", s.handleValidateSubscription)

	// Try-it console
	g.POST("/try-it", s.handleTryIt)
	g.POST("/try-it/proxy", s.handleTryItProxy)
}

// Run starts the HTTP server and blocks until shutdown.
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
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	s.logger.Info("developer portal starting",
		"addr", s.config.Addr,
		"cors_origins", s.config.CORSOrigins,
	)

	// Graceful shutdown handling
	go s.handleShutdown()

	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.logger.Error("developer portal server error", "error", err)
		return fmt.Errorf("portal server failed: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("developer portal shutting down")
	if s.http != nil {
		return s.http.Shutdown(ctx)
	}
	return nil
}

// handleShutdown listens for OS signals and initiates graceful shutdown.
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
		s.logger.Info("developer portal stopped gracefully")
	}
}

// --- Middleware ---

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		if raw != "" {
			path = path + "?" + raw
		}

		s.logger.Debug("http request",
			"client_ip", clientIP,
			"method", method,
			"path", path,
			"status", statusCode,
			"latency_ms", latency.Milliseconds(),
			"error", errorMessage,
			"request_id", c.GetString("request_id"),
		)
	}
}

func (s *Server) correlationID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = c.GetHeader("X-Correlation-ID")
		}
		if rid == "" {
			rid = generateRequestID()
		}
		c.Set("request_id", rid)
		c.Header("X-Request-ID", rid)
		c.Next()
	}
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	cfg := cors.DefaultConfig()
	cfg.AllowOrigins = s.config.CORSOrigins
	if len(s.config.CORSOrigins) == 1 && s.config.CORSOrigins[0] == "*" {
		cfg.AllowAllOrigins = true
	}
	cfg.AllowMethods = s.config.CORSMethods
	cfg.AllowHeaders = s.config.CORSHeaders
	cfg.AllowCredentials = s.config.AllowCredentials
	cfg.ExposeHeaders = []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"}
	cfg.MaxAge = 12 * time.Hour
	return cors.New(cfg)
}

func (s *Server) httpsRedirect() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.config.RequireHTTPS && c.GetHeader("X-Forwarded-Proto") == "http" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "HTTPS required",
				"code":  "HTTPS_REQUIRED",
			})
			return
		}
		c.Next()
	}
}

func (s *Server) subscriberAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractBearerToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error:   "missing or invalid authorization token",
				Code:    "AUTH_REQUIRED",
				Status:  http.StatusUnauthorized,
				RequestID: c.GetString("request_id"),
			})
			return
		}

		ctx := c.Request.Context()
		user, err := s.store.GetUserByToken(ctx, token)
		if err != nil {
			s.logger.Warn("authentication failed", "error", err, "request_id", c.GetString("request_id"))
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Error:   "invalid or expired token",
				Code:    "AUTH_INVALID",
				Status:  http.StatusUnauthorized,
				RequestID: c.GetString("request_id"),
			})
			return
		}

		c.Set("user", user)
		c.Set("user_id", user.ID)
		c.Set("tenant", user.TenantID)
		c.Next()
	}
}

// --- Public Handlers ---

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"service":   "vedadb-apim-developer-portal",
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleReadiness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ready",
		"service":   "vedadb-apim-developer-portal",
		"timestamp": time.Now().UTC(),
	})
}

// --- Helpers ---

func extractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	// Also check query param for swagger UI
	if token := c.Query("access_token"); token != "" {
		return token
	}
	return ""
}

func getUserID(c *gin.Context) string {
	uid, _ := c.Get("user_id")
	if id, ok := uid.(string); ok {
		return id
	}
	return ""
}

func getTenant(c *gin.Context) string {
	t, _ := c.Get("tenant")
	if tenant, ok := t.(string); ok {
		return tenant
	}
	return "carbon.super"
}

func generateRequestID() string {
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), generateRandomBytes(8))
}

func generateRandomBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(time.Now().UnixNano() % 256)
	}
	return b
}

func parsePagination(c *gin.Context) (offset, limit int) {
	offset = 0
	limit = 25
	if o := c.Query("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit > 100 {
			limit = 100
		}
		if limit < 1 {
			limit = 25
		}
	}
	return
}
