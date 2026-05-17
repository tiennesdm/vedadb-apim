// Package docs provides HTTP handlers for serving OpenAPI documentation
// and Swagger UI for APIs managed by the VedaDB API Manager.
package docs

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler provides HTTP handlers for API documentation.
type Handler struct {
	store APIStore
}

// NewHandler creates a new documentation handler.
func NewHandler(store APIStore) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes registers all documentation routes with the given router.
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	// OpenAPI spec endpoints
	router.GET("/apis/:id/swagger.json", h.GetOpenAPIJSON)
	router.GET("/apis/:id/openapi.json", h.GetOpenAPIJSON)
	router.GET("/apis/:id/swagger.yaml", h.GetOpenAPIYAML)
	router.GET("/apis/:id/openapi.yaml", h.GetOpenAPIYAML)

	// Swagger UI
	router.GET("/docs", h.GetSwaggerUI)
	router.GET("/docs/apis", h.GetAPIDocumentationListing)
	router.GET("/docs/:id", h.GetSwaggerUIForAPI)

	// Legacy Swagger endpoints
	router.GET("/apis/:id/swagger", h.GetSwaggerUIForAPI)
	router.GET("/apis/:id/openapi", h.GetSwaggerUIForAPI)
}

// GetOpenAPIJSON handles GET /apis/:id/swagger.json and /apis/:id/openapi.json.
// Returns the OpenAPI 3.0 specification as JSON.
func (h *Handler) GetOpenAPIJSON(c *gin.Context) {
	apiID := c.Param("id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "missing_api_id",
			"message": "API ID is required",
		})
		return
	}

	api, err := h.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "api_not_found",
			"message": fmt.Sprintf("API with ID '%s' not found", apiID),
		})
		return
	}

	specJSON, err := GenerateOpenAPISpecJSON(api)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "spec_generation_failed",
			"message": "Failed to generate OpenAPI spec: " + err.Error(),
		})
		return
	}

	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cache-Control", "public, max-age=300")
	c.String(http.StatusOK, string(specJSON))
}

// GetOpenAPIYAML handles GET /apis/:id/swagger.yaml and /apis/:id/openapi.yaml.
// Returns the OpenAPI 3.0 specification as YAML.
func (h *Handler) GetOpenAPIYAML(c *gin.Context) {
	apiID := c.Param("id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "missing_api_id",
			"message": "API ID is required",
		})
		return
	}

	api, err := h.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "api_not_found",
			"message": fmt.Sprintf("API with ID '%s' not found", apiID),
		})
		return
	}

	specYAML, err := GenerateOpenAPISpecYAML(api)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "spec_generation_failed",
			"message": "Failed to generate OpenAPI spec: " + err.Error(),
		})
		return
	}

	c.Header("Content-Type", "application/yaml; charset=utf-8")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cache-Control", "public, max-age=300")
	c.String(http.StatusOK, string(specYAML))
}

// GetSwaggerUI handles GET /docs.
// Serves the Swagger UI with a list of all available APIs.
func (h *Handler) GetSwaggerUI(c *gin.Context) {
	apis, err := h.store.ListAPIs()
	if err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error listing APIs: %v", err))
		return
	}

	if len(apis) == 0 {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, noAPIsHTML)
		return
	}

	// Build the API selector
	var options strings.Builder
	for _, api := range apis {
		options.WriteString(fmt.Sprintf(
			`<option value="/apis/%s/swagger.json">%s (v%s)</option>`,
			api.ID, api.Name, api.Version,
		))
	}

	html := fmt.Sprintf(swaggerUIWithSelectorHTML, options.String())
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, html)
}

// GetAPIDocumentationListing handles GET /docs/apis.
// Returns a JSON listing of all APIs with their documentation URLs.
func (h *Handler) GetAPIDocumentationListing(c *gin.Context) {
	apis, err := h.store.ListAPIs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "list_failed",
			"message": "Failed to list APIs: " + err.Error(),
		})
		return
	}

	type APIDocInfo struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Version         string `json:"version"`
		Context         string `json:"context"`
		Description     string `json:"description"`
		Status          string `json:"status"`
		SwaggerJSON     string `json:"swagger_json"`
		SwaggerYAML     string `json:"swagger_yaml"`
		SwaggerUI       string `json:"swagger_ui"`
	}

	var docInfo []APIDocInfo
	for _, api := range apis {
		docInfo = append(docInfo, APIDocInfo{
			ID:          api.ID,
			Name:        api.Name,
			Version:     api.Version,
			Context:     api.Context,
			Description: api.Description,
			Status:      api.Status,
			SwaggerJSON: fmt.Sprintf("/apis/%s/swagger.json", api.ID),
			SwaggerYAML: fmt.Sprintf("/apis/%s/swagger.yaml", api.ID),
			SwaggerUI:   fmt.Sprintf("/docs/%s", api.ID),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"count": len(docInfo),
		"apis":  docInfo,
	})
}

// GetSwaggerUIForAPI handles GET /docs/:id, /apis/:id/swagger, and /apis/:id/openapi.
// Serves Swagger UI for a specific API.
func (h *Handler) GetSwaggerUIForAPI(c *gin.Context) {
	apiID := c.Param("id")
	if apiID == "" {
		c.String(http.StatusBadRequest, "API ID is required")
		return
	}

	api, err := h.store.GetAPI(apiID)
	if err != nil {
		c.String(http.StatusNotFound, fmt.Sprintf("API '%s' not found", apiID))
		return
	}

	specURL := fmt.Sprintf("/apis/%s/swagger.json", apiID)
	html := fmt.Sprintf(dedicatedSwaggerUIHTML, api.Name, api.Version, specURL)

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, html)
}

// --- HTML Templates ---

const noAPIsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>VedaDB API Manager - Documentation</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; margin: 40px; background: #f5f5f5; }
        .container { max-width: 800px; margin: 0 auto; background: white; padding: 40px; border-radius: 8px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        h1 { color: #333; }
        p { color: #666; }
    </style>
</head>
<body>
    <div class="container">
        <h1>VedaDB API Manager</h1>
        <p>No APIs are currently registered in the platform.</p>
        <p>Once APIs are created and published, their documentation will appear here.</p>
    </div>
</body>
</html>`

const swaggerUIWithSelectorHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>VedaDB API Manager - API Documentation</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui.css">
    <style>
        body { margin: 0; padding: 0; }
        .topbar { display: none; }
        .api-selector { padding: 15px 20px; background: #1a1a1a; border-bottom: 1px solid #333; }
        .api-selector label { color: #fff; font-family: sans-serif; font-size: 14px; margin-right: 10px; }
        .api-selector select { padding: 8px 12px; border-radius: 4px; border: 1px solid #555; background: #2a2a2a; color: #fff; font-size: 14px; min-width: 300px; }
    </style>
</head>
<body>
    <div class="api-selector">
        <label for="api-select">Select API:</label>
        <select id="api-select" onchange="loadSpec()">
            <option value="">-- Choose an API --</option>
            %s
        </select>
    </div>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-standalone-preset.js"></script>
    <script>
        let ui;
        function initUI(url) {
            ui = SwaggerUIBundle({
                url: url,
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
                plugins: [SwaggerUIBundle.plugins.DownloadUrl],
                layout: 'StandaloneLayout'
            });
        }
        function loadSpec() {
            const select = document.getElementById('api-select');
            const url = select.value;
            if (url) { initUI(url); }
        }
    </script>
</body>
</html>`

const dedicatedSwaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>%%s v%%s - API Documentation</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui.css">
    <style>
        body { margin: 0; padding: 0; }
        .topbar { display: none; }
        .api-header { padding: 15px 20px; background: #1a1a1a; color: #fff; font-family: sans-serif; font-size: 14px; border-bottom: 1px solid #333; }
        .api-header a { color: #4990e2; text-decoration: none; }
        .api-header a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="api-header">
        <strong>%%s</strong> v%%s &mdash;
        <a href="/docs">All APIs</a> |
        <a href="%%s" target="_blank">OpenAPI JSON</a> |
        <a href="%%s" target="_blank" download>Download JSON</a>
    </div>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            SwaggerUIBundle({
                url: '%%s',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
                plugins: [SwaggerUIBundle.plugins.DownloadUrl],
                layout: 'StandaloneLayout'
            });
        };
    </script>
</body>
</html>`

// Middleware for API documentation routes.
// Adds CORS headers and caching for documentation endpoints.
func DocMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to doc routes
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/docs") || strings.HasPrefix(path, "/apis/") {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

// RegisterSwaggerRoutes is a convenience function that registers all
// documentation routes with the given router and API store.
func RegisterSwaggerRoutes(router *gin.Engine, store APIStore) {
	handler := NewHandler(store)
	handler.RegisterRoutes(router)
}

// GenerateAndServeSpec generates an OpenAPI spec and serves it directly.
// Useful for development and testing.
func GenerateAndServeSpec(apiID string, store APIStore, format SpecFormat) (string, error) {
	spec, err := GenerateAPISpecByID(apiID, store, format)
	if err != nil {
		return "", err
	}
	return string(spec), nil
}

// SpecServer is an HTTP server that serves OpenAPI specs.
type SpecServer struct {
	handler *Handler
	router  *gin.Engine
}

// NewSpecServer creates a new spec server.
func NewSpecServer(store APIStore) *SpecServer {
	router := gin.New()
	handler := NewHandler(store)
	handler.RegisterRoutes(router)
	return &SpecServer{handler: handler, router: router}
}

// ServeHTTP implements the http.Handler interface.
func (s *SpecServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// Router returns the underlying gin router for additional customization.
func (s *SpecServer) Router() *gin.Engine {
	return s.router
}
