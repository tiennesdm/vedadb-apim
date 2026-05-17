// Package publisher implements policy management (CRUD, attach/detach from APIs,
// throttling and access control policy templates) for the VedaDB API Manager Publisher.
package publisher

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tiennesdm/vedadb-apim/internal/auth"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// PolicyManager handles policy CRUD and API attachment.
type PolicyManager struct {
	store Store
}

// NewPolicyManager creates a new PolicyManager.
func NewPolicyManager(store Store) *PolicyManager {
	return &PolicyManager{store: store}
}

// validPolicyTypes are the allowed policy type values.
var validPolicyTypes = map[string]bool{
	"api":          true,
	"application":  true,
	"subscription": true,
	"resource":     true,
}

// CreatePolicy creates a new throttling or access control policy.
func (pm *PolicyManager) CreatePolicy(c *gin.Context) {
	var req struct {
		Name        string                   `json:"name" binding:"required"`
		DisplayName string                   `json:"display_name"`
		Description string                   `json:"description"`
		Type        string                   `json:"type" binding:"required"`
		TierLevel   string                   `json:"tier_level"`
		Quota       *models.Quota            `json:"quota"`
		RateLimit   *models.RateLimit        `json:"rate_limit"`
		Conditions  []models.PolicyCondition `json:"conditions"`
		Metadata    map[string]string        `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	// Validate policy type
	if !validPolicyTypes[strings.ToLower(req.Type)] {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid policy type",
			Description: "Type must be one of: api, application, subscription, resource. Got: " + req.Type,
		})
		return
	}

	tenantID := auth.GetTenantID(c)
	now := time.Now()

	policy := &models.Policy{
		ID:          uuid.New().String(),
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Type:        strings.ToLower(req.Type),
		TierLevel:   req.TierLevel,
		Quota:       req.Quota,
		RateLimit:   req.RateLimit,
		Conditions:  req.Conditions,
		IsDeployed:  false,
		TenantID:    tenantID,
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    req.Metadata,
	}

	if policy.DisplayName == "" {
		policy.DisplayName = policy.Name
	}

	// Set defaults for quota if not provided
	if policy.Quota == nil {
		policy.Quota = &models.Quota{
			RequestCount:      1000,
			RequestCountUnit:  "min",
			DataBandwidth:     0,
			DataBandwidthUnit: "MB",
		}
	}

	if policy.RateLimit == nil {
		policy.RateLimit = &models.RateLimit{
			RequestsPerSecond: 100,
			BurstSize:         10,
		}
	}

	if err := pm.store.SavePolicy(policy); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to create policy",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, policy)
}

// GetPolicy retrieves a policy by ID.
func (pm *PolicyManager) GetPolicy(c *gin.Context) {
	policyID := c.Param("policy_id")
	if policyID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "Policy ID is required",
		})
		return
	}

	policy, err := pm.store.GetPolicy(policyID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Policy not found",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, policy)
}

// ListPolicies returns all policies, optionally filtered by type.
func (pm *PolicyManager) ListPolicies(c *gin.Context) {
	policyType := c.Query("type")
	if policyType != "" && !validPolicyTypes[strings.ToLower(policyType)] {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid policy type filter",
			Description: "Type must be one of: api, application, subscription, resource",
		})
		return
	}

	tenantID := auth.GetTenantID(c)
	policies, err := pm.store.ListPolicies(tenantID, policyType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to list policies",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"policies": policies,
		"total":    len(policies),
		"type":     policyType,
	})
}

// UpdatePolicy modifies an existing policy.
func (pm *PolicyManager) UpdatePolicy(c *gin.Context) {
	policyID := c.Param("policy_id")
	if policyID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "Policy ID is required",
		})
		return
	}

	existing, err := pm.store.GetPolicy(policyID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Policy not found",
			Description: err.Error(),
		})
		return
	}

	var req struct {
		Name        string                   `json:"name"`
		DisplayName string                   `json:"display_name"`
		Description string                   `json:"description"`
		Type        string                   `json:"type"`
		TierLevel   string                   `json:"tier_level"`
		Quota       *models.Quota            `json:"quota"`
		RateLimit   *models.RateLimit        `json:"rate_limit"`
		Conditions  []models.PolicyCondition `json:"conditions"`
		IsDeployed  *bool                    `json:"is_deployed"`
		Metadata    map[string]string        `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.DisplayName != "" {
		existing.DisplayName = req.DisplayName
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Type != "" {
		if !validPolicyTypes[strings.ToLower(req.Type)] {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Code:        http.StatusBadRequest,
				Message:     "Invalid policy type",
				Description: "Type must be one of: api, application, subscription, resource",
			})
			return
		}
		existing.Type = strings.ToLower(req.Type)
	}
	if req.TierLevel != "" {
		existing.TierLevel = req.TierLevel
	}
	if req.Quota != nil {
		existing.Quota = req.Quota
	}
	if req.RateLimit != nil {
		existing.RateLimit = req.RateLimit
	}
	if req.Conditions != nil {
		existing.Conditions = req.Conditions
	}
	if req.IsDeployed != nil {
		existing.IsDeployed = *req.IsDeployed
	}
	if req.Metadata != nil {
		existing.Metadata = req.Metadata
	}
	existing.UpdatedAt = time.Now()

	if err := pm.store.UpdatePolicy(existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to update policy",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, existing)
}

// DeletePolicy removes a policy.
func (pm *PolicyManager) DeletePolicy(c *gin.Context) {
	policyID := c.Param("policy_id")
	if policyID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "Policy ID is required",
		})
		return
	}

	if err := pm.store.DeletePolicy(policyID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to delete policy",
			Description: err.Error(),
		})
		return
	}

	c.Status(http.StatusNoContent)
}

// AttachPolicy attaches a policy to an API.
func (pm *PolicyManager) AttachPolicy(c *gin.Context) {
	apiID := c.Param("api_id")
	policyID := c.Param("policy_id")
	if apiID == "" || policyID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID and Policy ID are required",
		})
		return
	}

	// Verify API exists
	api, err := pm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	// Verify policy exists
	policy, err := pm.store.GetPolicy(policyID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Policy not found",
			Description: err.Error(),
		})
		return
	}

	// Check if already attached
	for _, p := range api.Policies {
		if p == policyID {
			c.JSON(http.StatusConflict, models.ErrorResponse{
				Code:        http.StatusConflict,
				Message:     "Policy already attached",
				Description: fmt.Sprintf("Policy %s is already attached to API %s", policyID, apiID),
			})
			return
		}
	}

	// Attach policy
	api.Policies = append(api.Policies, policyID)
	api.UpdatedAt = time.Now()

	if err := pm.store.UpdateAPI(api); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to attach policy",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":    apiID,
		"policy_id": policyID,
		"policy":    policy.Name,
		"status":    "attached",
	})
}

// DetachPolicy removes a policy from an API.
func (pm *PolicyManager) DetachPolicy(c *gin.Context) {
	apiID := c.Param("api_id")
	policyID := c.Param("policy_id")
	if apiID == "" || policyID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID and Policy ID are required",
		})
		return
	}

	// Verify API exists
	api, err := pm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	// Find and remove policy
	found := false
	var newPolicies []string
	for _, p := range api.Policies {
		if p == policyID {
			found = true
			continue
		}
		newPolicies = append(newPolicies, p)
	}

	if !found {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Policy not attached",
			Description: fmt.Sprintf("Policy %s is not attached to API %s", policyID, apiID),
		})
		return
	}

	api.Policies = newPolicies
	api.UpdatedAt = time.Now()

	if err := pm.store.UpdateAPI(api); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to detach policy",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":    apiID,
		"policy_id": policyID,
		"status":    "detached",
	})
}

// ListAttachedPolicies returns policies attached to an API.
func (pm *PolicyManager) ListAttachedPolicies(c *gin.Context) {
	apiID := c.Param("api_id")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: "API ID is required",
		})
		return
	}

	api, err := pm.store.GetAPI(apiID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "API not found",
			Description: err.Error(),
		})
		return
	}

	var policies []models.Policy
	for _, pid := range api.Policies {
		p, err := pm.store.GetPolicy(pid)
		if err != nil {
			continue
		}
		policies = append(policies, *p)
	}

	c.JSON(http.StatusOK, gin.H{
		"api_id":   apiID,
		"policies": policies,
		"total":    len(policies),
	})
}

// PolicyTemplate provides pre-configured policy templates.
type PolicyTemplate struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	Description string        `json:"description"`
	Type        string        `json:"type"`
	Quota       models.Quota  `json:"quota"`
	RateLimit   models.RateLimit `json:"rate_limit"`
}

// GetPolicyTemplates returns built-in policy templates.
func GetPolicyTemplates() []PolicyTemplate {
	return []PolicyTemplate{
		{
			Name:        "Bronze",
			DisplayName: "Bronze Tier",
			Description: "1 request per second, 1000 requests per minute",
			Type:        "api",
			Quota: models.Quota{
				RequestCount:      1000,
				RequestCountUnit:  "min",
				DataBandwidth:     10,
				DataBandwidthUnit: "MB",
			},
			RateLimit: models.RateLimit{
				RequestsPerSecond: 1,
				BurstSize:         2,
			},
		},
		{
			Name:        "Silver",
			DisplayName: "Silver Tier",
			Description: "10 requests per second, 10000 requests per minute",
			Type:        "api",
			Quota: models.Quota{
				RequestCount:      10000,
				RequestCountUnit:  "min",
				DataBandwidth:     100,
				DataBandwidthUnit: "MB",
			},
			RateLimit: models.RateLimit{
				RequestsPerSecond: 10,
				BurstSize:         20,
			},
		},
		{
			Name:        "Gold",
			DisplayName: "Gold Tier",
			Description: "100 requests per second, 100000 requests per minute",
			Type:        "api",
			Quota: models.Quota{
				RequestCount:      100000,
				RequestCountUnit:  "min",
				DataBandwidth:     1024,
				DataBandwidthUnit: "MB",
			},
			RateLimit: models.RateLimit{
				RequestsPerSecond: 100,
				BurstSize:         200,
			},
		},
		{
			Name:        "Unlimited",
			DisplayName: "Unlimited Tier",
			Description: "Unlimited requests",
			Type:        "api",
			Quota: models.Quota{
				RequestCount:      0,
				RequestCountUnit:  "min",
				DataBandwidth:     0,
				DataBandwidthUnit: "MB",
			},
			RateLimit: models.RateLimit{
				RequestsPerSecond: 0,
				BurstSize:         0,
			},
		},
		{
			Name:        "BasicAuth",
			DisplayName: "Basic Authentication",
			Description: "Enforce basic authentication",
			Type:        "resource",
			Quota: models.Quota{
				RequestCount:      1000,
				RequestCountUnit:  "min",
				DataBandwidth:     10,
				DataBandwidthUnit: "MB",
			},
			RateLimit: models.RateLimit{
				RequestsPerSecond: 10,
				BurstSize:         5,
			},
		},
	}
}

// CreatePolicyFromTemplate creates a policy from a template.
func (pm *PolicyManager) CreatePolicyFromTemplate(c *gin.Context) {
	var req struct {
		TemplateName string            `json:"template_name" binding:"required"`
		Name         string            `json:"name"`
		TenantID     string            `json:"tenant_id"`
		Metadata     map[string]string `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:        http.StatusBadRequest,
			Message:     "Invalid request body",
			Description: err.Error(),
		})
		return
	}

	templates := GetPolicyTemplates()
	var template *PolicyTemplate
	for _, t := range templates {
		if t.Name == req.TemplateName {
			template = &t
			break
		}
	}

	if template == nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Code:        http.StatusNotFound,
			Message:     "Template not found",
			Description: fmt.Sprintf("Policy template '%s' does not exist", req.TemplateName),
		})
		return
	}

	name := req.Name
	if name == "" {
		name = template.Name + "-" + uuid.New().String()[:8]
	}

	tenantID := auth.GetTenantID(c)
	now := time.Now()

	policy := &models.Policy{
		ID:          uuid.New().String(),
		Name:        name,
		DisplayName: template.DisplayName,
		Description: template.Description,
		Type:        template.Type,
		Quota:       &template.Quota,
		RateLimit:   &template.RateLimit,
		IsDeployed:  false,
		TenantID:    tenantID,
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    req.Metadata,
	}

	if err := pm.store.SavePolicy(policy); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:        http.StatusInternalServerError,
			Message:     "Failed to create policy from template",
			Description: err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, policy)
}
