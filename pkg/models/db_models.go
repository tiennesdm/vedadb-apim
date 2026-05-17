// Package models defines additional DB-facing models for the VedaDB API Manager store layer.
package models

import (
	"time"
)

// TenantDB is the database representation of a tenant.
type TenantDB struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Domain    string    `json:"domain"`
	Tier      string    `json:"tier"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserDB is the database representation of a user.
type UserDB struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// APIDB is the database representation of an API.
type APIDB struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Context        string    `json:"context"`
	Version        string    `json:"version"`
	Endpoint       string    `json:"endpoint"`
	AuthType       string    `json:"auth_type"`
	Status         string    `json:"status"`
	Provider       string    `json:"provider"`
	Tags           string    `json:"tags"`
	ThumbnailURL   string    `json:"thumbnail_url"`
	Rating         float64   `json:"rating"`
	RatingCount    int       `json:"rating_count"`
	Visibility     string    `json:"visibility"`
	ThrottlePolicy string    `json:"throttle_policy"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// APIResourceDB is the database representation of an API resource.
type APIResourceDB struct {
	ID           string    `json:"id"`
	APIID        string    `json:"api_id"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Description  string    `json:"description"`
	AuthRequired bool      `json:"auth_required"`
	ThrottlePolicy string  `json:"throttle_policy"`
	CreatedAt    time.Time `json:"created_at"`
}

// APIVersionDB is the database representation of an API version.
type APIVersionDB struct {
	ID         string    `json:"id"`
	APIID      string    `json:"api_id"`
	Version    string    `json:"version"`
	Definition string    `json:"definition"`
	Status     string    `json:"status"`
	IsDefault  bool      `json:"is_default"`
	CreatedAt  time.Time `json:"created_at"`
}

// ApplicationDB is the database representation of an application.
type ApplicationDB struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	OwnerID     string    `json:"owner_id"`
	Tier        string    `json:"tier"`
	Status      string    `json:"status"`
	CallbackURL string    `json:"callback_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ApplicationKeyDB is the database representation of an application key.
type ApplicationKeyDB struct {
	ID            string     `json:"id"`
	AppID         string     `json:"app_id"`
	KeyType       string     `json:"key_type"`
	ConsumerKey   string     `json:"consumer_key"`
	ConsumerSecret string    `json:"consumer_secret"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// SubscriptionDB is the database representation of a subscription.
type SubscriptionDB struct {
	ID        string    `json:"id"`
	APIID     string    `json:"api_id"`
	AppID     string    `json:"app_id"`
	Tier      string    `json:"tier"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OAuth2ClientDB is the database representation of an OAuth2 client.
type OAuth2ClientDB struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	Name         string    `json:"name"`
	RedirectURIs string    `json:"redirect_uris"`
	GrantTypes   string    `json:"grant_types"`
	Scopes       string    `json:"scopes"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// TokenDB is the database representation of a token.
type TokenDB struct {
	ID        string     `json:"id"`
	Token     string     `json:"token"`
	TokenType string     `json:"token_type"`
	ClientID  string     `json:"client_id"`
	UserID    string     `json:"user_id"`
	Scopes    string     `json:"scopes"`
	ExpiresAt time.Time  `json:"expires_at"`
	Revoked   bool       `json:"revoked"`
	CreatedAt time.Time  `json:"created_at"`
}

// APIKeyDB is the database representation of an API key.
type APIKeyDB struct {
	ID         string     `json:"id"`
	AppID      string     `json:"app_id"`
	KeyHash    string     `json:"key_hash"`
	Name       string     `json:"name"`
	Scopes     string     `json:"scopes"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ThrottlePolicyDB is the database representation of a throttle policy.
type ThrottlePolicyDB struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Rate       int       `json:"rate"`
	Burst      int       `json:"burst"`
	Unit       string    `json:"unit"`
	Conditions string    `json:"conditions"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AnalyticsEventDB is the database representation of an analytics event.
type AnalyticsEventDB struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	RequestID    string    `json:"request_id"`
	APIID        string    `json:"api_id"`
	AppID        string    `json:"app_id"`
	UserID       string    `json:"user_id"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	StatusCode   int       `json:"status_code"`
	LatencyMs    int       `json:"latency_ms"`
	ErrorMessage string    `json:"error_message"`
	UserAgent    string    `json:"user_agent"`
	ClientIP     string    `json:"client_ip"`
	Country      string    `json:"country"`
	Timestamp    time.Time `json:"timestamp"`
}

// AnalyticsSummaryDB is the database representation of analytics summary.
type AnalyticsSummaryDB struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	APIID        string    `json:"api_id"`
	Period       string    `json:"period"`
	PeriodStart  time.Time `json:"period_start"`
	RequestCount int       `json:"request_count"`
	ErrorCount   int       `json:"error_count"`
	AvgLatencyMs int       `json:"avg_latency_ms"`
	P95LatencyMs int       `json:"p95_latency_ms"`
	P99LatencyMs int       `json:"p99_latency_ms"`
	UniqueUsers  int       `json:"unique_users"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuditLogDB is the database representation of an audit log entry.
type AuditLogDB struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	UserID       string    `json:"user_id"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Details      string    `json:"details"`
	IPAddress    string    `json:"ip_address"`
	UserAgent    string    `json:"user_agent"`
	Timestamp    time.Time `json:"timestamp"`
}

// WebhookDB is the database representation of a webhook.
type WebhookDB struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	APIID     string    `json:"api_id"`
	URL       string    `json:"url"`
	Events    string    `json:"events"`
	Secret    string    `json:"secret"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WebhookDeliveryDB is the database representation of a webhook delivery.
type WebhookDeliveryDB struct {
	ID             string    `json:"id"`
	WebhookID      string    `json:"webhook_id"`
	EventType      string    `json:"event_type"`
	Payload        string    `json:"payload"`
	ResponseStatus int       `json:"response_status"`
	ResponseBody   string    `json:"response_body"`
	AttemptCount   int       `json:"attempt_count"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// SchemaMigration tracks applied migrations.
type SchemaMigration struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	AppliedAt  time.Time `json:"applied_at"`
}

// APISummary is used for GetTopAPIs response.
type APISummary struct {
	APIID      string  `json:"api_id"`
	APIName    string  `json:"api_name"`
	RequestCount int64 `json:"request_count"`
	ErrorCount int64   `json:"error_count"`
	AvgLatency int64   `json:"avg_latency"`
}

// UsageDataPoint is a single data point for API usage over time.
type UsageDataPoint struct {
	Timestamp    time.Time `json:"timestamp"`
	RequestCount int64     `json:"request_count"`
	ErrorCount   int64     `json:"error_count"`
	AvgLatencyMs int64     `json:"avg_latency_ms"`
}
