// Package keymanager provides API key generation, validation, revocation and
// regeneration for the VedaDB API Manager.
package keymanager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// APIKeyStore persists and queries API keys.
type APIKeyStore interface {
	Create(key *models.APIKey) error
	GetByKey(key string) (*models.APIKey, error)
	GetByID(id string) (*models.APIKey, error)
	ListByApp(appID string) ([]*models.APIKey, error)
	Update(key *models.APIKey) error
	Revoke(id string) error
}

// APIKeyManager handles business logic for API keys.
type APIKeyManager struct {
	mu      sync.RWMutex
	store   APIKeyStore
	jwt     *JWTManager
	issuer  string
	keys    map[string]*models.APIKey // fast lookup by key hash
}

// NewAPIKeyManager creates a new APIKeyManager.
func NewAPIKeyManager(store APIKeyStore, jwtMgr *JWTManager, issuer string) *APIKeyManager {
	return &APIKeyManager{
		store:  store,
		jwt:    jwtMgr,
		issuer: issuer,
		keys:   make(map[string]*models.APIKey),
	}
}

// GenerateKey creates a new API key for an application.
// The returned string is the raw key (to be shown once); the stored version is hashed.
func (km *APIKeyManager) GenerateKey(req models.APIKeyCreateRequest, tenantID string) (*models.APIKey, string, error) {
	rawKey := km.generateRawKey()
	hashed := hashKey(rawKey)

	now := time.Now()
	validDays := req.ValidDays
	if validDays <= 0 {
		validDays = 30
	}
	if validDays > 365 {
		validDays = 365
	}

	key := &models.APIKey{
		ID:          uuid.New().String(),
		Key:         hashed,
		Name:        req.Name,
		Description: req.Description,
		AppID:       req.AppID,
		APIID:       req.APIID,
		TenantID:    tenantID,
		Scopes:      km.normalizeScopes(req.Scopes),
		ValidFrom:   now,
		ValidTo:     now.Add(time.Duration(validDays) * 24 * time.Hour),
		Revoked:     false,
		UsageCount:  0,
		Metadata:    req.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := km.store.Create(key); err != nil {
		return nil, "", fmt.Errorf("store api key: %w", err)
	}

	km.mu.Lock()
	defer km.mu.Unlock()
	km.keys[hashed] = key

	return key, rawKey, nil
}

// ValidateKey checks whether a raw API key is valid (exists, not revoked, not expired).
// Returns the stored APIKey record on success.
func (km *APIKeyManager) ValidateKey(rawKey string) (*models.APIKey, error) {
	hashed := hashKey(rawKey)

	km.mu.RLock()
	key, ok := km.keys[hashed]
	km.mu.RUnlock()

	if !ok {
		// fallback to store
		var err error
		key, err = km.store.GetByKey(hashed)
		if err != nil {
			return nil, fmt.Errorf("invalid api key: %w", err)
		}
	}

	if key == nil {
		return nil, fmt.Errorf("api key not found")
	}

	if key.Revoked {
		return nil, fmt.Errorf("api key has been revoked")
	}

	now := time.Now()
	if now.Before(key.ValidFrom) {
		return nil, fmt.Errorf("api key is not yet valid")
	}
	if now.After(key.ValidTo) {
		return nil, fmt.Errorf("api key has expired")
	}

	// increment usage count in background
	go func(id string) {
		_ = km.store.Update(&models.APIKey{ID: id, UsageCount: key.UsageCount + 1, UpdatedAt: time.Now()})
	}(key.ID)

	return key, nil
}

// ValidateKeyByHash checks a key by its hash value (used by gateway introspection).
func (km *APIKeyManager) ValidateKeyByHash(hash string) (*models.APIKey, error) {
	km.mu.RLock()
	key, ok := km.keys[hash]
	km.mu.RUnlock()

	if !ok {
		var err error
		key, err = km.store.GetByKey(hash)
		if err != nil {
			return nil, fmt.Errorf("invalid api key: %w", err)
		}
	}

	if key.Revoked {
		return nil, fmt.Errorf("api key has been revoked")
	}
	now := time.Now()
	if now.Before(key.ValidFrom) || now.After(key.ValidTo) {
		return nil, fmt.Errorf("api key is not valid at this time")
	}
	return key, nil
}

// RevokeKey revokes an API key by its ID.
func (km *APIKeyManager) RevokeKey(keyID string) error {
	key, err := km.store.GetByID(keyID)
	if err != nil {
		return fmt.Errorf("fetch api key: %w", err)
	}

	if key.Revoked {
		return fmt.Errorf("api key already revoked")
	}

	if err := km.store.Revoke(keyID); err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}

	km.mu.Lock()
	defer km.mu.Unlock()
	delete(km.keys, key.Key)

	return nil
}

// RegenerateKey revokes the old key and generates a new one with the same
// configuration (except a new validity period starting from now).
func (km *APIKeyManager) RegenerateKey(keyID string) (*models.APIKey, string, error) {
	oldKey, err := km.store.GetByID(keyID)
	if err != nil {
		return nil, "", fmt.Errorf("fetch old key: %w", err)
	}

	// Revoke old key
	if !oldKey.Revoked {
		if err := km.store.Revoke(keyID); err != nil {
			return nil, "", fmt.Errorf("revoke old key: %w", err)
		}
		km.mu.Lock()
		delete(km.keys, oldKey.Key)
		km.mu.Unlock()
	}

	// Calculate remaining validity
	validDays := int(oldKey.ValidTo.Sub(time.Now()).Hours() / 24)
	if validDays <= 0 {
		validDays = 30
	}

	req := models.APIKeyCreateRequest{
		Name:        oldKey.Name,
		Description: oldKey.Description,
		AppID:       oldKey.AppID,
		APIID:       oldKey.APIID,
		Scopes:      oldKey.Scopes,
		ValidDays:   validDays,
		Metadata:    oldKey.Metadata,
	}

	return km.GenerateKey(req, oldKey.TenantID)
}

// ListKeysByApp returns all non-revoked keys for an application.
func (km *APIKeyManager) ListKeysByApp(appID string) ([]*models.APIKey, error) {
	keys, err := km.store.ListByApp(appID)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	var active []*models.APIKey
	for _, k := range keys {
		if !k.Revoked {
			active = append(active, k)
		}
	}
	return active, nil
}

// GetKey retrieves a key by its ID.
func (km *APIKeyManager) GetKey(keyID string) (*models.APIKey, error) {
	return km.store.GetByID(keyID)
}

// generateRawKey creates a cryptographically secure API key string.
func (km *APIKeyManager) generateRawKey() string {
	return "vapim_" + uuid.New().String() + "_" + uuid.New().String()
}

// normalizeScopes deduplicates and trims scope values.
func (km *APIKeyManager) normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// hashKey hashes a raw API key using SHA-256 for secure storage.
func hashKey(raw string) string {
	h := sha256.New()
	h.Write([]byte(raw))
	return hex.EncodeToString(h.Sum(nil))
}

// GenerateJWTAPIKey creates a JWT-based API key (self-signed) using the JWT manager.
// The returned string is a JWT token that can be used as an API key.
func (km *APIKeyManager) GenerateJWTAPIKey(req models.APIKeyCreateRequest, tenantID string) (string, *models.APIKey, error) {
	now := time.Now()
	validDays := req.ValidDays
	if validDays <= 0 {
		validDays = 30
	}

	key := &models.APIKey{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		AppID:       req.AppID,
		APIID:       req.APIID,
		TenantID:    tenantID,
		Scopes:      km.normalizeScopes(req.Scopes),
		ValidFrom:   now,
		ValidTo:     now.Add(time.Duration(validDays) * 24 * time.Hour),
		Revoked:     false,
		UsageCount:  0,
		Metadata:    req.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := km.store.Create(key); err != nil {
		return "", nil, fmt.Errorf("store jwt api key: %w", err)
	}

	token, err := km.jwt.GenerateAPIKeyToken(
		req.APIID, req.AppID, tenantID, req.Scopes, validDays*24,
	)
	if err != nil {
		return "", nil, fmt.Errorf("generate jwt api key: %w", err)
	}

	return token, key, nil
}

// ValidateJWTAPIKey validates a JWT-formatted API key.
func (km *APIKeyManager) ValidateJWTAPIKey(tokenString string) (*models.APIKey, error) {
	claims, err := km.jwt.ValidateToken(tokenString)
	if err != nil {
		return nil, fmt.Errorf("invalid jwt api key: %w", err)
	}

	if claims.APIContext == "" {
		return nil, fmt.Errorf("jwt api key missing api_context claim")
	}
	if claims.ClientID == "" {
		return nil, fmt.Errorf("jwt api key missing client_id claim")
	}

	// Look up the key by its JTI (which serves as the key ID)
	key, err := km.store.GetByID(claims.Jti)
	if err != nil {
		// Fallback: allow validation based on claims alone for keys stored
		// via the standard flow.
		key = &models.APIKey{
			ID:       claims.Jti,
			APIID:    claims.APIContext,
			AppID:    claims.ClientID,
			TenantID: claims.TenantID,
			Scopes:   strings.Fields(claims.Scope),
			ValidTo:  time.Unix(claims.Exp, 0),
			ValidFrom: time.Unix(claims.Iat, 0),
		}
		return key, nil
	}

	if key.Revoked {
		return nil, fmt.Errorf("jwt api key has been revoked")
	}

	now := time.Now()
	if now.After(key.ValidTo) {
		return nil, fmt.Errorf("jwt api key has expired")
	}

	return key, nil
}
