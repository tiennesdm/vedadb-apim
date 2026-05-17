package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Auth Implementation
// ============================================================================

// Role represents a user role
type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RolePublisher  Role = "publisher"
	RoleReviewer   Role = "reviewer"
	RoleSubscriber Role = "subscriber"
	RoleViewer     Role = "viewer"
)

// Permission represents a specific permission
type Permission string

const (
	PermAPICreate   Permission = "api:create"
	PermAPIRead     Permission = "api:read"
	PermAPIUpdate   Permission = "api:update"
	PermAPIDelete   Permission = "api:delete"
	PermAPIPublish  Permission = "api:publish"
	PermAPIReview   Permission = "api:review"
	PermAppCreate   Permission = "app:create"
	PermAppRead     Permission = "app:read"
	PermAppDelete   Permission = "app:delete"
	PermSubCreate   Permission = "subscription:create"
	PermSubRead     Permission = "subscription:read"
	PermSubDelete   Permission = "subscription:delete"
	PermUserManage  Permission = "user:manage"
	PermConfigRead  Permission = "config:read"
	PermConfigWrite Permission = "config:write"
	PermAnalytics   Permission = "analytics:read"
)

// rolePermissions maps roles to their permissions
var rolePermissions = map[Role][]Permission{
	RoleSuperAdmin: {
		PermAPICreate, PermAPIRead, PermAPIUpdate, PermAPIDelete, PermAPIPublish, PermAPIReview,
		PermAppCreate, PermAppRead, PermAppDelete,
		PermSubCreate, PermSubRead, PermSubDelete,
		PermUserManage, PermConfigRead, PermConfigWrite, PermAnalytics,
	},
	RoleAdmin: {
		PermAPICreate, PermAPIRead, PermAPIUpdate, PermAPIDelete, PermAPIPublish,
		PermAppCreate, PermAppRead, PermAppDelete,
		PermSubCreate, PermSubRead, PermSubDelete,
		PermUserManage, PermConfigRead, PermConfigWrite, PermAnalytics,
	},
	RolePublisher: {
		PermAPICreate, PermAPIRead, PermAPIUpdate, PermAPIPublish,
		PermAppCreate, PermAppRead, PermAppDelete,
		PermSubCreate, PermSubRead, PermSubDelete,
	},
	RoleReviewer: {
		PermAPIRead, PermAPIReview, PermAppRead, PermSubRead, PermAnalytics,
	},
	RoleSubscriber: {
		PermAPIRead, PermAppCreate, PermAppRead, PermAppDelete,
		PermSubCreate, PermSubRead, PermSubDelete,
	},
	RoleViewer: {
		PermAPIRead, PermAppRead, PermSubRead,
	},
}

// RoleChecker handles role and permission checking
type RoleChecker struct{}

// NewRoleChecker creates a new role checker
func NewRoleChecker() *RoleChecker {
	return &RoleChecker{}
}

// HasRole checks if the user has a specific role
func (rc *RoleChecker) HasRole(userRoles []Role, required Role) bool {
	for _, r := range userRoles {
		if r == RoleSuperAdmin && required != RoleSuperAdmin {
			// Super admin has all roles
			return true
		}
		if r == required {
			return true
		}
	}
	return false
}

// HasAnyRole checks if the user has any of the specified roles
func (rc *RoleChecker) HasAnyRole(userRoles []Role, required []Role) bool {
	for _, req := range required {
		if rc.HasRole(userRoles, req) {
			return true
		}
	}
	return false
}

// HasPermission checks if the user has a specific permission
func (rc *RoleChecker) HasPermission(userRoles []Role, perm Permission) bool {
	for _, role := range userRoles {
		perms, ok := rolePermissions[role]
		if !ok {
			continue
		}
		for _, p := range perms {
			if p == perm {
				return true
			}
		}
	}
	return false
}

// HasAllPermissions checks if the user has all specified permissions
func (rc *RoleChecker) HasAllPermissions(userRoles []Role, perms []Permission) bool {
	for _, perm := range perms {
		if !rc.HasPermission(userRoles, perm) {
			return false
		}
	}
	return true
}

// HasAnyPermission checks if the user has any of the specified permissions
func (rc *RoleChecker) HasAnyPermission(userRoles []Role, perms []Permission) bool {
	for _, perm := range perms {
		if rc.HasPermission(userRoles, perm) {
			return true
		}
	}
	return false
}

// GetRolePermissions returns all permissions for a role
func (rc *RoleChecker) GetRolePermissions(role Role) []Permission {
	perms, ok := rolePermissions[role]
	if !ok {
		return nil
	}
	result := make([]Permission, len(perms))
	copy(result, perms)
	return result
}

// GetUserPermissions returns all permissions for a user (union of all roles)
func (rc *RoleChecker) GetUserPermissions(userRoles []Role) []Permission {
	permSet := make(map[Permission]bool)
	for _, role := range userRoles {
		perms := rc.GetRolePermissions(role)
		for _, p := range perms {
			permSet[p] = true
		}
	}
	result := make([]Permission, 0, len(permSet))
	for p := range permSet {
		result = append(result, p)
	}
	return result
}

// Middleware creates HTTP middleware for role-based access control
func (rc *RoleChecker) Middleware(requiredRoles []Role, requiredPerms []Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get roles from context (set by auth middleware)
			userRoles, ok := GetRolesFromContext(r.Context())
			if !ok || len(userRoles) == 0 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			// Check roles
			if len(requiredRoles) > 0 {
				if !rc.HasAnyRole(userRoles, requiredRoles) {
					http.Error(w, `{"error":"forbidden - insufficient role"}`, http.StatusForbidden)
					return
				}
			}

			// Check permissions
			if len(requiredPerms) > 0 {
				if !rc.HasAnyPermission(userRoles, requiredPerms) {
					http.Error(w, `{"error":"forbidden - insufficient permission"}`, http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// contextKey is used for context values
type contextKey string

const rolesContextKey contextKey = "user_roles"

// ContextWithRoles adds roles to a context
func ContextWithRoles(ctx context.Context, roles []Role) context.Context {
	return context.WithValue(ctx, rolesContextKey, roles)
}

// GetRolesFromContext extracts roles from a context
func GetRolesFromContext(ctx context.Context) ([]Role, bool) {
	roles, ok := ctx.Value(rolesContextKey).([]Role)
	return roles, ok
}

// ============================================================================
// TESTS
// ============================================================================

func TestRoleChecker_HasRole_GivenValidRole_WhenChecked_ThenReturnsTrue(t *testing.T) {
	rc := NewRoleChecker()

	tests := []struct {
		name      string
		userRoles []Role
		required  Role
		expected  bool
	}{
		{"exact match", []Role{RoleAdmin}, RoleAdmin, true},
		{"multiple roles contains", []Role{RoleViewer, RolePublisher}, RolePublisher, true},
		{"super admin has all", []Role{RoleSuperAdmin}, RoleAdmin, true},
		{"super admin has publisher", []Role{RoleSuperAdmin}, RolePublisher, true},
		{"no match", []Role{RoleViewer}, RoleAdmin, false},
		{"empty roles", []Role{}, RoleAdmin, false},
		{"viewer wants super", []Role{RoleViewer}, RoleSuperAdmin, false},
		{"publisher wants admin", []Role{RolePublisher}, RoleAdmin, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rc.HasRole(tt.userRoles, tt.required)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleChecker_HasAnyRole_GivenMultipleRequired_WhenChecked_ThenReturnsCorrectResult(t *testing.T) {
	rc := NewRoleChecker()

	tests := []struct {
		name      string
		userRoles []Role
		required  []Role
		expected  bool
	}{
		{"has one of required", []Role{RolePublisher, RoleViewer}, []Role{RoleAdmin, RolePublisher}, true},
		{"has none", []Role{RoleViewer}, []Role{RoleAdmin, RolePublisher}, false},
		{"has all", []Role{RoleAdmin, RolePublisher}, []Role{RoleAdmin, RolePublisher}, true},
		{"super admin matches any", []Role{RoleSuperAdmin}, []Role{RoleAdmin, RolePublisher}, true},
		{"empty required", []Role{RoleViewer}, []Role{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rc.HasAnyRole(tt.userRoles, tt.required)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleChecker_HasPermission_GivenValidPermission_WhenChecked_ThenReturnsTrue(t *testing.T) {
	rc := NewRoleChecker()

	tests := []struct {
		name      string
		userRoles []Role
		perm      Permission
		expected  bool
	}{
		{"admin can create API", []Role{RoleAdmin}, PermAPICreate, true},
		{"admin can read API", []Role{RoleAdmin}, PermAPIRead, true},
		{"admin can delete API", []Role{RoleAdmin}, PermAPIDelete, true},
		{"publisher can create API", []Role{RolePublisher}, PermAPICreate, true},
		{"publisher cannot review", []Role{RolePublisher}, PermAPIReview, false},
		{"reviewer can review", []Role{RoleReviewer}, PermAPIReview, true},
		{"reviewer cannot create", []Role{RoleReviewer}, PermAPICreate, false},
		{"viewer can only read", []Role{RoleViewer}, PermAPIRead, true},
		{"viewer cannot create", []Role{RoleViewer}, PermAPICreate, false},
		{"subscriber can create app", []Role{RoleSubscriber}, PermAppCreate, true},
		{"subscriber cannot create API", []Role{RoleSubscriber}, PermAPICreate, false},
		{"super admin has all", []Role{RoleSuperAdmin}, PermConfigWrite, true},
		{"empty roles", []Role{}, PermAPIRead, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rc.HasPermission(tt.userRoles, tt.perm)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleChecker_HasAllPermissions_GivenMultiplePerms_WhenChecked_ThenReturnsCorrectResult(t *testing.T) {
	rc := NewRoleChecker()

	tests := []struct {
		name      string
		userRoles []Role
		perms     []Permission
		expected  bool
	}{
		{
			name:      "has all",
			userRoles: []Role{RoleAdmin},
			perms:     []Permission{PermAPICreate, PermAPIRead, PermAPIDelete},
			expected:  true,
		},
		{
			name:      "missing one",
			userRoles: []Role{RolePublisher},
			perms:     []Permission{PermAPICreate, PermAPIReview},
			expected:  false,
		},
		{
			name:      "empty permissions",
			userRoles: []Role{RoleViewer},
			perms:     []Permission{},
			expected:  true,
		},
		{
			name:      "super admin has all",
			userRoles: []Role{RoleSuperAdmin},
			perms:     []Permission{PermAPICreate, PermAPIReview, PermConfigWrite, PermUserManage},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rc.HasAllPermissions(tt.userRoles, tt.perms)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleChecker_HasAnyPermission_GivenMultiplePerms_WhenChecked_ThenReturnsCorrectResult(t *testing.T) {
	rc := NewRoleChecker()

	tests := []struct {
		name      string
		userRoles []Role
		perms     []Permission
		expected  bool
	}{
		{
			name:      "has one of two",
			userRoles: []Role{RolePublisher},
			perms:     []Permission{PermAPIReview, PermAPICreate},
			expected:  true,
		},
		{
			name:      "has none",
			userRoles: []Role{RoleViewer},
			perms:     []Permission{PermAPICreate, PermAPIReview},
			expected:  false,
		},
		{
			name:      "empty permissions",
			userRoles: []Role{RoleViewer},
			perms:     []Permission{},
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rc.HasAnyPermission(tt.userRoles, tt.perms)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleChecker_GetRolePermissions_GivenRole_WhenQueried_ThenReturnsPermissions(t *testing.T) {
	rc := NewRoleChecker()

	tests := []struct {
		name         string
		role         Role
		expectLen    int
		expectPerm   Permission
		notExpectPerm Permission
	}{
		{"super_admin", RoleSuperAdmin, 16, PermConfigWrite, ""},
		{"admin", RoleAdmin, 15, PermUserManage, PermAPIReview},
		{"publisher", RolePublisher, 10, PermAPICreate, PermAPIReview},
		{"reviewer", RoleReviewer, 5, PermAPIReview, PermAPICreate},
		{"subscriber", RoleSubscriber, 8, PermAppCreate, PermAPICreate},
		{"viewer", RoleViewer, 3, PermAPIRead, PermAPICreate},
		{"unknown", "unknown", 0, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perms := rc.GetRolePermissions(tt.role)
			assert.Len(t, perms, tt.expectLen)
			if tt.expectPerm != "" {
				found := false
				for _, p := range perms {
					if p == tt.expectPerm {
						found = true
						break
					}
				}
				assert.True(t, found, "should contain %s", tt.expectPerm)
			}
			if tt.notExpectPerm != "" {
				found := false
				for _, p := range perms {
					if p == tt.notExpectPerm {
						found = true
						break
					}
				}
				assert.False(t, found, "should not contain %s", tt.notExpectPerm)
			}
		})
	}
}

func TestRoleChecker_GetUserPermissions_GivenMultipleRoles_WhenQueried_ThenReturnsUnion(t *testing.T) {
	rc := NewRoleChecker()

	// Viewer + Publisher should have union of both
	perms := rc.GetUserPermissions([]Role{RoleViewer, RolePublisher})

	// Should have viewer permissions
	assert.Contains(t, perms, PermAPIRead)

	// Should have publisher permissions
	assert.Contains(t, perms, PermAPICreate)
	assert.Contains(t, perms, PermAPIUpdate)

	// Should not have reviewer permissions
	assert.NotContains(t, perms, PermAPIReview)
}

func TestContextWithRoles_GivenRoles_WhenStored_ThenRetrievable(t *testing.T) {
	ctx := context.Background()
	roles := []Role{RoleAdmin, RolePublisher}

	ctx = ContextWithRoles(ctx, roles)

	retrieved, ok := GetRolesFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, roles, retrieved)
}

func TestGetRolesFromContext_GivenNoRoles_WhenRetrieved_ThenReturnsFalse(t *testing.T) {
	ctx := context.Background()
	_, ok := GetRolesFromContext(ctx)
	assert.False(t, ok)
}

func TestRoleChecker_Middleware_GivenValidRole_WhenRequestMade_ThenAllowed(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware([]Role{RoleAdmin}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	ctx := ContextWithRoles(req.Context(), []Role{RoleAdmin})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "success", w.Body.String())
}

func TestRoleChecker_Middleware_GivenInsufficientRole_WhenRequestMade_ThenForbidden(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware([]Role{RoleAdmin}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	ctx := ContextWithRoles(req.Context(), []Role{RoleViewer})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "forbidden")
}

func TestRoleChecker_Middleware_GivenNoRoles_WhenRequestMade_ThenUnauthorized(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware([]Role{RoleAdmin}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "unauthorized")
}

func TestRoleChecker_Middleware_GivenValidPermission_WhenRequestMade_ThenAllowed(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware(nil, []Permission{PermAPICreate})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("created"))
	}))

	req := httptest.NewRequest("POST", "/api", nil)
	ctx := ContextWithRoles(req.Context(), []Role{RolePublisher})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRoleChecker_Middleware_GivenInsufficientPermission_WhenRequestMade_ThenForbidden(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware(nil, []Permission{PermAPICreate})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api", nil)
	ctx := ContextWithRoles(req.Context(), []Role{RoleViewer})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "insufficient permission")
}

func TestRoleChecker_Middleware_GivenSuperAdmin_WhenAnyRoleRequired_ThenAllowed(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware([]Role{RoleAdmin}, []Permission{PermConfigWrite})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/admin", nil)
	ctx := ContextWithRoles(req.Context(), []Role{RoleSuperAdmin})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRoleChecker_Middleware_GivenNoRequirements_WhenRequestMade_ThenAllowed(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/public", nil)
	// No roles needed
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRoleChecker_EdgeCases_GivenBoundaryConditions_WhenChecked_ThenHandlesCorrectly(t *testing.T) {
	rc := NewRoleChecker()

	t.Run("nil user roles for HasRole", func(t *testing.T) {
		assert.False(t, rc.HasRole(nil, RoleAdmin))
	})

	t.Run("nil user roles for HasPermission", func(t *testing.T) {
		assert.False(t, rc.HasPermission(nil, PermAPIRead))
	})

	t.Run("unknown role", func(t *testing.T) {
		assert.False(t, rc.HasPermission([]Role{"unknown"}, PermAPIRead))
	})

	t.Run("duplicate roles", func(t *testing.T) {
		assert.True(t, rc.HasRole([]Role{RoleAdmin, RoleAdmin}, RoleAdmin))
	})

	t.Run("multiple roles with one matching", func(t *testing.T) {
		assert.True(t, rc.HasPermission([]Role{RoleViewer, RolePublisher}, PermAPICreate))
	})
}

func TestRole_String_GivenRole_WhenStringed_ThenReturnsCorrectValue(t *testing.T) {
	assert.Equal(t, "super_admin", string(RoleSuperAdmin))
	assert.Equal(t, "admin", string(RoleAdmin))
	assert.Equal(t, "publisher", string(RolePublisher))
	assert.Equal(t, "reviewer", string(RoleReviewer))
	assert.Equal(t, "subscriber", string(RoleSubscriber))
	assert.Equal(t, "viewer", string(RoleViewer))
}

func TestPermission_String_GivenPermission_WhenStringed_ThenReturnsCorrectValue(t *testing.T) {
	assert.Equal(t, "api:create", string(PermAPICreate))
	assert.Equal(t, "api:read", string(PermAPIRead))
	assert.Equal(t, "api:delete", string(PermAPIDelete))
	assert.Equal(t, "app:create", string(PermAppCreate))
	assert.Equal(t, "subscription:read", string(PermSubRead))
	assert.Equal(t, "user:manage", string(PermUserManage))
}

func TestRoleChecker_PermissionCombinations_GivenVariousRoles_WhenChecked_ThenCorrectResults(t *testing.T) {
	rc := NewRoleChecker()

	// Test all roles against all permissions
	allRoles := []Role{RoleSuperAdmin, RoleAdmin, RolePublisher, RoleReviewer, RoleSubscriber, RoleViewer}
	allPerms := []Permission{
		PermAPICreate, PermAPIRead, PermAPIUpdate, PermAPIDelete, PermAPIPublish, PermAPIReview,
		PermAppCreate, PermAppRead, PermAppDelete,
		PermSubCreate, PermSubRead, PermSubDelete,
		PermUserManage, PermConfigRead, PermConfigWrite, PermAnalytics,
	}

	for _, role := range allRoles {
		for _, perm := range allPerms {
			t.Run(fmt.Sprintf("%s_%s", role, perm), func(t *testing.T) {
				hasPerm := rc.HasPermission([]Role{role}, perm)
				rolePerms := rc.GetRolePermissions(role)
				expected := false
				for _, p := range rolePerms {
					if p == perm {
						expected = true
						break
					}
				}
				assert.Equal(t, expected, hasPerm,
					"role %s should %shave permission %s",
					role, map[bool]string{true: "", false: "not "}[expected], perm)
			})
		}
	}
}

func TestRoleChecker_Middleware_Chain_GivenMultipleMiddlewares_WhenUsed_ThenCorrectOrder(t *testing.T) {
	rc := NewRoleChecker()

	var order []string

	// First middleware - auth check
	authMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "auth")
			next.ServeHTTP(w, r)
		})
	}

	// Role middleware
	roleMiddleware := rc.Middleware([]Role{RoleAdmin}, nil)

	// Final handler
	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "final")
		w.WriteHeader(http.StatusOK)
	})

	// Chain: auth -> role -> final
	handler := authMiddleware(roleMiddleware(finalHandler))

	req := httptest.NewRequest("GET", "/api", nil)
	ctx := ContextWithRoles(req.Context(), []Role{RoleAdmin})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"auth", "final"}, order)
}

func TestRoleChecker_Middleware_MethodSpecific_GivenDifferentMethods_WhenChecked_ThenMethodIndependent(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware([]Role{RoleAdmin}, []Permission{PermAPIRead})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api", nil)
			ctx := ContextWithRoles(req.Context(), []Role{RoleAdmin})
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestRoleChecker_Middleware_ResponseBody_GivenForbidden_WhenBlocked_ThenJSONError(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware([]Role{RoleAdmin}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api", nil)
	ctx := ContextWithRoles(req.Context(), []Role{RoleViewer})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "error")
	assert.Contains(t, body, "forbidden")
}

func TestRolesFromContext_GivenContextWithRoles_WhenModified_ThenOriginalUnchanged(t *testing.T) {
	ctx := context.Background()
	originalRoles := []Role{RoleAdmin, RolePublisher}
	ctx = ContextWithRoles(ctx, originalRoles)

	retrieved, ok := GetRolesFromContext(ctx)
	require.True(t, ok)

	// Modify retrieved
	retrieved[0] = RoleViewer

	// Original context should be unchanged
	retrieved2, _ := GetRolesFromContext(ctx)
	assert.Equal(t, RoleAdmin, retrieved2[0])
}

func TestRoleChecker_ComplexScenario_GivenMultiRoleUser_WhenChecked_ThenCorrectPermissions(t *testing.T) {
	rc := NewRoleChecker()

	// A user with both publisher and reviewer roles
	userRoles := []Role{RolePublisher, RoleReviewer}

	// Should have publisher permissions
	assert.True(t, rc.HasPermission(userRoles, PermAPICreate))
	assert.True(t, rc.HasPermission(userRoles, PermAPIUpdate))

	// Should have reviewer permissions
	assert.True(t, rc.HasPermission(userRoles, PermAPIReview))

	// Should NOT have admin-specific permissions
	assert.False(t, rc.HasPermission(userRoles, PermAPIDelete))
	assert.False(t, rc.HasPermission(userRoles, PermUserManage))

	// Should have union of both
	allPerms := rc.GetUserPermissions(userRoles)
	assert.Contains(t, allPerms, PermAPICreate)
	assert.Contains(t, allPerms, PermAPIReview)
	assert.Contains(t, allPerms, PermAppCreate)
	assert.Contains(t, allPerms, PermSubRead)
}

func TestRoleChecker_Middleware_PathSpecific_GivenPath_WhenRequestMade_ThenPathIndependent(t *testing.T) {
	rc := NewRoleChecker()

	handler := rc.Middleware([]Role{RoleAdmin}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	paths := []string{"/", "/api", "/api/v1/users", "/api/v1/users/123", "/admin/config"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			ctx := ContextWithRoles(req.Context(), []Role{RoleAdmin})
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

func TestRoleChecker_PermsForUnknownRole_GivenUnknownRole_WhenQueried_ThenEmpty(t *testing.T) {
	rc := NewRoleChecker()
	perms := rc.GetRolePermissions("nonexistent_role")
	assert.Nil(t, perms)
	assert.Empty(t, perms)
}

func TestRoleChecker_HasAnyPermission_GivenEmptyPerms_WhenChecked_ThenFalse(t *testing.T) {
	rc := NewRoleChecker()
	result := rc.HasAnyPermission([]Role{RoleAdmin}, []Permission{})
	assert.False(t, result)
}

func TestRoleChecker_HasAllPermissions_GivenEmptyPerms_WhenChecked_ThenTrue(t *testing.T) {
	rc := NewRoleChecker()
	result := rc.HasAllPermissions([]Role{RoleAdmin}, []Permission{})
	assert.True(t, result)
}
