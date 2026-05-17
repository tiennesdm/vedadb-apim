// Package tenant provides multi-tenancy context utilities for the VedaDB API Manager.
package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/vedadb/vapim/pkg/models"
)

// contextKey is a private type to prevent collisions with other context keys.
type contextKey int

const (
	tenantContextKey contextKey = iota
	tenantIDContextKey
)

// Common errors.
var (
	ErrNoTenantInContext = errors.New("no tenant found in context")
	ErrNilTenant         = errors.New("cannot set nil tenant in context")
)

// WithTenant attaches a tenant to the provided context.
// Returns a new context with the tenant value embedded.
func WithTenant(ctx context.Context, tenant *models.Tenant) context.Context {
	if tenant == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, tenantContextKey, tenant)
	ctx = context.WithValue(ctx, tenantIDContextKey, tenant.ID)
	return ctx
}

// TenantFromContext extracts the full tenant model from the context.
// Returns ErrNoTenantInContext if no tenant has been set.
func TenantFromContext(ctx context.Context) (*models.Tenant, error) {
	v := ctx.Value(tenantContextKey)
	if v == nil {
		return nil, ErrNoTenantInContext
	}
	tenant, ok := v.(*models.Tenant)
	if !ok {
		return nil, fmt.Errorf("invalid tenant type in context: %T", v)
	}
	if tenant == nil {
		return nil, ErrNilTenant
	}
	return tenant, nil
}

// TenantIDFromContext extracts just the tenant ID string from the context.
// Returns empty string if no tenant has been set.
func TenantIDFromContext(ctx context.Context) string {
	v := ctx.Value(tenantIDContextKey)
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// MustTenantIDFromContext extracts the tenant ID and panics if not present.
// Use only in contexts where tenant is guaranteed to exist (e.g., after middleware).
func MustTenantIDFromContext(ctx context.Context) string {
	id := TenantIDFromContext(ctx)
	if id == "" {
		panic("tenant ID not found in context - middleware not applied")
	}
	return id
}

// ContextHasTenant returns true if the context contains a tenant.
func ContextHasTenant(ctx context.Context) bool {
	_, err := TenantFromContext(ctx)
	return err == nil
}
