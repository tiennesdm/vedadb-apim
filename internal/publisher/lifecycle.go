// Package publisher implements API lifecycle state machine management for the
// VedaDB API Manager Publisher. States: CREATED -> PROTOTYPED -> PUBLISHED ->
// BLOCKED/DEPRECATED -> RETIRED.
package publisher

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// APIStatus represents all possible API lifecycle states.
const (
	StateCreated    = models.StatusCreated
	StatePrototyped = models.StatusPrototyped
	StatePublished  = models.StatusPublished
	StateBlocked    = models.StatusBlocked
	StateDeprecated = models.StatusDeprecated
	StateRetired    = models.StatusRetired
)

// validTransitions defines the allowed state transitions.
// Key: current state, Value: list of allowed target states.
var validTransitions = map[models.APIStatus][]models.APIStatus{
	StateCreated: {
		StatePrototyped,
		StatePublished,
		StateRetired,
	},
	StatePrototyped: {
		StateCreated,
		StatePublished,
		StateRetired,
	},
	StatePublished: {
		StateBlocked,
		StateDeprecated,
		StateRetired,
	},
	StateBlocked: {
		StatePublished,
		StateDeprecated,
		StateRetired,
	},
	StateDeprecated: {
		StateRetired,
		StateBlocked,
	},
	StateRetired: {}, // terminal state
}

// LifecycleManager handles API lifecycle state transitions.
type LifecycleManager struct {
	store Store
}

// NewLifecycleManager creates a new LifecycleManager.
func NewLifecycleManager(store Store) *LifecycleManager {
	return &LifecycleManager{store: store}
}

// GetLifecycle returns the current lifecycle state of an API.
func (lm *LifecycleManager) GetLifecycle(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	api, err := lm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	availableTransitions := validTransitions[api.Status]
	if availableTransitions == nil {
		availableTransitions = []models.APIStatus{}
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":                api.ID,
		"current_status":        api.Status,
		"available_transitions": availableTransitions,
		"updated_at":            api.UpdatedAt,
		"updated_by":            api.UpdatedBy,
	})
}

// PublishAPI transitions an API from CREATED/PROTOTYPED to PUBLISHED.
func (lm *LifecycleManager) PublishAPI(c *gin.Context) {
	var req models.LifecycleTransitionRequest
	if err := c.ShouldBindJSON(&req); err == nil && req.Reason != "" {
		// reason is optional
	}

	apiID := c.Param("api_id")
	resp, err := lm.transition(c, apiID, StatePublished)
	if err != nil {
		status := http.StatusBadRequest
		if err == errAPINotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{
			Code:        status,
			Message:     "Failed to publish API",
			Description: err.Error(),
		})
		return
	}

	// In a real system, this would deploy the API to the gateway
	c.JSON(http.StatusOK, models.LifecycleTransitionResponse{
		APIID:           resp.APIID,
		PreviousStatus:  resp.PreviousStatus,
		CurrentStatus:   resp.CurrentStatus,
		Message:         fmt.Sprintf("API %s published to gateway. %s", apiID, req.Reason),
		Timestamp:       time.Now(),
	})
}

// BlockAPI transitions a PUBLISHED API to BLOCKED.
func (lm *LifecycleManager) BlockAPI(c *gin.Context) {
	var req models.LifecycleTransitionRequest
	_ = c.ShouldBindJSON(&req)

	apiID := c.Param("api_id")
	resp, err := lm.transition(c, apiID, StateBlocked)
	if err != nil {
		status := http.StatusBadRequest
		if err == errAPINotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{
			Code:        status,
			Message:     "Failed to block API",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.LifecycleTransitionResponse{
		APIID:          resp.APIID,
		PreviousStatus: resp.PreviousStatus,
		CurrentStatus:  resp.CurrentStatus,
		Message:        fmt.Sprintf("API %s blocked. %s", apiID, req.Reason),
		Timestamp:      time.Now(),
	})
}

// UnblockAPI transitions a BLOCKED API back to PUBLISHED.
func (lm *LifecycleManager) UnblockAPI(c *gin.Context) {
	apiID := c.Param("api_id")
	resp, err := lm.transition(c, apiID, StatePublished)
	if err != nil {
		status := http.StatusBadRequest
		if err == errAPINotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{
			Code:        status,
			Message:     "Failed to unblock API",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.LifecycleTransitionResponse{
		APIID:          resp.APIID,
		PreviousStatus: resp.PreviousStatus,
		CurrentStatus:  resp.CurrentStatus,
		Message:        fmt.Sprintf("API %s unblocked and restored to PUBLISHED", apiID),
		Timestamp:      time.Now(),
	})
}

// DeprecateAPI transitions a PUBLISHED/BLOCKED API to DEPRECATED.
func (lm *LifecycleManager) DeprecateAPI(c *gin.Context) {
	var req models.LifecycleTransitionRequest
	_ = c.ShouldBindJSON(&req)

	apiID := c.Param("api_id")
	resp, err := lm.transition(c, apiID, StateDeprecated)
	if err != nil {
		status := http.StatusBadRequest
		if err == errAPINotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{
			Code:        status,
			Message:     "Failed to deprecate API",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.LifecycleTransitionResponse{
		APIID:          resp.APIID,
		PreviousStatus: resp.PreviousStatus,
		CurrentStatus:  resp.CurrentStatus,
		Message:        fmt.Sprintf("API %s deprecated. %s", apiID, req.Reason),
		Timestamp:      time.Now(),
	})
}

// RetireAPI transitions an API to RETIRED (terminal state).
func (lm *LifecycleManager) RetireAPI(c *gin.Context) {
	var req models.LifecycleTransitionRequest
	_ = c.ShouldBindJSON(&req)

	apiID := c.Param("api_id")
	resp, err := lm.transition(c, apiID, StateRetired)
	if err != nil {
		status := http.StatusBadRequest
		if err == errAPINotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{
			Code:        status,
			Message:     "Failed to retire API",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.LifecycleTransitionResponse{
		APIID:          resp.APIID,
		PreviousStatus: resp.PreviousStatus,
		CurrentStatus:  resp.CurrentStatus,
		Message:        fmt.Sprintf("API %s retired and removed from gateway. %s", apiID, req.Reason),
		Timestamp:      time.Now(),
	})
}

// PrototypeAPI transitions an API to PROTOTYPED for testing.
func (lm *LifecycleManager) PrototypeAPI(c *gin.Context) {
	apiID := c.Param("api_id")
	resp, err := lm.transition(c, apiID, StatePrototyped)
	if err != nil {
		status := http.StatusBadRequest
		if err == errAPINotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{
			Code:        status,
			Message:     "Failed to prototype API",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, models.LifecycleTransitionResponse{
		APIID:          resp.APIID,
		PreviousStatus: resp.PreviousStatus,
		CurrentStatus:  resp.CurrentStatus,
		Message:        fmt.Sprintf("API %s moved to PROTOTYPED for testing", apiID),
		Timestamp:      time.Now(),
	})
}

// transition performs the actual state transition after validation.
func (lm *LifecycleManager) transition(c *gin.Context, apiID string, target models.APIStatus) (*models.LifecycleTransitionResponse, error) {
	if apiID == "" {
		return nil, fmt.Errorf("API ID is required")
	}

	api, err := lm.store.GetAPI(apiID)
	if err != nil {
		return nil, errAPINotFound
	}

	// Validate transition
	if !isValidTransition(api.Status, target) {
		return nil, fmt.Errorf("invalid transition from %s to %s", api.Status, target)
	}

	// Additional validation: cannot transition deleted APIs
	if api.Deleted {
		return nil, fmt.Errorf("cannot transition deleted API")
	}

	previousStatus := api.Status
	api.Status = target
	api.UpdatedAt = time.Now()

	userID, _ := c.Get("user_id")
	if uid, ok := userID.(string); ok && uid != "" {
		api.UpdatedBy = uid
	}

	if err := lm.store.TransitionAPIStatus(apiID, target); err != nil {
		return nil, fmt.Errorf("store transition failed: %w", err)
	}

	return &models.LifecycleTransitionResponse{
		APIID:          api.ID,
		PreviousStatus: previousStatus,
		CurrentStatus:  target,
		Message:        fmt.Sprintf("transitioned from %s to %s", previousStatus, target),
		Timestamp:      time.Now(),
	}, nil
}

// isValidTransition checks if a transition from current to target is allowed.
func isValidTransition(current, target models.APIStatus) bool {
	allowed, ok := validTransitions[current]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == target {
			return true
		}
	}
	return false
}

// GetAvailableTransitions returns the list of states that can be transitioned to
// from the given current state.
func GetAvailableTransitions(current models.APIStatus) []models.APIStatus {
	transitions, ok := validTransitions[current]
	if !ok {
		return []models.APIStatus{}
	}
	return transitions
}

// errAPINotFound is returned when an API is not found.
var errAPINotFound = fmt.Errorf("API not found")
