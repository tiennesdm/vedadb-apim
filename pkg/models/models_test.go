package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Model Definitions
// ============================================================================

// API represents a published API
type API struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	BaseURL     string            `json:"base_url"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	Owner       string            `json:"owner"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Endpoints   []Endpoint        `json:"endpoints,omitempty"`
	Policies    []Policy          `json:"policies,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	PublishedAt *time.Time        `json:"published_at,omitempty"`
	Deprecated  bool              `json:"deprecated,omitempty"`
}

// Validate performs validation on the API model
func (a *API) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return ValidationError{Field: "id", Message: "id is required"}
	}
	if strings.TrimSpace(a.Name) == "" {
		return ValidationError{Field: "name", Message: "name is required"}
	}
	if strings.TrimSpace(a.Version) == "" {
		return ValidationError{Field: "version", Message: "version is required"}
	}
	if strings.TrimSpace(a.BaseURL) == "" {
		return ValidationError{Field: "base_url", Message: "base_url is required"}
	}
	if !isValidAPIType(a.Type) {
		return ValidationError{Field: "type", Message: "type must be REST, GraphQL, or gRPC"}
	}
	if !isValidStatus(a.Status) {
		return ValidationError{Field: "status", Message: "invalid status"}
	}
	if len(a.Name) > 200 {
		return ValidationError{Field: "name", Message: "name must be at most 200 characters"}
	}
	if len(a.Description) > 5000 {
		return ValidationError{Field: "description", Message: "description must be at most 5000 characters"}
	}
	for _, ep := range a.Endpoints {
		if err := ep.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) IsPublished() bool {
	return a.Status == StatusPublished
}

func (a *API) IsDeprecated() bool {
	return a.Deprecated
}

func (a *API) CanTransitionTo(newStatus string) bool {
	transitions := map[string][]string{
		StatusDraft:     {StatusReview, StatusDeprecated},
		StatusReview:    {StatusPublished, StatusRejected, StatusDraft},
		StatusPublished: {StatusDeprecated, StatusRetired},
		StatusRejected:  {StatusDraft},
		StatusRetired:   {},
		StatusDeprecated:{StatusRetired},
	}
	allowed, ok := transitions[a.Status]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == newStatus {
			return true
		}
	}
	return false
}

// Endpoint represents an API endpoint
type Endpoint struct {
	Path        string            `json:"path"`
	Method      string            `json:"method"`
	Description string            `json:"description,omitempty"`
	Parameters  []Parameter       `json:"parameters,omitempty"`
	Responses   map[int]string    `json:"responses,omitempty"`
}

func (e *Endpoint) Validate() error {
	if strings.TrimSpace(e.Path) == "" {
		return ValidationError{Field: "path", Message: "endpoint path is required"}
	}
	if !isValidMethod(e.Method) {
		return ValidationError{Field: "method", Message: "invalid HTTP method"}
	}
	return nil
}

// Parameter represents an endpoint parameter
type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// Policy represents an API policy
type Policy struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Config   map[string]interface{} `json:"config"`
	Priority int               `json:"priority"`
}

// Application represents a developer application
type Application struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Owner       string    `json:"owner"`
	Status      string    `json:"status"`
	APIKeys     []APIKey  `json:"api_keys,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (a *Application) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return ValidationError{Field: "id", Message: "id is required"}
	}
	if strings.TrimSpace(a.Name) == "" {
		return ValidationError{Field: "name", Message: "name is required"}
	}
	if strings.TrimSpace(a.Owner) == "" {
		return ValidationError{Field: "owner", Message: "owner is required"}
	}
	return nil
}

// APIKey represents an API key
type APIKey struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Subscription represents an API subscription
type Subscription struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	APIID         string    `json:"api_id"`
	Plan          string    `json:"plan"`
	Status        string    `json:"status"`
	Throttling    ThrottleConfig `json:"throttling,omitempty"`
	Quota         QuotaConfig    `json:"quota,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ThrottleConfig represents throttling configuration
type ThrottleConfig struct {
	Rate  int `json:"rate"`
	Burst int `json:"burst"`
}

// QuotaConfig represents quota configuration
type QuotaConfig struct {
	Limit     int    `json:"limit"`
	Period    string `json:"period"`
	Used      int    `json:"used"`
	ResetTime time.Time `json:"reset_time"`
}

// ValidationError represents a validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (v ValidationError) Error() string {
	return v.Message
}

// User represents a system user
type User struct {
	ID        string   `json:"id"`
	Username  string   `json:"username"`
	Email     string   `json:"email"`
	Role      string   `json:"role"`
	Permissions []string `json:"permissions,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (u *User) Validate() error {
	if strings.TrimSpace(u.ID) == "" {
		return ValidationError{Field: "id", Message: "id is required"}
	}
	if strings.TrimSpace(u.Username) == "" {
		return ValidationError{Field: "username", Message: "username is required"}
	}
	if strings.TrimSpace(u.Email) == "" {
		return ValidationError{Field: "email", Message: "email is required"}
	}
	if !strings.Contains(u.Email, "@") {
		return ValidationError{Field: "email", Message: "invalid email format"}
	}
	if strings.TrimSpace(u.Role) == "" {
		return ValidationError{Field: "role", Message: "role is required"}
	}
	return nil
}

func (u *User) HasPermission(perm string) bool {
	for _, p := range u.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// Constants
const (
	StatusDraft      = "draft"
	StatusReview     = "review"
	StatusPublished  = "published"
	StatusRejected   = "rejected"
	StatusDeprecated = "deprecated"
	StatusRetired    = "retired"
	StatusActive     = "active"
	StatusInactive   = "inactive"
	StatusRevoked    = "revoked"
)

func isValidAPIType(t string) bool {
	switch t {
	case "REST", "GraphQL", "gRPC", "SOAP", "WebSocket":
		return true
	}
	return false
}

func isValidStatus(s string) bool {
	switch s {
	case StatusDraft, StatusReview, StatusPublished, StatusRejected, StatusDeprecated, StatusRetired, StatusActive, StatusInactive, StatusRevoked:
		return true
	}
	return false
}

func isValidMethod(m string) bool {
	switch m {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		return true
	}
	return false
}

// ============================================================================
// TESTS
// ============================================================================

func TestAPI_Validate_GivenValidAPI_WhenValidated_ThenReturnsNoError(t *testing.T) {
	tests := []struct {
		name string
		api  API
	}{
		{
			name: "minimal valid REST API",
			api: API{
				ID:      "api-1",
				Name:    "Test API",
				Version: "1.0.0",
				BaseURL: "https://api.example.com",
				Type:    "REST",
				Status:  StatusDraft,
			},
		},
		{
			name: "valid GraphQL API",
			api: API{
				ID:      "api-2",
				Name:    "GraphQL API",
				Version: "2.0.0",
				BaseURL: "https://graphql.example.com",
				Type:    "GraphQL",
				Status:  StatusPublished,
			},
		},
		{
			name: "valid gRPC API with endpoints",
			api: API{
				ID:      "api-3",
				Name:    "gRPC Service",
				Version: "1.0.0",
				BaseURL: "https://grpc.example.com",
				Type:    "gRPC",
				Status:  StatusDraft,
				Endpoints: []Endpoint{
					{Path: "/v1/method", Method: "POST"},
				},
			},
		},
		{
			name: "API with all fields",
			api: API{
				ID:          "api-4",
				Name:        "Full API",
				Description: "A comprehensive API with all fields set",
				Version:     "1.0.0",
				BaseURL:     "https://api.example.com",
				Type:        "REST",
				Status:      StatusPublished,
				Owner:       "team-a",
				Tags:        []string{"v1", "public", "stable"},
				Metadata:    map[string]string{"env": "production"},
				Endpoints: []Endpoint{
					{Path: "/users", Method: "GET"},
					{Path: "/users", Method: "POST"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.api.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestAPI_Validate_GivenInvalidAPI_WhenValidated_ThenReturnsValidationError(t *testing.T) {
	tests := []struct {
		name          string
		api           API
		expectedField string
		expectedMsg   string
	}{
		{
			name:          "empty id",
			api:           API{ID: "", Name: "Test", Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft},
			expectedField: "id",
			expectedMsg:   "id is required",
		},
		{
			name:          "empty name",
			api:           API{ID: "api-1", Name: "", Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft},
			expectedField: "name",
			expectedMsg:   "name is required",
		},
		{
			name:          "empty version",
			api:           API{ID: "api-1", Name: "Test", Version: "", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft},
			expectedField: "version",
			expectedMsg:   "version is required",
		},
		{
			name:          "empty base_url",
			api:           API{ID: "api-1", Name: "Test", Version: "1.0", BaseURL: "", Type: "REST", Status: StatusDraft},
			expectedField: "base_url",
			expectedMsg:   "base_url is required",
		},
		{
			name:          "invalid type",
			api:           API{ID: "api-1", Name: "Test", Version: "1.0", BaseURL: "https://a.com", Type: "INVALID", Status: StatusDraft},
			expectedField: "type",
			expectedMsg:   "type must be REST, GraphQL, or gRPC",
		},
		{
			name:          "name too long",
			api:           API{ID: "api-1", Name: strings.Repeat("a", 201), Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft},
			expectedField: "name",
			expectedMsg:   "name must be at most 200 characters",
		},
		{
			name:          "description too long",
			api:           API{ID: "api-1", Name: "Test", Description: strings.Repeat("a", 5001), Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft},
			expectedField: "description",
			expectedMsg:   "description must be at most 5000 characters",
		},
		{
			name:          "whitespace only name",
			api:           API{ID: "api-1", Name: "   ", Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft},
			expectedField: "name",
			expectedMsg:   "name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.api.Validate()
			require.Error(t, err)
			vErr, ok := err.(ValidationError)
			require.True(t, ok, "expected ValidationError")
			assert.Equal(t, tt.expectedField, vErr.Field)
			assert.Equal(t, tt.expectedMsg, vErr.Message)
		})
	}
}

func TestAPI_IsPublished_GivenStatus_WhenChecked_ThenReturnsCorrectValue(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected bool
	}{
		{"published status", StatusPublished, true},
		{"draft status", StatusDraft, false},
		{"review status", StatusReview, false},
		{"deprecated status", StatusDeprecated, false},
		{"retired status", StatusRetired, false},
		{"rejected status", StatusRejected, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := API{Status: tt.status}
			assert.Equal(t, tt.expected, api.IsPublished())
		})
	}
}

func TestAPI_CanTransitionTo_GivenCurrentAndNewStatus_WhenChecked_ThenReturnsCorrectValue(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		newStatus  string
		canTransition bool
	}{
		{"draft to review", StatusDraft, StatusReview, true},
		{"draft to deprecated", StatusDraft, StatusDeprecated, true},
		{"draft to published", StatusDraft, StatusPublished, false},
		{"review to published", StatusReview, StatusPublished, true},
		{"review to rejected", StatusReview, StatusRejected, true},
		{"review to draft", StatusReview, StatusDraft, true},
		{"published to deprecated", StatusPublished, StatusDeprecated, true},
		{"published to retired", StatusPublished, StatusRetired, true},
		{"published to draft", StatusPublished, StatusDraft, false},
		{"rejected to draft", StatusRejected, StatusDraft, true},
		{"rejected to published", StatusRejected, StatusPublished, false},
		{"retired to any", StatusRetired, StatusDraft, false},
		{"deprecated to retired", StatusDeprecated, StatusRetired, true},
		{"deprecated to published", StatusDeprecated, StatusPublished, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := API{Status: tt.current}
			assert.Equal(t, tt.canTransition, api.CanTransitionTo(tt.newStatus))
		})
	}
}

func TestAPI_JSONMarshalUnmarshal_GivenAPI_WhenSerialized_ThenPreservesData(t *testing.T) {
	now := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	published := now.Add(24 * time.Hour)

	original := API{
		ID:          "api-1",
		Name:        "Test API",
		Description: "A test API",
		Version:     "1.0.0",
		BaseURL:     "https://api.example.com",
		Type:        "REST",
		Status:      StatusPublished,
		Owner:       "team-a",
		Tags:        []string{"v1", "public"},
		Metadata:    map[string]string{"env": "prod"},
		Endpoints: []Endpoint{
			{Path: "/users", Method: "GET", Description: "List users"},
			{Path: "/users", Method: "POST", Description: "Create user"},
		},
		Policies: []Policy{
			{ID: "pol-1", Name: "Rate Limit", Type: "throttle", Config: map[string]interface{}{"rate": 100}, Priority: 1},
		},
		CreatedAt:   now,
		UpdatedAt:   now,
		PublishedAt: &published,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded API
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Description, decoded.Description)
	assert.Equal(t, original.Version, decoded.Version)
	assert.Equal(t, original.BaseURL, decoded.BaseURL)
	assert.Equal(t, original.Type, decoded.Type)
	assert.Equal(t, original.Status, decoded.Status)
	assert.Equal(t, original.Owner, decoded.Owner)
	assert.Equal(t, original.Tags, decoded.Tags)
	assert.Equal(t, original.Metadata, decoded.Metadata)
	assert.Len(t, decoded.Endpoints, 2)
	assert.Len(t, decoded.Policies, 1)
	assert.Equal(t, original.Policies[0].ID, decoded.Policies[0].ID)
}

func TestAPI_JSONUnmarshal_GivenInvalidJSON_WhenUnmarshaled_ThenReturnsError(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"invalid json", `{"id": "api-1", "name":}`},
		{"missing braces", `"id": "api-1"`},
		{"empty string", ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var api API
			err := json.Unmarshal([]byte(tt.json), &api)
			assert.Error(t, err)
		})
	}
}

func TestEndpoint_Validate_GivenEndpoint_WhenValidated_ThenReturnsCorrectResult(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    Endpoint
		expectError bool
		field       string
	}{
		{
			name:        "valid GET endpoint",
			endpoint:    Endpoint{Path: "/users", Method: "GET"},
			expectError: false,
		},
		{
			name:        "valid POST endpoint",
			endpoint:    Endpoint{Path: "/users", Method: "POST"},
			expectError: false,
		},
		{
			name:        "empty path",
			endpoint:    Endpoint{Path: "", Method: "GET"},
			expectError: true,
			field:       "path",
		},
		{
			name:        "invalid method",
			endpoint:    Endpoint{Path: "/users", Method: "INVALID"},
			expectError: true,
			field:       "method",
		},
		{
			name:        "lowercase method still valid",
			endpoint:    Endpoint{Path: "/users", Method: "get"},
			expectError: true,
			field:       "method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.endpoint.Validate()
			if tt.expectError {
				require.Error(t, err)
				vErr := err.(ValidationError)
				assert.Equal(t, tt.field, vErr.Field)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestApplication_Validate_GivenApplication_WhenValidated_ThenReturnsCorrectResult(t *testing.T) {
	tests := []struct {
		name        string
		app         Application
		expectError bool
		field       string
	}{
		{
			name:        "valid application",
			app:         Application{ID: "app-1", Name: "My App", Owner: "user-1", Status: StatusActive},
			expectError: false,
		},
		{
			name:        "empty id",
			app:         Application{ID: "", Name: "My App", Owner: "user-1"},
			expectError: true,
			field:       "id",
		},
		{
			name:        "empty name",
			app:         Application{ID: "app-1", Name: "", Owner: "user-1"},
			expectError: true,
			field:       "name",
		},
		{
			name:        "empty owner",
			app:         Application{ID: "app-1", Name: "My App", Owner: ""},
			expectError: true,
			field:       "owner",
		},
		{
			name:        "whitespace only fields",
			app:         Application{ID: "   ", Name: "   ", Owner: "   "},
			expectError: true,
			field:       "id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.app.Validate()
			if tt.expectError {
				require.Error(t, err)
				vErr := err.(ValidationError)
				assert.Equal(t, tt.field, vErr.Field)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUser_Validate_GivenUser_WhenValidated_ThenReturnsCorrectResult(t *testing.T) {
	tests := []struct {
		name        string
		user        User
		expectError bool
		field       string
	}{
		{
			name:        "valid user",
			user:        User{ID: "user-1", Username: "johndoe", Email: "john@example.com", Role: "admin"},
			expectError: false,
		},
		{
			name:        "empty id",
			user:        User{ID: "", Username: "johndoe", Email: "john@example.com", Role: "admin"},
			expectError: true,
			field:       "id",
		},
		{
			name:        "empty username",
			user:        User{ID: "user-1", Username: "", Email: "john@example.com", Role: "admin"},
			expectError: true,
			field:       "username",
		},
		{
			name:        "empty email",
			user:        User{ID: "user-1", Username: "johndoe", Email: "", Role: "admin"},
			expectError: true,
			field:       "email",
		},
		{
			name:        "invalid email",
			user:        User{ID: "user-1", Username: "johndoe", Email: "not-an-email", Role: "admin"},
			expectError: true,
			field:       "email",
		},
		{
			name:        "email without at sign",
			user:        User{ID: "user-1", Username: "johndoe", Email: "john.example.com", Role: "admin"},
			expectError: true,
			field:       "email",
		},
		{
			name:        "empty role",
			user:        User{ID: "user-1", Username: "johndoe", Email: "john@example.com", Role: ""},
			expectError: true,
			field:       "role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.user.Validate()
			if tt.expectError {
				require.Error(t, err)
				vErr := err.(ValidationError)
				assert.Equal(t, tt.field, vErr.Field)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUser_HasPermission_GivenPermissions_WhenChecked_ThenReturnsCorrectValue(t *testing.T) {
	tests := []struct {
		name       string
		user       User
		permission string
		expected   bool
	}{
		{
			name:       "has exact permission",
			user:       User{Permissions: []string{"api:read", "api:write", "api:delete"}},
			permission: "api:write",
			expected:   true,
		},
		{
			name:       "does not have permission",
			user:       User{Permissions: []string{"api:read", "api:write"}},
			permission: "api:admin",
			expected:   false,
		},
		{
			name:       "empty permissions",
			user:       User{Permissions: []string{}},
			permission: "api:read",
			expected:   false,
		},
		{
			name:       "nil permissions",
			user:       User{Permissions: nil},
			permission: "api:read",
			expected:   false,
		},
		{
			name:       "wildcard permission",
			user:       User{Permissions: []string{"api:*"}},
			permission: "api:read",
			expected:   false, // exact match only
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.user.HasPermission(tt.permission))
		})
	}
}

func TestAPIKey_StatusConstants_GivenStatuses_WhenUsed_ThenHaveCorrectValues(t *testing.T) {
	assert.Equal(t, "draft", StatusDraft)
	assert.Equal(t, "review", StatusReview)
	assert.Equal(t, "published", StatusPublished)
	assert.Equal(t, "rejected", StatusRejected)
	assert.Equal(t, "deprecated", StatusDeprecated)
	assert.Equal(t, "retired", StatusRetired)
	assert.Equal(t, "active", StatusActive)
	assert.Equal(t, "inactive", StatusInactive)
	assert.Equal(t, "revoked", StatusRevoked)
}

func TestSubscription_JSONMarshalUnmarshal_GivenSubscription_WhenSerialized_ThenPreservesData(t *testing.T) {
	now := time.Now()
	original := Subscription{
		ID:            "sub-1",
		ApplicationID: "app-1",
		APIID:         "api-1",
		Plan:          "premium",
		Status:        StatusActive,
		Throttling:    ThrottleConfig{Rate: 1000, Burst: 200},
		Quota:         QuotaConfig{Limit: 10000, Period: "month", Used: 500, ResetTime: now},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Subscription
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.ApplicationID, decoded.ApplicationID)
	assert.Equal(t, original.APIID, decoded.APIID)
	assert.Equal(t, original.Plan, decoded.Plan)
	assert.Equal(t, original.Status, decoded.Status)
	assert.Equal(t, original.Throttling.Rate, decoded.Throttling.Rate)
	assert.Equal(t, original.Throttling.Burst, decoded.Throttling.Burst)
	assert.Equal(t, original.Quota.Limit, decoded.Quota.Limit)
	assert.Equal(t, original.Quota.Period, decoded.Quota.Period)
	assert.Equal(t, original.Quota.Used, decoded.Quota.Used)
}

func TestModels_EdgeCases_GivenBoundaryValues_WhenProcessed_ThenHandlesCorrectly(t *testing.T) {
	t.Run("API with empty tags", func(t *testing.T) {
		api := API{ID: "api-1", Name: "Test", Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft, Tags: nil}
		assert.NoError(t, api.Validate())
		assert.Nil(t, api.Tags)
	})

	t.Run("API with empty metadata", func(t *testing.T) {
		api := API{ID: "api-1", Name: "Test", Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft, Metadata: nil}
		assert.NoError(t, api.Validate())
	})

	t.Run("API with exactly 200 char name", func(t *testing.T) {
		api := API{ID: "api-1", Name: strings.Repeat("a", 200), Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft}
		assert.NoError(t, api.Validate())
	})

	t.Run("API with exactly 5000 char description", func(t *testing.T) {
		api := API{ID: "api-1", Name: "Test", Description: strings.Repeat("a", 5000), Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft}
		assert.NoError(t, api.Validate())
	})

	t.Run("API with max length name + 1", func(t *testing.T) {
		api := API{ID: "api-1", Name: strings.Repeat("a", 201), Version: "1.0", BaseURL: "https://a.com", Type: "REST", Status: StatusDraft}
		assert.Error(t, api.Validate())
	})

	t.Run("Application with empty api keys", func(t *testing.T) {
		app := Application{ID: "app-1", Name: "Test", Owner: "user-1", APIKeys: nil, Status: StatusActive}
		assert.NoError(t, app.Validate())
	})
}

func TestValidationError_Error_GivenError_WhenStringCalled_ThenReturnsMessage(t *testing.T) {
	err := ValidationError{Field: "name", Message: "name is required"}
	assert.Equal(t, "name is required", err.Error())
}

func TestPolicy_JSONRoundTrip_GivenPolicy_WhenSerialized_ThenPreservesConfig(t *testing.T) {
	original := Policy{
		ID:       "pol-1",
		Name:     "Complex Policy",
		Type:     "transform",
		Config: map[string]interface{}{
			"strip_headers": []string{"X-Secret", "X-Internal"},
			"add_headers": map[string]string{
				"X-Processed-By": "gateway",
			},
			"rate": 100.5,
			"enabled": true,
		},
		Priority: 5,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Policy
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Type, decoded.Type)
	assert.Equal(t, original.Priority, decoded.Priority)
	assert.NotNil(t, decoded.Config)
}
