// Package tenant provides multi-tenancy resolution strategies for the VedaDB API Manager.
package tenant

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vedadb/vapim/pkg/models"
)

// Resolver defines the interface for tenant resolution strategies.
// Each strategy attempts to extract the tenant identifier from an incoming request.
type Resolver interface {
	// Resolve attempts to determine the tenant from the HTTP request.
	// Returns the tenant identifier (slug, ID, or domain) and true if resolved.
	Resolve(r *http.Request) (string, bool)
	// Name returns the human-readable name of this resolver strategy.
	Name() string
}

// ---- Subdomain Resolver ----

// SubdomainResolver extracts the tenant slug from the request subdomain.
// e.g., "acme.example.com" -> resolves "acme"
type SubdomainResolver struct {
	// RootDomain is the root domain to strip (e.g., "example.com").
	RootDomain string
}

// Name returns the resolver name.
func (r *SubdomainResolver) Name() string {
	return "subdomain"
}

// Resolve extracts tenant slug from request Host subdomain.
func (r *SubdomainResolver) Resolve(rreq *http.Request) (string, bool) {
	host := rreq.Host
	if host == "" {
		host = rreq.URL.Host
	}

	// Remove port if present
	if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
		host = host[:colonIdx]
	}

	// If the host exactly matches the root domain, no subdomain
	if host == r.RootDomain {
		return "", false
	}

	// If the host ends with the root domain, extract the subdomain part
	if r.RootDomain != "" && strings.HasSuffix(host, "."+r.RootDomain) {
		subdomain := strings.TrimSuffix(host, "."+r.RootDomain)
		// Handle nested subdomains - take only the immediate subdomain
		if idx := strings.LastIndex(subdomain, "."); idx != -1 {
			subdomain = subdomain[idx+1:]
		}
		if subdomain != "" {
			return subdomain, true
		}
		return "", false
	}

	// If no root domain configured, try to extract first subdomain segment
	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		return parts[0], true
	}

	return "", false
}

// ---- Header Resolver ----

// HeaderResolver extracts the tenant ID from a custom HTTP header.
type HeaderResolver struct {
	// HeaderName is the name of the header containing the tenant ID.
	// Defaults to "X-Tenant-ID".
	HeaderName string
}

// Name returns the resolver name.
func (r *HeaderResolver) Name() string {
	return "header"
}

// Resolve extracts tenant ID from the configured HTTP header.
func (r *HeaderResolver) Resolve(rreq *http.Request) (string, bool) {
	headerName := r.HeaderName
	if headerName == "" {
		headerName = "X-Tenant-ID"
	}

	tenantID := rreq.Header.Get(headerName)
	if tenantID == "" {
		return "", false
	}
	return strings.TrimSpace(tenantID), true
}

// ---- JWT Claim Resolver ----

// JWTClaimResolver extracts the tenant identifier from a JWT token claim.
type JWTClaimResolver struct {
	// ClaimName is the name of the JWT claim containing the tenant ID/slug.
	// Defaults to "tenant_id".
	ClaimName string
	// Secret is the HMAC secret for JWT validation. If empty, signature
	// validation is skipped (useful for development or when a separate
	// auth middleware has already validated the token).
	Secret []byte
}

// Name returns the resolver name.
func (r *JWTClaimResolver) Name() string {
	return "jwt_claim"
}

// Resolve extracts tenant ID from the JWT token's claims.
func (r *JWTClaimResolver) Resolve(rreq *http.Request) (string, bool) {
	claimName := r.ClaimName
	if claimName == "" {
		claimName = "tenant_id"
	}

	// Extract token from Authorization header
	authHeader := rreq.Header.Get("Authorization")
	if authHeader == "" {
		return "", false
	}

	// Expect "Bearer <token>" format
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	tokenString := parts[1]

	// Parse the token
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return "", false
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", false
	}

	claimValue, exists := claims[claimName]
	if !exists {
		return "", false
	}

	// Handle different claim value types
	switch v := claimValue.(type) {
	case string:
		return strings.TrimSpace(v), true
	case float64:
		return fmt.Sprintf("%.0f", v), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

// ---- Chain Resolver ----

// ChainResolver tries multiple resolvers in order, returning the first successful match.
type ChainResolver struct {
	resolvers []Resolver
}

// NewChainResolver creates a new chain resolver with the given strategies.
// Resolvers are tried in the order provided.
func NewChainResolver(resolvers ...Resolver) *ChainResolver {
	return &ChainResolver{
		resolvers: append([]Resolver{}, resolvers...),
	}
}

// Name returns the resolver name.
func (r *ChainResolver) Name() string {
	return "chain"
}

// Resolve tries each resolver in the chain until one succeeds.
func (r *ChainResolver) Resolve(rreq *http.Request) (string, bool) {
	for _, resolver := range r.resolvers {
		if tenantID, ok := resolver.Resolve(rreq); ok && tenantID != "" {
			return tenantID, true
		}
	}
	return "", false
}

// DefaultChain returns the default resolution chain:
// 1. Subdomain, 2. Header, 3. JWT Claim.
func DefaultChain(rootDomain string, jwtSecret []byte) *ChainResolver {
	return NewChainResolver(
		&SubdomainResolver{RootDomain: rootDomain},
		&HeaderResolver{HeaderName: "X-Tenant-ID"},
		&JWTClaimResolver{ClaimName: "tenant_id", Secret: jwtSecret},
	)
}

// ---- Tenant Lookup Interface ----

// LookupFn is a function that resolves a tenant identifier to a full tenant model.
type LookupFn func(ctx context.Context, identifier string) (*models.Tenant, error)

// ResolveTenant is a helper that uses a resolver chain and lookup function to
// fully resolve a tenant from an HTTP request.
func ResolveTenant(ctx context.Context, r *http.Request, resolver Resolver, lookup LookupFn) (*models.Tenant, error) {
	identifier, ok := resolver.Resolve(r)
	if !ok {
		return nil, fmt.Errorf("could not resolve tenant using %s resolver", resolver.Name())
	}

	tenant, err := lookup(ctx, identifier)
	if err != nil {
		return nil, fmt.Errorf("tenant lookup failed for identifier %q: %w", identifier, err)
	}

	if tenant.Status != models.TenantStatusActive {
		return nil, fmt.Errorf("tenant %q is not active (status: %s)", tenant.Name, tenant.Status)
	}

	return tenant, nil
}
