// Package webhook provides event types and the event publisher for the VedaDB API Manager webhook system.
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vedadb/vapim/pkg/models"
)

// ---- Event Type Constants ----

const (
	// API lifecycle events
	EventAPICreated    = "API_CREATED"
	EventAPIPublished  = "API_PUBLISHED"
	EventAPIDeprecated = "API_DEPRECATED"
	EventAPIDeleted    = "API_DELETED"
	EventAPIUpdated    = "API_UPDATED"

	// Subscription events
	EventSubscriptionCreated   = "SUBSCRIPTION_CREATED"
	EventSubscriptionCancelled = "SUBSCRIPTION_CANCELLED"
	EventSubscriptionBlocked   = "SUBSCRIPTION_BLOCKED"
	EventSubscriptionApproved  = "SUBSCRIPTION_APPROVED"

	// Application events
	EventAppCreated = "APP_CREATED"
	EventAppDeleted = "APP_DELETED"
	EventAppUpdated = "APP_UPDATED"

	// User events
	EventUserRegistered = "USER_REGISTERED"
	EventUserDeleted    = "USER_DELETED"
	EventUserUpdated    = "USER_UPDATED"

	// Policy events
	EventPolicyUpdated = "POLICY_UPDATED"
	EventPolicyCreated = "POLICY_CREATED"
	EventPolicyDeleted = "POLICY_DELETED"
)

// ---- Event Payload Structures ----

// Event is the envelope for all webhook events.
type Event struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Timestamp string                 `json:"timestamp"`
	TenantID  string                 `json:"tenantId"`
	Payload   map[string]interface{} `json:"payload"`
}

// EventPublisher defines the interface for publishing webhook events.
type EventPublisher interface {
	// Publish emits an event of the given type with the provided payload.
	Publish(ctx context.Context, eventType string, payload map[string]interface{}) error
	// PublishAPIEvent emits an API-related event.
	PublishAPIEvent(ctx context.Context, eventType string, api *models.API) error
	// PublishSubscriptionEvent emits a subscription-related event.
	PublishSubscriptionEvent(ctx context.Context, eventType string, sub *models.Subscription) error
	// PublishApplicationEvent emits an application-related event.
	PublishApplicationEvent(ctx context.Context, eventType string, app *models.Application) error
	// PublishUserEvent emits a user-related event.
	PublishUserEvent(ctx context.Context, eventType string, user *models.User) error
	// PublishPolicyEvent emits a policy-related event.
	PublishPolicyEvent(ctx context.Context, eventType string, policy *models.ThrottlePolicy) error
}

// DefaultEventPublisher is the production implementation of EventPublisher.
type DefaultEventPublisher struct {
	webhookManager WebhookManager
}

// NewEventPublisher creates a new event publisher backed by a webhook manager.
func NewEventPublisher(wm WebhookManager) *DefaultEventPublisher {
	return &DefaultEventPublisher{webhookManager: wm}
}

// Publish emits a generic event. It enriches the payload with standard metadata and delegates to the webhook manager.
func (p *DefaultEventPublisher) Publish(ctx context.Context, eventType string, payload map[string]interface{}) error {
	if payload == nil {
		payload = make(map[string]interface{})
	}

	event := Event{
		ID:        generateEventID(eventType),
		Type:      eventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload:   payload,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(eventJSON, &parsed); err != nil {
		return fmt.Errorf("unmarshal event to map: %w", err)
	}

	return p.webhookManager.Deliver(eventType, parsed)
}

// PublishAPIEvent emits an API-related event with the API entity embedded in the payload.
func (p *DefaultEventPublisher) PublishAPIEvent(ctx context.Context, eventType string, api *models.API) error {
	if api == nil {
		return fmt.Errorf("api is nil")
	}

	apiPayload := map[string]interface{}{
		"api": map[string]interface{}{
			"id":          api.ID,
			"name":        api.Name,
			"description": api.Description,
			"context":     api.Context,
			"version":     api.Version,
			"endpoint":    api.Endpoint,
			"authType":    api.AuthType,
			"status":      api.Status,
			"provider":    api.Provider,
			"tags":        api.Tags,
			"rating":      api.Rating,
			"createdAt":   api.CreatedAt.Format(time.RFC3339),
			"updatedAt":   api.UpdatedAt.Format(time.RFC3339),
		},
	}

	return p.Publish(ctx, eventType, apiPayload)
}

// PublishSubscriptionEvent emits a subscription-related event.
func (p *DefaultEventPublisher) PublishSubscriptionEvent(ctx context.Context, eventType string, sub *models.Subscription) error {
	if sub == nil {
		return fmt.Errorf("subscription is nil")
	}

	subPayload := map[string]interface{}{
		"subscription": map[string]interface{}{
			"id":            sub.ID,
			"apiId":         sub.APIID,
			"applicationId": sub.ApplicationID,
			"tier":          sub.Tier,
			"status":        sub.Status,
			"createdAt":     sub.CreatedAt.Format(time.RFC3339),
		},
	}

	if sub.API != nil {
		subPayload["api"] = map[string]interface{}{
			"id":   sub.API.ID,
			"name": sub.API.Name,
		}
	}

	if sub.Application != nil {
		subPayload["application"] = map[string]interface{}{
			"id":   sub.Application.ID,
			"name": sub.Application.Name,
		}
	}

	return p.Publish(ctx, eventType, subPayload)
}

// PublishApplicationEvent emits an application-related event.
func (p *DefaultEventPublisher) PublishApplicationEvent(ctx context.Context, eventType string, app *models.Application) error {
	if app == nil {
		return fmt.Errorf("application is nil")
	}

	appPayload := map[string]interface{}{
		"application": map[string]interface{}{
			"id":          app.ID,
			"name":        app.Name,
			"description": app.Description,
			"tier":        app.Tier,
			"status":      app.Status,
			"createdAt":   app.CreatedAt.Format(time.RFC3339),
		},
	}

	if app.Owner != nil {
		appPayload["owner"] = map[string]interface{}{
			"id":       app.Owner.ID,
			"username": app.Owner.Username,
			"email":    app.Owner.Email,
		}
	}

	return p.Publish(ctx, eventType, appPayload)
}

// PublishUserEvent emits a user-related event.
func (p *DefaultEventPublisher) PublishUserEvent(ctx context.Context, eventType string, user *models.User) error {
	if user == nil {
		return fmt.Errorf("user is nil")
	}

	userPayload := map[string]interface{}{
		"user": map[string]interface{}{
			"id":        user.ID,
			"username":  user.Username,
			"email":     user.Email,
			"role":      user.Role,
			"status":    user.Status,
			"createdAt": user.CreatedAt.Format(time.RFC3339),
		},
	}

	return p.Publish(ctx, eventType, userPayload)
}

// PublishPolicyEvent emits a throttle policy-related event.
func (p *DefaultEventPublisher) PublishPolicyEvent(ctx context.Context, eventType string, policy *models.ThrottlePolicy) error {
	if policy == nil {
		return fmt.Errorf("policy is nil")
	}

	policyPayload := map[string]interface{}{
		"policy": map[string]interface{}{
			"id":             policy.ID,
			"name":           policy.Name,
			"description":    policy.Description,
			"quotaType":      policy.QuotaType,
			"requestCount":   policy.RequestCount,
			"timeUnit":       policy.TimeUnit,
			"rateLimitCount": policy.RateLimitCount,
			"rateLimitUnit":  policy.RateLimitUnit,
			"isDeployed":     policy.IsDeployed,
			"createdAt":      policy.CreatedAt.Format(time.RFC3339),
			"updatedAt":      policy.UpdatedAt.Format(time.RFC3339),
		},
	}

	return p.Publish(ctx, eventType, policyPayload)
}

// EventFilter is used to filter webhook subscriptions by event type.
type EventFilter struct {
	// Include is a list of event types to include. If empty, all are included.
	Include []string
	// Exclude is a list of event types to exclude.
	Exclude []string
}

// Matches returns true if the given event type passes the filter.
func (f *EventFilter) Matches(eventType string) bool {
	// Check exclusions first
	for _, ex := range f.Exclude {
		if ex == eventType {
			return false
		}
	}

	// If no inclusions specified, accept all (except excluded)
	if len(f.Include) == 0 {
		return true
	}

	// Check inclusions
	for _, inc := range f.Include {
		if inc == eventType {
			return true
		}
	}
	return false
}

// AllEventTypes returns the full list of supported event types.
func AllEventTypes() []string {
	return []string{
		EventAPICreated,
		EventAPIPublished,
		EventAPIDeprecated,
		EventAPIDeleted,
		EventAPIUpdated,
		EventSubscriptionCreated,
		EventSubscriptionCancelled,
		EventSubscriptionBlocked,
		EventSubscriptionApproved,
		EventAppCreated,
		EventAppDeleted,
		EventAppUpdated,
		EventUserRegistered,
		EventUserDeleted,
		EventUserUpdated,
		EventPolicyUpdated,
		EventPolicyCreated,
		EventPolicyDeleted,
	}
}

// IsValidEventType checks if the given string is a known event type.
func IsValidEventType(eventType string) bool {
	for _, t := range AllEventTypes() {
		if t == eventType {
			return true
		}
	}
	return false
}

// generateEventID creates a unique event identifier.
func generateEventID(eventType string) string {
	return fmt.Sprintf("evt_%s_%d", eventType, time.Now().UnixNano())
}
