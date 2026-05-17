// Package audit provides a comprehensive audit logging system for the VedaDB API Manager.
// It logs all admin actions, API lifecycle changes, subscription changes, and authentication events.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/vedadb/vapim/internal/tenant"
	"github.com/vedadb/vapim/pkg/models"
)

const (
	// TableName is the database table for audit logs.
	TableName = "audit_log"

	// ActionAPICreate is logged when an API is created.
	ActionAPICreate = "API_CREATE"
	// ActionAPIUpdate is logged when an API is updated.
	ActionAPIUpdate = "API_UPDATE"
	// ActionAPIDelete is logged when an API is deleted.
	ActionAPIDelete = "API_DELETE"
	// ActionAPIPublish is logged when an API is published.
	ActionAPIPublish = "API_PUBLISH"
	// ActionAPIDeprecate is logged when an API is deprecated.
	ActionAPIDeprecate = "API_DEPRECATE"
	// ActionAPIRetire is logged when an API is retired.
	ActionAPIRetire = "API_RETIRE"

	// ActionSubscriptionCreate is logged when a subscription is created.
	ActionSubscriptionCreate = "SUBSCRIPTION_CREATE"
	// ActionSubscriptionCancel is logged when a subscription is cancelled.
	ActionSubscriptionCancel = "SUBSCRIPTION_CANCEL"
	// ActionSubscriptionBlock is logged when a subscription is blocked.
	ActionSubscriptionBlock = "SUBSCRIPTION_BLOCK"

	// ActionAuthLogin is logged on login attempts.
	ActionAuthLogin = "AUTH_LOGIN"
	// ActionAuthLogout is logged on logout.
	ActionAuthLogout = "AUTH_LOGOUT"
	// ActionAuthTokenRefresh is logged on token refresh.
	ActionAuthTokenRefresh = "AUTH_TOKEN_REFRESH"
	// ActionAuthPasswordChange is logged on password change.
	ActionAuthPasswordChange = "AUTH_PASSWORD_CHANGE"

	// ActionAdminCreate is logged for admin creation operations.
	ActionAdminCreate = "ADMIN_CREATE"
	// ActionAdminUpdate is logged for admin update operations.
	ActionAdminUpdate = "ADMIN_UPDATE"
	// ActionAdminDelete is logged for admin delete operations.
	ActionAdminDelete = "ADMIN_DELETE"
	// ActionAdminConfigChange is logged for configuration changes.
	ActionAdminConfigChange = "ADMIN_CONFIG_CHANGE"

	// ActionWebhookCreate is logged when a webhook is registered.
	ActionWebhookCreate = "WEBHOOK_CREATE"
	// ActionWebhookDelete is logged when a webhook is unregistered.
	ActionWebhookDelete = "WEBHOOK_DELETE"

	// ActionPolicyCreate is logged when a throttle policy is created.
	ActionPolicyCreate = "POLICY_CREATE"
	// ActionPolicyUpdate is logged when a throttle policy is updated.
	ActionPolicyUpdate = "POLICY_UPDATE"
	// ActionPolicyDelete is logged when a throttle policy is deleted.
	ActionPolicyDelete = "POLICY_DELETE"
)

// SQLStore defines the interface for database operations used by the audit logger.
type SQLStore interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// LoggerOpts configures the audit logger behavior.
type LoggerOpts struct {
	// Store is the SQL database handle.
	Store SQLStore
	// StdoutEnabled writes audit entries to stdout.
	StdoutEnabled bool
	// Async enables asynchronous (non-blocking) logging via goroutines.
	Async bool
	// MaxAsyncQueueSize limits the number of pending async log entries.
	MaxAsyncQueueSize int
}

// AuditLogger defines the interface for audit logging operations.
type AuditLogger interface {
	// Log records a generic audit event.
	Log(ctx context.Context, action, resourceType, resourceID string, details map[string]interface{})
	// LogAPIAction records an API lifecycle action.
	LogAPIAction(ctx context.Context, action string, api *models.API)
	// LogSubscriptionAction records a subscription action.
	LogSubscriptionAction(ctx context.Context, action string, sub *models.Subscription)
	// LogAuthEvent records an authentication event.
	LogAuthEvent(ctx context.Context, action, userID string, success bool, details string)
	// LogAdminAction records an administrative action.
	LogAdminAction(ctx context.Context, action string, details map[string]interface{})
	// GetLogs retrieves audit logs with pagination.
	GetLogs(ctx context.Context, tenantID string, limit, offset int) ([]*models.AuditLog, int, error)
}

// Ensure DefaultLogger implements AuditLogger.
var _ AuditLogger = (*DefaultLogger)(nil)

// DefaultLogger is the production implementation of AuditLogger.
type DefaultLogger struct {
	store           SQLStore
	slogger         *slog.Logger
	stdoutEnabled   bool
	async           bool
	asyncQueue      chan *logEntry
	asyncDone       chan struct{}
}

// logEntry is an internal structure for queued log entries.
type logEntry struct {
	ctx          context.Context
	action       string
	resourceType string
	resourceID   string
	userID       *string
	username     *string
	ipAddress    *string
	details      map[string]interface{}
}

// NewLogger creates a new audit logger.
func NewLogger(opts LoggerOpts) (*DefaultLogger, error) {
	slogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	l := &DefaultLogger{
		store:         opts.Store,
		slogger:       slogger,
		stdoutEnabled: opts.StdoutEnabled,
		async:         opts.Async,
	}

	if opts.Async {
		queueSize := opts.MaxAsyncQueueSize
		if queueSize <= 0 {
			queueSize = 1000
		}
		l.asyncQueue = make(chan *logEntry, queueSize)
		l.asyncDone = make(chan struct{})
		go l.asyncWorker()
	}

	return l, nil
}

// Close gracefully shuts down the async worker.
func (l *DefaultLogger) Close() error {
	if l.async && l.asyncDone != nil {
		close(l.asyncQueue)
		<-l.asyncDone
	}
	return nil
}

// asyncWorker processes queued log entries in the background.
func (l *DefaultLogger) asyncWorker() {
	defer close(l.asyncDone)
	for entry := range l.asyncQueue {
		if entry == nil {
			continue
		}
		l.writeSync(entry.ctx, entry.action, entry.resourceType, entry.resourceID,
			entry.userID, entry.username, entry.ipAddress, entry.details)
	}
}

// Log records a generic audit event.
func (l *DefaultLogger) Log(ctx context.Context, action, resourceType, resourceID string, details map[string]interface{}) {
	userID, username := extractUserFromContext(ctx)
	ipAddress := extractIPFromContext(ctx)

	entry := &logEntry{
		ctx:          ctx,
		action:       action,
		resourceType: resourceType,
		resourceID:   resourceID,
		userID:       userID,
		username:     username,
		ipAddress:    ipAddress,
		details:      details,
	}

	if l.async {
		select {
		case l.asyncQueue <- entry:
			// queued successfully
		default:
			// queue full, fall back to sync logging
			l.writeSync(ctx, action, resourceType, resourceID, userID, username, ipAddress, details)
		}
	} else {
		l.writeSync(ctx, action, resourceType, resourceID, userID, username, ipAddress, details)
	}
}

// LogAPIAction records an API lifecycle action.
func (l *DefaultLogger) LogAPIAction(ctx context.Context, action string, api *models.API) {
	details := map[string]interface{}{
		"api_name":    api.Name,
		"api_version": api.Version,
		"api_context": api.Context,
		"auth_type":   api.AuthType,
		"status":      api.Status,
		"endpoint":    api.Endpoint,
	}
	if api.Provider != nil {
		details["provider"] = *api.Provider
	}
	l.Log(ctx, action, "API", api.ID, details)
}

// LogSubscriptionAction records a subscription action.
func (l *DefaultLogger) LogSubscriptionAction(ctx context.Context, action string, sub *models.Subscription) {
	details := map[string]interface{}{
		"api_id":         sub.APIID,
		"application_id": sub.ApplicationID,
		"tier":           sub.Tier,
		"status":         sub.Status,
	}
	if sub.API != nil {
		details["api_name"] = sub.API.Name
	}
	if sub.Application != nil {
		details["app_name"] = sub.Application.Name
	}
	l.Log(ctx, action, "SUBSCRIPTION", sub.ID, details)
}

// LogAuthEvent records an authentication event.
func (l *DefaultLogger) LogAuthEvent(ctx context.Context, action, userID string, success bool, details string) {
	d := map[string]interface{}{
		"success": success,
	}
	if details != "" {
		d["details"] = details
	}

	var userIDPtr *string
	if userID != "" {
		userIDPtr = &userID
	}

	username, _ := extractUserFromContext(ctx)
	ipAddress := extractIPFromContext(ctx)

	l.writeSync(ctx, action, "AUTH", userID, userIDPtr, username, ipAddress, d)
}

// LogAdminAction records an administrative action.
func (l *DefaultLogger) LogAdminAction(ctx context.Context, action string, details map[string]interface{}) {
	// Ensure details is not nil
	if details == nil {
		details = map[string]interface{}{}
	}

	// Add admin role context
	if role, ok := ctx.Value("user_role").(string); ok {
		details["actor_role"] = role
	}

	l.Log(ctx, action, "ADMIN", "", details)
}

// GetLogs retrieves audit logs filtered by tenant with pagination.
func (l *DefaultLogger) GetLogs(ctx context.Context, tenantID string, limit, offset int) ([]*models.AuditLog, int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	// Count total
	var total int
	countQuery := `SELECT COUNT(*) FROM ` + TableName + ` WHERE tenant_id = $1`
	row := l.store.QueryRowContext(ctx, countQuery, tenantID)
	if err := row.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	// Fetch paginated results
	query := `
		SELECT id, action, resource_type, resource_id, user_id, username, ip_address, details, tenant_id, created_at
		FROM ` + TableName + `
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := l.store.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("query audit logs: %w", err)
	}
	defer rows.Close()

	var logs []*models.AuditLog
	for rows.Next() {
		entry := &models.AuditLog{}
		var detailsBytes []byte
		err := rows.Scan(
			&entry.ID, &entry.Action, &entry.ResourceType, &entry.ResourceID,
			&entry.UserID, &entry.Username, &entry.IPAddress,
			&detailsBytes, &entry.TenantID, &entry.CreatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		if len(detailsBytes) > 0 {
			_ = json.Unmarshal(detailsBytes, &entry.Details)
		}
		logs = append(logs, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate audit logs: %w", err)
	}

	return logs, total, nil
}

// writeSync performs the actual synchronous write to database and optionally stdout.
func (l *DefaultLogger) writeSync(ctx context.Context, action, resourceType, resourceID string,
	userID, username, ipAddress *string, details map[string]interface{}) {

	tenantID := tenant.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = "system"
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	var detailsJSON []byte
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	// Write to database
	query := `
		INSERT INTO ` + TableName + `
		(id, action, resource_type, resource_id, user_id, username, ip_address, details, tenant_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := l.store.ExecContext(ctx, query, id, action, resourceType, resourceID,
		userID, username, ipAddress, detailsJSON, tenantID, now)
	if err != nil {
		l.slogger.ErrorContext(ctx, "audit log db write failed",
			slog.String("error", err.Error()),
			slog.String("action", action),
		)
	}

	// Write to stdout
	if l.stdoutEnabled {
		l.slogger.InfoContext(ctx, "AUDIT",
			slog.String("id", id),
			slog.String("tenant", tenantID),
			slog.String("action", action),
			slog.String("resource_type", resourceType),
			slog.String("resource_id", resourceID),
			slog.Time("timestamp", now),
		)
	}
}

// extractUserFromContext attempts to extract user information from the context.
func extractUserFromContext(ctx context.Context) (*string, *string) {
	if userID, ok := ctx.Value("user_id").(string); ok && userID != "" {
		var username *string
		if un, ok := ctx.Value("username").(string); ok {
			username = &un
		}
		return &userID, username
	}
	return nil, nil
}

// extractIPFromContext attempts to extract the client IP from the context.
func extractIPFromContext(ctx context.Context) *string {
	if ip, ok := ctx.Value("client_ip").(string); ok && ip != "" {
		return &ip
	}
	return nil
}

// NopLogger is a no-op implementation of AuditLogger for testing.
type NopLogger struct{}

// Log implements AuditLogger as a no-op.
func (n *NopLogger) Log(ctx context.Context, action, resourceType, resourceID string, details map[string]interface{}) {}

// LogAPIAction implements AuditLogger as a no-op.
func (n *NopLogger) LogAPIAction(ctx context.Context, action string, api *models.API) {}

// LogSubscriptionAction implements AuditLogger as a no-op.
func (n *NopLogger) LogSubscriptionAction(ctx context.Context, action string, sub *models.Subscription) {}

// LogAuthEvent implements AuditLogger as a no-op.
func (n *NopLogger) LogAuthEvent(ctx context.Context, action, userID string, success bool, details string) {}

// LogAdminAction implements AuditLogger as a no-op.
func (n *NopLogger) LogAdminAction(ctx context.Context, action string, details map[string]interface{}) {}

// GetLogs implements AuditLogger as a no-op.
func (n *NopLogger) GetLogs(ctx context.Context, tenantID string, limit, offset int) ([]*models.AuditLog, int, error) {
	return nil, 0, nil
}
