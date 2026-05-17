package errors

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Error Types
// ============================================================================

// VAPIMError is the base error type for VedaDB API Manager
type VAPIMError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

func (e VAPIMError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// New creates a new VAPIMError
func New(code, message string, status int) VAPIMError {
	return VAPIMError{Code: code, Message: message, Status: status}
}

// NotFoundError represents a not found error
type NotFoundError struct {
	VAPIMError
	Resource string `json:"resource"`
	ID       string `json:"id"`
}

func NewNotFound(resource, id string) NotFoundError {
	return NotFoundError{
		VAPIMError: New("NOT_FOUND", fmt.Sprintf("%s '%s' not found", resource, id), 404),
		Resource:   resource,
		ID:         id,
	}
}

// ValidationError represents a validation error
type ValidationError struct {
	VAPIMError
	Field   string `json:"field"`
}

func NewValidation(field, message string) ValidationError {
	return ValidationError{
		VAPIMError: New("VALIDATION_ERROR", message, 400),
		Field:      field,
	}
}

// ConflictError represents a conflict error
type ConflictError struct {
	VAPIMError
	Resource string `json:"resource"`
}

func NewConflict(resource, message string) ConflictError {
	return ConflictError{
		VAPIMError: New("CONFLICT", message, 409),
		Resource:   resource,
	}
}

// UnauthorizedError represents an unauthorized error
type UnauthorizedError struct {
	VAPIMError
}

func NewUnauthorized(message string) UnauthorizedError {
	return UnauthorizedError{
		VAPIMError: New("UNAUTHORIZED", message, 401),
	}
}

// ForbiddenError represents a forbidden error
type ForbiddenError struct {
	VAPIMError
}

func NewForbidden(message string) ForbiddenError {
	return ForbiddenError{
		VAPIMError: New("FORBIDDEN", message, 403),
	}
}

// RateLimitError represents a rate limit exceeded error
type RateLimitError struct {
	VAPIMError
	RetryAfter int `json:"retry_after"`
}

func NewRateLimit(message string, retryAfter int) RateLimitError {
	return RateLimitError{
		VAPIMError: New("RATE_LIMIT_EXCEEDED", message, 429),
		RetryAfter: retryAfter,
	}
}

// InternalError represents an internal server error
type InternalError struct {
	VAPIMError
	Cause string `json:"cause,omitempty"`
}

func NewInternal(message string) InternalError {
	return InternalError{
		VAPIMError: New("INTERNAL_ERROR", message, 500),
	}
}

// IsVAPIMError checks if an error is a VAPIMError
func IsVAPIMError(err error) bool {
	var vErr VAPIMError
	return errors.As(err, &vErr)
}

// GetStatusCode extracts the HTTP status code from an error
func GetStatusCode(err error) int {
	var vErr VAPIMError
	if errors.As(err, &vErr) {
		return vErr.Status
	}
	return 500
}

// GetCode extracts the error code from an error
func GetCode(err error) string {
	var vErr VAPIMError
	if errors.As(err, &vErr) {
		return vErr.Code
	}
	return "UNKNOWN"
}

// Wrap wraps a standard error with VAPIMError context
func Wrap(err error, code, message string, status int) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", New(code, message, status), err)
}

// ============================================================================
// TESTS
// ============================================================================

func TestVAPIMError_Error_GivenError_WhenStringed_ThenReturnsFormattedString(t *testing.T) {
	tests := []struct {
		name     string
		err      VAPIMError
		expected string
	}{
		{
			name:     "simple error",
			err:      VAPIMError{Code: "TEST_ERROR", Message: "something went wrong"},
			expected: "[TEST_ERROR] something went wrong",
		},
		{
			name:     "error with status",
			err:      VAPIMError{Code: "NOT_FOUND", Message: "resource not found", Status: 404},
			expected: "[NOT_FOUND] resource not found",
		},
		{
			name:     "empty error",
			err:      VAPIMError{},
			expected: "[] ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestNew_GivenParameters_WhenCreated_ThenReturnsVAPIMError(t *testing.T) {
	err := New("CUSTOM_ERROR", "custom message", 418)
	assert.Equal(t, "CUSTOM_ERROR", err.Code)
	assert.Equal(t, "custom message", err.Message)
	assert.Equal(t, 418, err.Status)
	assert.Equal(t, "[CUSTOM_ERROR] custom message", err.Error())
}

func TestNewNotFound_GivenResourceAndID_WhenCreated_ThenReturnsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		id       string
		expected string
		status   int
	}{
		{"API not found", "API", "api-123", "[NOT_FOUND] API 'api-123' not found", 404},
		{"User not found", "User", "user-456", "[NOT_FOUND] User 'user-456' not found", 404},
		{"empty resource", "", "id-1", "[NOT_FOUND] 'id-1' not found", 404},
		{"empty id", "API", "", "[NOT_FOUND] API '' not found", 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewNotFound(tt.resource, tt.id)
			assert.Equal(t, "NOT_FOUND", err.Code)
			assert.Equal(t, tt.expected, err.Error())
			assert.Equal(t, tt.status, err.Status)
			assert.Equal(t, tt.resource, err.Resource)
			assert.Equal(t, tt.id, err.ID)
		})
	}
}

func TestNewValidation_GivenFieldAndMessage_WhenCreated_ThenReturnsValidationError(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		message string
	}{
		{"name required", "name", "name is required"},
		{"invalid email", "email", "invalid email format"},
		{"too long", "description", "description exceeds 5000 characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewValidation(tt.field, tt.message)
			assert.Equal(t, "VALIDATION_ERROR", err.Code)
			assert.Equal(t, 400, err.Status)
			assert.Equal(t, tt.field, err.Field)
			assert.Equal(t, tt.message, err.Message)
		})
	}
}

func TestNewConflict_GivenResourceAndMessage_WhenCreated_ThenReturnsConflictError(t *testing.T) {
	err := NewConflict("API", "API with name 'Test' already exists")
	assert.Equal(t, "CONFLICT", err.Code)
	assert.Equal(t, 409, err.Status)
	assert.Equal(t, "API", err.Resource)
	assert.Contains(t, err.Message, "already exists")
}

func TestNewUnauthorized_GivenMessage_WhenCreated_ThenReturnsUnauthorizedError(t *testing.T) {
	err := NewUnauthorized("invalid credentials")
	assert.Equal(t, "UNAUTHORIZED", err.Code)
	assert.Equal(t, 401, err.Status)
	assert.Equal(t, "invalid credentials", err.Message)
}

func TestNewForbidden_GivenMessage_WhenCreated_ThenReturnsForbiddenError(t *testing.T) {
	err := NewForbidden("insufficient permissions")
	assert.Equal(t, "FORBIDDEN", err.Code)
	assert.Equal(t, 403, err.Status)
	assert.Equal(t, "insufficient permissions", err.Message)
}

func TestNewRateLimit_GivenMessageAndRetry_WhenCreated_ThenReturnsRateLimitError(t *testing.T) {
	err := NewRateLimit("rate limit exceeded", 60)
	assert.Equal(t, "RATE_LIMIT_EXCEEDED", err.Code)
	assert.Equal(t, 429, err.Status)
	assert.Equal(t, 60, err.RetryAfter)
}

func TestNewInternal_GivenMessage_WhenCreated_ThenReturnsInternalError(t *testing.T) {
	err := NewInternal("database connection failed")
	assert.Equal(t, "INTERNAL_ERROR", err.Code)
	assert.Equal(t, 500, err.Status)
	assert.Equal(t, "database connection failed", err.Message)
}

func TestIsVAPIMError_GivenVAPIMError_WhenChecked_ThenReturnsTrue(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"VAPIMError", New("TEST", "test", 500), true},
		{"NotFoundError", NewNotFound("API", "123"), true},
		{"ValidationError", NewValidation("name", "required"), true},
		{"ConflictError", NewConflict("API", "exists"), true},
		{"UnauthorizedError", NewUnauthorized("bad creds"), true},
		{"ForbiddenError", NewForbidden("no access"), true},
		{"RateLimitError", NewRateLimit("too many", 60), true},
		{"InternalError", NewInternal("oops"), true},
		{"standard error", errors.New("standard error"), false},
		{"nil error", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsVAPIMError(tt.err))
		})
	}
}

func TestGetStatusCode_GivenError_WhenExtracted_ThenReturnsCorrectCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{"not found", NewNotFound("API", "123"), 404},
		{"validation", NewValidation("name", "required"), 400},
		{"conflict", NewConflict("API", "exists"), 409},
		{"unauthorized", NewUnauthorized("bad"), 401},
		{"forbidden", NewForbidden("no"), 403},
		{"rate limit", NewRateLimit("slow down", 30), 429},
		{"internal", NewInternal("db fail"), 500},
		{"standard error", errors.New("standard"), 500},
		{"nil", nil, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetStatusCode(tt.err))
		})
	}
}

func TestGetCode_GivenError_WhenExtracted_ThenReturnsCorrectCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"not found", NewNotFound("API", "123"), "NOT_FOUND"},
		{"validation", NewValidation("name", "required"), "VALIDATION_ERROR"},
		{"standard error", errors.New("standard"), "UNKNOWN"},
		{"nil", nil, "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetCode(tt.err))
		})
	}
}

func TestWrap_GivenError_WhenWrapped_ThenPreservesCause(t *testing.T) {
	original := errors.New("original error")
	wrapped := Wrap(original, "WRAPPED", "wrapped message", 500)
	assert.Error(t, wrapped)
	assert.Contains(t, wrapped.Error(), "original error")
	assert.Contains(t, wrapped.Error(), "wrapped message")
	assert.True(t, IsVAPIMError(wrapped))
	assert.Equal(t, 500, GetStatusCode(wrapped))
}

func TestWrap_GivenNil_WhenWrapped_ThenReturnsNil(t *testing.T) {
	result := Wrap(nil, "CODE", "message", 500)
	assert.NoError(t, result)
}

func TestErrorWrapping_GivenWrappedErrors_WhenUnwrapped_ThenChainAccessible(t *testing.T) {
	// Create a chain: internal -> wrapped -> outer
	inner := errors.New("database connection refused")
	middle := Wrap(inner, "DB_ERROR", "database error", 500)
	outer := Wrap(middle, "API_ERROR", "API request failed", 500)

	assert.Error(t, outer)
	assert.True(t, IsVAPIMError(outer))
	assert.Equal(t, "API_ERROR", GetCode(outer))
	assert.Contains(t, outer.Error(), "database connection refused")
	assert.Contains(t, outer.Error(), "API request failed")
}

func TestErrorEquality_GivenSameErrors_WhenCompared_ThenEqual(t *testing.T) {
	err1 := New("TEST", "test message", 400)
	err2 := New("TEST", "test message", 400)
	assert.Equal(t, err1, err2)
	assert.Equal(t, err1.Error(), err2.Error())
}

func TestErrorWithDetail_GivenDetail_WhenStringed_ThenContainsDetail(t *testing.T) {
	err := VAPIMError{Code: "TEST", Message: "test", Detail: "additional context"}
	assert.Equal(t, "[TEST] test", err.Error())
	assert.Equal(t, "additional context", err.Detail)
}

func TestAllErrorTypes_GivenVariousTypes_WhenCreated_ThenHaveCorrectDefaults(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   string
		status int
	}{
		{"VAPIMError", New("CUSTOM", "msg", 200), "CUSTOM", 200},
		{"NotFoundError", NewNotFound("r", "i"), "NOT_FOUND", 404},
		{"ValidationError", NewValidation("f", "m"), "VALIDATION_ERROR", 400},
		{"ConflictError", NewConflict("r", "m"), "CONFLICT", 409},
		{"UnauthorizedError", NewUnauthorized("m"), "UNAUTHORIZED", 401},
		{"ForbiddenError", NewForbidden("m"), "FORBIDDEN", 403},
		{"RateLimitError", NewRateLimit("m", 1), "RATE_LIMIT_EXCEEDED", 429},
		{"InternalError", NewInternal("m"), "INTERNAL_ERROR", 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.code, GetCode(tt.err))
			assert.Equal(t, tt.status, GetStatusCode(tt.err))
		})
	}
}
