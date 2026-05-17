// Package docs provides OpenAPI 3.0 specification generation and
// documentation serving capabilities for the VedaDB API Manager.
package docs

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// APIResource represents a single resource/endpoint in an API definition.
type APIResource struct {
	Path       string                 `json:"path"`
	Method     string                 `json:"method"`
	Produces   []string               `json:"produces"`
	Consumes   []string               `json:"consumes"`
	AuthType   string                 `json:"auth_type"`
	Throttling string                 `json:"throttling"`
	Parameters []APIParameter         `json:"parameters"`
	Responses  map[string]APIResponse `json:"responses"`
	Summary    string                 `json:"summary,omitempty"`
	OperationID string                `json:"operation_id,omitempty"`
	Tags       []string               `json:"tags,omitempty"`
}

// APIParameter represents a parameter in an API resource.
type APIParameter struct {
	Name        string                 `json:"name"`
	In          string                 `json:"in"`
	Description string                 `json:"description"`
	Required    bool                   `json:"required"`
	Type        string                 `json:"type"`
	Default     string                 `json:"default,omitempty"`
	Schema      map[string]interface{} `json:"schema,omitempty"`
}

// APIResponse represents a response definition.
type APIResponse struct {
	Description string                 `json:"description"`
	Headers     map[string]string      `json:"headers,omitempty"`
	Schema      map[string]interface{} `json:"schema,omitempty"`
}

// APIModel represents the API entity used for spec generation.
type APIModel struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Context      string          `json:"context"`
	Description  string          `json:"description"`
	Provider     string          `json:"provider"`
	Status       string          `json:"status"`
	Visibility   string         `json:"visibility"`
	Tier         string          `json:"tier"`
	EndpointURL  string          `json:"endpoint_url"`
	SandboxURL   string          `json:"sandbox_url"`
	Transport    []string        `json:"transport"`
	Tags         []string        `json:"tags"`
	Resources    []APIResource   `json:"resources"`
	BusinessInfo *BusinessInfo   `json:"business_info,omitempty"`
}

// BusinessInfo contains business metadata for an API.
type BusinessInfo struct {
	Owner      string `json:"owner"`
	OwnerEmail string `json:"owner_email"`
	Department string `json:"department"`
}

// GenerateOpenAPISpec generates an OpenAPI 3.0 specification from an API model.
func GenerateOpenAPISpec(api *APIModel) (*openapi3.T, error) {
	if api == nil {
		return nil, fmt.Errorf("api model is nil")
	}

	spec := &openapi3.T{
		OpenAPI: "3.0.3",
		Info: &openapi3.Info{
			Title:       api.Name,
			Description: api.Description,
			Version:     api.Version,
			Contact: &openapi3.Contact{
				Name: api.Provider,
			},
		},
		Servers: openapi3.Servers{
			{URL: api.EndpointURL, Description: "Production server"},
			{URL: api.SandboxURL, Description: "Sandbox server"},
		},
		Paths:   make(openapi3.Paths),
		Tags:    buildTags(api),
		Components: &openapi3.Components{
			SecuritySchemes: buildSecuritySchemes(),
			Schemas:         make(openapi3.Schemas),
		},
	}

	if api.BusinessInfo != nil {
		spec.Info.Contact = &openapi3.Contact{
			Name:  api.BusinessInfo.Owner,
			Email: api.BusinessInfo.OwnerEmail,
		}
	}

	// Add default schemas
	addDefaultSchemas(spec.Components.Schemas)

	// Build paths from resources
	for _, resource := range api.Resources {
		pathItem := spec.Paths[resource.Path]
		if pathItem == nil {
			pathItem = &openapi3.PathItem{}
			spec.Paths[resource.Path] = pathItem
		}

		operation := buildOperation(api, &resource, spec)

		switch strings.ToUpper(resource.Method) {
		case "GET":
			pathItem.Get = operation
		case "POST":
			pathItem.Post = operation
		case "PUT":
			pathItem.Put = operation
		case "PATCH":
			pathItem.Patch = operation
		case "DELETE":
			pathItem.Delete = operation
		case "HEAD":
			pathItem.Head = operation
		case "OPTIONS":
			pathItem.Options = operation
		default:
			pathItem.Get = operation
		}
	}

	// Apply security globally
	defaultSec := map[string][]string{"BearerAuth": {}, "ApiKeyAuth": {}}
	spec.Security = &openapi3.SecurityRequirements{defaultSec}

	return spec, nil
}

// GenerateOpenAPISpecJSON returns the OpenAPI spec as JSON bytes.
func GenerateOpenAPISpecJSON(api *APIModel) ([]byte, error) {
	spec, err := GenerateOpenAPISpec(api)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(spec, "", "  ")
}

// GenerateOpenAPISpecYAML returns the OpenAPI spec as YAML-like JSON representation.
func GenerateOpenAPISpecYAML(api *APIModel) ([]byte, error) {
	// Returns JSON for now - YAML conversion can be added with goccy/go-yaml
	return GenerateOpenAPISpecJSON(api)
}

// ServeSwaggerUI serves the Swagger UI HTML page.
func ServeSwaggerUI(router *gin.Engine) {
	router.GET("/docs", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(200, swaggerUIHTML)
	})
}

// ServeOpenAPIJSON serves the OpenAPI JSON spec.
func ServeOpenAPIJSON(router *gin.Engine, apiStore APIStore) {
	router.GET("/apis/:id/swagger.json", func(c *gin.Context) {
		apiID := c.Param("id")
		api, err := apiStore.GetAPI(apiID)
		if err != nil {
			c.JSON(404, gin.H{"error": "API not found"})
			return
		}

		specJSON, err := GenerateOpenAPISpecJSON(api)
		if err != nil {
			c.JSON(500, gin.H{"error": "Failed to generate spec: " + err.Error()})
			return
		}

		c.Header("Content-Type", "application/json")
		c.String(200, string(specJSON))
	})
}

// ServeOpenAPIYAML serves the OpenAPI spec as YAML.
func ServeOpenAPIYAML(router *gin.Engine, apiStore APIStore) {
	router.GET("/apis/:id/swagger.yaml", func(c *gin.Context) {
		apiID := c.Param("id")
		api, err := apiStore.GetAPI(apiID)
		if err != nil {
			c.JSON(404, gin.H{"error": "API not found"})
			return
		}

		specYAML, err := GenerateOpenAPISpecYAML(api)
		if err != nil {
			c.JSON(500, gin.H{"error": "Failed to generate spec: " + err.Error()})
			return
		}

		c.Header("Content-Type", "application/yaml")
		c.String(200, string(specYAML))
	})
}

// APIStore provides access to API definitions.
type APIStore interface {
	GetAPI(apiID string) (*APIModel, error)
	ListAPIs() ([]*APIModel, error)
}

// --- Build helpers ---

func buildOperation(api *APIModel, resource *APIResource, spec *openapi3.T) *openapi3.Operation {
	op := &openapi3.Operation{
		Summary:     resource.Summary,
		Description: buildDescription(resource),
		OperationID: resource.OperationID,
		Tags:        resource.Tags,
		Responses:   &openapi3.Responses{},
		Parameters:  buildParameters(resource),
		Security:    &openapi3.SecurityRequirements{},
	}

	if op.OperationID == "" {
		op.OperationID = generateOperationID(resource.Method, resource.Path)
	}

	if op.Tags == nil {
		op.Tags = []string{api.Name}
	}

	// Add request body for POST/PUT/PATCH
	if resource.Method == "POST" || resource.Method == "PUT" || resource.Method == "PATCH" {
		op.RequestBody = buildRequestBody(resource)
	}

	// Build responses
	for code, resp := range resource.Responses {
		op.Responses.Set(code, &openapi3.ResponseRef{
			Value: &openapi3.Response{
				Description: &resp.Description,
				Headers:     buildResponseHeaders(resp.Headers),
				Content:     buildContentFromSchema(resp.Schema, resource.Produces),
			},
		})
	}

	// Ensure at least a default response
	if op.Responses.Len() == 0 {
		desc := "Success"
		op.Responses.Set("200", &openapi3.ResponseRef{
			Value: &openapi3.Response{
				Description: &desc,
				Content:     openapi3.NewContentWithJSONSchema(&openapi3.Schema{Type: &openapi3.Types{"object"}}),
			},
		})
	}

	// Add auth security
	if resource.AuthType != "NONE" {
		op.Security = &openapi3.SecurityRequirements{
			{"BearerAuth": {}},
			{"ApiKeyAuth": {}},
		}
	}

	return op
}

func buildParameters(resource *APIResource) openapi3.Parameters {
	var params openapi3.Parameters
	for _, p := range resource.Parameters {
		param := &openapi3.ParameterRef{
			Value: &openapi3.Parameter{
				Name:        p.Name,
				In:          p.In,
				Description: p.Description,
				Required:    p.Required,
				Schema:      buildSchemaFromType(p.Type),
			},
		}
		params = append(params, param)
	}
	return params
}

func buildSchemaFromType(t string) *openapi3.SchemaRef {
	schema := &openapi3.Schema{}
	switch t {
	case "string":
		schema.Type = &openapi3.Types{"string"}
	case "integer", "int":
		schema.Type = &openapi3.Types{"integer"}
		schema.Format = "int64"
	case "number", "float":
		schema.Type = &openapi3.Types{"number"}
		schema.Format = "float"
	case "boolean":
		schema.Type = &openapi3.Types{"boolean"}
	case "array":
		schema.Type = &openapi3.Types{"array"}
		schema.Items = &openapi3.SchemaRef{
			Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
		}
	case "object":
		schema.Type = &openapi3.Types{"object"}
	default:
		schema.Type = &openapi3.Types{"string"}
	}
	return &openapi3.SchemaRef{Value: schema}
}

func buildRequestBody(resource *APIResource) *openapi3.RequestBodyRef {
	schema := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: make(openapi3.Schemas),
	}

	for _, p := range resource.Parameters {
		if p.In == "body" || p.In == "formData" {
			schema.Properties[p.Name] = buildSchemaFromType(p.Type)
			if p.Required {
				schema.Required = append(schema.Required, p.Name)
			}
		}
	}

	content := openapi3.Content{}
	produces := resource.Consumes
	if len(produces) == 0 {
		produces = []string{"application/json"}
	}
	for _, ct := range produces {
		content[ct] = &openapi3.MediaType{
			Schema: &openapi3.SchemaRef{Value: schema},
		}
	}

	return &openapi3.RequestBodyRef{
		Value: &openapi3.RequestBody{
			Content: content,
			Required: true,
		},
	}
}

func buildResponseHeaders(headers map[string]string) openapi3.Headers {
	if len(headers) == 0 {
		return nil
	}
	h := make(openapi3.Headers)
	for name, desc := range headers {
		h[name] = &openapi3.HeaderRef{
			Value: &openapi3.Header{
				Parameter: openapi3.Parameter{
					Description: desc,
					Schema: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
					},
				},
			},
		}
	}
	return h
}

func buildContentFromSchema(schemaMap map[string]interface{}, produces []string) openapi3.Content {
	if len(produces) == 0 {
		produces = []string{"application/json"}
	}

	content := openapi3.Content{}
	for _, ct := range produces {
		var schema *openapi3.SchemaRef
		if schemaMap != nil {
			schema = mapToSchema(schemaMap)
		} else {
			schema = &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"object"}},
			}
		}
		content[ct] = &openapi3.MediaType{
			Schema: schema,
		}
	}
	return content
}

func mapToSchema(m map[string]interface{}) *openapi3.SchemaRef {
	schema := &openapi3.Schema{
		Properties: make(openapi3.Schemas),
	}

	if t, ok := m["type"].(string); ok {
		switch t {
		case "object":
			schema.Type = &openapi3.Types{"object"}
			if props, ok := m["properties"].(map[string]interface{}); ok {
				for k, v := range props {
					if vm, ok := v.(map[string]interface{}); ok {
						schema.Properties[k] = mapToSchema(vm)
					}
				}
			}
		case "array":
			schema.Type = &openapi3.Types{"array"}
			if items, ok := m["items"].(map[string]interface{}); ok {
				schema.Items = mapToSchema(items)
			}
		case "string":
			schema.Type = &openapi3.Types{"string"}
			if format, ok := m["format"].(string); ok {
				schema.Format = format
			}
		case "integer":
			schema.Type = &openapi3.Types{"integer"}
		case "number":
			schema.Type = &openapi3.Types{"number"}
		case "boolean":
			schema.Type = &openapi3.Types{"boolean"}
		default:
			schema.Type = &openapi3.Types{"string"}
		}
	} else {
		schema.Type = &openapi3.Types{"object"}
	}

	if ref, ok := m["$ref"].(string); ok {
		return &openapi3.SchemaRef{Ref: ref}
	}

	return &openapi3.SchemaRef{Value: schema}
}

func buildTags(api *APIModel) openapi3.Tags {
	var tags openapi3.Tags
	seen := make(map[string]bool)

	// Add API name as tag
	if !seen[api.Name] {
		tags = append(tags, &openapi3.Tag{
			Name:        api.Name,
			Description: api.Description,
		})
		seen[api.Name] = true
	}

	// Add resource-level tags
	for _, r := range api.Resources {
		for _, t := range r.Tags {
			if !seen[t] {
				tags = append(tags, &openapi3.Tag{Name: t})
				seen[t] = true
			}
		}
	}

	return tags
}

func buildSecuritySchemes() openapi3.SecuritySchemes {
	return openapi3.SecuritySchemes{
		"BearerAuth": &openapi3.SecuritySchemeRef{
			Value: &openapi3.SecurityScheme{
				Type:         "http",
				Scheme:       "bearer",
				BearerFormat: "JWT",
				Description:  "JWT Bearer token authentication",
			},
		},
		"ApiKeyAuth": &openapi3.SecuritySchemeRef{
			Value: &openapi3.SecurityScheme{
				Type:        "apiKey",
				In:          "header",
				Name:        "X-API-Key",
				Description: "API Key authentication via X-API-Key header",
			},
		},
		"OAuth2Auth": &openapi3.SecuritySchemeRef{
			Value: &openapi3.SecurityScheme{
				Type: "oauth2",
				Flows: &openapi3.OAuthFlows{
					ClientCredentials: &openapi3.OAuthFlow{
						TokenURL: "/oauth2/token",
						Scopes: map[string]string{
							"read":  "Read access",
							"write": "Write access",
						},
					},
				},
				Description: "OAuth 2.0 client credentials flow",
			},
		},
	}
}

func addDefaultSchemas(schemas openapi3.Schemas) {
	schemas["Error"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			Properties: openapi3.Schemas{
				"code": &openapi3.SchemaRef{
					Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
				},
				"message": &openapi3.SchemaRef{
					Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
				"description": &openapi3.SchemaRef{
					Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			Required: []string{"code", "message"},
		},
	}

	schemas["APIInfo"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			Properties: openapi3.Schemas{
				"id":        &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				"name":      &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				"version":   &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				"context":   &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				"status":    &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			},
		},
	}
}

func buildDescription(resource *APIResource) string {
	parts := []string{resource.Method + " " + resource.Path}
	if resource.AuthType != "" {
		parts = append(parts, "\n\nAuth: "+resource.AuthType)
	}
	if resource.Throttling != "" {
		parts = append(parts, "\nThrottling: "+resource.Throttling)
	}
	return strings.Join(parts, "")
}

func generateOperationID(method, path string) string {
	parts := []string{strings.ToLower(method)}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for _, seg := range segments {
		seg = strings.Trim(seg, "{}")
		seg = strings.ReplaceAll(seg, "_", "")
		seg = strings.ReplaceAll(seg, "-", "")
		if seg != "" {
			parts = append(parts, capitalize(seg))
		}
	}
	return strings.Join(parts, "")
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ValidateOpenAPISpec validates an OpenAPI 3.0 specification.
func ValidateOpenAPISpec(spec *openapi3.T) error {
	if spec.OpenAPI == "" {
		return fmt.Errorf("openapi version is required")
	}
	if spec.Info == nil || spec.Info.Title == "" {
		return fmt.Errorf("info.title is required")
	}
	if spec.Info.Version == "" {
		return fmt.Errorf("info.version is required")
	}
	if len(spec.Paths) == 0 {
		return fmt.Errorf("at least one path is required")
	}
	return nil
}

// GenerateSwaggerUIHTML generates the Swagger UI HTML for a given API.
func GenerateSwaggerUIHTML(apiID string) string {
	specURL := fmt.Sprintf("/apis/%s/swagger.json", apiID)
	return fmt.Sprintf(swaggerUIPageTemplate, specURL)
}

var swaggerUIPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>%%s - API Documentation</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui.css">
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            SwaggerUIBundle({
                url: '%%s',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: 'StandaloneLayout'
            });
        };
    </script>
</body>
</html>`

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>VedaDB API Manager - API Documentation</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui.css">
    <style>
        body { margin: 0; padding: 0; }
        .topbar { display: none; }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.10.0/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            const ui = SwaggerUIBundle({
                urls: [],
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: 'StandaloneLayout'
            });
        };
    </script>
</body>
</html>`

// generateUUID creates a random UUID string.
func generateUUID() string {
	return uuid.New().String()
}

// Extension types for OpenAPI generation.
const (
	ContentTypeJSON    = "application/json"
	ContentTypeXML     = "application/xml"
	ContentTypeForm    = "application/x-www-form-urlencoded"
	ContentTypeMultipart = "multipart/form-data"
)

// SpecFormat represents the output format for OpenAPI specs.
type SpecFormat string

const (
	FormatJSON SpecFormat = "json"
	FormatYAML SpecFormat = "yaml"
)

// GenerateAPISpecByID generates an OpenAPI spec for an API by its ID.
func GenerateAPISpecByID(apiID string, store APIStore, format SpecFormat) ([]byte, error) {
	api, err := store.GetAPI(apiID)
	if err != nil {
		return nil, fmt.Errorf("failed to get API %s: %w", apiID, err)
	}

	switch format {
	case FormatYAML:
		return GenerateOpenAPISpecYAML(api)
	default:
		return GenerateOpenAPISpecJSON(api)
	}
}

// GlobalSpecStore is the global API store used by the documentation handlers.
var GlobalSpecStore APIStore

// SetAPIStore sets the global API store.
func SetAPIStore(store APIStore) {
	GlobalSpecStore = store
}

// ServeSwaggerUIWithStore serves Swagger UI using a specific store.
func ServeSwaggerUIWithStore(router *gin.Engine, store APIStore) {
	SetAPIStore(store)
	ServeSwaggerUI(router)
	ServeOpenAPIJSON(router, store)
	ServeOpenAPIYAML(router, store)
}

// Time-related helpers for spec generation.
func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
