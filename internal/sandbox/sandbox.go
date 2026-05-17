// Package sandbox provides sandbox environment isolation for the VedaDB API Manager.
// It handles sandbox-specific API keys, gateway routing, policies, and watermarks.
package sandbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SandboxMode represents the current environment mode.
type SandboxMode string

const (
	// ModeProduction is the production environment.
	ModeProduction SandboxMode = "production"
	// ModeSandbox is the sandbox environment.
	ModeSandbox SandboxMode = "sandbox"
)

// IsValid checks if the sandbox mode is valid.
func (m SandboxMode) IsValid() bool {
	switch m {
	case ModeProduction, ModeSandbox:
		return true
	}
	return false
}

// SandboxKey represents a sandbox-specific API key.
type SandboxKey struct {
	ID            string            `json:"id"`
	AppID         string            `json:"app_id"`
	ConsumerKey   string            `json:"consumer_key"`
	ConsumerSecret string           `json:"consumer_secret,omitempty"`
	Mode          SandboxMode       `json:"mode"`
	Status        string            `json:"status"`
	ValidUntil    *time.Time        `json:"valid_until,omitempty"`
	RateLimit     int               `json:"rate_limit"`       // requests per minute
	QuotaLimit    int64             `json:"quota_limit"`      // max requests
	QuotaUsed     int64             `json:"quota_used"`       // current usage
	Policies      []string          `json:"policies"`         // sandbox-specific policy names
	Watermark     bool              `json:"watermark"`        // add sandbox watermark
	CreatedAt     time.Time         `json:"created_at"`
	Attributes    map[string]string `json:"attributes,omitempty"`
}

// IsValid checks if the sandbox key is valid and not expired.
func (k *SandboxKey) IsValid() bool {
	if k.Status != "ACTIVE" {
		return false
	}
	if k.ValidUntil != nil && time.Now().After(*k.ValidUntil) {
		return false
	}
	if k.QuotaLimit > 0 && k.QuotaUsed >= k.QuotaLimit {
		return false
	}
	return true
}

// HasQuota checks if the key has remaining quota.
func (k *SandboxKey) HasQuota() bool {
	if k.QuotaLimit <= 0 {
		return true // unlimited
	}
	return k.QuotaUsed < k.QuotaLimit
}

// UseQuota increments the quota usage.
func (k *SandboxKey) UseQuota() bool {
	if !k.HasQuota() {
		return false
	}
	k.QuotaUsed++
	return true
}

// SandboxPolicy represents a sandbox-specific policy.
type SandboxPolicy struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Mode        SandboxMode       `json:"mode"`
	Priority    int               `json:"priority"`
	Enabled     bool              `json:"enabled"`
	Rules       []PolicyRule      `json:"rules"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// PolicyRule represents a single rule in a policy.
type PolicyRule struct {
	Field       string   `json:"field"`       // header, query, body
	Operator    string   `json:"operator"`    // equals, contains, regex, exists
	Value       string   `json:"value"`       // value to match
	Actions     []string `json:"actions"`     // add_header, modify_body, throttle, block
	ActionParams map[string]string `json:"action_params,omitempty"`
}

// SandboxManager manages sandbox environment isolation.
type SandboxManager struct {
	keys      map[string]*SandboxKey // consumerKey -> key
	policies  map[string]*SandboxPolicy // policyID -> policy
	byApp     map[string][]string // appID -> []consumerKeys
	mode      SandboxMode
	mu        sync.RWMutex

	// Configuration
	watermarkEnabled  bool
	defaultRateLimit  int
	defaultQuotaLimit int64
	sandboxTTL        time.Duration
}

// ManagerOption configures the SandboxManager.
type ManagerOption func(*SandboxManager)

// WithWatermark enables or disables sandbox watermarking.
func WithWatermark(enabled bool) ManagerOption {
	return func(m *SandboxManager) {
		m.watermarkEnabled = enabled
	}
}

// WithDefaultRateLimit sets the default rate limit.
func WithDefaultRateLimit(limit int) ManagerOption {
	return func(m *SandboxManager) {
		m.defaultRateLimit = limit
	}
}

// WithDefaultQuotaLimit sets the default quota limit.
func WithDefaultQuotaLimit(limit int64) ManagerOption {
	return func(m *SandboxManager) {
		m.defaultQuotaLimit = limit
	}
}

// WithSandboxTTL sets the default sandbox key TTL.
func WithSandboxTTL(ttl time.Duration) ManagerOption {
	return func(m *SandboxManager) {
		m.sandboxTTL = ttl
	}
}

// NewSandboxManager creates a new sandbox manager.
func NewSandboxManager(opts ...ManagerOption) *SandboxManager {
	m := &SandboxManager{
		keys:              make(map[string]*SandboxKey),
		policies:          make(map[string]*SandboxPolicy),
		byApp:             make(map[string][]string),
		mode:              ModeSandbox,
		watermarkEnabled:  true,
		defaultRateLimit:  60,              // 60 requests per minute
		defaultQuotaLimit: 10000,           // 10K requests
		sandboxTTL:        24 * time.Hour,  // 24 hours
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// SetMode sets the sandbox mode.
func (m *SandboxManager) SetMode(mode SandboxMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
}

// GetMode returns the current sandbox mode.
func (m *SandboxManager) GetMode() SandboxMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode
}

// IsSandbox returns true if in sandbox mode.
func (m *SandboxManager) IsSandbox() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode == ModeSandbox
}

// CreateSandboxKey creates a new sandbox key for an application.
func (m *SandboxManager) CreateSandboxKey(appID string, opts ...KeyOption) (*SandboxKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := &SandboxKey{
		ID:             uuid.New().String(),
		AppID:          appID,
		ConsumerKey:    generateKey("sb_ck_"),
		ConsumerSecret: generateKey("sb_cs_"),
		Mode:           ModeSandbox,
		Status:         "ACTIVE",
		RateLimit:      m.defaultRateLimit,
		QuotaLimit:     m.defaultQuotaLimit,
		QuotaUsed:      0,
		Watermark:      m.watermarkEnabled,
		CreatedAt:      time.Now().UTC(),
		Policies:       []string{"sandbox-default"},
		Attributes:     make(map[string]string),
	}

	// Apply TTL
	if m.sandboxTTL > 0 {
		expiry := key.CreatedAt.Add(m.sandboxTTL)
		key.ValidUntil = &expiry
	}

	// Apply options
	for _, opt := range opts {
		opt(key)
	}

	m.keys[key.ConsumerKey] = key
	m.byApp[appID] = append(m.byApp[appID], key.ConsumerKey)

	return key, nil
}

// GetKeyByConsumerKey finds a key by its consumer key.
func (m *SandboxManager) GetKeyByConsumerKey(consumerKey string) (*SandboxKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key, ok := m.keys[consumerKey]
	if !ok {
		return nil, fmt.Errorf("sandbox key not found for consumer key: %s", consumerKey)
	}
	return key, nil
}

// GetKeysByApp returns all sandbox keys for an application.
func (m *SandboxManager) GetKeysByApp(appID string) ([]*SandboxKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	consumerKeys, ok := m.byApp[appID]
	if !ok {
		return nil, fmt.Errorf("no sandbox keys found for app: %s", appID)
	}

	var keys []*SandboxKey
	for _, ck := range consumerKeys {
		if key, ok := m.keys[ck]; ok {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// RevokeKey revokes a sandbox key.
func (m *SandboxManager) RevokeKey(consumerKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key, ok := m.keys[consumerKey]
	if !ok {
		return fmt.Errorf("sandbox key not found: %s", consumerKey)
	}
	key.Status = "REVOKED"
	return nil
}

// DeleteKey permanently deletes a sandbox key.
func (m *SandboxManager) DeleteKey(consumerKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key, ok := m.keys[consumerKey]
	if !ok {
		return fmt.Errorf("sandbox key not found: %s", consumerKey)
	}

	delete(m.keys, consumerKey)

	// Remove from app index
	keys := m.byApp[key.AppID]
	var updated []string
	for _, ck := range keys {
		if ck != consumerKey {
			updated = append(updated, ck)
		}
	}
	if len(updated) > 0 {
		m.byApp[key.AppID] = updated
	} else {
		delete(m.byApp, key.AppID)
	}

	return nil
}

// CreatePolicy creates a sandbox policy.
func (m *SandboxManager) CreatePolicy(policy *SandboxPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if policy.ID == "" {
		policy.ID = uuid.New().String()
	}
	policy.Mode = ModeSandbox
	policy.CreatedAt = time.Now().UTC()
	policy.UpdatedAt = policy.CreatedAt

	m.policies[policy.ID] = policy
	return nil
}

// GetPolicy returns a policy by ID.
func (m *SandboxManager) GetPolicy(policyID string) (*SandboxPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	policy, ok := m.policies[policyID]
	if !ok {
		return nil, fmt.Errorf("policy not found: %s", policyID)
	}
	return policy, nil
}

// ListPolicies returns all sandbox policies.
func (m *SandboxManager) ListPolicies() []*SandboxPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var policies []*SandboxPolicy
	for _, p := range m.policies {
		if p.Mode == ModeSandbox {
			policies = append(policies, p)
		}
	}
	return policies
}

// Stats returns sandbox manager statistics.
func (m *SandboxManager) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalKeys := len(m.keys)
	activeKeys := 0
	revokedKeys := 0
	var totalQuotaUsed int64

	for _, key := range m.keys {
		switch key.Status {
		case "ACTIVE":
			activeKeys++
		case "REVOKED":
			revokedKeys++
		}
		totalQuotaUsed += key.QuotaUsed
	}

	return map[string]interface{}{
		"mode":            string(m.mode),
		"total_keys":      totalKeys,
		"active_keys":     activeKeys,
		"revoked_keys":    revokedKeys,
		"total_quota_used": totalQuotaUsed,
		"watermark_enabled": m.watermarkEnabled,
		"default_rate_limit": m.defaultRateLimit,
		"default_quota_limit": m.defaultQuotaLimit,
		"policy_count":    len(m.policies),
	}
}

// --- Key Options ---

// KeyOption configures a sandbox key.
type KeyOption func(*SandboxKey)

// WithRateLimit sets a custom rate limit.
func WithRateLimit(limit int) KeyOption {
	return func(k *SandboxKey) {
		k.RateLimit = limit
	}
}

// WithQuotaLimit sets a custom quota limit.
func WithQuotaLimit(limit int64) KeyOption {
	return func(k *SandboxKey) {
		k.QuotaLimit = limit
	}
}

// WithValidUntil sets an explicit expiry time.
func WithValidUntil(t time.Time) KeyOption {
	return func(k *SandboxKey) {
		k.ValidUntil = &t
	}
}

// WithPolicies sets custom policies.
func WithPolicies(policies ...string) KeyOption {
	return func(k *SandboxKey) {
		k.Policies = policies
	}
}

// WithoutWatermark disables watermark for this key.
func WithoutWatermark() KeyOption {
	return func(k *SandboxKey) {
		k.Watermark = false
	}
}

// WithAttributes sets custom attributes.
func WithAttributes(attrs map[string]string) KeyOption {
	return func(k *SandboxKey) {
		k.Attributes = attrs
	}
}

// --- Middleware ---

// SandboxMiddleware creates a Gin middleware for sandbox environment handling.
func (m *SandboxManager) SandboxMiddleware() gin.HandlerFunc {
	rateLimiter := NewRateLimiter()

	return func(c *gin.Context) {
		// Check if sandbox mode
		if m.GetMode() != ModeSandbox {
			c.Next()
			return
		}

		// Extract consumer key from request
		consumerKey := extractConsumerKey(c)
		if consumerKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "missing_consumer_key",
				"message": "Sandbox consumer key is required (X-Sandbox-Key header or X-API-Key)",
			})
			c.Abort()
			return
		}

		// Validate key
		key, err := m.GetKeyByConsumerKey(consumerKey)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "invalid_consumer_key",
				"message": err.Error(),
			})
			c.Abort()
			return
		}

		if !key.IsValid() {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "invalid_key",
				"message": "Sandbox key is invalid, expired, or quota exceeded",
				"status":  key.Status,
			})
			c.Abort()
			return
		}

		// Check rate limit
		if !rateLimiter.Allow(consumerKey, key.RateLimit) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":      "rate_limit_exceeded",
				"message":    fmt.Sprintf("Rate limit of %d requests/min exceeded", key.RateLimit),
				"retry_after": 60,
			})
			c.Abort()
			return
		}

		// Check quota
		if !key.UseQuota() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "quota_exceeded",
				"message": fmt.Sprintf("Quota limit of %d requests exceeded", key.QuotaLimit),
			})
			c.Abort()
			return
		}

		// Add sandbox headers
		c.Header("X-Environment", "sandbox")
		c.Header("X-Sandbox-Key-ID", key.ID)
		c.Header("X-Sandbox-Rate-Limit", fmt.Sprintf("%d", key.RateLimit))
		c.Header("X-Sandbox-Quota-Remaining", fmt.Sprintf("%d", key.QuotaLimit-key.QuotaUsed))

		c.Next()

		// Add sandbox watermark to response
		if key.Watermark {
			c.Header("X-Sandbox-Watermark", "true")
			c.Header("X-Sandbox-Response", "true")
		}
	}
}

// SandboxGatewayRouter routes requests to sandbox or production endpoints.
func (m *SandboxManager) SandboxGatewayRouter() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Determine sandbox mode from header or query
		mode := c.GetHeader("X-Sandbox-Mode")
		if mode == "" {
			mode = c.Query("sandbox")
		}

		sandbox := strings.EqualFold(mode, "true") || mode == "1"
		c.Set("sandbox", sandbox)

		if sandbox {
			c.Set("gateway_target", "sandbox")
		} else {
			c.Set("gateway_target", "production")
		}

		c.Next()
	}
}

// SandboxResponseMiddleware adds sandbox watermark to responses.
func (m *SandboxManager) SandboxResponseMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		sandbox, exists := c.Get("sandbox")
		if !exists || !sandbox.(bool) {
			return
		}

		// Only watermark JSON responses
		contentType := c.Writer.Header().Get("Content-Type")
		if strings.Contains(contentType, "application/json") && m.watermarkEnabled {
			c.Header("X-Sandbox-Watermark", "VAPIM-SANDBOX")
			c.Header("Warning", "199 - Sandbox Response")
		}
	}
}

// SandboxErrorHandler handles errors in sandbox mode with additional context.
func SandboxErrorHandler(c *gin.Context) {
	c.Next()

	if len(c.Errors) > 0 {
		sandbox, _ := c.Get("sandbox")
		err := c.Errors.Last()

		response := gin.H{
			"error":     "sandbox_error",
			"message":   err.Error(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}

		if sandbox != nil && sandbox.(bool) {
			response["environment"] = "sandbox"
			response["sandbox_help"] = "This error occurred in sandbox mode. Check your sandbox key and quotas."
		}

		c.JSON(-1, response)
	}
}

// --- Helper functions ---

func extractConsumerKey(c *gin.Context) string {
	// Check X-Sandbox-Key header first
	key := c.GetHeader("X-Sandbox-Key")
	if key != "" {
		return key
	}

	// Check X-API-Key header
	key = c.GetHeader("X-API-Key")
	if key != "" {
		return key
	}

	// Check query parameter
	key = c.Query("api_key")
	if key != "" {
		return key
	}

	// Check Authorization header for sandbox tokens
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Sandbox ") {
		return strings.TrimPrefix(auth, "Sandbox ")
	}

	return ""
}

func generateKey(prefix string) string {
	b := make([]byte, 16)
	// Use UUID-based generation
	u := uuid.New().String()
	// Create HMAC for uniqueness
	h := hmac.New(sha256.New, []byte(prefix))
	h.Write([]byte(u + time.Now().String()))
	return prefix + hex.EncodeToString(h.Sum(nil))[:32]
}

// --- Rate Limiter ---

// RateLimiter implements a simple per-key rate limiter.
type RateLimiter struct {
	buckets map[string]*tokenBucket
	mu      sync.RWMutex
}

type tokenBucket struct {
	tokens     float64
	lastUpdate time.Time
	rate       float64 // tokens per second
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
	}
}

// Allow checks if a request is allowed under the rate limit.
func (rl *RateLimiter) Allow(key string, limitPerMinute int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, ok := rl.buckets[key]
	if !ok {
		bucket = &tokenBucket{
			tokens:     float64(limitPerMinute),
			lastUpdate: time.Now(),
			rate:       float64(limitPerMinute) / 60.0,
		}
		rl.buckets[key] = bucket
	}

	now := time.Now()
	elapsed := now.Sub(bucket.lastUpdate).Seconds()
	bucket.tokens += elapsed * bucket.rate
	maxTokens := float64(limitPerMinute)
	if bucket.tokens > maxTokens {
		bucket.tokens = maxTokens
	}
	bucket.lastUpdate = now

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}
	return false
}

// Reset resets the rate limiter for a key.
func (rl *RateLimiter) Reset(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, key)
}

// --- Sandbox Response Watermark ---

// Watermark adds sandbox watermark data to a response body.
type Watermark struct {
	Enabled     bool                   `json:"-"`
	Environment string                 `json:"_sandbox_environment"`
	Timestamp   string                 `json:"_sandbox_timestamp"`
	KeyID       string                 `json:"_sandbox_key_id,omitempty"`
	Disclaimer  string                 `json:"_sandbox_disclaimer"`
	Extra       map[string]interface{} `json:"_sandbox_extra,omitempty"`
}

// NewWatermark creates a new watermark.
func NewWatermark(keyID string, extra map[string]interface{}) *Watermark {
	return &Watermark{
		Enabled:     true,
		Environment: "sandbox",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		KeyID:       keyID,
		Disclaimer:  "This is a sandbox response. Data may be simulated and should not be used in production.",
		Extra:       extra,
	}
}

// Apply adds watermark fields to a map.
func (w *Watermark) Apply(data map[string]interface{}) map[string]interface{} {
	if !w.Enabled {
		return data
	}
	if data == nil {
		data = make(map[string]interface{})
	}
	data["_sandbox_environment"] = w.Environment
	data["_sandbox_timestamp"] = w.Timestamp
	if w.KeyID != "" {
		data["_sandbox_key_id"] = w.KeyID
	}
	data["_sandbox_disclaimer"] = w.Disclaimer
	for k, v := range w.Extra {
		data[k] = v
	}
	return data
}

// ApplyJSON adds watermark to a JSON byte slice.
func (w *Watermark) ApplyJSON(data []byte) []byte {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return data
	}
	m = w.Apply(m)
	result, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return result
}

// SandboxResponse wraps an HTTP response with sandbox watermarks.
type SandboxResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string]string   `json:"headers"`
	Body       interface{}         `json:"body"`
	Watermark  *Watermark          `json:"_sandbox_meta"`
}

// WrapResponse wraps a response with sandbox metadata.
func WrapResponse(statusCode int, body interface{}, keyID string) *SandboxResponse {
	return &SandboxResponse{
		StatusCode: statusCode,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body:      body,
		Watermark: NewWatermark(keyID, nil),
	}
}

// MarshalJSON implements custom JSON marshaling.
func (sr *SandboxResponse) MarshalJSON() ([]byte, error) {
	type wrapper struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string]string   `json:"headers"`
		Body       interface{}         `json:"body"`
		SandboxEnv string              `json:"_sandbox_environment"`
		SandboxTS  string              `json:"_sandbox_timestamp"`
		SandboxKey string              `json:"_sandbox_key_id,omitempty"`
		Disclaimer string              `json:"_sandbox_disclaimer"`
	}

	w := wrapper{
		StatusCode: sr.StatusCode,
		Headers:    sr.Headers,
		Body:       sr.Body,
		SandboxEnv: "sandbox",
		SandboxTS:  time.Now().UTC().Format(time.RFC3339),
		Disclaimer: "This is a sandbox response. Data may be simulated.",
	}

	if sr.Watermark != nil {
		w.SandboxKey = sr.Watermark.KeyID
	}

	return json.Marshal(w)
}

// --- Default Sandbox Policies ---

// DefaultSandboxPolicies returns the default set of sandbox policies.
func DefaultSandboxPolicies() []*SandboxPolicy {
	return []*SandboxPolicy{
		{
			ID:          "sandbox-default",
			Name:        "Default Sandbox Policy",
			Description: "Default policy applied to all sandbox requests",
			Mode:        ModeSandbox,
			Priority:    100,
			Enabled:     true,
			Rules: []PolicyRule{
				{
					Field:    "header",
					Operator: "exists",
					Value:    "X-Sandbox-Key",
					Actions:  []string{"allow"},
				},
			},
		},
		{
			ID:          "sandbox-throttle",
			Name:        "Sandbox Throttling Policy",
			Description: "Throttles sandbox requests to prevent abuse",
			Mode:        ModeSandbox,
			Priority:    50,
			Enabled:     true,
			Rules: []PolicyRule{
				{
					Field:    "header",
					Operator: "exists",
					Value:    "X-Sandbox-Key",
					Actions:  []string{"throttle"},
					ActionParams: map[string]string{
						"requests_per_second": "10",
						"burst_size":         "20",
					},
				},
			},
		},
		{
			ID:          "sandbox-watermark",
			Name:        "Sandbox Watermark Policy",
			Description: "Adds sandbox watermark to all responses",
			Mode:        ModeSandbox,
			Priority:    10,
			Enabled:     true,
			Rules: []PolicyRule{
				{
					Field:    "header",
					Operator: "exists",
					Value:    "X-Sandbox-Key",
					Actions:  []string{"add_header", "modify_body"},
					ActionParams: map[string]string{
						"header_name":  "X-Sandbox-Watermark",
						"header_value": "true",
					},
				},
			},
		},
	}
}
