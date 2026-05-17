// Package publisher implements API versioning for the VedaDB API Manager
// Publisher: create versions, list versions, set default, copy resources.
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

// VersionManager handles API version operations.
type VersionManager struct {
	store Store
}

// NewVersionManager creates a new VersionManager.
func NewVersionManager(store Store) *VersionManager {
	return &VersionManager{store: store}
}

// CreateVersion creates a new version of an existing API.
func (vm *VersionManager) CreateVersion(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	existing, err := vm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	var req struct {
		NewVersion string `json:"new_version" binding:"required"`
		CopyResources bool `json:"copy_resources"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	// Validate version format (basic semver-like check)
	if !isValidVersion(req.NewVersion) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid version format",
			Description: "Version must follow semantic versioning (e.g., 1.0.0, 2.1.0)",
		})
		return
	}

	// Create new API with same details but new version
	newAPI := &models.API{
		ID:           uuid.New().String(),
		Name:         existing.Name,
		Context:      existing.Context,
		Version:      req.NewVersion,
		Endpoint:     existing.Endpoint,
		AuthType:     existing.AuthType,
		Status:       models.StatusCreated,
		Policies:     copyStringSlice(existing.Policies),
		Tags:         copyStringSlice(existing.Tags),
		Description:  existing.Description + " (version " + req.NewVersion + ")",
		Visibility:   existing.Visibility,
		IsDefault:    false,
		CreatedBy:    existing.UpdatedBy,
		UpdatedBy:    existing.UpdatedBy,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Deleted:      false,
		TenantID:     existing.TenantID,
		VersionSetID: existing.VersionSetID,
	}

	if req.CopyResources {
		newAPI.Resources = copyResources(existing.Resources)
	}

	if err := vm.store.SaveAPI(newAPI); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to create API version",
			Description: err.Error(),
		})
		return
	}

	// Update version set
	if newAPI.VersionSetID != "" {
		vs, err := vm.store.GetVersionSet(newAPI.VersionSetID)
		if err == nil && vs != nil {
			vs.Versions = append(vs.Versions, req.NewVersion)
			vs.UpdatedAt = time.Now()
			_ = vm.store.SaveVersionSet(vs)
		}
	} else {
		// Create a new version set for this API
		vs := &models.VersionSet{
			ID:             uuid.New().String(),
			APIContext:     existing.Context,
			APIName:        existing.Name,
			Versions:       []string{existing.Version, req.NewVersion},
			DefaultVersion: existing.Version,
			TenantID:       existing.TenantID,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := vm.store.SaveVersionSet(vs); err == nil {
			existing.VersionSetID = vs.ID
			newAPI.VersionSetID = vs.ID
			_ = vm.store.UpdateAPI(&models.API{ID: existing.ID, VersionSetID: vs.ID, UpdatedAt: time.Now()})
			_ = vm.store.UpdateAPI(&models.API{ID: newAPI.ID, VersionSetID: vs.ID, UpdatedAt: time.Now()})
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":             newAPI.ID,
		"name":           newAPI.Name,
		"context":        newAPI.Context,
		"version":        newAPI.Version,
		"status":         newAPI.Status,
		"parent_api_id":  existing.ID,
		"version_set_id": newAPI.VersionSetID,
		"created_at":     newAPI.CreatedAt,
	})
}

// ListVersions returns all versions of an API.
func (vm *VersionManager) ListVersions(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	api, err := vm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	if api.VersionSetID == "" {
		// This API has no version set
		c.JSON(http.StatusOK, gin.H{
			"api_id":          apiID,
			"versions":        []string{api.Version},
			"total":           1,
			"default_version": api.Version,
		})
		return
	}

	versions, err := vm.store.ListAPIVersions(api.VersionSetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to list API versions",
			Description: err.Error(),
		})
		return
	}

	var result []gin.H
	for _, v := range versions {
		result = append(result, gin.H{
			"id":      v.ID,
			"name":    v.Name,
			"version": v.Version,
			"status":  v.Status,
		})
	}

	vs, _ := vm.store.GetVersionSet(api.VersionSetID)
	defaultVersion := ""
	if vs != nil {
		defaultVersion = vs.DefaultVersion
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":          apiID,
		"version_set_id":  api.VersionSetID,
		"versions":        result,
		"total":           len(result),
		"default_version": defaultVersion,
	})
}

// GetDefaultVersion returns the default version for an API's version set.
func (vm *VersionManager) GetDefaultVersion(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	api, err := vm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	if api.VersionSetID == "" {
		c.JSON(http.StatusOK, gin.H{
			"api_id":          apiID,
			"default_version": api.Version,
			"has_version_set": false,
		})
		return
	}

	vs, err := vm.store.GetVersionSet(api.VersionSetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to get version set",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":          apiID,
		"version_set_id":  api.VersionSetID,
		"default_version": vs.DefaultVersion,
		"has_version_set": true,
	})
}

// SetDefaultVersion sets the default version for an API's version set.
func (vm *VersionManager) SetDefaultVersion(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	var req struct {
		Version string `json:"version" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	api, err := vm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	if api.VersionSetID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API has no version set",
		})
		return
	}

	if err := vm.store.UpdateVersionSetDefault(api.VersionSetID, req.Version); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to set default version",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":          apiID,
		"version_set_id":  api.VersionSetID,
		"default_version": req.Version,
	})
}

// isValidVersion performs basic version string validation.
func isValidVersion(v string) bool {
	if v == "" {
		return false
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

// copyStringSlice creates a deep copy of a string slice.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// copyResources creates a deep copy of API resources with new IDs.
func copyResources(src []models.APIResource) []models.APIResource {
	if src == nil {
		return nil
	}
	dst := make([]models.APIResource, len(src))
	for i, r := range src {
		dst[i] = models.APIResource{
			ID:           uuid.New().String(),
			Path:         r.Path,
			Methods:      copyStringSlice(r.Methods),
			AuthRequired: r.AuthRequired,
			Throttling:   r.Throttling,
			Policies:     copyStringSlice(r.Policies),
			Parameters:   copyParameters(r.Parameters),
			Produces:     copyStringSlice(r.Produces),
			Consumes:     copyStringSlice(r.Consumes),
			Description:  r.Description,
			Metadata:     copyMetadata(r.Metadata),
		}
	}
	return dst
}

// copyParameters creates a deep copy of path parameters.
func copyParameters(src []models.PathParameter) []models.PathParameter {
	if src == nil {
		return nil
	}
	dst := make([]models.PathParameter, len(src))
	copy(dst, src)
	return dst
}

// copyMetadata creates a deep copy of metadata map.
func copyMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// DeprecateOldVersions marks all versions except the specified one as deprecated.
// This is a helper that can be called after setting a new default version.
func (vm *VersionManager) DeprecateOldVersions(apiID, keepVersion string) error {
	api, err := vm.store.GetAPI(apiID)
	if err != nil {
		return fmt.Errorf("get api: %w", err)
	}

	if api.VersionSetID == "" {
		return nil // no version set, nothing to deprecate
	}

	versions, err := vm.store.ListAPIVersions(api.VersionSetID)
	if err != nil {
		return fmt.Errorf("list versions: %w", err)
	}

	for _, v := range versions {
		if v.Version != keepVersion && v.Status == models.StatusPublished {
			_ = vm.store.TransitionAPIStatus(v.ID, models.StatusDeprecated)
		}
	}
	return nil
}
