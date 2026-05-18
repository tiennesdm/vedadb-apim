// Package publisher provides the Publisher HTTP server using the Gin framework.
// It exposes API CRUD endpoints, lifecycle management, versioning, resource and
// policy management protected by role-based authentication.
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vedadb/vapim/internal/audit"
	"github.com/vedadb/vapim/internal/auth"
	"github.com/vedadb/vapim/pkg/models"
)

// Store defines the persistence interface required by the Publisher.
type Store interface {
	// API CRUD
	SaveAPI(api *models.API) error
	GetAPI(id string) (*models.API, error)
	UpdateAPI(api *models.API) error
	DeleteAPI(id string) error
	ListAPIs(tenantID string, offset, limit int) ([]models.API, int, error)
	SearchAPIs(req models.SearchRequest) ([]models.API, int, error)

	// Versions
	GetVersionSet(id string) (*models.VersionSet, error)
	SaveVersionSet(vs *models.VersionSet) error
	ListAPIVersions(versionSetID string) ([]models.API, error)
	UpdateVersionSetDefault(id, version string) error

	// Resources
	SaveResource(res *models.APIResource) error
	GetResource(id string) (*models.APIResource, error)
	UpdateResource(res *models.APIResource) error
	DeleteResource(id string) error
	ListResourcesByAPI(apiID string) ([]models.APIResource, error)

	// Policies
	SavePolicy(p *models.Policy) error
	GetPolicy(id string) (*models.Policy, error)
	UpdatePolicy(p *models.Policy) error
	DeletePolicy(id string) error
	ListPolicies(tenantID, policyType string) ([]models.Policy, error)

	// Lifecycle
	TransitionAPIStatus(id string, newStatus models.APIStatus) error
}

// TokenValidator validates JWT/OAuth2 tokens.
type TokenValidator interface {
	ValidateToken(token string) (*models.JWTClaims, error)
}

// ServerConfig holds configuration for the Publisher server.
type ServerConfig struct {
	Host     string
	Port     string
	TenantID string
}

// DefaultServerConfig returns sensible defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Host:     "0.0.0.0",
		Port:     "9763",
		TenantID: "carbon.super",
	}
}

// Server is the Publisher HTTP server.
type Server struct {
	router    *gin.Engine
	config    ServerConfig
	store     Store
	validator TokenValidator
	apiMgr    *APIManager
	lifecycle *LifecycleManager
	version   *VersionManager
	resource  *ResourceManager
	policy    *PolicyManager
	httpSrv   *http.Server
}

// NewServer creates a new Publisher server.
func NewServer(cfg ServerConfig, store Store, validator TokenValidator) (*Server, error) {
	s := &Server{
		config:    cfg,
		store:     store,
		validator: validator,
	}

	s.apiMgr = NewAPIManager(store)
	s.lifecycle = NewLifecycleManager(store)
	s.version = NewVersionManager(store)
	s.resource = NewResourceManager(store)
	s.policy = NewPolicyManager(store)

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
	r.Use(s.authMiddleware())

	// Health check
	r.GET("/health", s.handleHealth)
	r.GET("/ready", s.handleReady)

	// API management routes
	apiRoutes := r.Group("/apis")
	{
		// CRUD
		apiRoutes.POST("", auth.PublisherOrAbove(), s.apiMgr.CreateAPI)
		apiRoutes.GET("", s.apiMgr.ListAPIs)
		apiRoutes.GET("/search", s.apiMgr.SearchAPIs)
		apiRoutes.GET("/:api_id", s.apiMgr.GetAPI)
		apiRoutes.PUT("/:api_id", auth.PublisherOrAbove(), s.apiMgr.UpdateAPI)
		apiRoutes.DELETE("/:api_id", auth.AdminOnly(), s.apiMgr.DeleteAPI)

		// Lifecycle
		apiRoutes.POST("/:api_id/publish", auth.PublisherOrAbove(), s.lifecycle.PublishAPI)
		apiRoutes.POST("/:api_id/block", auth.AdminOnly(), s.lifecycle.BlockAPI)
		apiRoutes.POST("/:api_id/unblock", auth.AdminOnly(), s.lifecycle.UnblockAPI)
		apiRoutes.POST("/:api_id/deprecate", auth.PublisherOrAbove(), s.lifecycle.DeprecateAPI)
		apiRoutes.POST("/:api_id/retire", auth.AdminOnly(), s.lifecycle.RetireAPI)
		apiRoutes.POST("/:api_id/prototype", auth.PublisherOrAbove(), s.lifecycle.PrototypeAPI)
		apiRoutes.GET("/:api_id/lifecycle", s.lifecycle.GetLifecycle)

		// Versions
		apiRoutes.POST("/:api_id/versions", auth.PublisherOrAbove(), s.version.CreateVersion)
		apiRoutes.GET("/:api_id/versions", s.version.ListVersions)
		apiRoutes.GET("/:api_id/versions/default", s.version.GetDefaultVersion)
		apiRoutes.PUT("/:api_id/versions/default", auth.PublisherOrAbove(), s.version.SetDefaultVersion)

		// Resources
		apiRoutes.POST("/:api_id/resources", auth.PublisherOrAbove(), s.resource.CreateResource)
		apiRoutes.GET("/:api_id/resources", s.resource.ListResources)
		apiRoutes.GET("/:api_id/resources/:resource_id", s.resource.GetResource)
		apiRoutes.PUT("/:api_id/resources/:resource_id", auth.PublisherOrAbove(), s.resource.UpdateResource)
		apiRoutes.DELETE("/:api_id/resources/:resource_id", auth.AdminOnly(), s.resource.DeleteResource)

		// Policies
		apiRoutes.POST("/:api_id/policies/:policy_id", auth.PublisherOrAbove(), s.policy.AttachPolicy)
		apiRoutes.DELETE("/:api_id/policies/:policy_id", auth.AdminOnly(), s.policy.DetachPolicy)
		apiRoutes.GET("/:api_id/policies", s.policy.ListAttachedPolicies)

		// Export
		apiRoutes.GET("/:api_id/export", s.handleExportOpenAPI)
	}

	// Standalone policy management
	policyRoutes := r.Group("/policies")
	{
		policyRoutes.POST("", auth.AdminOnly(), s.policy.CreatePolicy)
		policyRoutes.GET("", s.policy.ListPolicies)
		policyRoutes.GET("/:policy_id", s.policy.GetPolicy)
		policyRoutes.PUT("/:policy_id", auth.AdminOnly(), s.policy.UpdatePolicy)
		policyRoutes.DELETE("/:policy_id", auth.AdminOnly(), s.policy.DeletePolicy)
		policyRoutes.POST("/templates", auth.AdminOnly(), s.policy.CreatePolicyFromTemplate)
		policyRoutes.GET("/templates", s.listPolicyTemplates)
	}

	// Audit log routes
	auditAPI := audit.NewAPI(s.store)
	auditAPI.RegisterRoutes(r.Group(""))

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
			fmt.Fprintf(os.Stderr, "publisher server error: %v\n", err)
		}
	}()

	fmt.Printf("Publisher server listening on %s:%s\n", s.config.Host, s.config.Port)

	<-quit
	fmt.Println("Shutting down Publisher server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.httpSrv.Shutdown(ctx)
}

// Router returns the Gin engine for testing.
func (s *Server) Router() *gin.Engine {
	return s.router
}

// --- HTTP Handlers ---

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"service":   "publisher",
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleReady(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
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

// authMiddleware extracts the bearer token and validates it, populating the
// Gin context with user information.
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// Allow anonymous for read endpoints
			if c.Request.Method == "GET" || c.Request.Method == "HEAD" {
				auth.SetContextUser(c, "anonymous", []string{"anonymous"}, s.config.TenantID)
				c.Next()
				return
			}
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
		claims, err := s.validator.ValidateToken(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token: " + err.Error()})
			c.Abort()
			return
		}

		// Derive roles from scope
		roles := []string{"subscriber"}
		for _, sc := range strings.Fields(claims.Scope) {
			if sc == "publish" || sc == "api:admin" {
				roles = append(roles, "publisher")
			}
			if sc == "api:admin" {
				roles = append(roles, "admin")
			}
		}

		userID := claims.Sub
		if userID == "" {
			userID = claims.ClientID
		}
		tenantID := claims.TenantID
		if tenantID == "" {
			tenantID = s.config.TenantID
		}

		auth.SetContextUser(c, userID, roles, tenantID)
		c.Set("token_claims", claims)
		c.Next()
	}
}

// listPolicyTemplates returns built-in policy templates.
func (s *Server) listPolicyTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"templates": GetPolicyTemplates(),
		"total":     len(GetPolicyTemplates()),
	})
}

// handleExportOpenAPI exports an API as an OpenAPI 3.0 JSON specification.
func (s *Server) handleExportOpenAPI(c *gin.Context) {
	apiID := c.Param("api_id")

	// Fetch the API
	api, err := s.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "API not found"})
		return
	}

	// Fetch API resources
	resources, err := s.store.ListResourcesByAPI(apiID)
	if err != nil {
		resources = []models.APIResource{}
	}

	// Build OpenAPI 3.0 spec
	spec := buildOpenAPISpec(api, resources)

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s-openapi.json", api.Context))
	c.JSON(http.StatusOK, spec)
}

// buildOpenAPISpec constructs an OpenAPI 3.0 specification from an API and its resources.
func buildOpenAPISpec(api *models.API, resources []models.APIResource) map[string]interface{} {
	paths := make(map[string]interface{})
	components := map[string]interface{}{
		"securitySchemes": map[string]interface{}{},
	}

	// Security scheme based on auth type
	switch api.AuthType {
	case "oauth2":
		components["securitySchemes"] = map[string]interface{}{
			"OAuth2": map[string]interface{}{
				"type": "oauth2",
				"flows": map[string]interface{}{
					"clientCredentials": map[string]interface{}{
						"tokenUrl": fmt.Sprintf("%s/oauth2/token", api.Endpoint),
						"scopes":   map[string]string{},
					},
					"authorizationCode": map[string]interface{}{
						"authorizationUrl": fmt.Sprintf("%s/oauth2/authorize", api.Endpoint),
						"tokenUrl":         fmt.Sprintf("%s/oauth2/token", api.Endpoint),
						"scopes":           map[string]string{},
					},
				},
			},
		}
	case "apikey":
		components["securitySchemes"] = map[string]interface{}{
			"ApiKeyAuth": map[string]interface{}{
				"type": "apiKey",
				"in":   "header",
				"name": "X-API-Key",
			},
		}
	case "basic":
		components["securitySchemes"] = map[string]interface{}{
			"BasicAuth": map[string]interface{}{
				"type":   "http",
				"scheme": "basic",
			},
		}
	}

	// Build paths from resources
	for _, res := range resources {
		if _, ok := paths[res.Path]; !ok {
			paths[res.Path] = map[string]interface{}{}
		}
		pathItem := paths[res.Path].(map[string]interface{})

		op := map[string]interface{}{
			"operationId": fmt.Sprintf("%s_%s", strings.ToLower(res.Method), strings.ReplaceAll(res.Path, "/", "_")),
			"description": res.Description,
			"responses": map[string]interface{}{
				"200": map[string]interface{}{
					"description": "Successful response",
				},
				"401": map[string]interface{}{
					"description": "Unauthorized",
				},
				"403": map[string]interface{}{
					"description": "Forbidden",
				},
				"500": map[string]interface{}{
					"description": "Internal server error",
				},
			},
		}

		if res.AuthRequired {
			op["security"] = []map[string][]string{
				{strings.Title(api.AuthType) + "Auth": {}},
			}
		}

		method := strings.ToLower(res.Method)
		switch method {
		case "get", "post", "put", "patch", "delete", "head", "options":
			pathItem[method] = op
		}
	}

	spec := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":       api.Name,
			"description":  api.Description,
			"version":     api.Version,
			"contact": map[string]interface{}{
				"name": api.CreatedBy,
			},
		},
		"servers": []map[string]interface{}{
			{
				"url":         api.Endpoint,
				"description": api.Name + " server",
			},
		},
		"paths":      paths,
		"components": components,
	}

	// Tags
	if len(api.Tags) > 0 {
		tags := make([]map[string]interface{}, 0, len(api.Tags))
		for _, tag := range api.Tags {
			tags = append(tags, map[string]interface{}{"name": tag})
		}
		spec["tags"] = tags
	}

	return spec
}
