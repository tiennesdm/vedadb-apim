// Package keymanager provides the Key Manager HTTP server using the Gin framework.
// It wires OAuth2 endpoints, token validation, API key management, and JWKS.
package keymanager

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vedadb/vapim/internal/auth"
	"github.com/vedadb/vapim/pkg/models"
)

// ServerConfig holds configuration for the Key Manager server.
type ServerConfig struct {
	Host       string
	Port       string
	Issuer     string
	TenantID   string
	JWTKeyBits int
}

// DefaultServerConfig returns sensible defaults for development.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Host:       "0.0.0.0",
		Port:       "9443",
		Issuer:     "https://keymanager.vedadb-apim.io",
		TenantID:   "carbon.super",
		JWTKeyBits: 2048,
	}
}

// Server is the Key Manager HTTP server.
type Server struct {
	router      *gin.Engine
	config      ServerConfig
	oauth2      *OAuth2Server
	jwt         *JWTManager
	apiKeys     *APIKeyManager
	store       OAuth2Store
	apiKeyStore APIKeyStore
	httpSrv     *http.Server
}

// NewServer creates a new Key Manager server with all dependencies wired.
func NewServer(cfg ServerConfig, store OAuth2Store, apiKeyStore APIKeyStore) (*Server, error) {
	jwtMgr, err := NewJWTManager(cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("init jwt manager: %w", err)
	}

	oauth2Srv := NewOAuth2Server(store, jwtMgr, cfg.Issuer, cfg.TenantID)
	apiKeyMgr := NewAPIKeyManager(apiKeyStore, jwtMgr, cfg.Issuer)

	s := &Server{
		config:      cfg,
		oauth2:      oauth2Srv,
		jwt:         jwtMgr,
		apiKeys:     apiKeyMgr,
		store:       store,
		apiKeyStore: apiKeyStore,
	}

	s.setupRouter()
	return s, nil
}

// setupRouter initializes the Gin router with all middleware and routes.
func (s *Server) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Global middleware
	r.Use(gin.Recovery())
	r.Use(s.loggingMiddleware())
	r.Use(s.corsMiddleware())
	r.Use(s.securityHeaders())

	// Health check
	r.GET("/health", s.handleHealth)
	r.GET("/ready", s.handleReady)

	// JWKS endpoint (public)
	r.GET("/jwks", s.handleJWKS)
	r.GET("/.well-known/jwks.json", s.handleJWKS)
	r.GET("/.well-known/openid-configuration", s.handleOIDCConfig)

	// API Key management routes
	apiKeyRoutes := r.Group("/api-keys")
	{
		apiKeyRoutes.Use(s.authMiddleware())
		apiKeyRoutes.POST("", s.handleCreateAPIKey)
		apiKeyRoutes.GET("/:id", s.handleGetAPIKey)
		apiKeyRoutes.GET("/app/:app_id", s.handleListAPIKeys)
		apiKeyRoutes.POST("/:id/regenerate", s.handleRegenerateAPIKey)
		apiKeyRoutes.DELETE("/:id", s.handleRevokeAPIKey)
		apiKeyRoutes.POST("/validate", s.handleValidateAPIKey)
		apiKeyRoutes.POST("/jwt", s.handleCreateJWTAPIKey)
		apiKeyRoutes.POST("/jwt/validate", s.handleValidateJWTAPIKey)
	}

	// Token validation (can be used by gateway)
	r.POST("/validate", s.handleValidateToken)
	r.POST("/token/validate", s.handleValidateToken)

	// OAuth2 routes
	oauth2Group := r.Group("/oauth2")
	s.oauth2.BuildOAuth2Routes(oauth2Group)

	s.router = r
}

// Run starts the HTTP server and blocks until shutdown signal.
func (s *Server) Run() error {
	s.httpSrv = &http.Server{
		Addr:    s.config.Host + ":" + s.config.Port,
		Handler: s.router,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "keymanager server error: %v\n", err)
		}
	}()

	fmt.Printf("Key Manager server listening on %s:%s\n", s.config.Host, s.config.Port)

	<-quit
	fmt.Println("Shutting down Key Manager server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.httpSrv.Shutdown(ctx)
}

// Router returns the Gin engine for testing or additional middleware attachment.
func (s *Server) Router() *gin.Engine {
	return s.router
}

// --- HTTP Handlers ---

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"service":   "keymanager",
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleReady(c *gin.Context) {
	ready := s.jwt != nil && s.jwt.CountActiveKeys() > 0
	if !ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

func (s *Server) handleJWKS(c *gin.Context) {
	c.JSON(http.StatusOK, s.jwt.GetJWKS())
}

func (s *Server) handleOIDCConfig(c *gin.Context) {
	issuer := s.config.Issuer
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                 issuer,
		"authorization_endpoint":                 issuer + "/oauth2/authorize",
		"token_endpoint":                         issuer + "/oauth2/token",
		"token_introspection_endpoint":           issuer + "/oauth2/introspect",
		"revocation_endpoint":                    issuer + "/oauth2/revoke",
		"jwks_uri":                               issuer + "/jwks",
		"userinfo_endpoint":                      issuer + "/oauth2/userinfo",
		"registration_endpoint":                  issuer + "/oauth2/register",
		"scopes_supported":                       []string{"openid", "profile", "email", "api:read", "api:write", "subscribe", "publish"},
		"response_types_supported":               []string{"code", "token"},
		"grant_types_supported":                  []string{"client_credentials", "password", "authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":    []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":         []string{"S256", "plain"},
		"id_token_signing_alg_values_supported":    []string{"RS256", "ES256", "HS256"},
		"subject_types_supported":                  []string{"public"},
	})
}

func (s *Server) handleValidateToken(c *gin.Context) {
	token := c.PostForm("token")
	if token == "" {
		token = c.GetHeader("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
	}

	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}

	claims, err := s.oauth2.ValidateAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"active": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"active":    true,
		"sub":       claims.Sub,
		"iss":       claims.Iss,
		"aud":       claims.Aud,
		"exp":       claims.Exp,
		"iat":       claims.Iat,
		"jti":       claims.Jti,
		"scope":     claims.Scope,
		"client_id": claims.ClientID,
		"tenant_id": claims.TenantID,
	})
}

// handleCreateAPIKey creates a new API key.
func (s *Server) handleCreateAPIKey(c *gin.Context) {
	var req models.APIKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantID := auth.GetTenantID(c)
	key, rawKey, err := s.apiKeys.GenerateKey(req, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Return raw key only once
	c.JSON(http.StatusCreated, gin.H{
		"id":         key.ID,
		"name":       key.Name,
		"app_id":     key.AppID,
		"api_id":     key.APIID,
		"api_key":    rawKey,
		"scopes":     key.Scopes,
		"valid_from": key.ValidFrom,
		"valid_to":   key.ValidTo,
		"created_at": key.CreatedAt,
	})
}

// handleGetAPIKey retrieves an API key by ID (does not return the key value).
func (s *Server) handleGetAPIKey(c *gin.Context) {
	id := c.Param("id")
	key, err := s.apiKeys.GetKey(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          key.ID,
		"name":        key.Name,
		"description": key.Description,
		"app_id":      key.AppID,
		"api_id":      key.APIID,
		"scopes":      key.Scopes,
		"valid_from":  key.ValidFrom,
		"valid_to":    key.ValidTo,
		"revoked":     key.Revoked,
		"usage_count": key.UsageCount,
		"created_at":  key.CreatedAt,
	})
}

// handleListAPIKeys lists API keys for an application.
func (s *Server) handleListAPIKeys(c *gin.Context) {
	appID := c.Param("app_id")
	keys, err := s.apiKeys.ListKeysByApp(appID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var result []gin.H
	for _, k := range keys {
		result = append(result, gin.H{
			"id":          k.ID,
			"name":        k.Name,
			"app_id":      k.AppID,
			"api_id":      k.APIID,
			"scopes":      k.Scopes,
			"valid_from":  k.ValidFrom,
			"valid_to":    k.ValidTo,
			"revoked":     k.Revoked,
			"usage_count": k.UsageCount,
			"created_at":  k.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"keys": result, "total": len(result)})
}

// handleRegenerateAPIKey revokes the old key and creates a new one.
func (s *Server) handleRegenerateAPIKey(c *gin.Context) {
	id := c.Param("id")
	key, rawKey, err := s.apiKeys.RegenerateKey(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      key.ID,
		"name":    key.Name,
		"app_id":  key.AppID,
		"api_key": rawKey,
		"scopes":  key.Scopes,
	})
}

// handleRevokeAPIKey revokes an API key.
func (s *Server) handleRevokeAPIKey(c *gin.Context) {
	id := c.Param("id")
	if err := s.apiKeys.RevokeKey(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// handleValidateAPIKey validates a raw API key.
func (s *Server) handleValidateAPIKey(c *gin.Context) {
	rawKey := c.PostForm("api_key")
	if rawKey == "" {
		var body struct {
			APIKey string `json:"api_key" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_key required"})
			return
		}
		rawKey = body.APIKey
	}

	key, err := s.apiKeys.ValidateKey(rawKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":      true,
		"key_id":     key.ID,
		"app_id":     key.AppID,
		"api_id":     key.APIID,
		"scopes":     key.Scopes,
		"valid_from": key.ValidFrom,
		"valid_to":   key.ValidTo,
	})
}

// handleCreateJWTAPIKey creates a JWT-based API key.
func (s *Server) handleCreateJWTAPIKey(c *gin.Context) {
	var req models.APIKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tenantID := auth.GetTenantID(c)
	token, key, err := s.apiKeys.GenerateJWTAPIKey(req, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":         key.ID,
		"name":       key.Name,
		"app_id":     key.AppID,
		"api_id":     key.APIID,
		"api_key":    token,
		"token_type": "JWT",
		"scopes":     key.Scopes,
		"valid_from": key.ValidFrom,
		"valid_to":   key.ValidTo,
	})
}

// handleValidateJWTAPIKey validates a JWT-based API key.
func (s *Server) handleValidateJWTAPIKey(c *gin.Context) {
	var body struct {
		Token string `json:"api_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		token := c.PostForm("api_key")
		if token == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_key (JWT) required"})
			return
		}
		body.Token = token
	}

	key, err := s.apiKeys.ValidateJWTAPIKey(body.Token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":      true,
		"key_id":     key.ID,
		"app_id":     key.AppID,
		"api_id":     key.APIID,
		"scopes":     key.Scopes,
		"valid_from": key.ValidFrom,
		"valid_to":   key.ValidTo,
	})
}

// --- Middleware ---

func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
			param.ClientIP,
			param.TimeStamp.Format("2006-01-02 15:04:05"),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	})
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
		c.Writer.Header().Set("X-Frame-Options", "DENY")
		c.Writer.Header().Set("X-XSS-Protection", "1; mode=block")
		c.Writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}

// authMiddleware extracts the bearer token, validates it, and sets user context.
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authorization header required"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header format"})
			c.Abort()
			return
		}

		token := parts[1]
		claims, err := s.oauth2.ValidateAccessToken(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			c.Abort()
			return
		}

		// Set user context for downstream handlers
		roles := []string{"subscriber"}
		if claims.Scope != "" {
			for _, sc := range strings.Fields(claims.Scope) {
				if sc == "publish" {
					roles = append(roles, "publisher")
				}
				if sc == "api:admin" {
					roles = append(roles, "admin")
				}
			}
		}

		userID := claims.Sub
		if userID == "" {
			userID = claims.ClientID
		}
		tenantID := claims.TenantID
		if tenantID == "" {
			tenantID = "carbon.super"
		}

		auth.SetContextUser(c, userID, roles, tenantID)
		c.Set("token_claims", claims)
		c.Next()
	}
}
