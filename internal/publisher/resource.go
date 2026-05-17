// Package publisher implements API resource management (CRUD, path parameters,
// policies per resource) for the VedaDB API Manager Publisher.
package publisher

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// ResourceManager handles API resource operations.
type ResourceManager struct {
	store Store
}

// NewResourceManager creates a new ResourceManager.
func NewResourceManager(store Store) *ResourceManager {
	return &ResourceManager{store: store}
}

// validHTTPMethods is the set of allowed HTTP methods for resources.
var validHTTPMethods = map[string]bool{
	"GET":     true,
	"POST":    true,
	"PUT":     true,
	"DELETE":  true,
	"PATCH":   true,
	"HEAD":    true,
	"OPTIONS": true,
}

// CreateResource adds a new resource to an API.
func (rm *ResourceManager) CreateResource(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	// Verify API exists
	_, err := rm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	var req struct {
		Path         string                 `json:"path" binding:"required"`
		Methods      []string               `json:"methods" binding:"required"`
		AuthRequired bool                   `json:"auth_required"`
		Throttling   string                 `json:"throttling_policy"`
		Policies     []string               `json:"policies"`
		Parameters   []models.PathParameter `json:"parameters"`
		Produces     []string               `json:"produces"`
		Consumes     []string               `json:"consumes"`
		Description  string                 `json:"description"`
		Metadata     map[string]string      `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	// Validate methods
	if err := validateMethods(req.Methods); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid HTTP methods",
			Description: err.Error(),
		})
		return
	}

	// Validate path format
	if !strings.HasPrefix(req.Path, "/") {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid path",
			Description: "Path must start with '/'",
		})
		return
	}

	// Extract path parameters from the path if not provided
	params := req.Parameters
	if len(params) == 0 {
		params = extractPathParameters(req.Path)
	}

	resource := &models.APIResource{
		ID:           uuid.New().String(),
		APIID:        apiID,
		Path:         req.Path,
		Methods:      normalizeMethods(req.Methods),
		AuthRequired: req.AuthRequired,
		Throttling:   req.Throttling,
		Policies:     req.Policies,
		Parameters:   params,
		Produces:     req.Produces,
		Consumes:     req.Consumes,
		Description:  req.Description,
		Metadata:     req.Metadata,
	}

	if err := rm.store.SaveResource(resource); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to create resource",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, resource)
}

// GetResource retrieves a single resource by ID.
func (rm *ResourceManager) GetResource(c *gin.Context) {
	apiID := c.Param("api_id")
	resourceID := c.Param("resource_id")
	if apiID == "" || resourceID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID and resource ID are required",
		})
		return
	}

	resource, err := rm.store.GetResource(resourceID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Resource not found",
			Description: err.Error(),
		})
		return
	}

	if resource.APIID != apiID {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "Resource does not belong to the specified API",
		})
		return
	}

	c.JSON(http.StatusOK, resource)
}

// ListResources returns all resources for an API.
func (rm *ResourceManager) ListResources(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	resources, err := rm.store.ListResourcesByAPI(apiID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to list resources",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":    apiID,
		"resources": resources,
		"total":     len(resources),
	})
}

// UpdateResource modifies an existing resource.
func (rm *ResourceManager) UpdateResource(c *gin.Context) {
	apiID := c.Param("api_id")
	resourceID := c.Param("resource_id")
	if apiID == "" || resourceID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID and resource ID are required",
		})
		return
	}

	existing, err := rm.store.GetResource(resourceID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Resource not found",
			Description: err.Error(),
		})
		return
	}

	if existing.APIID != apiID {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "Resource does not belong to the specified API",
		})
		return
	}

	var req struct {
		Path         string                 `json:"path"`
		Methods      []string               `json:"methods"`
		AuthRequired *bool                  `json:"auth_required"`
		Throttling   string                 `json:"throttling_policy"`
		Policies     []string               `json:"policies"`
		Parameters   []models.PathParameter `json:"parameters"`
		Produces     []string               `json:"produces"`
		Consumes     []string               `json:"consumes"`
		Description  string                 `json:"description"`
		Metadata     map[string]string      `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	// Validate methods if provided
	if len(req.Methods) > 0 {
		if err := validateMethods(req.Methods); err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Code:        http.StatusBadRequest,
				Message:     "Invalid HTTP methods",
				Description: err.Error(),
			})
			return
		}
		existing.Methods = normalizeMethods(req.Methods)
	}

	if req.Path != "" {
		if !strings.HasPrefix(req.Path, "/") {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Code:        http.StatusBadRequest,
				Message:     "Invalid path",
				Description: "Path must start with '/'",
			})
			return
		}
		existing.Path = req.Path
	}
	if req.AuthRequired != nil {
		existing.AuthRequired = *req.AuthRequired
	}
	if req.Throttling != "" {
		existing.Throttling = req.Throttling
	}
	if req.Policies != nil {
		existing.Policies = req.Policies
	}
	if req.Parameters != nil {
		existing.Parameters = req.Parameters
	} else if req.Path != "" {
		existing.Parameters = extractPathParameters(existing.Path)
	}
	if req.Produces != nil {
		existing.Produces = req.Produces
	}
	if req.Consumes != nil {
		existing.Consumes = req.Consumes
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Metadata != nil {
		existing.Metadata = req.Metadata
	}

	if err := rm.store.UpdateResource(existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to update resource",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, existing)
}

// DeleteResource removes a resource from an API.
func (rm *ResourceManager) DeleteResource(c *gin.Context) {
	apiID := c.Param("api_id")
	resourceID := c.Param("resource_id")
	if apiID == "" || resourceID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID and resource ID are required",
		})
		return
	}

	resource, err := rm.store.GetResource(resourceID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Resource not found",
			Description: err.Error(),
		})
		return
	}

	if resource.APIID != apiID {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "Resource does not belong to the specified API",
		})
		return
	}

	if err := rm.store.DeleteResource(resourceID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to delete resource",
			Description: err.Error(),
		})
		return
	}

	c.Status(http.StatusNoContent)
}

// validateMethods checks each method is in the allowed set.
func validateMethods(methods []string) error {
	if len(methods) == 0 {
		return fmt.Errorf("at least one HTTP method is required")
	}
	seen := make(map[string]bool)
	for _, m := range methods {
		upper := strings.ToUpper(strings.TrimSpace(m))
		if upper == "" {
			return fmt.Errorf("empty method not allowed")
		}
		if !validHTTPMethods[upper] {
			return fmt.Errorf("unsupported HTTP method: %s", m)
		}
		if seen[upper] {
			return fmt.Errorf("duplicate method: %s", upper)
		}
		seen[upper] = true
	}
	return nil
}

// normalizeMethods uppercases and deduplicates methods.
func normalizeMethods(methods []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range methods {
		upper := strings.ToUpper(strings.TrimSpace(m))
		if upper == "" || seen[upper] {
			continue
		}
		seen[upper] = true
		out = append(out, upper)
	}
	return out
}

// extractPathParameters parses path template parameters (e.g., {id}, :id).
func extractPathParameters(path string) []models.PathParameter {
	var params []models.PathParameter

	// Handle {param} syntax
	for {
		start := strings.Index(path, "{")
		if start == -1 {
			break
		}
		end := strings.Index(path[start:], "}")
		if end == -1 {
			break
		}
		paramName := path[start+1 : start+end]
		path = path[start+end+1:]

		// Extract type hint if present: {id:integer}
		paramType := "string"
		if colon := strings.Index(paramName, ":"); colon != -1 {
			paramType = paramName[colon+1:]
			paramName = paramName[:colon]
		}

		params = append(params, models.PathParameter{
			Name:     paramName,
			Type:     paramType,
			Required: true,
		})
	}

	return params
}

// MatchPath checks if a request path matches a resource path template.
func MatchPath(resourcePath, requestPath string) (bool, map[string]string) {
	resourceParts := strings.Split(strings.Trim(resourcePath, "/"), "/")
	requestParts := strings.Split(strings.Trim(requestPath, "/"), "/")

	if len(resourceParts) != len(requestParts) {
		return false, nil
	}

	params := make(map[string]string)
	for i, rp := range resourceParts {
		if strings.HasPrefix(rp, "{") && strings.HasSuffix(rp, "}") {
			paramName := rp[1 : len(rp)-1]
			if colon := strings.Index(paramName, ":"); colon != -1 {
				paramName = paramName[:colon]
			}
			params[paramName] = requestParts[i]
		} else if rp != requestParts[i] {
			return false, nil
		}
	}

	return true, params
}

// ApplyResourcePolicies applies resource-level policies (auth, throttling).
func ApplyResourcePolicies(resource *models.APIResource, apiPolicies []string) []string {
	var policies []string
	policies = append(policies, apiPolicies...)
	if resource.Throttling != "" {
		policies = append(policies, resource.Throttling)
	}
	for _, p := range resource.Policies {
		found := false
		for _, existing := range policies {
			if existing == p {
				found = true
				break
			}
		}
		if !found {
			policies = append(policies, p)
		}
	}
	return policies
}

// ResourceStats holds statistics about API resources.
type ResourceStats struct {
	TotalResources   int            `json:"total_resources"`
	MethodsCount     map[string]int `json:"methods_count"`
	AuthRequiredCount int           `json:"auth_required_count"`
	PublicCount       int           `json:"public_count"`
}

// GetResourceStats calculates statistics for an API's resources.
func (rm *ResourceManager) GetResourceStats(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	resources, err := rm.store.ListResourcesByAPI(apiID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to list resources",
			Description: err.Error(),
		})
		return
	}

	stats := ResourceStats{
		TotalResources:    len(resources),
		MethodsCount:      make(map[string]int),
		AuthRequiredCount: 0,
		PublicCount:       0,
	}

	for _, r := range resources {
		if r.AuthRequired {
			stats.AuthRequiredCount++
		} else {
			stats.PublicCount++
		}
		for _, m := range r.Methods {
			stats.MethodsCount[m]++
		}
	}

	c.JSON(http.StatusOK, stats)
}

// ThrottledRequest represents a request subject to throttling.
type ThrottledRequest struct {
	APIID       string
	ResourceID  string
	ClientID    string
	RequestTime time.Time
}

// CheckThrottling checks if a request should be throttled based on resource policy.
func CheckThrottling(resource *models.APIResource, request ThrottledRequest) (bool, string) {
	// In production this would check against a rate limiter service.
	// For now we return allowed.
	return true, ""
}
