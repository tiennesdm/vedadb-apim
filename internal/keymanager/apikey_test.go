package keymanager

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// API Key Implementation
// ============================================================================

// APIKeyEntry represents a stored API key
type APIKeyEntry struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Owner       string    `json:"owner"`
	Status      string    `json:"status"` // active, inactive, revoked, expired
	Scopes      []string  `json:"scopes,omitempty"`
	Quota       int64     `json:"quota,omitempty"`
	UsedQuota   int64     `json:"used_quota"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// IsExpired checks if the key has expired
func (k *APIKeyEntry) IsExpired() bool {
	if k.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(k.ExpiresAt)
}

// IsActive checks if the key is active and not expired
func (k *APIKeyEntry) IsActive() bool {
	return k.Status == "active" && !k.IsExpired()
}

// HasScope checks if the key has a specific scope
func (k *APIKeyEntry) HasScope(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// CanUseQuota checks if the key has remaining quota
func (k *APIKeyEntry) CanUseQuota() bool {
	if k.Quota <= 0 {
		return true // No quota limit
	}
	return k.UsedQuota < k.Quota
}

// APIKeyManager manages API keys
type APIKeyManager struct {
	keys map[string]*APIKeyEntry // key -> entry
	mu   sync.RWMutex
}

// NewAPIKeyManager creates a new API key manager
func NewAPIKeyManager() *APIKeyManager {
	return &APIKeyManager{
		keys: make(map[string]*APIKeyEntry),
	}
}

// GenerateKey creates a new API key
func (m *APIKeyManager) GenerateKey(name, owner string, scopes []string, expiry time.Duration) (*APIKeyEntry, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if owner == "" {
		return nil, fmt.Errorf("owner is required")
	}

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	entry := &APIKeyEntry{
		ID:        generateRandomString(16),
		Key:       "vapim_" + hex.EncodeToString(keyBytes),
		Name:      name,
		Owner:     owner,
		Status:    "active",
		Scopes:    scopes,
		CreatedAt: time.Now(),
	}

	if expiry > 0 {
		entry.ExpiresAt = time.Now().Add(expiry)
	}

	m.mu.Lock()
	m.keys[entry.Key] = entry
	m.mu.Unlock()

	return entry, nil
}

// ValidateKey checks if a key is valid and active
func (m *APIKeyManager) ValidateKey(key string) (*APIKeyEntry, error) {
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	m.mu.RLock()
	entry, exists := m.keys[key]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("invalid key")
	}

	if !entry.IsActive() {
		if entry.IsExpired() {
			return nil, fmt.Errorf("key has expired")
		}
		return nil, fmt.Errorf("key is %s", entry.Status)
	}

	if !entry.CanUseQuota() {
		return nil, fmt.Errorf("quota exceeded")
	}

	// Update last used
	m.mu.Lock()
	entry.LastUsedAt = time.Now()
	entry.UsedQuota++
	m.mu.Unlock()

	return entry, nil
}

// RevokeKey revokes an API key
func (m *APIKeyManager) RevokeKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.keys[key]
	if !exists {
		return fmt.Errorf("key not found")
	}

	if entry.Status == "revoked" {
		return fmt.Errorf("key already revoked")
	}

	entry.Status = "revoked"
	return nil
}

// DeactivateKey deactivates an API key
func (m *APIKeyManager) DeactivateKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.keys[key]
	if !exists {
		return fmt.Errorf("key not found")
	}

	if entry.Status == "revoked" {
		return fmt.Errorf("cannot deactivate revoked key")
	}

	entry.Status = "inactive"
	return nil
}

// ReactivateKey reactivates an inactive API key
func (m *APIKeyManager) ReactivateKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.keys[key]
	if !exists {
		return fmt.Errorf("key not found")
	}

	if entry.Status == "revoked" {
		return fmt.Errorf("cannot reactivate revoked key")
	}

	entry.Status = "active"
	return nil
}

// GetKey retrieves a key by its value
func (m *APIKeyManager) GetKey(key string) (*APIKeyEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, exists := m.keys[key]
	if !exists {
		return nil, fmt.Errorf("key not found")
	}

	// Return a copy
	entryCopy := *entry
	return &entryCopy, nil
}

// ListKeysByOwner returns all keys for an owner
func (m *APIKeyManager) ListKeysByOwner(owner string) []*APIKeyEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*APIKeyEntry
	for _, entry := range m.keys {
		if entry.Owner == owner {
			entryCopy := *entry
			result = append(result, &entryCopy)
		}
	}
	return result
}

// DeleteKey permanently deletes a key
func (m *APIKeyManager) DeleteKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.keys[key]; !exists {
		return fmt.Errorf("key not found")
	}

	delete(m.keys, key)
	return nil
}

// ============================================================================
// TESTS
// ============================================================================

func TestAPIKeyManager_GenerateKey_GivenValidInput_WhenGenerated_ThenReturnsKey(t *testing.T) {
	manager := NewAPIKeyManager()

	tests := []struct {
		name    string
		kName   string
		owner   string
		scopes  []string
		expiry  time.Duration
	}{
		{
			name:   "simple key",
			kName:  "Production Key",
			owner:  "user-1",
			scopes: []string{"read"},
			expiry: 24 * time.Hour,
		},
		{
			name:   "key with multiple scopes",
			kName:  "Admin Key",
			owner:  "user-2",
			scopes: []string{"read", "write", "admin"},
			expiry: 0, // No expiry
		},
		{
			name:   "key with empty scopes",
			kName:  "Basic Key",
			owner:  "user-3",
			scopes: []string{},
			expiry: 1 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := manager.GenerateKey(tt.kName, tt.owner, tt.scopes, tt.expiry)
			require.NoError(t, err)
			require.NotNil(t, key)

			assert.NotEmpty(t, key.ID)
			assert.NotEmpty(t, key.Key)
			assert.True(t, strings.HasPrefix(key.Key, "vapim_"))
			assert.Equal(t, tt.kName, key.Name)
			assert.Equal(t, tt.owner, key.Owner)
			assert.Equal(t, "active", key.Status)
			assert.Equal(t, tt.scopes, key.Scopes)
			assert.False(t, key.CreatedAt.IsZero())

			if tt.expiry > 0 {
				assert.False(t, key.ExpiresAt.IsZero())
				assert.True(t, key.ExpiresAt.After(time.Now()))
			} else {
				assert.True(t, key.ExpiresAt.IsZero())
			}
		})
	}
}

func TestAPIKeyManager_GenerateKey_GivenInvalidInput_WhenGenerated_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()

	tests := []struct {
		name   string
		kName  string
		owner  string
		expect string
	}{
		{"empty name", "", "user-1", "name is required"},
		{"empty owner", "My Key", "", "owner is required"},
		{"both empty", "", "", "name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := manager.GenerateKey(tt.kName, tt.owner, nil, 0)
			assert.Error(t, err)
			assert.Nil(t, key)
			assert.Contains(t, err.Error(), tt.expect)
		})
	}
}

func TestAPIKeyManager_ValidateKey_GivenValidKey_WhenValidated_ThenReturnsEntry(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", []string{"read", "write"}, 24*time.Hour)
	require.NoError(t, err)

	entry, err := manager.ValidateKey(key.Key)
	require.NoError(t, err)
	assert.NotNil(t, entry)
	assert.Equal(t, key.ID, entry.ID)
	assert.Equal(t, key.Name, entry.Name)
	assert.False(t, entry.LastUsedAt.IsZero())
	assert.Equal(t, int64(1), entry.UsedQuota)
}

func TestAPIKeyManager_ValidateKey_GivenInvalidKey_WhenValidated_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()

	tests := []struct {
		name string
		key  string
	}{
		{"empty key", ""},
		{"wrong key", "vapim_00000000000000000000000000000000"},
		{"random string", "not-a-valid-key"},
		{"malformed prefix", "invalid_prefix_abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := manager.ValidateKey(tt.key)
			assert.Error(t, err)
			assert.Nil(t, entry)
		})
	}
}

func TestAPIKeyManager_ValidateKey_GivenExpiredKey_WhenValidated_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 1*time.Millisecond)
	require.NoError(t, err)

	// Wait for expiry
	time.Sleep(50 * time.Millisecond)

	entry, err := manager.ValidateKey(key.Key)
	assert.Error(t, err)
	assert.Nil(t, entry)
	assert.Contains(t, err.Error(), "expired")
}

func TestAPIKeyManager_ValidateKey_GivenRevokedKey_WhenValidated_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	err = manager.RevokeKey(key.Key)
	require.NoError(t, err)

	entry, err := manager.ValidateKey(key.Key)
	assert.Error(t, err)
	assert.Nil(t, entry)
	assert.Contains(t, err.Error(), "revoked")
}

func TestAPIKeyManager_ValidateKey_GivenInactiveKey_WhenValidated_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	err = manager.DeactivateKey(key.Key)
	require.NoError(t, err)

	entry, err := manager.ValidateKey(key.Key)
	assert.Error(t, err)
	assert.Nil(t, entry)
	assert.Contains(t, err.Error(), "inactive")
}

func TestAPIKeyManager_RevokeKey_GivenValidKey_WhenRevoked_ThenCannotBeUsed(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	// Should work before revocation
	_, err = manager.ValidateKey(key.Key)
	require.NoError(t, err)

	// Revoke
	err = manager.RevokeKey(key.Key)
	require.NoError(t, err)

	// Should fail after revocation
	_, err = manager.ValidateKey(key.Key)
	assert.Error(t, err)

	// Double revoke should fail
	err = manager.RevokeKey(key.Key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already revoked")
}

func TestAPIKeyManager_RevokeKey_GivenNonExistent_WhenRevoked_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()
	err := manager.RevokeKey("non-existent-key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIKeyManager_DeactivateKey_GivenActiveKey_WhenDeactivated_ThenInactive(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	err = manager.DeactivateKey(key.Key)
	require.NoError(t, err)

	entry, _ := manager.GetKey(key.Key)
	assert.Equal(t, "inactive", entry.Status)
}

func TestAPIKeyManager_ReactivateKey_GivenInactiveKey_WhenReactivated_ThenActive(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	err = manager.DeactivateKey(key.Key)
	require.NoError(t, err)

	err = manager.ReactivateKey(key.Key)
	require.NoError(t, err)

	// Should work again
	_, err = manager.ValidateKey(key.Key)
	assert.NoError(t, err)
}

func TestAPIKeyManager_ReactivateKey_GivenRevokedKey_WhenReactivated_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	err = manager.RevokeKey(key.Key)
	require.NoError(t, err)

	err = manager.ReactivateKey(key.Key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot reactivate revoked key")
}

func TestAPIKeyManager_DeactivateKey_GivenRevokedKey_WhenDeactivated_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	err = manager.RevokeKey(key.Key)
	require.NoError(t, err)

	err = manager.DeactivateKey(key.Key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot deactivate revoked key")
}

func TestAPIKeyEntry_IsExpired_GivenVariousStates_WhenChecked_ThenCorrectResult(t *testing.T) {
	tests := []struct {
		name     string
		expires  time.Time
		expected bool
	}{
		{"not expired", time.Now().Add(1 * time.Hour), false},
		{"expired", time.Now().Add(-1 * time.Hour), true},
		{"just expired", time.Now().Add(-1 * time.Second), true},
		{"never expires", time.Time{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &APIKeyEntry{ExpiresAt: tt.expires}
			assert.Equal(t, tt.expected, entry.IsExpired())
		})
	}
}

func TestAPIKeyEntry_IsActive_GivenVariousStates_WhenChecked_ThenCorrectResult(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expires  time.Time
		expected bool
	}{
		{"active not expired", "active", time.Now().Add(1 * time.Hour), true},
		{"active expired", "active", time.Now().Add(-1 * time.Hour), false},
		{"inactive not expired", "inactive", time.Now().Add(1 * time.Hour), false},
		{"revoked not expired", "revoked", time.Now().Add(1 * time.Hour), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &APIKeyEntry{Status: tt.status, ExpiresAt: tt.expires}
			assert.Equal(t, tt.expected, entry.IsActive())
		})
	}
}

func TestAPIKeyEntry_HasScope_GivenScopes_WhenChecked_ThenCorrectResult(t *testing.T) {
	entry := &APIKeyEntry{Scopes: []string{"read", "write", "admin"}}

	assert.True(t, entry.HasScope("read"))
	assert.True(t, entry.HasScope("write"))
	assert.True(t, entry.HasScope("admin"))
	assert.False(t, entry.HasScope("delete"))
	assert.False(t, entry.HasScope(""))

	// Empty scopes
	entry2 := &APIKeyEntry{Scopes: nil}
	assert.False(t, entry2.HasScope("read"))
}

func TestAPIKeyEntry_CanUseQuota_GivenVariousQuotas_WhenChecked_ThenCorrectResult(t *testing.T) {
	tests := []struct {
		name     string
		quota    int64
		used     int64
		expected bool
	}{
		{"unlimited quota", 0, 1000, true},
		{"negative quota", -1, 1000, true},
		{"within quota", 100, 50, true},
		{"at quota limit", 100, 100, false},
		{"over quota", 100, 101, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &APIKeyEntry{Quota: tt.quota, UsedQuota: tt.used}
			assert.Equal(t, tt.expected, entry.CanUseQuota())
		})
	}
}

func TestAPIKeyManager_Quota_GivenLimitedQuota_WhenValidated_ThenTracksUsage(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 24*time.Hour)
	require.NoError(t, err)

	// Manually set quota
	manager.mu.Lock()
	manager.keys[key.Key].Quota = 3
	manager.mu.Unlock()

	// First 3 validations should succeed
	for i := 0; i < 3; i++ {
		_, err = manager.ValidateKey(key.Key)
		require.NoError(t, err, "validation %d should succeed", i+1)
	}

	// 4th should fail with quota exceeded
	_, err = manager.ValidateKey(key.Key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "quota exceeded")
}

func TestAPIKeyManager_ListKeysByOwner_GivenMultipleOwners_WhenListed_ThenReturnsOnlyOwnersKeys(t *testing.T) {
	manager := NewAPIKeyManager()

	_, err := manager.GenerateKey("Key 1", "user-1", nil, 0)
	require.NoError(t, err)
	_, err = manager.GenerateKey("Key 2", "user-1", nil, 0)
	require.NoError(t, err)
	_, err = manager.GenerateKey("Key 3", "user-2", nil, 0)
	require.NoError(t, err)

	user1Keys := manager.ListKeysByOwner("user-1")
	assert.Len(t, user1Keys, 2)

	user2Keys := manager.ListKeysByOwner("user-2")
	assert.Len(t, user2Keys, 1)

	noKeys := manager.ListKeysByOwner("user-3")
	assert.Len(t, noKeys, 0)
}

func TestAPIKeyManager_DeleteKey_GivenExistingKey_WhenDeleted_ThenGone(t *testing.T) {
	manager := NewAPIKeyManager()
	key, err := manager.GenerateKey("Test Key", "user-1", nil, 0)
	require.NoError(t, err)

	err = manager.DeleteKey(key.Key)
	require.NoError(t, err)

	_, err = manager.GetKey(key.Key)
	assert.Error(t, err)
}

func TestAPIKeyManager_DeleteKey_GivenNonExistent_WhenDeleted_ThenReturnsError(t *testing.T) {
	manager := NewAPIKeyManager()
	err := manager.DeleteKey("non-existent")
	assert.Error(t, err)
}

func TestAPIKeyManager_ConcurrentAccess_GivenMultipleGoroutines_WhenOperating_ThenNoRace(t *testing.T) {
	manager := NewAPIKeyManager()

	// Pre-generate some keys
	var keys []string
	for i := 0; i < 10; i++ {
		key, _ := manager.GenerateKey(fmt.Sprintf("Key %d", i), fmt.Sprintf("user-%d", i%3), nil, 24*time.Hour)
		keys = append(keys, key.Key)
	}

	var wg sync.WaitGroup
	wg.Add(4)

	// Concurrent validates
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			manager.ValidateKey(keys[i%len(keys)])
		}
	}()

	// Concurrent revokes
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			manager.RevokeKey(keys[i%len(keys)])
		}
	}()

	// Concurrent generates
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			manager.GenerateKey(fmt.Sprintf("New Key %d", i), "user-concurrent", nil, 0)
		}
	}()

	// Concurrent gets
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			manager.GetKey(keys[i%len(keys)])
		}
	}()

	wg.Wait()
}

func TestAPIKeyManager_GenerateKey_UniqueKeys_GivenMultipleKeys_WhenGenerated_ThenAllUnique(t *testing.T) {
	manager := NewAPIKeyManager()
	keySet := make(map[string]bool)

	for i := 0; i < 100; i++ {
		key, err := manager.GenerateKey(fmt.Sprintf("Key %d", i), "user-1", nil, 0)
		require.NoError(t, err)
		assert.False(t, keySet[key.Key], "key %d should be unique", i)
		keySet[key.Key] = true
	}

	assert.Len(t, keySet, 100)
}

func TestAPIKeyManager_GetKey_GivenExistingKey_WhenRetrieved_ThenReturnsCopy(t *testing.T) {
	manager := NewAPIKeyManager()
	key, _ := manager.GenerateKey("Test Key", "user-1", nil, 0)

	entry1, err := manager.GetKey(key.Key)
	require.NoError(t, err)

	entry2, err := manager.GetKey(key.Key)
	require.NoError(t, err)

	// Modifying one should not affect the other
	entry1.Name = "Modified"
	assert.Equal(t, "Test Key", entry2.Name)
}
