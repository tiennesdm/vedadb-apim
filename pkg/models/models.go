// Package models defines all shared data models for the VedaDB API Manager.
package models

import (
	"time"
)

// APIStatus represents the lifecycle state of an API.
type APIStatus string

const (
	StatusCreated    APIStatus = "CREATED"
	StatusPrototyped APIStatus = "PROTOTYPED"
	StatusPublished  APIStatus = "PUBLISHED"
	StatusBlocked    APIStatus = "BLOCKED"
	StatusDeprecated APIStatus = "DEPRECATED"
	StatusRetired    APIStatus = "RETIRED"
)

// Role represents a user role in the system.
type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RolePublisher  Role = "publisher"
	RoleSubscriber Role = "subscriber"
	RoleAnonymous  Role = "anonymous"
)

// APICreateRequest is the payload for creating an API.
type APICreateRequest struct {
	Name        string            `json:"name" binding:"required"`
	Context     string            `json:"context" binding:"required"`
	Version     string            `json:"version" binding:"required"`
	Endpoint    string            `json:"endpoint" binding:"required,url"`
	AuthType    string            `json:"auth_type" binding:"required,oneof=oauth2 apikey none mutualtls"`
	Policies    []string          `json:"policies"`
	Tags        []string          `json:"tags"`
	Description string            `json:"description"`
	Visibility  string            `json:"visibility" binding:"oneof=PUBLIC PRIVATE RESTRICTED"`
	Resources   []APIResource     `json:"resources"`
	Metadata    map[string]string `json:"metadata"`
}

// APIUpdateRequest is the payload for updating an API.
type APIUpdateRequest struct {
	Name        string            `json:"name"`
	Endpoint    string            `json:"endpoint" binding:"omitempty,url"`
	AuthType    string            `json:"auth_type" binding:"omitempty,oneof=oauth2 apikey none mutualtls"`
	Policies    []string          `json:"policies"`
	Tags        []string          `json:"tags"`
	Description string            `json:"description"`
	Visibility  string            `json:"visibility" binding:"omitempty,oneof=PUBLIC PRIVATE RESTRICTED"`
	Resources   []APIResource     `json:"resources"`
	Metadata    map[string]string `json:"metadata"`
	Status      APIStatus         `json:"status"`
}

// API represents a published API.
type API struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Context      string            `json:"context"`
	Version      string            `json:"version"`
	Endpoint     string            `json:"endpoint"`
	AuthType     string            `json:"auth_type"`
	Status       APIStatus         `json:"status"`
	Policies     []string          `json:"policies"`
	Tags         []string          `json:"tags"`
	Description  string            `json:"description"`
	Visibility   string            `json:"visibility"`
	Resources    []APIResource     `json:"resources"`
	Metadata     map[string]string `json:"metadata"`
	IsDefault    bool              `json:"is_default_version"`
	CreatedBy    string            `json:"created_by"`
	UpdatedBy    string            `json:"updated_by"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Deleted      bool              `json:"deleted"`
	TenantID     string            `json:"tenant_id"`
	VersionSetID string            `json:"version_set_id"`
}

// APIResource represents an HTTP resource of an API.
type APIResource struct {
	ID           string            `json:"id"`
	APIID        string            `json:"api_id"`
	Path         string            `json:"path" binding:"required"`
	Methods      []string          `json:"methods" binding:"required,dive,oneof=GET POST PUT DELETE PATCH HEAD OPTIONS"`
	AuthRequired bool              `json:"auth_required"`
	Throttling   string            `json:"throttling_policy"`
	Policies     []string          `json:"policies"`
	Parameters   []PathParameter   `json:"parameters"`
	Produces     []string          `json:"produces"`
	Consumes     []string          `json:"consumes"`
	Description  string            `json:"description"`
	Metadata     map[string]string `json:"metadata"`
}

// PathParameter represents a path parameter in a resource.
type PathParameter struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// APIListResponse is returned when listing APIs.
type APIListResponse struct {
	APIs    []API `json:"apis"`
	Total   int   `json:"total"`
	Offset  int   `json:"offset"`
	Limit   int   `json:"limit"`
	HasMore bool  `json:"has_more"`
}

// Policy represents a throttling or access control policy.
type Policy struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description"`
	Type        string            `json:"type" binding:"oneof=api application subscription resource"`
	TierLevel   string            `json:"tier_level"`
	Quota       *Quota            `json:"quota"`
	RateLimit   *RateLimit        `json:"rate_limit"`
	Conditions  []PolicyCondition `json:"conditions"`
	IsDeployed  bool              `json:"is_deployed"`
	TenantID    string            `json:"tenant_id"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Metadata    map[string]string `json:"metadata"`
}

// Quota defines request count / bandwidth limits.
type Quota struct {
	RequestCount     int    `json:"request_count"`
	RequestCountUnit string `json:"request_count_unit"` // "sec", "min", "hour", "day"
	DataBandwidth    int64  `json:"data_bandwidth"`
	DataBandwidthUnit string `json:"data_bandwidth_unit"` // "KB", "MB", "GB"
}

// RateLimit defines burst rate limiting.
type RateLimit struct {
	RequestsPerSecond int `json:"requests_per_second"`
	BurstSize         int `json:"burst_size"`
}

// PolicyCondition defines when a policy applies.
type PolicyCondition struct {
	HeaderName  string `json:"header_name"`
	HeaderValue string `json:"header_value"`
	IPRange     string `json:"ip_range"`
}

// OAuth2Client represents a registered OAuth2 client.
type OAuth2Client struct {
	ID                   string            `json:"client_id"`
	Secret               string            `json:"client_secret"`
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	CallbackURLs         []string          `json:"callback_urls"`
	GrantTypes           []string          `json:"grant_types"`
	Scopes               []string          `json:"scopes"`
	Owner                string            `json:"owner"`
	TenantID             string            `json:"tenant_id"`
	AccessTokenLifetime  int               `json:"access_token_lifetime"`  // seconds
	RefreshTokenLifetime int               `json:"refresh_token_lifetime"` // seconds
	IDTokenLifetime      int               `json:"id_token_lifetime"`    // seconds
	RequirePKCE          bool              `json:"require_pkce"`
	Metadata             map[string]string `json:"metadata"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	Enabled              bool              `json:"enabled"`
}

// ClientRegisterRequest is used to register a new OAuth2 client.
type ClientRegisterRequest struct {
	Name         string   `json:"name" binding:"required"`
	Description  string   `json:"description"`
	CallbackURLs []string `json:"callback_urls"`
	GrantTypes   []string `json:"grant_types" binding:"required,dive,oneof=client_credentials password authorization_code refresh_token"`
	Scopes       []string `json:"scopes"`
	RequirePKCE  bool     `json:"require_pkce"`
	Owner        string   `json:"owner" binding:"required"`
	TenantID     string   `json:"tenant_id"`
}

// TokenRequest represents an OAuth2 token request.
type TokenRequest struct {
	GrantType    string `form:"grant_type" binding:"required,oneof=client_credentials password authorization_code refresh_token"`
	ClientID     string `form:"client_id" binding:"required"`
	ClientSecret string `form:"client_secret" binding:"required"`
	Username     string `form:"username"`
	Password     string `form:"password"`
	Code         string `form:"code"`
	RedirectURI  string `form:"redirect_uri"`
	RefreshToken string `form:"refresh_token"`
	Scope        string `form:"scope"`
	CodeVerifier string `form:"code_verifier"`
}

// TokenResponse is the successful token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// TokenIntrospectionRequest represents an introspection request.
type TokenIntrospectionRequest struct {
	Token         string `form:"token" binding:"required"`
	TokenTypeHint string `form:"token_type_hint"` // access_token, refresh_token
}

// TokenIntrospectionResponse represents an introspection response.
type TokenIntrospectionResponse struct {
	Active    bool     `json:"active"`
	Scope     string   `json:"scope,omitempty"`
	ClientID  string   `json:"client_id,omitempty"`
	Username  string   `json:"username,omitempty"`
	TokenType string   `json:"token_type,omitempty"`
	Exp       int64    `json:"exp,omitempty"`
	Iat       int64    `json:"iat,omitempty"`
	Sub       string   `json:"sub,omitempty"`
	Iss       string   `json:"iss,omitempty"`
	Aud       []string `json:"aud,omitempty"`
	Jti       string   `json:"jti,omitempty"`
}

// TokenRevocationRequest represents a token revocation request.
type TokenRevocationRequest struct {
	Token         string `form:"token" binding:"required"`
	TokenTypeHint string `form:"token_type_hint"`
	ClientID      string `form:"client_id" binding:"required"`
	ClientSecret  string `form:"client_secret" binding:"required"`
}

// AuthorizationRequest represents an authorization request.
type AuthorizationRequest struct {
	ResponseType        string `form:"response_type" binding:"required,oneof=code token"`
	ClientID            string `form:"client_id" binding:"required"`
	RedirectURI         string `form:"redirect_uri" binding:"required"`
	Scope               string `form:"scope"`
	State               string `form:"state"`
	CodeChallenge       string `form:"code_challenge"`
	CodeChallengeMethod string `form:"code_challenge_method" binding:"omitempty,oneof=S256 plain"`
}

// AuthorizationResponse represents a successful authorization response.
type AuthorizationResponse struct {
	Code  string `json:"code,omitempty"`
	State string `json:"state,omitempty"`
	Token string `json:"access_token,omitempty"`
	Type  string `json:"token_type,omitempty"`
	Exp   int    `json:"expires_in,omitempty"`
}

// APIKey represents an API key for application authentication.
type APIKey struct {
	ID          string            `json:"id"`
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	AppID       string            `json:"app_id"`
	APIID       string            `json:"api_id"`
	TenantID    string            `json:"tenant_id"`
	Scopes      []string          `json:"scopes"`
	ValidFrom   time.Time         `json:"valid_from"`
	ValidTo     time.Time         `json:"valid_to"`
	Revoked     bool              `json:"revoked"`
	RevokedAt   *time.Time        `json:"revoked_at,omitempty"`
	UsageCount  int64             `json:"usage_count"`
	Metadata    map[string]string `json:"metadata"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// APIKeyCreateRequest is the payload for creating an API key.
type APIKeyCreateRequest struct {
	Name        string            `json:"name" binding:"required"`
	Description string            `json:"description"`
	AppID       string            `json:"app_id" binding:"required"`
	APIID       string            `json:"api_id"`
	Scopes      []string          `json:"scopes"`
	ValidDays   int               `json:"valid_days" binding:"min=1,max=365"`
	Metadata    map[string]string `json:"metadata"`
}

// JWKS represents a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK represents a JSON Web Key.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n,omitempty"`  // RSA modulus
	E   string `json:"e,omitempty"`  // RSA exponent
	X   string `json:"x,omitempty"`  // EC x coordinate
	Y   string `json:"y,omitempty"`  // EC y coordinate
	Crv string `json:"crv,omitempty"` // EC curve
	K   string `json:"k,omitempty"`  // symmetric key
}

// JWTClaims defines custom claims for VAPIM tokens.
type JWTClaims struct {
	Sub        string   `json:"sub"`
	Iss        string   `json:"iss"`
	Aud        []string `json:"aud"`
	Exp        int64    `json:"exp"`
	Iat        int64    `json:"iat"`
	Jti        string   `json:"jti"`
	Scope      string   `json:"scope"`
	APIContext string   `json:"api_context,omitempty"`
	ClientID   string   `json:"client_id,omitempty"`
	TenantID   string   `json:"tenant_id,omitempty"`
}

// User represents a system user for password grant flow.
type User struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Password string   `json:"password"` // hashed
	Roles    []string `json:"roles"`
	TenantID string   `json:"tenant_id"`
	Enabled  bool     `json:"enabled"`
}

// AuthCode represents an authorization code for the authorization_code flow.
type AuthCode struct {
	Code                string    `json:"code"`
	ClientID            string    `json:"client_id"`
	UserID              string    `json:"user_id"`
	RedirectURI         string    `json:"redirect_uri"`
	Scope               string    `json:"scope"`
	CodeChallenge       string    `json:"code_challenge"`
	CodeChallengeMethod string    `json:"code_challenge_method"`
	ExpiresAt           time.Time `json:"expires_at"`
	Used                bool      `json:"used"`
}

// VersionSet groups versions of the same API.
type VersionSet struct {
	ID         string    `json:"id"`
	APIContext string    `json:"api_context"`
	APIName    string    `json:"api_name"`
	Versions   []string  `json:"versions"`
	DefaultVersion string `json:"default_version"`
	TenantID   string    `json:"tenant_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// LifecycleTransitionRequest represents a request to transition API lifecycle state.
type LifecycleTransitionRequest struct {
	TargetStatus APIStatus `json:"target_status" binding:"required"`
	Reason       string    `json:"reason"`
}

// LifecycleTransitionResponse represents the result of a lifecycle transition.
type LifecycleTransitionResponse struct {
	APIID        string    `json:"api_id"`
	PreviousStatus APIStatus `json:"previous_status"`
	CurrentStatus  APIStatus `json:"current_status"`
	Message      string    `json:"message"`
	Timestamp    time.Time `json:"timestamp"`
}

// SearchRequest represents API search parameters.
type SearchRequest struct {
	Query    string `form:"query"`
	Tag      string `form:"tag"`
	Context  string `form:"context"`
	Status   string `form:"status"`
	AuthType string `form:"auth_type"`
	TenantID string `form:"tenant_id"`
	Offset   int    `form:"offset,default=0"`
	Limit    int    `form:"limit,default=20"`
}

// ErrorResponse is the standard API error response.
type ErrorResponse struct {
	Code        int       `json:"code"`
	Message     string    `json:"message"`
	Description string    `json:"description,omitempty"`
	MoreInfo    string    `json:"more_info,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// PaginationParams holds pagination query parameters.
type PaginationParams struct {
	Offset int `form:"offset,default=0"`
	Limit  int `form:"limit,default=20"`
}

// ValidatePagination ensures pagination parameters are within bounds.
func (p *PaginationParams) ValidatePagination() {
	if p.Limit <= 0 {
		p.Limit = 25
	}
	if p.Limit > 100 {
		p.Limit = 100
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
}

// ==================== DEVELOPER PORTAL MODELS ====================

// PublishedAPI represents an API visible in the Developer Portal catalog.
type PublishedAPI struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Context     string   `json:"context"`
	Version     string   `json:"version"`
	Provider    string   `json:"provider"`
	Status      string   `json:"status"`
	Thumbnail   string   `json:"thumbnail,omitempty"`
	Tags        []string `json:"tags"`
	Category    string   `json:"category,omitempty"`
	Rating      float64  `json:"rating"`
	RatingCount int64    `json:"rating_count"`
	Tier        string   `json:"tier"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// APIFilter provides filtering options for API catalog queries.
type APIFilter struct {
	Category string   `json:"category,omitempty"`
	Status   string   `json:"status,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Version  string   `json:"version,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Tenant   string   `json:"tenant,omitempty"`
}

// APIRating represents the aggregated rating for an API.
type APIRating struct {
	APIID       string  `json:"api_id"`
	Average     float64 `json:"average"`
	Count       int64   `json:"count"`
	FiveStar    int64   `json:"five_star"`
	FourStar    int64   `json:"four_star"`
	ThreeStar   int64   `json:"three_star"`
	TwoStar     int64   `json:"two_star"`
	OneStar     int64   `json:"one_star"`
}

// APIReview represents a user review for an API.
type APIReview struct {
	ID        string    `json:"id"`
	APIID     string    `json:"api_id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Rating    int       `json:"rating"`
	Comment   string    `json:"comment"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// APIDoc represents a documentation entry for an API.
type APIDoc struct {
	ID          string    `json:"id"`
	APIID       string    `json:"api_id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"` // markdown, pdf, yaml, inline
	Summary     string    `json:"summary"`
	SourceURL   string    `json:"source_url,omitempty"`
	Content     string    `json:"content,omitempty"`
	FileName    string    `json:"file_name,omitempty"`
	Visibility  string    `json:"visibility"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SwaggerDef represents an OpenAPI/Swagger definition for an API.
type SwaggerDef struct {
	APIID    string      `json:"api_id"`
	Version  string      `json:"version"` // "2.0" or "3.0.x"
	Spec     interface{} `json:"spec"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Application represents a consumer application in the Developer Portal.
type Application struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Tier        string            `json:"tier"`
	Status      string            `json:"status"`
	OwnerID     string            `json:"owner_id"`
	Tenant      string            `json:"tenant"`
	GroupID     string            `json:"group_id,omitempty"`
	Keys        []ApplicationKeys `json:"keys,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ApplicationKeys represents generated API keys for an application.
type ApplicationKeys struct {
	ID              string    `json:"id"`
	AppID           string    `json:"app_id"`
	KeyType         string    `json:"key_type"` // PRODUCTION or SANDBOX
	ConsumerKey     string    `json:"consumer_key"`
	ConsumerSecret  string    `json:"consumer_secret"`
	GrantTypes      []string  `json:"grant_types"`
	CallbackURL     string    `json:"callback_url,omitempty"`
	ValidityPeriod  int64     `json:"validity_period"` // seconds
	Scopes          []string  `json:"scopes"`
	Revoked         bool      `json:"revoked"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateApplicationRequest is the payload for creating an application.
type CreateApplicationRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Tier        string `json:"tier"` // Bronze, Silver, Gold, Unlimited
}

// UpdateApplicationRequest is the payload for updating an application.
type UpdateApplicationRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Tier        string `json:"tier"`
}

// GenerateKeysRequest is the payload for generating application keys.
type GenerateKeysRequest struct {
	KeyType        string   `json:"key_type"` // PRODUCTION or SANDBOX
	GrantTypes     []string `json:"grant_types"`
	CallbackURL    string   `json:"callback_url"`
	ValidityPeriod int64    `json:"validity_period"`
	Scopes         []string `json:"scopes"`
	Tier           string   `json:"tier"`
}

// Subscription represents an API subscription by an application.
type Subscription struct {
	ID         string    `json:"id"`
	APIID      string    `json:"api_id"`
	AppID      string    `json:"app_id"`
	Tier       string    `json:"tier"`
	Status     string    `json:"status"` // ACTIVE, BLOCKED, ON_HOLD, REJECTED
	Subscriber string    `json:"subscriber"`
	Tenant     string    `json:"tenant"`
	ExpiryDate time.Time `json:"expiry_date,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SubscribeRequest is the payload for subscribing to an API.
type SubscribeRequest struct {
	APIID string `json:"api_id" binding:"required"`
	AppID string `json:"app_id" binding:"required"`
	Tier  string `json:"tier"`
}

// SubscriptionValidationResponse is returned when validating a subscription.
type SubscriptionValidationResponse struct {
	Valid      bool      `json:"valid"`
	Status     string    `json:"status"`
	Tier       string    `json:"tier"`
	AppID      string    `json:"app_id"`
	APIID      string    `json:"api_id"`
	ExpiryDate time.Time `json:"expiry_date,omitempty"`
}

// TryItRequest is the payload for the try-it console.
type TryItRequest struct {
	Method    string            `json:"method"`
	URL       string            `json:"url" binding:"required"`
	Headers   map[string]string `json:"headers"`
	Body      interface{}       `json:"body"`
	AuthType  string            `json:"auth_type"` // none, basic, bearer, api_key
	AuthToken string            `json:"auth_token"`
	TimeoutMs int               `json:"timeout_ms"`
}

// TryItProxyRequest is the payload for the CORS proxy.
type TryItProxyRequest struct {
	TargetURL string            `json:"target_url" binding:"required"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Body      interface{}       `json:"body"`
	AuthType  string            `json:"auth_type"`
	AuthToken string            `json:"auth_token"`
}

// RequestDetails captures the outgoing request for the try-it response.
type RequestDetails struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"`
}

// ResponseDetails captures the incoming response for the try-it response.
type ResponseDetails struct {
	StatusCode int64             `json:"status_code"`
	Status     string            `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       interface{}       `json:"body"`
	BodySize   int64             `json:"body_size"`
}

// TryItResponse is the response from the try-it console.
type TryItResponse struct {
	Success        bool            `json:"success"`
	Error          string          `json:"error,omitempty"`
	LatencyMs      int64           `json:"latency_ms"`
	Request        RequestDetails  `json:"request"`
	Response       ResponseDetails `json:"response,omitempty"`
	RequestID      string          `json:"request_id"`
}

// APIResponse is a generic single-item API response wrapper.
type APIResponse struct {
	Data      interface{} `json:"data"`
	RequestID string      `json:"request_id,omitempty"`
}

// PaginatedResponse is a generic paginated API response wrapper.
type PaginatedResponse[T any] struct {
	Data      []T   `json:"data"`
	Total     int64 `json:"total"`
	Offset    int   `json:"offset"`
	Limit     int   `json:"limit"`
	Count     int64 `json:"count"`
	RequestID string `json:"request_id,omitempty"`
}

// PortalUser extends User with developer portal specific fields.
type PortalUser struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Roles    []string `json:"roles"`
	Tenant   string   `json:"tenant"`
}

// ==================== TRAFFIC MANAGER MODELS ====================

// ThrottlePolicy defines a rate limiting policy.
type ThrottlePolicy struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"` // Advanced, Subscription, Application, Global, Custom
	Description string            `json:"description"`
	Priority    int               `json:"priority"`
	Enabled     bool              `json:"enabled"`
	Action      string            `json:"action"` // BLOCK, THROTTLE, LOG, ALLOW
	Limit       int64             `json:"limit"`
	TimeWindow  time.Duration     `json:"time_window"`
	Tier        string            `json:"tier,omitempty"`
	Conditions  []PolicyCondition `json:"conditions"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// PolicyCondition defines a condition for policy evaluation.
type PolicyCondition struct {
	Type     string   `json:"type"`     // IP, Header, QueryParam, JWTClaim, Time, UserAgent
	Field    string   `json:"field"`    // header name, query param name, claim name, etc.
	Operator string   `json:"operator"` // eq, ne, in, contains, startswith, endswith, regex, range, exists
	Value    string   `json:"value"`
	Values   []string `json:"values,omitempty"` // for IN operator
}

// ThrottleCheckRequest is sent by the gateway to check if a request should be throttled.
type ThrottleCheckRequest struct {
	APIID             string            `json:"api_id"`
	AppID             string            `json:"app_id"`
	UserID            string            `json:"user_id"`
	Tenant            string            `json:"tenant"`
	Tier              string            `json:"tier"`
	ResourcePath      string            `json:"resource_path"`
	HTTPMethod        string            `json:"http_method"`
	ClientIP          string            `json:"client_ip"`
	Headers           map[string]string `json:"headers"`
	QueryParams       map[string]string `json:"query_params"`
	JWTClaims         map[string]interface{} `json:"jwt_claims"`
	APILimitPerMin    int64             `json:"api_limit_per_min"`
	ResourceLimitPerMin int64           `json:"resource_limit_per_min"`
}

// ThrottleResult is the outcome of a throttle check.
type ThrottleResult struct {
	Throttled        bool          `json:"throttled"`
	Allowed          bool          `json:"allowed"`
	Level            string        `json:"level,omitempty"` // Global, Tenant, API, Resource, Application, User
	PolicyID         string        `json:"policy_id,omitempty"`
	PolicyName       string        `json:"policy_name,omitempty"`
	Limit            int64         `json:"limit"`
	Remaining        int64         `json:"remaining"`
	RetryAfter       time.Duration `json:"retry_after,omitempty"`
	ResetTime        time.Time     `json:"reset_time,omitempty"`
	Timestamp        time.Time     `json:"timestamp"`
}

// ThrottleStatus represents the current throttle state for a key.
type ThrottleStatus struct {
	Key       string    `json:"key"`
	Count     int64     `json:"count"`
	Timestamp time.Time `json:"timestamp"`
}

// ThrottleEvent is published when a request is throttled.
type ThrottleEvent struct {
	APIID      string        `json:"api_id"`
	APIName    string        `json:"api_name,omitempty"`
	AppID      string        `json:"app_id"`
	UserID     string        `json:"user_id"`
	Tier       string        `json:"tier"`
	Level      string        `json:"level"`
	PolicyID   string        `json:"policy_id"`
	ClientIP   string        `json:"client_ip,omitempty"`
	RetryAfter time.Duration `json:"retry_after"`
	Timestamp  time.Time     `json:"timestamp"`
}

// BurstRequest is the payload for burst allowance checks.
type BurstRequest struct {
	Key       string `json:"key"`
	BurstSize int64  `json:"burst_size"`
}

// BurstStatus represents the current burst control state.
type BurstStatus struct {
	Key          string    `json:"key"`
	MaxBurst     int64     `json:"max_burst"`
	CurrentBurst int64     `json:"current_burst"`
	Used         int64     `json:"used"`
	LastUsed     time.Time `json:"last_used"`
	RecoveryRate float64   `json:"recovery_rate"`
}

// QuotaUsage tracks quota consumption for a given key.
type QuotaUsage struct {
	QuotaKey   string    `json:"quota_key"`
	Tier       string    `json:"tier"`
	Used       int64     `json:"used"`
	Limit      int64     `json:"limit"`
	Remaining  int64     `json:"remaining"`
	Period     string    `json:"period"`
	ResetTime  time.Time `json:"reset_time"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// QuotaStatus is the result of a quota check.
type QuotaStatus struct {
	QuotaKey   string  `json:"quota_key"`
	Tier       string  `json:"tier"`
	Period     string  `json:"period"`
	Limit      int64   `json:"limit"`
	Used       int64   `json:"used"`
	Remaining  int64   `json:"remaining"`
	UsageRatio float64 `json:"usage_ratio"`
	Exceeded   bool    `json:"exceeded"`
}

// QuotaDefinition defines limits for a subscription tier.
type QuotaDefinition struct {
	Name             string `json:"name"`
	RequestsPerMin   int64  `json:"requests_per_min"`
	RequestsPerHour  int64  `json:"requests_per_hour"`
	RequestsPerDay   int64  `json:"requests_per_day"`
	RequestsPerMonth int64  `json:"requests_per_month"`
	BurstAllowance   int64  `json:"burst_allowance"`
	Description      string `json:"description"`
}

// CounterEvent is published for distributed counter updates.
type CounterEvent struct {
	Key       string    `json:"key"`
	Delta     int64     `json:"delta"`
	Timestamp time.Time `json:"timestamp"`
}

// DistributedLimitResult is the result of a distributed rate limit check.
type DistributedLimitResult struct {
	Allowed     bool          `json:"allowed"`
	Limit       int64         `json:"limit"`
	Remaining   int64         `json:"remaining"`
	ResetTime   time.Time     `json:"reset_time"`
	RetryAfter  time.Duration `json:"retry_after,omitempty"`
	Synced      bool          `json:"synced"`
}

// ClusterUsage shows aggregated usage across cluster nodes.
type ClusterUsage struct {
	Key         string    `json:"key"`
	StoreValue  int64     `json:"store_value"`
	LocalValue  int64     `json:"local_value"`
	Total       int64     `json:"total"`
	LastSync    time.Time `json:"last_sync"`
	WindowStart time.Time `json:"window_start"`
}

// ==================== ANALYTICS MODELS ====================

// EventType represents the type of analytics event.
type EventType string

const (
	EventTypeRequest  EventType = "request"
	EventTypeResponse EventType = "response"
	EventTypeFault    EventType = "fault"
	EventTypeThrottle EventType = "throttle"
)

// AnalyticsEvent represents a single API invocation analytics event.
type AnalyticsEvent struct {
	EventType        EventType `json:"event_type"`
	APIID            string    `json:"api_id"`
	APIName          string    `json:"api_name,omitempty"`
	APIVersion       string    `json:"api_version,omitempty"`
	AppID            string    `json:"app_id,omitempty"`
	AppName          string    `json:"app_name,omitempty"`
	UserID           string    `json:"user_id,omitempty"`
	Tenant           string    `json:"tenant,omitempty"`
	Method           string    `json:"method"`
	Path             string    `json:"path"`
	StatusCode       int       `json:"status_code"`
	ResponseSize     int64     `json:"response_size,omitempty"`
	RequestSize      int64     `json:"request_size,omitempty"`
	LatencyMs        int64     `json:"latency_ms"`
	BackendLatencyMs int64     `json:"backend_latency_ms,omitempty"`
	ClientIP         string    `json:"client_ip,omitempty"`
	UserAgent        string    `json:"user_agent,omitempty"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	GatewayNode      string    `json:"gateway_node,omitempty"`
	CorrelationID    string    `json:"correlation_id,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

// APIMetric represents aggregated metrics for an API.
type APIMetric struct {
	APIID     string    `json:"api_id"`
	Count     int64     `json:"count"`
	Errors    int64     `json:"errors"`
	LatencyMs int64     `json:"latency_ms"`
	Period    string    `json:"period"`
	Timestamp time.Time `json:"timestamp"`
}

// AppMetric represents aggregated metrics for an application.
type AppMetric struct {
	AppID     string    `json:"app_id"`
	Count     int64     `json:"count"`
	Errors    int64     `json:"errors"`
	LatencyMs int64     `json:"latency_ms"`
	Period    string    `json:"period"`
	Timestamp time.Time `json:"timestamp"`
}

// UserMetric represents aggregated metrics for a user.
type UserMetric struct {
	UserID    string    `json:"user_id"`
	Count     int64     `json:"count"`
	Errors    int64     `json:"errors"`
	LatencyMs int64     `json:"latency_ms"`
	Period    string    `json:"period"`
	Timestamp time.Time `json:"timestamp"`
}

// AggregationReport is a periodic summary of API analytics.
type AggregationReport struct {
	WindowKey                string                    `json:"window_key"`
	GeneratedAt              time.Time                 `json:"generated_at"`
	TotalRequests            int64                     `json:"total_requests"`
	TotalErrors              int64                     `json:"total_errors"`
	TotalLatencyMs           int64                     `json:"total_latency_ms"`
	AvgLatencyMs             float64                   `json:"avg_latency_ms"`
	ErrorRate                float64                   `json:"error_rate"`
	TopAPIs                  []TopAPIEntry             `json:"top_apis"`
	TopApps                  []TopAppEntry             `json:"top_apps"`
	TopUsers                 []TopUserEntry            `json:"top_users"`
	APILatencyPercentiles    []APILatencyPercentile    `json:"api_latency_percentiles"`
	APIErrorRates            []APIErrorRate            `json:"api_error_rates"`
}

// TopAPIEntry represents a single API in the top APIs list.
type TopAPIEntry struct {
	APIID     string  `json:"api_id"`
	Count     int64   `json:"count"`
	Errors    int64   `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
}

// TopAppEntry represents a single app in the top apps list.
type TopAppEntry struct {
	AppID     string  `json:"app_id"`
	Count     int64   `json:"count"`
	Errors    int64   `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
}

// TopUserEntry represents a single user in the top users list.
type TopUserEntry struct {
	UserID    string  `json:"user_id"`
	Count     int64   `json:"count"`
	Errors    int64   `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
}

// APILatencyPercentile shows latency distribution for an API.
type APILatencyPercentile struct {
	APIID string  `json:"api_id"`
	P50   int64   `json:"p50"`
	P95   int64   `json:"p95"`
	P99   int64   `json:"p99"`
	Avg   float64 `json:"avg"`
	Count int64   `json:"count"`
}

// APIErrorRate shows the error rate for an API.
type APIErrorRate struct {
	APIID     string  `json:"api_id"`
	Count     int64   `json:"count"`
	Errors    int64   `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
}

// CollectorStats shows analytics collector statistics.
type CollectorStats struct {
	EventsCollected   int64 `json:"events_collected"`
	EventsPublished   int64 `json:"events_published"`
	EventsDropped     int64 `json:"events_dropped"`
	BatchesPublished  int64 `json:"batches_published"`
	BufferSize        int64 `json:"buffer_size"`
	BufferUsed        int64 `json:"buffer_used"`
}


// InvocationRecord captures a complete API invocation for analytics.
type InvocationRecord struct {
	APIID            string `json:"api_id"`
	APIName          string `json:"api_name,omitempty"`
	APIVersion       string `json:"api_version,omitempty"`
	AppID            string `json:"app_id,omitempty"`
	AppName          string `json:"app_name,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	Tenant           string `json:"tenant,omitempty"`
	Method           string `json:"method"`
	Path             string `json:"path"`
	StatusCode       int    `json:"status_code"`
	ResponseSize     int64  `json:"response_size,omitempty"`
	RequestSize      int64  `json:"request_size,omitempty"`
	LatencyMs        int64  `json:"latency_ms"`
	BackendLatencyMs int64  `json:"backend_latency_ms,omitempty"`
	ClientIP         string `json:"client_ip,omitempty"`
	UserAgent        string `json:"user_agent,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
	GatewayNode      string `json:"gateway_node,omitempty"`
	CorrelationID    string `json:"correlation_id,omitempty"`
}

// ==================== EVENT / SYNC MODELS ====================

// CounterSyncRequest triggers a counter sync across the cluster.
type CounterSyncRequest struct {
	Key       string    `json:"key"`
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
}

// CounterSyncResponse is the result of a counter sync.
type CounterSyncResponse struct {
	Key        string    `json:"key"`
	StoreValue int64     `json:"store_value"`
	LocalValue int64     `json:"local_value"`
	Synced     bool      `json:"synced"`
	Timestamp  time.Time `json:"timestamp"`
}

// PolicyEvaluationRequest triggers policy re-evaluation.
type PolicyEvaluationRequest struct {
	PolicyID  string    `json:"policy_id"`
	Trigger   string    `json:"trigger"` // manual, scheduled, event
	Timestamp time.Time `json:"timestamp"`
}

// PolicyEvaluationResponse reports policy evaluation results.
type PolicyEvaluationResponse struct {
	PolicyID   string `json:"policy_id"`
	Evaluated  bool   `json:"evaluated"`
	MatchCount int64  `json:"match_count"`
	Error      string `json:"error,omitempty"`
}

// ==================== TENANT MODELS ====================

// Tenant represents an API management tenant.
type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Domain    string    `json:"domain"`
	Active    bool      `json:"active"`
	Plan      string    `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TenantQuota defines resource limits for a tenant.
type TenantQuota struct {
	TenantID  string `json:"tenant_id"`
	MaxAPIs   int    `json:"max_apis"`
	MaxApps   int    `json:"max_apps"`
	MaxReq    int64  `json:"max_requests"`
	BurstLim  int64  `json:"burst_limit"`
}

// ==================== UTILITY / HELPER METHODS ====================

// IsAdmin returns true if the user has the super_admin or admin role.
func (u *User) IsAdmin() bool {
	for _, r := range u.Roles {
		if r == string(RoleSuperAdmin) || r == string(RoleAdmin) {
			return true
		}
	}
	return false
}

// IsPublisher returns true if the user has the publisher role.
func (u *User) IsPublisher() bool {
	for _, r := range u.Roles {
		if r == string(RolePublisher) {
			return true
		}
	}
	return false
}

// IsSubscriber returns true if the user has the subscriber role.
func (u *User) IsSubscriber() bool {
	for _, r := range u.Roles {
		if r == string(RoleSubscriber) {
			return true
		}
	}
	return false
}

// HasRole returns true if the user has the given role.
func (u *User) HasRole(role Role) bool {
	for _, r := range u.Roles {
		if r == string(role) {
			return true
		}
	}
	return false
}

// GetScope returns the tenant scope identifier.
func (t *Tenant) GetScope() string {
	if t.Domain == "" {
		return t.ID
	}
	return t.Domain
}

// QuotaExceeded returns true if the quota usage has exceeded its limit.
func (q *QuotaUsage) QuotaExceeded() bool {
	return q.Used >= q.Limit
}

// ThrottlePolicySummary provides a short summary of a policy.
type ThrottlePolicySummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Action   string `json:"action"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
}

// ToSummary converts a ThrottlePolicy to its summary form.
func (p *ThrottlePolicy) ToSummary() ThrottlePolicySummary {
	return ThrottlePolicySummary{
		ID:       p.ID,
		Name:     p.Name,
		Type:     p.Type,
		Action:   p.Action,
		Enabled:  p.Enabled,
		Priority: p.Priority,
	}
}

// APIKeyUsage tracks usage statistics for an API key.
type APIKeyUsage struct {
	KeyID        string    `json:"key_id"`
	RequestCount int64     `json:"request_count"`
	LastUsed     time.Time `json:"last_used"`
	Status       string    `json:"status"`
}

// Notification represents a system notification.
type Notification struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Read      bool      `json:"read"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Webhook represents an outbound webhook configuration.
type Webhook struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Events    []string          `json:"events"`
	Headers   map[string]string `json:"headers"`
	Active    bool              `json:"active"`
	Secret    string            `json:"secret,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// APIGatewayConfig holds gateway-specific configuration for an API.
type APIGatewayConfig struct {
	APIID              string            `json:"api_id"`
	ProductionURL      string            `json:"production_url"`
	SandboxURL         string            `json:"sandbox_url"`
	EndpointSecurity   string            `json:"endpoint_security"`
	EndpointUsername   string            `json:"endpoint_username,omitempty"`
	EndpointPassword   string            `json:"endpoint_password,omitempty"`
	TimeoutMS          int               `json:"timeout_ms"`
	RetryCount         int               `json:"retry_count"`
	CacheEnabled       bool              `json:"cache_enabled"`
	CacheTTL           int               `json:"cache_ttl"`
	CORSAllowedOrigins []string          `json:"cors_allowed_origins"`
	CORSAllowedMethods []string          `json:"cors_allowed_methods"`
	CORSAllowedHeaders []string          `json:"cors_allowed_headers"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// Scope represents an OAuth2 scope.
type Scope struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Roles       []string `json:"roles,omitempty"`
	TenantID    string   `json:"tenant_id,omitempty"`
}

// BlockingCondition represents a global blocking condition.
type BlockingCondition struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Value     string    `json:"value"`
	Enabled   bool      `json:"enabled"`
	TenantID  string    `json:"tenant_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Endpoint represents a single backend endpoint.
type Endpoint struct {
	URL      string `json:"url"`
	Protocol string `json:"protocol"`
	Timeout  int    `json:"timeout"`
}
