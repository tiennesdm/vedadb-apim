// Package models provides the domain models for the VedaDB API Manager GraphQL layer.
package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// APIStatus represents the possible states of an API.
type APIStatus string

const (
	APIStatusCreated    APIStatus = "CREATED"
	APIStatusPublished  APIStatus = "PUBLISHED"
	APIStatusDeprecated APIStatus = "DEPRECATED"
	APIStatusRetired    APIStatus = "RETIRED"
	APIStatusBlocked    APIStatus = "BLOCKED"
)

// SubscriptionStatus represents the possible states of a subscription.
type SubscriptionStatus string

const (
	SubscriptionStatusActive    SubscriptionStatus = "ACTIVE"
	SubscriptionStatusBlocked   SubscriptionStatus = "BLOCKED"
	SubscriptionStatusCancelled SubscriptionStatus = "CANCELLED"
	SubscriptionStatusPending   SubscriptionStatus = "PENDING"
)

// UserRole represents the possible roles for a user.
type UserRole string

const (
	UserRoleAdmin       UserRole = "ADMIN"
	UserRolePublisher   UserRole = "PUBLISHER"
	UserRoleSubscriber  UserRole = "SUBSCRIBER"
	UserRoleReadOnly    UserRole = "READONLY"
	UserRoleAPIConsumer UserRole = "API_CONSUMER"
)

// UserStatus represents the possible states of a user.
type UserStatus string

const (
	UserStatusActive   UserStatus = "ACTIVE"
	UserStatusInactive UserStatus = "INACTIVE"
	UserStatusBlocked  UserStatus = "BLOCKED"
	UserStatusDeleted  UserStatus = "DELETED"
)

// TenantStatus represents the possible states of a tenant.
type TenantStatus string

const (
	TenantStatusActive   TenantStatus = "ACTIVE"
	TenantStatusInactive TenantStatus = "INACTIVE"
	TenantStatusSuspended TenantStatus = "SUSPENDED"
)

// API represents an API entity in the system.
type API struct {
	ID            string          `db:"id" json:"id"`
	Name          string          `db:"name" json:"name"`
	Description   *string         `db:"description" json:"description,omitempty"`
	Context       string          `db:"context" json:"context"`
	Version       string          `db:"version" json:"version"`
	Endpoint      string          `db:"endpoint" json:"endpoint"`
	AuthType      string          `db:"auth_type" json:"authType"`
	Status        APIStatus       `db:"status" json:"status"`
	Provider      *string         `db:"provider" json:"provider,omitempty"`
	Tags          StringSlice     `db:"tags" json:"tags,omitempty"`
	Rating        *float64        `db:"rating" json:"rating,omitempty"`
	TenantID      string          `db:"tenant_id" json:"tenantId"`
	CreatedAt     time.Time       `db:"created_at" json:"createdAt"`
	UpdatedAt     time.Time       `db:"updated_at" json:"updatedAt"`
	Resources     []*APIResource  `json:"resources,omitempty"`
	Versions      []*APIVersion   `json:"versions,omitempty"`
	Subscriptions []*Subscription `json:"subscriptions,omitempty"`
}

// APIResource represents a single resource/operation of an API.
type APIResource struct {
	ID           string    `db:"id" json:"id"`
	APIID        string    `db:"api_id" json:"apiId"`
	Method       string    `db:"method" json:"method"`
	Path         string    `db:"path" json:"path"`
	Description  *string   `db:"description" json:"description,omitempty"`
	AuthRequired bool      `db:"auth_required" json:"authRequired"`
	TenantID     string    `db:"tenant_id" json:"tenantId"`
	CreatedAt    time.Time `db:"created_at" json:"createdAt"`
}

// APIVersion represents a version entry for an API.
type APIVersion struct {
	ID         string    `db:"id" json:"id"`
	APIID      string    `db:"api_id" json:"apiId"`
	Version    string    `db:"version" json:"version"`
	Status     string    `db:"status" json:"status"`
	TenantID   string    `db:"tenant_id" json:"tenantId"`
	CreatedAt  time.Time `db:"created_at" json:"createdAt"`
}

// Application represents a developer application.
type Application struct {
	ID            string          `db:"id" json:"id"`
	Name          string          `db:"name" json:"name"`
	Description   *string         `db:"description" json:"description,omitempty"`
	OwnerID       string          `db:"owner_id" json:"ownerId"`
	Tier          string          `db:"tier" json:"tier"`
	Status        string          `db:"status" json:"status"`
	TenantID      string          `db:"tenant_id" json:"tenantId"`
	CreatedAt     time.Time       `db:"created_at" json:"createdAt"`
	Owner         *User           `json:"owner,omitempty"`
	Keys          []*ApplicationKey `json:"keys,omitempty"`
}

// ApplicationKey represents an API key for an application.
type ApplicationKey struct {
	ID            string    `db:"id" json:"id"`
	ApplicationID string    `db:"application_id" json:"applicationId"`
	KeyType       string    `db:"key_type" json:"keyType"`
	Token         string    `db:"token" json:"token"`
	Validity      int64     `db:"validity" json:"validity"`
	TenantID      string    `db:"tenant_id" json:"tenantId"`
	CreatedAt     time.Time `db:"created_at" json:"createdAt"`
}

// User represents a user in the system.
type User struct {
	ID        string     `db:"id" json:"id"`
	Username  string     `db:"username" json:"username"`
	Email     string     `db:"email" json:"email"`
	Role      UserRole   `db:"role" json:"role"`
	Status    UserStatus `db:"status" json:"status"`
	TenantID  string     `db:"tenant_id" json:"tenantId"`
	CreatedAt time.Time  `db:"created_at" json:"createdAt"`
}

// Subscription represents an API subscription by an application.
type Subscription struct {
	ID            string             `db:"id" json:"id"`
	APIID         string             `db:"api_id" json:"apiId"`
	ApplicationID string             `db:"application_id" json:"applicationId"`
	Tier          string             `db:"tier" json:"tier"`
	Status        SubscriptionStatus `db:"status" json:"status"`
	TenantID      string             `db:"tenant_id" json:"tenantId"`
	CreatedAt     time.Time          `db:"created_at" json:"createdAt"`
	API           *API               `json:"api,omitempty"`
	Application   *Application       `json:"application,omitempty"`
}

// AnalyticsSummary represents aggregated analytics data for an API.
type AnalyticsSummary struct {
	APIID       string  `db:"api_id" json:"apiId"`
	APIName     string  `db:"api_name" json:"apiName"`
	RequestCount int64  `db:"request_count" json:"requestCount"`
	ErrorCount  int64   `db:"error_count" json:"errorCount"`
	AvgLatency  int64   `db:"avg_latency" json:"avgLatency"`
	P95Latency  int64   `db:"p95_latency" json:"p95Latency"`
	P99Latency  int64   `db:"p99_latency" json:"p99Latency"`
	UniqueUsers int64   `db:"unique_users" json:"uniqueUsers"`
}

// ThrottlePolicy represents a rate limiting / throttling policy.
type ThrottlePolicy struct {
	ID              string    `db:"id" json:"id"`
	Name            string    `db:"name" json:"name"`
	Description     *string   `db:"description" json:"description,omitempty"`
	QuotaType       string    `db:"quota_type" json:"quotaType"`
	RequestCount    int64     `db:"request_count" json:"requestCount"`
	TimeUnit        string    `db:"time_unit" json:"timeUnit"`
	RateLimitCount  int64     `db:"rate_limit_count" json:"rateLimitCount"`
	RateLimitUnit   string    `db:"rate_limit_unit" json:"rateLimitUnit"`
	IsDeployed      bool      `db:"is_deployed" json:"isDeployed"`
	TenantID        string    `db:"tenant_id" json:"tenantId"`
	CreatedAt       time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt       time.Time `db:"updated_at" json:"updatedAt"`
}

// Webhook represents a registered webhook endpoint.
type Webhook struct {
	ID          string    `db:"id" json:"id"`
	APIID       *string   `db:"api_id" json:"apiId,omitempty"`
	Name        string    `db:"name" json:"name"`
	CallbackURL string    `db:"callback_url" json:"callbackUrl"`
	Secret      string    `db:"secret" json:"secret"`
	EventTypes  StringSlice `db:"event_types" json:"eventTypes"`
	Active      bool      `db:"active" json:"active"`
	TenantID    string    `db:"tenant_id" json:"tenantId"`
	CreatedAt   time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt   time.Time `db:"updated_at" json:"updatedAt"`
}

// WebhookDelivery represents a single webhook delivery attempt.
type WebhookDelivery struct {
	ID           string    `db:"id" json:"id"`
	WebhookID    string    `db:"webhook_id" json:"webhookId"`
	EventType    string    `db:"event_type" json:"eventType"`
	Payload      JSONMap   `db:"payload" json:"payload"`
	Status       string    `db:"status" json:"status"`
	HTTPStatus   *int      `db:"http_status" json:"httpStatus,omitempty"`
	ResponseBody *string   `db:"response_body" json:"responseBody,omitempty"`
	ErrorMessage *string   `db:"error_message" json:"errorMessage,omitempty"`
	AttemptCount int       `db:"attempt_count" json:"attemptCount"`
	NextRetryAt  *time.Time `db:"next_retry_at" json:"nextRetryAt,omitempty"`
	CompletedAt  *time.Time `db:"completed_at" json:"completedAt,omitempty"`
	TenantID     string    `db:"tenant_id" json:"tenantId"`
	CreatedAt    time.Time `db:"created_at" json:"createdAt"`
}

// AuditLog represents a single audit log entry.
type AuditLog struct {
	ID           string    `db:"id" json:"id"`
	Action       string    `db:"action" json:"action"`
	ResourceType string    `db:"resource_type" json:"resourceType"`
	ResourceID   string    `db:"resource_id" json:"resourceId"`
	UserID       *string   `db:"user_id" json:"userId,omitempty"`
	Username     *string   `db:"username" json:"username,omitempty"`
	IPAddress    *string   `db:"ip_address" json:"ipAddress,omitempty"`
	Details      JSONMap   `db:"details" json:"details"`
	TenantID     string    `db:"tenant_id" json:"tenantId"`
	CreatedAt    time.Time `db:"created_at" json:"createdAt"`
}

// Tenant represents a tenant / organization in the multi-tenant system.
type Tenant struct {
	ID          string     `db:"id" json:"id"`
	Name        string     `db:"name" json:"name"`
	Slug        string     `db:"slug" json:"slug"`
	Domain      *string    `db:"domain" json:"domain,omitempty"`
	Status      TenantStatus `db:"status" json:"status"`
	Plan        string     `db:"plan" json:"plan"`
	MaxAPIs     int        `db:"max_apis" json:"maxApis"`
	MaxApps     int        `db:"max_apps" json:"maxApps"`
	Settings    JSONMap    `db:"settings" json:"settings"`
	CreatedAt   time.Time  `db:"created_at" json:"createdAt"`
	UpdatedAt   time.Time  `db:"updated_at" json:"updatedAt"`
}

// ---- GraphQL Input Types ----

// CreateAPIInput is the input for creating a new API.
type CreateAPIInput struct {
	Name        string   `json:"name"`
	Description *string  `json:"description,omitempty"`
	Context     string   `json:"context"`
	Version     string   `json:"version"`
	Endpoint    string   `json:"endpoint"`
	AuthType    string   `json:"authType"`
	Provider    *string  `json:"provider,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// UpdateAPIInput is the input for updating an existing API.
type UpdateAPIInput struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Context     *string  `json:"context,omitempty"`
	Version     *string  `json:"version,omitempty"`
	Endpoint    *string  `json:"endpoint,omitempty"`
	AuthType    *string  `json:"authType,omitempty"`
	Provider    *string  `json:"provider,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Status      *string  `json:"status,omitempty"`
}

// CreateAppInput is the input for creating a new application.
type CreateAppInput struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Tier        string  `json:"tier"`
}

// CreateUserInput is the input for creating a new user.
type CreateUserInput struct {
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Role     UserRole `json:"role"`
}

// CreatePolicyInput is the input for creating a throttle policy.
type CreatePolicyInput struct {
	Name           string `json:"name"`
	Description    *string `json:"description,omitempty"`
	QuotaType      string `json:"quotaType"`
	RequestCount   int64  `json:"requestCount"`
	TimeUnit       string `json:"timeUnit"`
	RateLimitCount int64  `json:"rateLimitCount"`
	RateLimitUnit  string `json:"rateLimitUnit"`
}

// CreateWebhookInput is the input for registering a webhook.
type CreateWebhookInput struct {
	APIID       *string  `json:"apiId,omitempty"`
	Name        string   `json:"name"`
	CallbackURL string   `json:"callbackUrl"`
	Secret      string   `json:"secret"`
	EventTypes  []string `json:"eventTypes"`
}

// AuditLogEntry is the GraphQL-facing audit log entry type.
type AuditLogEntry struct {
	ID           string                 `json:"id"`
	Action       string                 `json:"action"`
	ResourceType string                 `json:"resourceType"`
	ResourceID   string                 `json:"resourceId"`
	User         *User                  `json:"user,omitempty"`
	IPAddress    *string                `json:"ipAddress,omitempty"`
	Details      map[string]interface{} `json:"details"`
	CreatedAt    string                 `json:"createdAt"`
}

// ---- Custom Types ----

// StringSlice is a custom type for handling string arrays in PostgreSQL.
type StringSlice []string

// Value implements the driver.Valuer interface.
func (s StringSlice) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(s)
}

// Scan implements the sql.Scanner interface.
func (s *StringSlice) Scan(value interface{}) error {
	if value == nil {
		*s = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, s)
	case string:
		return json.Unmarshal([]byte(v), s)
	default:
		return errors.New("unsupported type for StringSlice")
	}
}

// JSONMap is a custom type for handling JSONB/map data in PostgreSQL.
type JSONMap map[string]interface{}

// Value implements the driver.Valuer interface.
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

// Scan implements the sql.Scanner interface.
func (m *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*m = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, m)
	case string:
		return json.Unmarshal([]byte(v), m)
	default:
		return errors.New("unsupported type for JSONMap")
	}
}
