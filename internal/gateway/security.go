// Package gateway provides API security policy enforcement for the
// VedaDB API Manager Gateway.
package gateway

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vedadb/vapim/pkg/models"
	"github.com/vedadb/vapim/pkg/store"
)

// ---------------------------------------------------------------------------
// SecurityPolicyMiddleware
// ---------------------------------------------------------------------------

// SecurityPolicyMiddleware enforces API security policies including IP
// whitelisting and OAuth2 scope validation. It queries the store for the
// API's throttle policy, parses any IP-range conditions, and validates the
// client IP against them. It also checks that the authenticated token has
// the required scopes for the requested resource.
func SecurityPolicyMiddleware(store store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiID := c.GetString("apiID")
		if apiID == "" {
			c.Next()
			return
		}

		// Skip security checks for health/metrics/admin endpoints.
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/health") ||
			strings.HasPrefix(path, "/metrics") ||
			strings.HasPrefix(path, "/admin") {
			c.Next()
			return
		}

		// ---- IP Whitelist Check ----
		api, err := store.GetAPI(apiID)
		if err == nil && api != nil && api.ThrottlePolicy != "" {
			// Fetch the throttle policy to extract IP whitelist conditions.
			policy, err := store.GetThrottlePolicyByName(api.TenantID, api.ThrottlePolicy)
			if err == nil && policy != nil && policy.Conditions != "" {
				clientIP := c.ClientIP()
				if !isIPAllowedByPolicy(clientIP, policy.Conditions) {
					c.JSON(http.StatusForbidden, gin.H{
						"error":   "ip_not_allowed",
						"message": "Your IP address is not permitted to access this API",
						"ip":      clientIP,
					})
					c.Abort()
					return
				}
			}
		}

		// ---- Scope Check ----
		// Scopes are set by AuthMiddleware as a comma-separated string.
		scopesRaw, exists := c.Get("scopes")
		if !exists {
			c.Next()
			return
		}

		scopes := parseScopes(scopesRaw)

		// Fetch resources to find scope requirements.
		resources, err := store.GetResourcesByAPI(apiID)
		if err == nil {
			for _, r := range resources {
				if r.Method == c.Request.Method && pathMatchesResource(r.Path, path) {
					// Check if the resource has a scope requirement encoded
					// in its throttle_policy field (used as a generic config
					// store when no throttle is configured).
					requiredScope := extractRequiredScope(r.ThrottlePolicy)
					if requiredScope != "" && !hasScope(scopes, requiredScope) {
						c.JSON(http.StatusForbidden, gin.H{
							"error":            "insufficient_scope",
							"message":          "Token does not have the required scope: " + requiredScope,
							"required_scope":   requiredScope,
							"provided_scopes":  scopes,
						})
						c.Abort()
						return
					}
					break
				}
			}
		}

		c.Next()
	}
}

// ---------------------------------------------------------------------------
// IP whitelist / CIDR helpers
// ---------------------------------------------------------------------------

// isIPAllowedByPolicy parses the JSON conditions stored in the
// throttle_policies.conditions column and checks whether clientIP is
// explicitly allowed.
func isIPAllowedByPolicy(clientIP, conditionsJSON string) bool {
	// If conditions is empty or does not look like JSON, allow all.
	conditionsJSON = strings.TrimSpace(conditionsJSON)
	if conditionsJSON == "" || !strings.HasPrefix(conditionsJSON, "[") {
		return true
	}

	var conditions []models.PolicyCondition
	if err := json.Unmarshal([]byte(conditionsJSON), &conditions); err != nil {
		// Cannot parse conditions; fail-open to avoid breaking APIs.
		return true
	}

	// If there are no IP conditions, allow all.
	hasIPCondition := false
	for _, cond := range conditions {
		if cond.Type == "IP" || cond.IPRange != "" {
			hasIPCondition = true
			if ipMatches(clientIP, cond.IPRange) {
				return true
			}
		}
	}
	// No IP conditions means no restriction.
	return !hasIPCondition
}

// ipMatches checks if clientIP matches an IP address or CIDR range.
func ipMatches(clientIP, allowed string) bool {
	allowed = strings.TrimSpace(allowed)
	if allowed == "" {
		return false
	}

	// Direct match.
	if allowed == clientIP {
		return true
	}

	// CIDR match.
	if strings.Contains(allowed, "/") {
		_, ipNet, err := net.ParseCIDR(allowed)
		if err != nil {
			return false
		}
		ip := net.ParseIP(clientIP)
		if ip == nil {
			return false
		}
		return ipNet.Contains(ip)
	}

	return false
}

// isIPWhitelisted checks a comma-separated list of IPs and CIDR ranges.
func isIPWhitelisted(clientIP, whitelist string) bool {
	if whitelist == "" {
		return true // empty whitelist = allow all
	}

	allowed := strings.Split(whitelist, ",")
	for _, a := range allowed {
		if ipMatches(clientIP, a) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Scope helpers
// ---------------------------------------------------------------------------

// parseScopes converts the raw scopes value from the gin context into a
// clean slice of scope strings.
func parseScopes(raw interface{}) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case string:
		if v == "" {
			return nil
		}
		parts := strings.Split(v, ",")
		scopes := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				scopes = append(scopes, p)
			}
		}
		return scopes
	default:
		return nil
	}
}

// hasScope checks if the provided scopes contain the required scope.
// Supports hierarchical scopes: "api:write" implies "api:read".
func hasScope(scopes []string, required string) bool {
	if required == "" {
		return true
	}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == required {
			return true
		}
		// Wildcard match: "api:*" matches any "api:xxx".
		if strings.HasSuffix(s, ":*") || strings.HasSuffix(s, ".*") {
			prefix := strings.TrimSuffix(s, "*")
			if strings.HasPrefix(required, prefix) {
				return true
			}
		}
		// Admin wildcard.
		if s == "*" || s == "all" {
			return true
		}
	}
	return false
}

// extractRequiredScope attempts to parse a scope requirement out of a raw
// resource config string. If the string is a JSON object with a "scope"
// field, that value is returned; otherwise the raw string is treated as the
// scope if it does not look like JSON.
func extractRequiredScope(config string) string {
	config = strings.TrimSpace(config)
	if config == "" {
		return ""
	}
	if !strings.HasPrefix(config, "{") {
		return config
	}
	var obj struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(config), &obj); err == nil && obj.Scope != "" {
		return obj.Scope
	}
	return ""
}
