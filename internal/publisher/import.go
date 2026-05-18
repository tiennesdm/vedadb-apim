// Package publisher provides API import from OpenAPI / Swagger specifications
// for the VedaDB API Manager.
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
	"github.com/tiennesdm/vedadb-apim/pkg/store"
)

// ---------------------------------------------------------------------------
// OpenAPI 3.0 minimal spec structs
// ---------------------------------------------------------------------------

// openAPI3Spec is a minimal representation of an OpenAPI 3.0.x document
// sufficient for importing API metadata and resources.
type openAPI3Spec struct {
	OpenAPI string                 `json:"openapi"`
	Info    openAPIInfo            `json:"info"`
	Servers []openAPIServer        `json:"servers"`
	Paths   map[string]openAPIPath `json:"paths"`
}

type openAPIInfo struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

type openAPIServer struct {
	URL string `json:"url"`
}

// openAPIPath represents the operations available on a single path.
type openAPIPath struct {
	Get        *openAPIOperation `json:"get,omitempty"`
	Post       *openAPIOperation `json:"post,omitempty"`
	Put        *openAPIOperation `json:"put,omitempty"`
	Delete     *openAPIOperation `json:"delete,omitempty"`
	Patch      *openAPIOperation `json:"patch,omitempty"`
	Head       *openAPIOperation `json:"head,omitempty"`
	Options    *openAPIOperation `json:"options,omitempty"`
	Trace      *openAPIOperation `json:"trace,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Description string           `json:"description,omitempty"`
}

// openAPIOperation describes a single API operation.
type openAPIOperation struct {
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	OperationID string                `json:"operationId,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Security    []map[string][]string `json:"security,omitempty"`
}

// ---------------------------------------------------------------------------
// Swagger 2.0 minimal spec structs
// ---------------------------------------------------------------------------

type swagger2Spec struct {
	Swagger  string                  `json:"swagger"`
	Info     openAPIInfo             `json:"info"`
	Host     string                  `json:"host,omitempty"`
	BasePath string                  `json:"basePath,omitempty"`
	Schemes  []string                `json:"schemes,omitempty"`
	Paths    map[string]swagger2Path `json:"paths"`
}

type swagger2Path struct {
	Get        *swagger2Operation `json:"get,omitempty"`
	Post       *swagger2Operation `json:"post,omitempty"`
	Put        *swagger2Operation `json:"put,omitempty"`
	Delete     *swagger2Operation `json:"delete,omitempty"`
	Patch      *swagger2Operation `json:"patch,omitempty"`
	Head       *swagger2Operation `json:"head,omitempty"`
	Options    *swagger2Operation `json:"options,omitempty"`
	Parameters []interface{}      `json:"parameters,omitempty"`
}

type swagger2Operation struct {
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	OperationID string                `json:"operationId,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Security    []map[string][]string `json:"security,omitempty"`
}

// ---------------------------------------------------------------------------
// Import functions
// ---------------------------------------------------------------------------

// ImportFromOpenAPI imports an API definition from an OpenAPI 3.0 JSON spec.
// It creates the API record plus all resources derived from the spec paths.
func ImportFromOpenAPI(ctx context.Context, s store.Store, tenantID string, specData []byte) (*models.APIDB, []*models.APIResourceDB, error) {
	if tenantID == "" {
		return nil, nil, fmt.Errorf("tenant_id is required")
	}
	if len(specData) == 0 {
		return nil, nil, fmt.Errorf("spec_data is empty")
	}

	var spec openAPI3Spec
	if err := json.Unmarshal(specData, &spec); err != nil {
		return nil, nil, fmt.Errorf("invalid OpenAPI JSON: %w", err)
	}

	if spec.OpenAPI == "" {
		return nil, nil, fmt.Errorf("not a valid OpenAPI spec: missing 'openapi' field")
	}

	// Derive the backend endpoint from the first server URL.
	endpoint := getFirstServerURL(spec.Servers)
	if endpoint == "" {
		endpoint = "http://localhost:8080"
	}

	// Determine auth type from the global security section.
	authType := inferAuthType(specData)

	api := &models.APIDB{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        spec.Info.Title,
		Description: spec.Info.Description,
		Context:     "/api/" + slugify(spec.Info.Title),
		Version:     spec.Info.Version,
		Endpoint:    endpoint,
		AuthType:    authType,
		Status:      string(models.StatusCreated),
		Provider:    "imported",
		Tags:        "",
	}

	if api.Version == "" {
		api.Version = "1.0.0"
	}

	// Persist the API using the DB store.
	if err := s.CreateAPI(api); err != nil {
		return nil, nil, fmt.Errorf("save imported API: %w", err)
	}

	// Create resources from spec paths.
	var resources []*models.APIResourceDB
	for path, pathItem := range spec.Paths {
		ops := pathItem.Operations()
		for method, op := range ops {
			desc := op.Description
			if desc == "" {
				desc = op.Summary
			}
			if desc == "" {
				desc = pathItem.Description
			}
			if desc == "" {
				desc = pathItem.Summary
			}

			// Determine auth requirement per operation.
			authRequired := authType != "none"
			if len(op.Security) > 0 {
				authRequired = !isPublicOperation(op)
			}

			resource := &models.APIResourceDB{
				ID:           uuid.New().String(),
				APIID:        api.ID,
				Method:       strings.ToUpper(method),
				Path:         path,
				Description:  desc,
				AuthRequired: authRequired,
			}
			if err := s.CreateResource(resource); err != nil {
				// Log and continue; don't fail the whole import for one resource.
				continue
			}
			resources = append(resources, resource)
		}
	}

	return api, resources, nil
}

// ImportFromSwagger2 imports an API definition from a Swagger 2.0 JSON spec.
func ImportFromSwagger2(ctx context.Context, s store.Store, tenantID string, specData []byte) (*models.APIDB, []*models.APIResourceDB, error) {
	if tenantID == "" {
		return nil, nil, fmt.Errorf("tenant_id is required")
	}
	if len(specData) == 0 {
		return nil, nil, fmt.Errorf("spec_data is empty")
	}

	var spec swagger2Spec
	if err := json.Unmarshal(specData, &spec); err != nil {
		return nil, nil, fmt.Errorf("invalid Swagger 2.0 JSON: %w", err)
	}

	if spec.Swagger == "" {
		return nil, nil, fmt.Errorf("not a valid Swagger 2.0 spec: missing 'swagger' field")
	}

	basePath := spec.BasePath
	if basePath == "" {
		basePath = "/api/" + slugify(spec.Info.Title)
	}

	host := spec.Host
	if host == "" {
		host = "localhost"
	}
	scheme := "http"
	if len(spec.Schemes) > 0 {
		scheme = spec.Schemes[0]
	}
	endpoint := fmt.Sprintf("%s://%s%s", scheme, host, spec.BasePath)

	authType := inferAuthTypeSwagger2(spec)

	api := &models.APIDB{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        spec.Info.Title,
		Description: spec.Info.Description,
		Context:     basePath,
		Version:     spec.Info.Version,
		Endpoint:    endpoint,
		AuthType:    authType,
		Status:      string(models.StatusCreated),
		Provider:    "imported",
		Tags:        "",
	}

	if api.Version == "" {
		api.Version = "1.0.0"
	}

	if err := s.CreateAPI(api); err != nil {
		return nil, nil, fmt.Errorf("save imported API: %w", err)
	}

	var resources []*models.APIResourceDB
	for path, pathItem := range spec.Paths {
		ops := pathItem.Operations()
		for method, op := range ops {
			desc := op.Description
			if desc == "" {
				desc = op.Summary
			}

			authRequired := authType != "none"
			if len(op.Security) > 0 {
				authRequired = !isPublicSwaggerOp(op)
			}

			resource := &models.APIResourceDB{
				ID:           uuid.New().String(),
				APIID:        api.ID,
				Method:       strings.ToUpper(method),
				Path:         path,
				Description:  desc,
				AuthRequired: authRequired,
			}
			if err := s.CreateResource(resource); err != nil {
				continue
			}
			resources = append(resources, resource)
		}
	}

	return api, resources, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Operations returns a map of HTTP method -> operation for OpenAPI 3 paths.
func (p openAPIPath) Operations() map[string]*openAPIOperation {
	ops := make(map[string]*openAPIOperation)
	if p.Get != nil {
		ops["GET"] = p.Get
	}
	if p.Post != nil {
		ops["POST"] = p.Post
	}
	if p.Put != nil {
		ops["PUT"] = p.Put
	}
	if p.Delete != nil {
		ops["DELETE"] = p.Delete
	}
	if p.Patch != nil {
		ops["PATCH"] = p.Patch
	}
	if p.Head != nil {
		ops["HEAD"] = p.Head
	}
	if p.Options != nil {
		ops["OPTIONS"] = p.Options
	}
	if p.Trace != nil {
		ops["TRACE"] = p.Trace
	}
	return ops
}

// Operations returns a map of HTTP method -> operation for Swagger 2 paths.
func (p swagger2Path) Operations() map[string]*swagger2Operation {
	ops := make(map[string]*swagger2Operation)
	if p.Get != nil {
		ops["GET"] = p.Get
	}
	if p.Post != nil {
		ops["POST"] = p.Post
	}
	if p.Put != nil {
		ops["PUT"] = p.Put
	}
	if p.Delete != nil {
		ops["DELETE"] = p.Delete
	}
	if p.Patch != nil {
		ops["PATCH"] = p.Patch
	}
	if p.Head != nil {
		ops["HEAD"] = p.Head
	}
	if p.Options != nil {
		ops["OPTIONS"] = p.Options
	}
	return ops
}

// getFirstServerURL returns the first server URL, or an empty string.
func getFirstServerURL(servers []openAPIServer) string {
	if len(servers) == 0 {
		return ""
	}
	u := servers[0].URL
	// Remove trailing slash for consistency.
	u = strings.TrimSuffix(u, "/")
	// Resolve relative URLs.
	if strings.HasPrefix(u, "/") {
		u = "http://localhost:8080" + u
	}
	return u
}

// slugify converts a title into a URL-friendly slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	replacer := strings.NewReplacer(
		" ", "-",
		"_", "-",
		"/", "-",
		"\\", "-",
		":", "",
		"?", "",
		"&", "",
		"=", "",
	)
	s = replacer.Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	return s
}

// inferAuthType examines the raw spec JSON for security scheme definitions
// and returns the dominant auth type.
func inferAuthType(specData []byte) string {
	var doc map[string]interface{}
	if err := json.Unmarshal(specData, &doc); err != nil {
		return "oauth2"
	}

	components, _ := doc["components"].(map[string]interface{})
	if components == nil {
		return "oauth2"
	}
	secSchemes, _ := components["securitySchemes"].(map[string]interface{})
	if secSchemes == nil {
		return "oauth2"
	}

	for _, v := range secSchemes {
		scheme, _ := v.(map[string]interface{})
		if scheme == nil {
			continue
		}
		stype, _ := scheme["type"].(string)
		switch stype {
		case "http":
			if s, _ := scheme["scheme"].(string); s == "bearer" {
				return "oauth2"
			}
			return "mutualtls"
		case "apiKey":
			return "apikey"
		case "oauth2":
			return "oauth2"
		case "openIdConnect":
			return "oauth2"
		}
	}
	return "oauth2"
}

// inferAuthTypeSwagger2 determines the auth type from a Swagger 2 spec.
func inferAuthTypeSwagger2(spec swagger2Spec) string {
	data, _ := json.Marshal(spec)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	defs, _ := raw["securityDefinitions"].(map[string]interface{})
	if defs == nil {
		return "oauth2"
	}

	for _, v := range defs {
		d, _ := v.(map[string]interface{})
		if d == nil {
			continue
		}
		typ, _ := d["type"].(string)
		switch typ {
		case "basic":
			return "mutualtls"
		case "apiKey":
			return "apikey"
		case "oauth2":
			return "oauth2"
		}
	}
	return "oauth2"
}

// isPublicOperation checks whether an operation has an explicit empty
// security requirement (i.e. it is marked as public).
func isPublicOperation(op *openAPIOperation) bool {
	for _, sec := range op.Security {
		if len(sec) == 0 {
			return true
		}
	}
	return false
}

// isPublicSwaggerOp checks whether a Swagger 2.0 operation is public.
func isPublicSwaggerOp(op *swagger2Operation) bool {
	for _, sec := range op.Security {
		if len(sec) == 0 {
			return true
		}
	}
	return false
}

// ValidateSpecURL validates a spec URL and returns its likely format.
func ValidateSpecURL(specURL string) (format string, err error) {
	u, err := url.Parse(specURL)
	if err != nil {
		return "", fmt.Errorf("invalid spec URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme: %s", u.Scheme)
	}
	return "", nil
}
