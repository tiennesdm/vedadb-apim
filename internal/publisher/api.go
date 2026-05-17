// Package publisher implements API management (CRUD, search, pagination) for the
// VedaDB API Manager Publisher.
package publisher

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tiennesdm/vedadb-apim/internal/auth"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// APIManager handles API CRUD and search operations.
type APIManager struct {
	store Store
}

// NewAPIManager creates a new APIManager.
func NewAPIManager(store Store) *APIManager {
	return &APIManager{store: store}
}

// CreateAPI creates a new API.
func (m *APIManager) CreateAPI(c *gin.Context) {
	var req models.APICreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	// Validate context format (must start with /)
	if !strings.HasPrefix(req.Context, "/") {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid API context",
			Description: "API context must start with '/'",
		})
		return
	}

	tenantID := auth.GetTenantID(c)
	userID, _ := c.Get(auth.CtxKeyUserID)
	createdBy, _ := userID.(string)
	if createdBy == "" {
		createdBy = "system"
	}

	now := time.Now()
	api := &models.API{
		ID:           uuid.New().String(),
		Name:         req.Name,
		Context:      req.Context,
		Version:      req.Version,
		Endpoint:     req.Endpoint,
		AuthType:     req.AuthType,
		Status:       models.StatusCreated,
		Policies:     req.Policies,
		Tags:         req.Tags,
		Description:  req.Description,
		Visibility:   req.Visibility,
		Resources:    req.Resources,
		Metadata:     req.Metadata,
		IsDefault:    false,
		CreatedBy:    createdBy,
		UpdatedBy:    createdBy,
		CreatedAt:    now,
		UpdatedAt:    now,
		Deleted:      false,
		TenantID:     tenantID,
		VersionSetID: "",
	}

	if api.Visibility == "" {
		api.Visibility = "PUBLIC"
	}

	if err := m.store.SaveAPI(api); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to create API",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, api)
}

// GetAPI returns a single API by ID.
func (m *APIManager) GetAPI(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	api, err := m.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	tenantID := auth.GetTenantID(c)
	if api.TenantID != "" && api.TenantID != tenantID {
		c.JSON(http.StatusForbidden, models.ErrorResponse{
			Code:    http.StatusForbidden,
			Message: "Access denied: API belongs to a different tenant",
		})
		return
	}

	if api.Visibility == "PRIVATE" {
		userID, _ := c.Get(auth.CtxKeyUserID)
		if api.CreatedBy != userID {
			roles, _ := c.Get(auth.CtxKeyRoles)
			roleList, _ := roles.([]string)
			if !containsRole(roleList, "admin") && !containsRole(roleList, "super_admin") {
				c.JSON(http.StatusForbidden, models.ErrorResponse{
					Code:    http.StatusForbidden,
					Message: "Access denied: private API",
				})
				return
			}
		}
	}

	c.JSON(http.StatusOK, api)
}

// ListAPIs returns paginated list of all APIs.
func (m *APIManager) ListAPIs(c *gin.Context) {
	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "20")

	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	tenantID := auth.GetTenantID(c)
	apis, total, err := m.store.ListAPIs(tenantID, offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to list APIs",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIListResponse{
		APIs:    apis,
		Total:   total,
		Offset:  offset,
		Limit:   limit,
		HasMore: offset+len(apis) < total,
	})
}

// UpdateAPI updates API details.
func (m *APIManager) UpdateAPI(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	existing, err := m.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	// Cannot update retired APIs
	if existing.Status == models.StatusRetired {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Cannot update retired API",
			Description: fmt.Sprintf("API %s is in RETIRED state and cannot be modified", apiID),
		})
		return
	}

	// Cannot modify certain fields if published
	if existing.Status == models.StatusPublished {
		roles, _ := c.Get(auth.CtxKeyRoles)
		roleList, _ := roles.([]string)
		if !containsRole(roleList, "admin") && !containsRole(roleList, "super_admin") {
			c.JSON(http.StatusForbidden, models.ErrorResponse{
				Code:    http.StatusForbidden,
				Message: "Only admins can modify published APIs",
			})
			return
		}
	}

	var req models.APIUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	// Apply updates
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Endpoint != "" {
		existing.Endpoint = req.Endpoint
	}
	if req.AuthType != "" {
		existing.AuthType = req.AuthType
	}
	if len(req.Policies) > 0 {
		existing.Policies = req.Policies
	}
	if len(req.Tags) > 0 {
		existing.Tags = req.Tags
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Visibility != "" {
		existing.Visibility = req.Visibility
	}
	if len(req.Resources) > 0 {
		existing.Resources = req.Resources
	}
	if req.Metadata != nil {
		existing.Metadata = req.Metadata
	}

	userID, _ := c.Get(auth.CtxKeyUserID)
	if uid, ok := userID.(string); ok && uid != "" {
		existing.UpdatedBy = uid
	} else {
		existing.UpdatedBy = "system"
	}
	existing.UpdatedAt = time.Now()

	if err := m.store.UpdateAPI(existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to update API",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, existing)
}

// DeleteAPI performs a soft delete of an API.
func (m *APIManager) DeleteAPI(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	existing, err := m.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	// Only allow deleting Created or Retired APIs
	if existing.Status != models.StatusCreated && existing.Status != models.StatusRetired {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Cannot delete API in current state",
			Description: fmt.Sprintf("API must be in CREATED or RETIRED state to delete, currently %s", existing.Status),
		})
		return
	}

	if err := m.store.DeleteAPI(apiID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to delete API",
			Description: err.Error(),
		})
		return
	}

	c.Status(http.StatusNoContent)
}

// SearchAPIs searches APIs by name, context, tag, or general query.
func (m *APIManager) SearchAPIs(c *gin.Context) {
	var req models.SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid search parameters",
			Description: err.Error(),
		})
		return
	}

	req.TenantID = auth.GetTenantID(c)

	if req.Limit < 1 {
		req.Limit = 20
	}
	if req.Limit > 100 {
		req.Limit = 100
	}
	if req.Offset < 0 {
		req.Offset = 0
	}

	apis, total, err := m.store.SearchAPIs(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to search APIs",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIListResponse{
		APIs:    apis,
		Total:   total,
		Offset:  req.Offset,
		Limit:   req.Limit,
		HasMore: req.Offset+len(apis) < total,
	})
}

// containsRole checks if a role list contains a specific role.
func containsRole(roles []string, target string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, target) {
			return true
		}
	}
	return false
}
