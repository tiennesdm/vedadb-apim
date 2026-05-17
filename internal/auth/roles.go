// Package auth provides role-based access control (RBAC), permission enforcement,
// tenant isolation helpers and reusable Gin middleware for the VedaDB API Manager.
package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// Permission represents a named action that can be guarded.
type Permission string

const (
	PermAPICreate       Permission = "api:create"
	PermAPIRead         Permission = "api:read"
	PermAPIUpdate       Permission = "api:update"
	PermAPIDelete       Permission = "api:delete"
	PermAPIPublish      Permission = "api:publish"
	PermAPIDeprecate    Permission = "api:deprecate"
	PermAPIRetire       Permission = "api:retire"
	PermPolicyManage    Permission = "policy:manage"
	PermClientRegister  Permission = "client:register"
	PermTokenRevoke     Permission = "token:revoke"
	PermKeyManage       Permission = "key:manage"
	PermTenantAdmin     Permission = "tenant:admin"
	PermUserManage      Permission = "user:manage"
	PermLifecycleChange Permission = "lifecycle:change"
)

// RolePermissions maps each role to the set of permissions it holds.
var RolePermissions = map[models.Role][]Permission{
	models.RoleSuperAdmin: {
		PermAPICreate, PermAPIRead, PermAPIUpdate, PermAPIDelete,
		PermAPIPublish, PermAPIDeprecate, PermAPIRetire,
		PermPolicyManage, PermClientRegister, PermTokenRevoke,
		PermKeyManage, PermTenantAdmin, PermUserManage,
		PermLifecycleChange,
	},
	models.RoleAdmin: {
		PermAPICreate, PermAPIRead, PermAPIUpdate, PermAPIDelete,
		PermAPIPublish, PermAPIDeprecate, PermAPIRetire,
		PermPolicyManage, PermClientRegister, PermTokenRevoke,
		PermKeyManage, PermTenantAdmin, PermUserManage,
		PermLifecycleChange,
	},
	models.RolePublisher: {
		PermAPICreate, PermAPIRead, PermAPIUpdate,
		PermAPIPublish, PermAPIDeprecate, PermAPIRetire,
		PermClientRegister, PermKeyManage, PermPolicyManage,
		PermLifecycleChange,
	},
	models.RoleSubscriber: {
		PermAPIRead, PermClientRegister, PermKeyManage,
	},
	models.RoleAnonymous: {
		PermAPIRead,
	},
}

// Context keys used for storing values in *gin.Context.
const (
	CtxKeyUserID   = "user_id"
	CtxKeyRoles    = "user_roles"
	CtxKeyTenantID = "tenant_id"
)

// RoleMiddleware returns a Gin middleware that ensures the caller has at least
// one of the required roles. It reads the roles from the Gin context set by an
// upstream authentication middleware.
func RoleMiddleware(allowed ...models.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		val, exists := c.Get(CtxKeyRoles)
		if !exists {
			respondForbidden(c, "no roles found in request context")
			return
		}

		userRoles, ok := val.([]string)
		if !ok || len(userRoles) == 0 {
			respondForbidden(c, "invalid roles in request context")
			return
		}

		for _, ur := range userRoles {
			for _, ar := range allowed {
				if models.Role(strings.ToLower(ur)) == ar {
					c.Next()
					return
				}
			}
		}

		respondForbidden(c, "insufficient role privileges")
	}
}

// RequirePermission returns a middleware that checks the caller has a specific
// permission. It uses the roles already stored in context.
func RequirePermission(perm Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		val, exists := c.Get(CtxKeyRoles)
		if !exists {
			respondForbidden(c, "no roles found in request context")
			return
		}

		userRoles, ok := val.([]string)
		if !ok || len(userRoles) == 0 {
			respondForbidden(c, "invalid roles in request context")
			return
		}

		if !HasPermission(userRoles, perm) {
			respondForbidden(c, "missing required permission: "+string(perm))
			return
		}

		c.Next()
	}
}

// AdminOnly middleware restricts access to super_admin or admin roles only.
func AdminOnly() gin.HandlerFunc {
	return RoleMiddleware(models.RoleSuperAdmin, models.RoleAdmin)
}

// PublisherOrAbove allows super_admin, admin, or publisher.
func PublisherOrAbove() gin.HandlerFunc {
	return RoleMiddleware(models.RoleSuperAdmin, models.RoleAdmin, models.RolePublisher)
}

// HasPermission checks whether any of the provided roles grant the permission.
func HasPermission(roles []string, perm Permission) bool {
	for _, r := range roles {
		perms, ok := RolePermissions[models.Role(strings.ToLower(r))]
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

// GetTenantID extracts the tenant ID from the Gin context. Returns empty string
// when multitenancy is disabled or tenant has not been resolved.
func GetTenantID(c *gin.Context) string {
	val, exists := c.Get(CtxKeyTenantID)
	if !exists {
		return "carbon.super"
	}
	tid, ok := val.(string)
	if !ok {
		return "carbon.super"
	}
	return tid
}

// WithTenant adds tenant isolation to a query context (used for DB queries).
func WithTenant(c *gin.Context, baseQuery map[string]interface{}) map[string]interface{} {
	tid := GetTenantID(c)
	baseQuery["tenant_id"] = tid
	return baseQuery
}

// SetContextUser is a helper used by upstream auth middleware to populate the
// Gin context with user information.
func SetContextUser(c *gin.Context, userID string, roles []string, tenantID string) {
	c.Set(CtxKeyUserID, userID)
	c.Set(CtxKeyRoles, roles)
	c.Set(CtxKeyTenantID, tenantID)
}

func respondForbidden(c *gin.Context, message string) {
	c.JSON(http.StatusForbidden, models.ErrorResponse{
		Code:      http.StatusForbidden,
		Message:   "Forbidden",
		Description: message,
	})
	c.Abort()
}
