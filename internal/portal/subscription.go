package portal

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// handleSubscribeToAPI creates a new subscription linking an application to an API.
// The application must be owned by the authenticated user.
//
// Request body:
//   - api_id: ID of the API to subscribe to (required)
//   - app_id: ID of the application (required)
//   - tier: subscription tier (Bronze, Silver, Gold, Unlimited)
//   - throttling_policy: optional custom throttle policy
func (s *Server) handleSubscribeToAPI(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)

	var req models.SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "invalid request body: " + err.Error(),
			Code:      "INVALID_REQUEST",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if req.APIID == "" || req.AppID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "api_id and app_id are required",
			Code:      "VALIDATION_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Default tier
	if req.Tier == "" {
		req.Tier = "Unlimited"
	}

	sub := &models.Subscription{
		APIID:      req.APIID,
		AppID:      req.AppID,
		Tier:       req.Tier,
		Status:     "ACTIVE",
		Subscriber: userID,
		Tenant:     getTenant(c),
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	if err := s.store.SubscribeToAPI(ctx, sub); err != nil {
		s.logger.Error("failed to subscribe", "api_id", req.APIID, "app_id", req.AppID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to create subscription: " + err.Error(),
			Code:      "SUBSCRIBE_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	s.logger.Info("subscription created", "sub_id", sub.ID, "api_id", req.APIID, "app_id", req.AppID, "user_id", userID)
	c.JSON(http.StatusCreated, models.APIResponse{
		Data:      sub,
		RequestID: c.GetString("request_id"),
	})
}

// handleUnsubscribeFromAPI removes a subscription.
func (s *Server) handleUnsubscribeFromAPI(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	subID := c.Param("subID")

	if err := s.store.UnsubscribeFromAPI(ctx, subID, userID); err != nil {
		s.logger.Error("failed to unsubscribe", "sub_id", subID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to unsubscribe: " + err.Error(),
			Code:      "UNSUBSCRIBE_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	s.logger.Info("subscription removed", "sub_id", subID, "user_id", userID)
	c.JSON(http.StatusNoContent, nil)
}

// handleListSubscriptions returns all subscriptions for the authenticated user.
func (s *Server) handleListSubscriptions(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	offset, limit := parsePagination(c)

	subs, total, err := s.store.ListSubscriptions(ctx, userID, offset, limit)
	if err != nil {
		s.logger.Error("failed to list subscriptions", "error", err, "user_id", userID, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:     "failed to list subscriptions",
			Code:      "LIST_ERROR",
			Status:    http.StatusInternalServerError,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.PaginatedResponse[models.Subscription]{
		Data:       subs,
		Total:      total,
		Offset:     offset,
		Limit:      limit,
		Count:      int64(len(subs)),
		RequestID:  c.GetString("request_id"),
	})
}

// handleGetSubscription returns details of a specific subscription.
func (s *Server) handleGetSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	userID := getUserID(c)
	subID := c.Param("subID")

	sub, err := s.store.GetSubscription(ctx, subID, userID)
	if err != nil {
		s.logger.Error("failed to get subscription", "sub_id", subID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error:     "subscription not found",
			Code:      "SUB_NOT_FOUND",
			Status:    http.StatusNotFound,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data:      sub,
		RequestID: c.GetString("request_id"),
	})
}

// handleValidateSubscription checks if a subscription is active for a given
// application and API combination. This is used by the gateway.
//
// Query parameters:
//   - app_id: application ID
//   - api_id: API ID
func (s *Server) handleValidateSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	appID := c.Query("app_id")
	apiID := c.Query("api_id")

	if appID == "" || apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:     "app_id and api_id query parameters are required",
			Code:      "VALIDATION_ERROR",
			Status:    http.StatusBadRequest,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	sub, err := s.store.ValidateSubscription(ctx, appID, apiID)
	if err != nil {
		s.logger.Warn("subscription validation failed", "app_id", appID, "api_id", apiID, "error", err, "request_id", c.GetString("request_id"))
		c.JSON(http.StatusForbidden, models.ErrorResponse{
			Error:     "no active subscription found",
			Code:      "SUBSCRIPTION_INVALID",
			Status:    http.StatusForbidden,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, models.SubscriptionValidationResponse{
		Valid:      true,
		Status:     sub.Status,
		Tier:       sub.Tier,
		AppID:      sub.AppID,
		APIID:      sub.APIID,
		ExpiryDate: sub.ExpiryDate,
	})
}
