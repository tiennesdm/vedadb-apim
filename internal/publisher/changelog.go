// Package publisher provides API changelog tracking for the VedaDB API Manager.
package publisher

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
	"github.com/tiennesdm/vedadb-apim/pkg/store"
)

// ---------------------------------------------------------------------------
// Changelog operations using the public Store interface
// ---------------------------------------------------------------------------

// LogChange records a change to an API in the changelog table.
// The userID is read from the context under the key "userID".
func LogChange(ctx context.Context, s store.Store, apiID, changeType, description string) error {
	if apiID == "" {
		return fmt.Errorf("api_id is required")
	}
	if changeType == "" {
		return fmt.Errorf("change_type is required")
	}

	userID, _ := ctx.Value("userID").(string)
	if userID == "" {
		userID = "system"
	}

	entry := &models.APIChangeDB{
		ID:          uuid.New().String(),
		APIID:       apiID,
		ChangeType:  changeType,
		Description: description,
		ChangedBy:   userID,
		ChangedAt:   time.Now(),
	}

	return s.InsertChangelog(entry)
}

// GetChangelog returns the change history for an API ordered newest first.
func GetChangelog(s store.Store, apiID string) ([]*models.APIChangeDB, error) {
	if apiID == "" {
		return nil, fmt.Errorf("api_id is required")
	}
	return s.GetChangelog(apiID)
}

// GetChangelogPaginated returns a paginated changelog. Since the base Store
// interface does not expose LIMIT/OFFSET on GetChangelog, this helper
// fetches all entries and slices them in-memory. For high-volume APIs,
// extend the Store interface with a paginated variant.
func GetChangelogPaginated(s store.Store, apiID string, limit, offset int) ([]*models.APIChangeDB, int, error) {
	if apiID == "" {
		return nil, 0, fmt.Errorf("api_id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	all, err := s.GetChangelog(apiID)
	if err != nil {
		return nil, 0, err
	}

	total := len(all)
	if offset >= total {
		return []*models.APIChangeDB{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

// LogLifecycleChange records a lifecycle state transition.
func LogLifecycleChange(ctx context.Context, s store.Store, apiID string, fromStatus, toStatus models.APIStatus) error {
	desc := fmt.Sprintf("API lifecycle changed from %s to %s", fromStatus, toStatus)
	return LogChange(ctx, s, apiID, "lifecycle_change", desc)
}

// LogResourceChange records the addition, update, or removal of a resource.
func LogResourceChange(ctx context.Context, s store.Store, apiID, action, method, path string) error {
	desc := fmt.Sprintf("Resource %s: %s %s", action, method, path)
	return LogChange(ctx, s, apiID, "resource_"+action, desc)
}

// LogPolicyChange records a policy attachment or detachment.
func LogPolicyChange(ctx context.Context, s store.Store, apiID, policyName, action string) error {
	desc := fmt.Sprintf("Policy '%s' %s", policyName, action)
	return LogChange(ctx, s, apiID, "policy_change", desc)
}

// LogVersionChange records a version-related change.
func LogVersionChange(ctx context.Context, s store.Store, apiID, version, action string) error {
	desc := fmt.Sprintf("Version %s %s", version, action)
	return LogChange(ctx, s, apiID, "version_change", desc)
}

// ---------------------------------------------------------------------------
// ChangelogService provides a struct-based API for changelog operations.
// ---------------------------------------------------------------------------

// ChangelogService wraps changelog operations with a store reference.
type ChangelogService struct {
	store store.Store
}

// NewChangelogService creates a new ChangelogService.
func NewChangelogService(s store.Store) *ChangelogService {
	return &ChangelogService{store: s}
}

// Log delegates to the package-level LogChange with the service's store.
func (cs *ChangelogService) Log(ctx context.Context, apiID, changeType, description string) error {
	return LogChange(ctx, cs.store, apiID, changeType, description)
}

// Get returns the full changelog for an API.
func (cs *ChangelogService) Get(apiID string) ([]*models.APIChangeDB, error) {
	return GetChangelog(cs.store, apiID)
}

// GetPaginated returns a paginated view of the changelog.
func (cs *ChangelogService) GetPaginated(apiID string, limit, offset int) ([]*models.APIChangeDB, int, error) {
	return GetChangelogPaginated(cs.store, apiID, limit, offset)
}

// SchemaSQL returns the DDL required to create the api_changelog table.
func SchemaSQL() string {
	return `
CREATE TABLE IF NOT EXISTS api_changelog (
    id VARCHAR(36) PRIMARY KEY,
    api_id VARCHAR(36) NOT NULL REFERENCES apis(id),
    change_type VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    changed_by VARCHAR(36),
    changed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_changelog_api ON api_changelog(api_id);
CREATE INDEX IF NOT EXISTS idx_changelog_time ON api_changelog(changed_at DESC);
`
}
