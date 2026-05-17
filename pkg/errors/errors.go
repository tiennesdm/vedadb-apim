// Package errors provides a comprehensive error handling system for the VedaDB API Manager.
// It defines typed errors with HTTP status codes, error codes, and structured error responses
// suitable for production API gateway scenarios.
package errors

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ErrorCode defines a unique error code string for each error type in the system.
// These codes are stable identifiers that clients can use to handle specific error conditions.
type ErrorCode string

const (
	// ErrCodeInternal represents an internal server error.
	ErrCodeInternal ErrorCode = "APIM.INTERNAL_ERROR"
	// ErrCodeAPINotFound indicates the requested API does not exist.
	ErrCodeAPINotFound ErrorCode = "APIM.API_NOT_FOUND"
	// ErrCodeInvalidToken indicates the provided authentication token is invalid or malformed.
	ErrCodeInvalidToken ErrorCode = "APIM.INVALID_TOKEN"
	// ErrCodeTokenExpired indicates the authentication token has expired.
	ErrCodeTokenExpired ErrorCode = "APIM.TOKEN_EXPIRED"
	// ErrCodeRateLimited indicates the request has been throttled due to rate limiting.
	ErrCodeRateLimited ErrorCode = "APIM.RATE_LIMITED"
	// ErrCodeSubscriptionRequired indicates a valid subscription is required but not found.
	ErrCodeSubscriptionRequired ErrorCode = "APIM.SUBSCRIPTION_REQUIRED"
	// ErrCodeUnauthorized indicates the request lacks valid authentication credentials.
	ErrCodeUnauthorized ErrorCode = "APIM.UNAUTHORIZED"
	// ErrCodeForbidden indicates the authenticated user does not have permission.
	ErrCodeForbidden ErrorCode = "APIM.FORBIDDEN"
	// ErrCodeBadRequest indicates the request is malformed or invalid.
	ErrCodeBadRequest ErrorCode = "APIM.BAD_REQUEST"
	// ErrCodeInvalidCredentials indicates invalid username/password or client credentials.
	ErrCodeInvalidCredentials ErrorCode = "APIM.INVALID_CREDENTIALS"
	// ErrCodeApplicationNotFound indicates the requested application does not exist.
	ErrCodeApplicationNotFound ErrorCode = "APIM.APPLICATION_NOT_FOUND"
	// ErrCodeUserNotFound indicates the requested user does not exist.
	ErrCodeUserNotFound ErrorCode = "APIM.USER_NOT_FOUND"
	// ErrCodeDuplicateResource indicates a resource with the same identifier already exists.
	ErrCodeDuplicateResource ErrorCode = "APIM.DUPLICATE_RESOURCE"
	// ErrCodeValidationFailed indicates request validation failed.
	ErrCodeValidationFailed ErrorCode = "APIM.VALIDATION_FAILED"
	// ErrCodeDatabaseError indicates a database/VedaDB operation failed.
	ErrCodeDatabaseError ErrorCode = "APIM.DATABASE_ERROR"
	// ErrCodeServiceUnavailable indicates a backend service is temporarily unavailable.
	ErrCodeServiceUnavailable ErrorCode = "APIM.SERVICE_UNAVAILABLE"
	// ErrCodeGatewayTimeout indicates the gateway timed out waiting for a backend response.
	ErrCodeGatewayTimeout ErrorCode = "APIM.GATEWAY_TIMEOUT"
	// ErrCodeCircuitOpen indicates the circuit breaker is open for a backend service.
	ErrCodeCircuitOpen ErrorCode = "APIM.CIRCUIT_OPEN"
	// ErrCodeQuotaExceeded indicates the API quota for the application/user has been exceeded.
	ErrCodeQuotaExceeded ErrorCode = "APIM.QUOTA_EXCEEDED"
	// ErrCodeThrottled indicates the request was throttled due to spike arrest.
	ErrCodeThrottled ErrorCode = "APIM.THROTTLED"
	// ErrCodeScopeInsufficient indicates the token lacks required scopes.
	ErrCodeScopeInsufficient ErrorCode = "APIM.INSUFFICIENT_SCOPE"
	// ErrCodeMethodNotAllowed indicates the HTTP method is not allowed for the resource.
	ErrCodeMethodNotAllowed ErrorCode = "APIM.METHOD_NOT_ALLOWED"
	// ErrCodeNotFound indicates the requested resource was not found.
	ErrCodeNotFound ErrorCode = "APIM.NOT_FOUND"
	// ErrCodeInvalidAPIKey indicates the provided API key is invalid.
	ErrCodeInvalidAPIKey ErrorCode = "APIM.INVALID_API_KEY"
)

// APIMError is the standard error type used throughout the VedaDB API Manager.
// It implements the error interface and provides structured information for HTTP responses.
type APIMError struct {
	// Code is a machine-readable error code string.
	Code ErrorCode `json:"code"`
	// Message is a human-readable description of the error.
	Message string `json:"message"`
	// HTTPStatus is the recommended HTTP status code for this error.
	HTTPStatus int `json:"-"`
	// Details contains additional context-specific error details (optional).
	Details map[string]interface{} `json:"details,omitempty"`
	// Cause is the underlying error that caused this error, for internal debugging.
	Cause error `json:"-"`
}

// Error implements the error interface. It returns a JSON string representation
// of the error for structured logging purposes.
func (e *APIMError) Error() string {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Sprintf("[%s] %s", e.Code, e.Message)
	}
	return string(data)
}

// Unwrap returns the underlying cause of this error, supporting errors.Is and errors.As.
func (e *APIMError) Unwrap() error {
	return e.Cause
}

// WithCause wraps the error with an underlying cause for error chain tracing.
func (e *APIMError) WithCause(cause error) *APIMError {
	e.Cause = cause
	return e
}

// WithDetail adds a key-value detail to the error for additional context.
func (e *APIMError) WithDetail(key string, value interface{}) *APIMError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// WithMessage returns a new APIMError with a custom message while preserving other fields.
func (e *APIMError) WithMessage(msg string) *APIMError {
	return &APIMError{
		Code:       e.Code,
		Message:    msg,
		HTTPStatus: e.HTTPStatus,
		Details:    e.Details,
		Cause:      e.Cause,
	}
}

// Is checks if this error matches the target error code.
func (e *APIMError) Is(target *APIMError) bool {
	if target == nil {
		return false
	}
	return e.Code == target.Code
}

// WriteHTTPResponse writes the error as a JSON HTTP response to the client.
func (e *APIMError) WriteHTTPResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.HTTPStatus)
	json.NewEncoder(w).Encode(e)
}

// New creates a new APIMError with the given code, message, and HTTP status.
func New(code ErrorCode, message string, httpStatus int) *APIMError {
	return &APIMError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		Details:    nil,
	}
}

// Newf creates a new APIMError with a formatted message.
func Newf(code ErrorCode, httpStatus int, format string, args ...interface{}) *APIMError {
	return &APIMError{
		Code:       code,
		Message:    fmt.Sprintf(format, args...),
		HTTPStatus: httpStatus,
		Details:    nil,
	}
}

// Wrap wraps an existing error with an APIMError, preserving the cause chain.
func Wrap(code ErrorCode, message string, httpStatus int, cause error) *APIMError {
	return &APIMError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		Cause:      cause,
	}
}

// Wrapf wraps an existing error with a formatted message.
func Wrapf(code ErrorCode, httpStatus int, cause error, format string, args ...interface{}) *APIMError {
	return &APIMError{
		Code:       code,
		Message:    fmt.Sprintf(format, args...),
		HTTPStatus: httpStatus,
		Cause:      cause,
	}
}

// ---------------------------------------------------------------------------
// Predefined errors for common scenarios in the API Gateway.
// These provide consistent error responses across the entire system.
// ---------------------------------------------------------------------------

var (
	// ErrInternal is a generic internal server error.
	ErrInternal = New(ErrCodeInternal, "an internal server error occurred", http.StatusInternalServerError)

	// ErrAPINotFound indicates the requested API does not exist in the gateway.
	ErrAPINotFound = New(ErrCodeAPINotFound, "the requested API was not found", http.StatusNotFound)

	// ErrInvalidToken indicates the bearer token is malformed, expired, or otherwise invalid.
	ErrInvalidToken = New(ErrCodeInvalidToken, "the provided access token is invalid", http.StatusUnauthorized)

	// ErrTokenExpired indicates the access token has expired and must be refreshed.
	ErrTokenExpired = New(ErrCodeTokenExpired, "the access token has expired", http.StatusUnauthorized)

	// ErrRateLimited indicates the client has exceeded the allowed request rate.
	ErrRateLimited = New(ErrCodeRateLimited, "rate limit exceeded, please try again later", http.StatusTooManyRequests)

	// ErrSubscriptionRequired indicates no active subscription was found for the API.
	ErrSubscriptionRequired = New(ErrCodeSubscriptionRequired, "an active subscription is required to access this API", http.StatusForbidden)

	// ErrUnauthorized indicates missing or invalid authentication credentials.
	ErrUnauthorized = New(ErrCodeUnauthorized, "authentication is required to access this resource", http.StatusUnauthorized)

	// ErrForbidden indicates the authenticated user lacks permission for this resource.
	ErrForbidden = New(ErrCodeForbidden, "you do not have permission to access this resource", http.StatusForbidden)

	// ErrBadRequest indicates the client request is malformed.
	ErrBadRequest = New(ErrCodeBadRequest, "the request is invalid or malformed", http.StatusBadRequest)

	// ErrInvalidCredentials indicates invalid username/password combination.
	ErrInvalidCredentials = New(ErrCodeInvalidCredentials, "invalid credentials provided", http.StatusUnauthorized)

	// ErrApplicationNotFound indicates the application does not exist.
	ErrApplicationNotFound = New(ErrCodeApplicationNotFound, "the requested application was not found", http.StatusNotFound)

	// ErrUserNotFound indicates the user does not exist.
	ErrUserNotFound = New(ErrCodeUserNotFound, "the requested user was not found", http.StatusNotFound)

	// ErrDuplicateResource indicates a resource already exists.
	ErrDuplicateResource = New(ErrCodeDuplicateResource, "a resource with the same identifier already exists", http.StatusConflict)

	// ErrValidationFailed indicates request validation failed.
	ErrValidationFailed = New(ErrCodeValidationFailed, "request validation failed", http.StatusBadRequest)

	// ErrDatabaseError indicates a database operation failed.
	ErrDatabaseError = New(ErrCodeDatabaseError, "database operation failed", http.StatusInternalServerError)

	// ErrServiceUnavailable indicates a backend service is unavailable.
	ErrServiceUnavailable = New(ErrCodeServiceUnavailable, "the requested service is temporarily unavailable", http.StatusServiceUnavailable)

	// ErrGatewayTimeout indicates the gateway timed out waiting for backend.
	ErrGatewayTimeout = New(ErrCodeGatewayTimeout, "gateway timeout while waiting for backend service", http.StatusGatewayTimeout)

	// ErrCircuitOpen indicates the circuit breaker is open.
	ErrCircuitOpen = New(ErrCodeCircuitOpen, "service is temporarily unavailable due to circuit breaker", http.StatusServiceUnavailable)

	// ErrQuotaExceeded indicates the API quota has been exceeded.
	ErrQuotaExceeded = New(ErrCodeQuotaExceeded, "API quota has been exceeded for this billing period", http.StatusTooManyRequests)

	// ErrThrottled indicates the request was throttled.
	ErrThrottled = New(ErrCodeThrottled, "request has been throttled due to high traffic", http.StatusTooManyRequests)

	// ErrScopeInsufficient indicates the token lacks required scopes.
	ErrScopeInsufficient = New(ErrCodeScopeInsufficient, "the access token does not have the required scopes", http.StatusForbidden)

	// ErrMethodNotAllowed indicates the HTTP method is not allowed.
	ErrMethodNotAllowed = New(ErrCodeMethodNotAllowed, "the HTTP method is not allowed for this resource", http.StatusMethodNotAllowed)

	// ErrNotFound indicates the resource was not found.
	ErrNotFound = New(ErrCodeNotFound, "the requested resource was not found", http.StatusNotFound)

	// ErrInvalidAPIKey indicates the API key is invalid.
	ErrInvalidAPIKey = New(ErrCodeInvalidAPIKey, "the provided API key is invalid or has been revoked", http.StatusUnauthorized)
)

// ErrorResponse is the standard error response body sent to API clients.
type ErrorResponse struct {
	Code    ErrorCode              `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// NewErrorResponse creates an ErrorResponse from an APIMError.
func NewErrorResponse(err *APIMError) ErrorResponse {
	return ErrorResponse{
		Code:    err.Code,
		Message: err.Message,
		Details: err.Details,
	}
}
