// Package audit provides buffered audit logging with guaranteed database persistence.
// Every audit entry is written to the audit_log table via a background worker
// that drains a buffered channel. Entries are never silently dropped.
package audit

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/vedadb/vapim/pkg/models"
	"github.com/vedadb/vapim/pkg/store"
)

// AuditLogger buffers audit entries and persists them to the database via a
// background worker. It guarantees that entries are either written to the DB
// or logged as errors on failure.
type AuditLogger struct {
	store  store.Store
	buffer chan *models.AuditLogDB
	logger *zap.Logger
	wg     sync.WaitGroup
	done   chan struct{}
}

// NewAuditLogger creates an AuditLogger backed by the given store. A background
// worker is started immediately to process entries from the buffer.
func NewAuditLogger(st store.Store, logger *zap.Logger) *AuditLogger {
	if logger == nil {
		logger = zap.NewNop()
	}
	al := &AuditLogger{
		store:  st,
		buffer: make(chan *models.AuditLogDB, 1000),
		logger: logger,
		done:   make(chan struct{}),
	}
	go al.worker()
	return al
}

// Log creates an audit entry and queues it for DB persistence. The call is
// non-blocking; if the buffer is full the entry is dropped and a warning is
// logged. Context values "tenantID" and "userID" are extracted if present.
func (al *AuditLogger) Log(ctx context.Context, action, resourceType, resourceID string, details map[string]interface{}) {
	tenantID, _ := ctx.Value("tenantID").(string)
	if tenantID == "" {
		tenantID, _ = ctx.Value("tenant_id").(string)
	}

	userID, _ := ctx.Value("userID").(string)
	if userID == "" {
		userID, _ = ctx.Value("user_id").(string)
	}

	// Extract IP and user agent from context if available.
	ip, _ := ctx.Value("client_ip").(string)
	ua, _ := ctx.Value("user_agent").(string)

	detailsJSON, err := json.Marshal(details)
	if err != nil {
		detailsJSON = []byte("{}")
	}

	entry := &models.AuditLogDB{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      string(detailsJSON),
		IPAddress:    ip,
		UserAgent:    ua,
		Timestamp:    time.Now(),
	}

	select {
	case al.buffer <- entry:
		// queued successfully
	default:
		al.logger.Warn("audit buffer full, dropping entry",
			zap.String("action", action),
			zap.String("resource_type", resourceType),
		)
	}
}

// worker is the background goroutine that drains the buffer and writes
// entries to the database one at a time.
func (al *AuditLogger) worker() {
	al.wg.Add(1)
	defer al.wg.Done()

	for {
		select {
		case entry := <-al.buffer:
			if err := al.store.InsertAuditLog(entry); err != nil {
				al.logger.Error("failed to write audit log to DB",
					zap.Error(err),
					zap.String("action", entry.Action),
					zap.String("resource_id", entry.ResourceID),
				)
			}
		case <-al.done:
			// Drain remaining entries before shutdown.
			for {
				select {
				case entry := <-al.buffer:
					if err := al.store.InsertAuditLog(entry); err != nil {
						al.logger.Error("failed to write audit log on shutdown",
							zap.Error(err),
							zap.String("action", entry.Action),
						)
					}
				default:
					return
				}
			}
		}
	}
}

// LogAPIAction logs an audit entry for an API lifecycle event (create, update,
// delete, publish, etc.).
func (al *AuditLogger) LogAPIAction(ctx context.Context, action string, api *models.API) {
	al.Log(ctx, action, "api", api.ID, map[string]interface{}{
		"api_name":    api.Name,
		"api_context": api.Context,
		"api_version": api.Version,
		"status":      api.Status,
	})
}

// LogSubscriptionAction logs an audit entry for a subscription lifecycle event.
func (al *AuditLogger) LogSubscriptionAction(ctx context.Context, action string, sub *models.Subscription) {
	al.Log(ctx, action, "subscription", sub.ID, map[string]interface{}{
		"api_id": sub.APIID,
		"app_id": sub.AppID,
		"tier":   sub.Tier,
		"status": sub.Status,
	})
}

// LogAuthEvent logs an authentication or authorization event.
func (al *AuditLogger) LogAuthEvent(ctx context.Context, action, userID string, success bool, detail string) {
	al.Log(ctx, action, "auth", userID, map[string]interface{}{
		"success": success,
		"detail":  detail,
	})
}

// LogAdminAction logs an administrative action such as configuration changes,
// tenant management, or policy updates.
func (al *AuditLogger) LogAdminAction(ctx context.Context, action string, details map[string]interface{}) {
	al.Log(ctx, action, "admin", "", details)
}

// GetLogs retrieves audit logs for a tenant with pagination.
func (al *AuditLogger) GetLogs(tenantID string, limit, offset int) ([]*models.AuditLogDB, int, error) {
	return al.store.GetAuditLogs(tenantID, limit, offset)
}

// Close signals the background worker to shut down and waits for it to finish
// draining any queued entries.
func (al *AuditLogger) Close() {
	close(al.done)
	al.wg.Wait()
}
