// Package tenant provides multi-tenancy middleware for the VedaDB API Manager.
package tenant

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vedadb/vapim/pkg/models"
)

// Config holds the configuration for the tenant middleware.
type Config struct {
	// Resolver is the tenant resolution strategy. Defaults to subdomain + header chain.
	Resolver Resolver
	// LookupFn resolves a tenant identifier to a full tenant model.
	LookupFn LookupFn
	// RootDomain is the root domain for subdomain-based resolution.
	RootDomain string
	// RequireTenant if true, rejects requests that cannot resolve a tenant.
	RequireTenant bool
	// HeaderName is the custom header for tenant ID. Defaults to "X-Tenant-ID".
	HeaderName string
}

// Middleware returns a Gin middleware handler that resolves and injects the tenant into the context.
func Middleware(cfg Config) gin.HandlerFunc {
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = DefaultChain(cfg.RootDomain, nil)
	}

	headerName := cfg.HeaderName
	if headerName == "" {
		headerName = "X-Tenant-ID"
	}

	return func(c *gin.Context) {
		start := time.Now()

		// Attempt to resolve the tenant identifier from the request
		tenantID, resolved := resolver.Resolve(c.Request)

		if !resolved && cfg.RequireTenant {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "tenant_resolution_failed",
				"message": "Unable to resolve tenant from request. Provide subdomain, X-Tenant-ID header, or valid JWT.",
			})
			c.Abort()
			return
		}

		// If we have an identifier but no lookup function, inject a minimal tenant
		if resolved && cfg.LookupFn == nil {
			tenant := &models.Tenant{
				ID:     tenantID,
				Name:   tenantID,
				Slug:   tenantID,
				Status: models.TenantStatusActive,
			}
			ctx := WithTenant(c.Request.Context(), tenant)
			c.Request = c.Request.WithContext(ctx)
			c.Set("tenant", tenant)
			c.Set("tenant_id", tenantID)
			c.Next()
			return
		}

		// If resolved, look up the full tenant record
		if resolved && cfg.LookupFn != nil {
			ctx := c.Request.Context()
			tenant, err := cfg.LookupFn(ctx, tenantID)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{
					"error":   "tenant_not_found",
					"message": "The specified tenant does not exist or is not accessible.",
					"details": err.Error(),
				})
				c.Abort()
				return
			}

			// Validate tenant status
			if tenant.Status == models.TenantStatusSuspended {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "tenant_suspended",
					"message": "This tenant account has been suspended. Contact support.",
				})
				c.Abort()
				return
			}

			if tenant.Status == models.TenantStatusInactive {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "tenant_inactive",
					"message": "This tenant account is currently inactive.",
				})
				c.Abort()
				return
			}

			// Inject tenant into context
			ctx = WithTenant(ctx, tenant)
			c.Request = c.Request.WithContext(ctx)
			c.Set("tenant", tenant)
			c.Set("tenant_id", tenant.ID)
			c.Set("tenant_slug", tenant.Slug)

			// Add tenant response header for client awareness
			c.Header("X-Tenant-Resolved", tenant.Slug)
		} else if !cfg.RequireTenant {
			// No tenant required, inject a default/system tenant
			defaultTenant := &models.Tenant{
				ID:     "default",
				Name:   "Default Tenant",
				Slug:   "default",
				Status: models.TenantStatusActive,
			}
			ctx := WithTenant(c.Request.Context(), defaultTenant)
			c.Request = c.Request.WithContext(ctx)
			c.Set("tenant", defaultTenant)
			c.Set("tenant_id", "default")
		}

		elapsed := time.Since(start)
		c.Set("tenant_resolve_ms", elapsed.Milliseconds())

		c.Next()
	}
}

// RequireTenant is a shortcut middleware that enforces tenant resolution.
func RequireTenant(lookupFn LookupFn, rootDomain string) gin.HandlerFunc {
	return Middleware(Config{
		Resolver:      DefaultChain(rootDomain, nil),
		LookupFn:      lookupFn,
		RootDomain:    rootDomain,
		RequireTenant: true,
	})
}

// OptionalTenant is a shortcut middleware that allows requests without a tenant.
func OptionalTenant(lookupFn LookupFn, rootDomain string) gin.HandlerFunc {
	return Middleware(Config{
		Resolver:      DefaultChain(rootDomain, nil),
		LookupFn:      lookupFn,
		RootDomain:    rootDomain,
		RequireTenant: false,
	})
}

// TenantFromGin extracts the tenant from the Gin context.
// Must be called after the tenant middleware has run.
func TenantFromGin(c *gin.Context) (*models.Tenant, bool) {
	v, exists := c.Get("tenant")
	if !exists {
		return nil, false
	}
	tenant, ok := v.(*models.Tenant)
	return tenant, ok
}

// TenantIDFromGin extracts the tenant ID from the Gin context.
func TenantIDFromGin(c *gin.Context) string {
	v, exists := c.Get("tenant_id")
	if !exists {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// RequireAdminRole middleware ensures the current user has an admin role for the tenant.
func RequireAdminRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("user_role")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "role_required",
				"message": "User role information is missing.",
			})
			c.Abort()
			return
		}

		roleStr, ok := role.(string)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "invalid_role",
				"message": "Invalid role format.",
			})
			c.Abort()
			return
		}

		if !strings.EqualFold(roleStr, string(models.UserRoleAdmin)) {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "admin_required",
				"message": "This operation requires administrator privileges.",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
