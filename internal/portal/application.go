package portal

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vedadb/vapim/pkg/models"
)

// handleCreateApplication creates a new application for the authenticated user.
// Applications are required before subscribing to APIs.
//
// Request body:
//   - name: application name (required, unique per user)
//   - description: optional description
//   - tier: subscription tier (Bronze, Silver, Gold, Unlimited)
func (s *Server) handleCreateApplication(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)

	var req models.CreateApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid request body: " + err.Error(),
			Code:      "INVALID_REQUEST",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "application name is required",
			Code:      "VALIDATION_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Default tier if not specified
	if req.Tier == "" {
		req.Tier = "Unlimited"
	}

	app := &models.Application{
		Name:        req.Name,
		Description: req.Description,
		Tier:        req.Tier,
		Status:      "ACTIVE",
		OwnerID:     userID,
		Tenant:      getTenant(c),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if err := s.store.CreateApplication(ctx, app); err != nil {
		s.logger.Error("failed to create application", "error", err, "user_id", userID, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to create application",
			Code:      "CREATE_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	s.logger.Info("application created", "app_id", app.ID, "user_id", userID)
	c.JSON(http.StatusCreated, models.APIResponse{
		Data:      app,
		RequestID: c.GetString("request_id"),
	})
}

// handleGetApplication returns details of a specific application owned by the user.
func (s *Server) handleGetApplication(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	appID := c.Param("appID")

	app, err := s.store.GetApplication(ctx, appID, userID)
	if err != nil {
		s.logger.Error("failed to get application", "app_id", appID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:     "application not found",
			Code:      "APP_NOT_FOUND",
			Status:    http.StatusNotFound,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data:      app,
		RequestID: c.GetString("request_id"),
	})
}

// handleListApplications returns all applications owned by the authenticated user.
func (s *Server) handleListApplications(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	offset, limit := parsePagination(c)

	apps, total, err := s.store.ListApplications(ctx, userID, offset, limit)
	if err != nil {
		s.logger.Error("failed to list applications", "error", err, "user_id", userID, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to list applications",
			Code:      "LIST_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.PaginatedResponse[models.Application]{
		Data:       apps,
		Total:      total,
		Offset:     offset,
		Limit:      limit,
		Count:      int64(len(apps)),
		RequestID:  c.GetString("request_id"),
	})
}

// handleUpdateApplication updates an existing application's name, description, or tier.
func (s *Server) handleUpdateApplication(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	appID := c.Param("appID")

	var req models.UpdateApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid request body: " + err.Error(),
			Code:      "INVALID_REQUEST",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Fetch existing to merge
	existing, err := s.store.GetApplication(ctx, appID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:     "application not found",
			Code:      "APP_NOT_FOUND",
			Status:    http.StatusNotFound,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Tier != "" {
		existing.Tier = req.Tier
	}
	existing.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateApplication(ctx, existing); err != nil {
		s.logger.Error("failed to update application", "app_id", appID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to update application",
			Code:      "UPDATE_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	s.logger.Info("application updated", "app_id", appID, "user_id", userID)
	c.JSON(http.StatusOK, models.APIResponse{
		Data:      existing,
		RequestID: c.GetString("request_id"),
	})
}

// handleDeleteApplication deletes an application and all its subscriptions.
func (s *Server) handleDeleteApplication(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	appID := c.Param("appID")

	if err := s.store.DeleteApplication(ctx, appID, userID); err != nil {
		s.logger.Error("failed to delete application", "app_id", appID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to delete application",
			Code:      "DELETE_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	s.logger.Info("application deleted", "app_id", appID, "user_id", userID)
	c.JSON(http.StatusNoContent, nil)
}

// handleGenerateApplicationKeys generates production or sandbox API keys
// (OAuth2 client credentials) for an application.
//
// Path parameters:
//   - appID: application ID
//
// Request body:
//   - key_type: "PRODUCTION" or "SANDBOX"
//   - grant_types: array of grant types (client_credentials, password, etc.)
//   - callback_url: optional OAuth callback URL
//   - validity_period: key validity in seconds (default 3600)
func (s *Server) handleGenerateApplicationKeys(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	appID := c.Param("appID")

	var req models.GenerateKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid request body: " + err.Error(),
			Code:      "INVALID_REQUEST",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if req.KeyType != "PRODUCTION" && req.KeyType != "SANDBOX" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "key_type must be PRODUCTION or SANDBOX",
			Code:      "VALIDATION_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if req.ValidityPeriod <= 0 {
		req.ValidityPeriod = 3600 // default 1 hour
	}

	tier := req.Tier
	if tier == "" {
		tier = "Unlimited"
	}

	keys, err := s.store.GenerateApplicationKeys(ctx, appID, userID, req.KeyType, tier)
	if err != nil {
		s.logger.Error("failed to generate keys", "app_id", appID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to generate application keys",
			Code:      "KEYGEN_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	s.logger.Info("keys generated", "app_id", appID, "key_type", req.keyType, "user_id", userID)
	c.JSON(http.StatusCreated, models.APIResponse{
		Data:      keys,
		RequestID: c.GetString("request_id"),
	})
}

// handleGetApplicationKeys returns all generated keys for an application.
func (s *Server) handleGetApplicationKeys(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	appID := c.Param("appID")

	keys, err := s.store.GetApplicationKeys(ctx, appID, userID)
	if err != nil {
		s.logger.Error("failed to get application keys", "app_id", appID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to retrieve application keys",
			Code:      "KEYS_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data:      keys,
		RequestID: c.GetString("request_id"),
	})
}

// handleRegenerateKeys rotates (revokes and regenerates) application keys.
//
// Path parameters:
//   - appID: application ID
//   - keyType: "PRODUCTION" or "SANDBOX"
func (s *Server) handleRegenerateKeys(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	appID := c.Param("appID")
	keyType := c.Param("keyType")

	if keyType != "PRODUCTION" && keyType != "SANDBOX" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "keyType must be PRODUCTION or SANDBOX",
			Code:      "VALIDATION_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	keys, err := s.store.RegenerateKeys(ctx, appID, userID, keyType)
	if err != nil {
		s.logger.Error("failed to regenerate keys", "app_id", appID, "key_type", keyType, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to regenerate keys",
			Code:      "REGENERATE_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	s.logger.Info("keys regenerated", "app_id", appID, "key_type", keyType, "user_id", userID)
	c.JSON(http.StatusOK, models.APIResponse{
		Data:      keys,
		RequestID: c.GetString("request_id"),
	})
}
