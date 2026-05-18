// Package publisher provides an API template library for the VedaDB API Manager.
package publisher

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
	"github.com/tiennesdm/vedadb-apim/pkg/store"
)

// ---------------------------------------------------------------------------
// Template definitions
// ---------------------------------------------------------------------------

// apiTemplate defines the skeleton for a pre-built API pattern.
type apiTemplate struct {
	Name        string
	Description string
	AuthType    string
	Resources   []templateResource
}

// templateResource is a simplified resource definition used in templates.
type templateResource struct {
	Method       string
	Path         string
	Description  string
	AuthRequired bool
}

var apiTemplates = map[string]*apiTemplate{
	"rest-crud": {
		Name:        "REST CRUD API",
		Description: "Standard REST CRUD operations with list, get, create, update, and delete endpoints.",
		AuthType:    "oauth2",
		Resources: []templateResource{
			{Method: "GET", Path: "/", Description: "List all items"},
			{Method: "GET", Path: "/{id}", Description: "Get a single item by ID"},
			{Method: "POST", Path: "/", Description: "Create a new item"},
			{Method: "PUT", Path: "/{id}", Description: "Update an existing item"},
			{Method: "DELETE", Path: "/{id}", Description: "Delete an item"},
		},
	},
	"graphql": {
		Name:        "GraphQL API",
		Description: "GraphQL single-endpoint API with query and mutation support.",
		AuthType:    "oauth2",
		Resources: []templateResource{
			{Method: "POST", Path: "/graphql", Description: "GraphQL query and mutation endpoint"},
			{Method: "GET", Path: "/graphql", Description: "GraphQL introspection and GET queries"},
		},
	},
	"webhook": {
		Name:        "Webhook API",
		Description: "Webhook receiver that ingests event payloads and forwards them to subscribers.",
		AuthType:    "apikey",
		Resources: []templateResource{
			{Method: "POST", Path: "/webhook", Description: "Receive webhook events"},
			{Method: "GET", Path: "/webhook/health", Description: "Webhook health check (public)", AuthRequired: false},
		},
	},
	"websocket": {
		Name:        "WebSocket API",
		Description: "WebSocket upgrade endpoint for real-time bidirectional communication.",
		AuthType:    "oauth2",
		Resources: []templateResource{
			{Method: "GET", Path: "/ws", Description: "WebSocket upgrade endpoint"},
			{Method: "GET", Path: "/ws/health", Description: "Connection health check", AuthRequired: false},
		},
	},
	"file-upload": {
		Name:        "File Upload API",
		Description: "Multipart file upload with status and download endpoints.",
		AuthType:    "apikey",
		Resources: []templateResource{
			{Method: "POST", Path: "/upload", Description: "Upload a file"},
			{Method: "GET", Path: "/upload/{id}/status", Description: "Check upload status"},
			{Method: "GET", Path: "/files/{id}", Description: "Download a file"},
			{Method: "DELETE", Path: "/files/{id}", Description: "Delete a file"},
		},
	},
	"oauth-proxy": {
		Name:        "OAuth2 Proxy API",
		Description: "Proxy API that validates OAuth2 tokens before forwarding to a backend.",
		AuthType:    "oauth2",
		Resources: []templateResource{
			{Method: "GET", Path: "/proxy", Description: "Proxied GET request"},
			{Method: "POST", Path: "/proxy", Description: "Proxied POST request"},
			{Method: "PUT", Path: "/proxy", Description: "Proxied PUT request"},
			{Method: "DELETE", Path: "/proxy", Description: "Proxied DELETE request"},
		},
	},
}

// ---------------------------------------------------------------------------
// Template engine
// ---------------------------------------------------------------------------

// TemplateEngine creates APIs from built-in templates.
type TemplateEngine struct {
	store store.Store
}

// NewTemplateEngine creates a new TemplateEngine.
func NewTemplateEngine(store store.Store) *TemplateEngine {
	return &TemplateEngine{store: store}
}

// CreateFromTemplate instantiates a new API from a named template.
// It persists the API and all template resources to the store.
func (te *TemplateEngine) CreateFromTemplate(ctx context.Context, tenantID, templateName, name, endpoint string) (*models.APIDB, []*models.APIResourceDB, error) {
	if tenantID == "" {
		return nil, nil, fmt.Errorf("tenant_id is required")
	}
	if name == "" {
		return nil, nil, fmt.Errorf("name is required")
	}
	if endpoint == "" {
		return nil, nil, fmt.Errorf("endpoint is required")
	}

	tmpl, ok := apiTemplates[templateName]
	if !ok {
		return nil, nil, fmt.Errorf("template not found: %s (available: %v)", templateName, ListTemplateNames())
	}

	api := &models.APIDB{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        name,
		Description: tmpl.Description,
		Context:     "/api/" + slugify(name),
		Version:     "1.0.0",
		Endpoint:    endpoint,
		AuthType:    tmpl.AuthType,
		Status:      string(models.StatusCreated),
		Provider:    "template:" + templateName,
		Tags:        "",
	}

	if err := te.store.CreateAPI(api); err != nil {
		return nil, nil, fmt.Errorf("create API from template: %w", err)
	}

	var resources []*models.APIResourceDB
	for _, r := range tmpl.Resources {
		authRequired := true
		if !r.AuthRequired {
			authRequired = false
		}

		resource := &models.APIResourceDB{
			ID:           uuid.New().String(),
			APIID:        api.ID,
			Method:       r.Method,
			Path:         r.Path,
			Description:  r.Description,
			AuthRequired: authRequired,
		}
		if err := te.store.CreateResource(resource); err != nil {
			return api, resources, fmt.Errorf("create resource %s %s: %w", r.Method, r.Path, err)
		}
		resources = append(resources, resource)
	}

	return api, resources, nil
}

// ListTemplates returns a slice of human-readable template metadata.
func ListTemplates() []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(apiTemplates))
	for name, tmpl := range apiTemplates {
		result = append(result, map[string]interface{}{
			"name":        name,
			"title":       tmpl.Name,
			"description": tmpl.Description,
			"auth_type":   tmpl.AuthType,
			"resources":   len(tmpl.Resources),
		})
	}
	return result
}

// ListTemplateNames returns the names of all available templates.
func ListTemplateNames() []string {
	names := make([]string, 0, len(apiTemplates))
	for name := range apiTemplates {
		names = append(names, name)
	}
	return names
}

// GetTemplate returns a single template definition by name, or nil if not found.
func GetTemplate(name string) *apiTemplate {
	return apiTemplates[name]
}

// IsValidTemplate reports whether name is a known template.
func IsValidTemplate(name string) bool {
	_, ok := apiTemplates[name]
	return ok
}

// TemplateCount returns the number of built-in templates.
func TemplateCount() int {
	return len(apiTemplates)
}
