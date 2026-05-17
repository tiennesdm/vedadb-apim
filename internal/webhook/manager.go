// Package webhook provides webhook management for the VedaDB API Manager.
package webhook

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vedadb/vapim/internal/tenant"
	"github.com/vedadb/vapim/pkg/models"
)

// SQLStore defines the database interface used by the webhook manager.
type SQLStore interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// WebhookManager defines the interface for webhook management operations.
type WebhookManager interface {
	// Register creates a new webhook registration.
	Register(ctx context.Context, webhook *models.Webhook) error
	// Unregister removes a webhook registration by ID.
	Unregister(ctx context.Context, id string) error
	// Deliver sends a payload to all registered webhooks matching the event type.
	Deliver(eventType string, payload map[string]interface{}) error
	// ListByAPI returns all webhooks registered for a specific API.
	ListByAPI(ctx context.Context, apiID string) ([]*models.Webhook, error)
	// ListAll returns all webhooks for the current tenant.
	ListAll(ctx context.Context) ([]*models.Webhook, error)
	// ListDeliveries returns recent delivery attempts for a webhook.
	ListDeliveries(ctx context.Context, webhookID string, limit int) ([]*models.WebhookDelivery, error)
	// RetryDelivery re-attempts a failed delivery.
	RetryDelivery(ctx context.Context, deliveryID string) error
	// GetWebhook retrieves a single webhook by ID.
	GetWebhook(ctx context.Context, id string) (*models.Webhook, error)
	// UpdateWebhook updates an existing webhook.
	UpdateWebhook(ctx context.Context, webhook *models.Webhook) error
	// ToggleWebhook activates or deactivates a webhook.
	ToggleWebhook(ctx context.Context, id string, active bool) error
}

// Ensure DefaultManager implements WebhookManager.
var _ WebhookManager = (*DefaultManager)(nil)

// DefaultManager is the production implementation of WebhookManager.
type DefaultManager struct {
	store     SQLStore
	deliverer Deliverer
}

// NewManager creates a new webhook manager.
func NewManager(store SQLStore, deliverer Deliverer) *DefaultManager {
	return &DefaultManager{
		store:     store,
		deliverer: deliverer,
	}
}

// Register creates a new webhook in the database.
func (m *DefaultManager) Register(ctx context.Context, webhook *models.Webhook) error {
	if webhook == nil {
		return fmt.Errorf("webhook is nil")
	}
	if webhook.Name == "" {
		return fmt.Errorf("webhook name is required")
	}
	if webhook.CallbackURL == "" {
		return fmt.Errorf("webhook callback URL is required")
	}
	if len(webhook.EventTypes) == 0 {
		return fmt.Errorf("at least one event type is required")
	}

	// Validate event types
	for _, et := range webhook.EventTypes {
		if !IsValidEventType(et) {
			return fmt.Errorf("invalid event type: %s", et)
		}
	}

	webhook.ID = uuid.New().String()
	webhook.Active = true
	webhook.TenantID = tenant.TenantIDFromContext(ctx)
	webhook.CreatedAt = time.Now().UTC()
	webhook.UpdatedAt = webhook.CreatedAt

	query := `
		INSERT INTO webhooks (id, api_id, name, callback_url, secret, event_types, active, tenant_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	var apiID interface{}
	if webhook.APIID != nil {
		apiID = *webhook.APIID
	} else {
		apiID = nil
	}

	_, err := m.store.ExecContext(ctx, query,
		webhook.ID, apiID, webhook.Name, webhook.CallbackURL,
		webhook.Secret, webhook.EventTypes, webhook.Active,
		webhook.TenantID, webhook.CreatedAt, webhook.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert webhook: %w", err)
	}

	return nil
}

// Unregister soft-deletes a webhook by setting it inactive.
func (m *DefaultManager) Unregister(ctx context.Context, id string) error {
	query := `
		UPDATE webhooks SET active = false, updated_at = $1
		WHERE id = $2 AND tenant_id = $3
	`
	result, err := m.store.ExecContext(ctx, query, time.Now().UTC(), id, tenant.TenantIDFromContext(ctx))
	if err != nil {
		return fmt.Errorf("deactivate webhook: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("webhook not found: %s", id)
	}

	return nil
}

// Deliver sends the event payload to all active webhooks subscribed to the event type.
func (m *DefaultManager) Deliver(eventType string, payload map[string]interface{}) error {
	ctx := context.Background()

	// Find all active webhooks that subscribe to this event type
	query := `
		SELECT id, api_id, name, callback_url, secret, event_types, active, tenant_id, created_at, updated_at
		FROM webhooks
		WHERE active = true AND $1 = ANY(event_types)
	`

	rows, err := m.store.QueryContext(ctx, query, eventType)
	if err != nil {
		return fmt.Errorf("query webhooks for event %s: %w", eventType, err)
	}
	defer rows.Close()

	var webhooks []*models.Webhook
	for rows.Next() {
		wh := &models.Webhook{}
		var apiID sql.NullString
		err := rows.Scan(
			&wh.ID, &apiID, &wh.Name, &wh.CallbackURL, &wh.Secret,
			&wh.EventTypes, &wh.Active, &wh.TenantID, &wh.CreatedAt, &wh.UpdatedAt,
		)
		if err != nil {
			continue
		}
		if apiID.Valid {
			wh.APIID = &apiID.String
		}
		webhooks = append(webhooks, wh)
	}

	// Deliver to each webhook asynchronously
	for _, wh := range webhooks {
		wh := wh // capture range variable
		go func() {
			ctx := tenant.WithTenant(ctx, &models.Tenant{ID: wh.TenantID, Slug: wh.TenantID})
			if err := m.deliverer.Deliver(ctx, wh, eventType, payload); err != nil {
				// Delivery errors are tracked in the delivery records
				_ = err
			}
		}()
	}

	return nil
}

// ListByAPI returns all webhooks for a specific API.
func (m *DefaultManager) ListByAPI(ctx context.Context, apiID string) ([]*models.Webhook, error) {
	query := `
		SELECT id, api_id, name, callback_url, secret, event_types, active, tenant_id, created_at, updated_at
		FROM webhooks
		WHERE api_id = $1 AND tenant_id = $2 AND active = true
		ORDER BY created_at DESC
	`

	rows, err := m.store.QueryContext(ctx, query, apiID, tenant.TenantIDFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("query webhooks by API: %w", err)
	}
	defer rows.Close()

	return scanWebhooks(rows)
}

// ListAll returns all webhooks for the current tenant.
func (m *DefaultManager) ListAll(ctx context.Context) ([]*models.Webhook, error) {
	query := `
		SELECT id, api_id, name, callback_url, secret, event_types, active, tenant_id, created_at, updated_at
		FROM webhooks
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`

	rows, err := m.store.QueryContext(ctx, query, tenant.TenantIDFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("query all webhooks: %w", err)
	}
	defer rows.Close()

	return scanWebhooks(rows)
}

// GetWebhook retrieves a single webhook by ID.
func (m *DefaultManager) GetWebhook(ctx context.Context, id string) (*models.Webhook, error) {
	query := `
		SELECT id, api_id, name, callback_url, secret, event_types, active, tenant_id, created_at, updated_at
		FROM webhooks
		WHERE id = $1 AND tenant_id = $2
	`

	wh := &models.Webhook{}
	var apiID sql.NullString
	err := m.store.QueryRowContext(ctx, query, id, tenant.TenantIDFromContext(ctx)).Scan(
		&wh.ID, &apiID, &wh.Name, &wh.CallbackURL, &wh.Secret,
		&wh.EventTypes, &wh.Active, &wh.TenantID, &wh.CreatedAt, &wh.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("webhook not found: %s", id)
		}
		return nil, fmt.Errorf("get webhook: %w", err)
	}
	if apiID.Valid {
		wh.APIID = &apiID.String
	}

	return wh, nil
}

// UpdateWebhook updates an existing webhook.
func (m *DefaultManager) UpdateWebhook(ctx context.Context, webhook *models.Webhook) error {
	if webhook == nil || webhook.ID == "" {
		return fmt.Errorf("webhook ID is required")
	}

	query := `
		UPDATE webhooks SET
			name = $1,
			callback_url = $2,
			secret = $3,
			event_types = $4,
			active = $5,
			updated_at = $6
		WHERE id = $7 AND tenant_id = $8
	`

	result, err := m.store.ExecContext(ctx, query,
		webhook.Name, webhook.CallbackURL, webhook.Secret,
		webhook.EventTypes, webhook.Active, time.Now().UTC(),
		webhook.ID, tenant.TenantIDFromContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("update webhook: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("webhook not found or no changes: %s", webhook.ID)
	}

	return nil
}

// ToggleWebhook activates or deactivates a webhook.
func (m *DefaultManager) ToggleWebhook(ctx context.Context, id string, active bool) error {
	query := `
		UPDATE webhooks SET active = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`
	result, err := m.store.ExecContext(ctx, query, active, time.Now().UTC(), id, tenant.TenantIDFromContext(ctx))
	if err != nil {
		return fmt.Errorf("toggle webhook: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("webhook not found: %s", id)
	}

	return nil
}

// ListDeliveries returns recent delivery attempts for a webhook.
func (m *DefaultManager) ListDeliveries(ctx context.Context, webhookID string, limit int) ([]*models.WebhookDelivery, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	query := `
		SELECT id, webhook_id, event_type, payload, status, http_status, response_body,
		       error_message, attempt_count, next_retry_at, completed_at, tenant_id, created_at
		FROM webhook_deliveries
		WHERE webhook_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := m.store.QueryContext(ctx, query, webhookID, tenant.TenantIDFromContext(ctx), limit)
	if err != nil {
		return nil, fmt.Errorf("query deliveries: %w", err)
	}
	defer rows.Close()

	return scanDeliveries(rows)
}

// RetryDelivery re-attempts a failed delivery.
func (m *DefaultManager) RetryDelivery(ctx context.Context, deliveryID string) error {
	// Fetch the delivery record
	delivery, err := m.getDelivery(ctx, deliveryID)
	if err != nil {
		return err
	}

	// Fetch the associated webhook
	webhook, err := m.GetWebhook(ctx, delivery.WebhookID)
	if err != nil {
		return fmt.Errorf("get webhook for retry: %w", err)
	}

	// Reset delivery status and re-attempt
	updateQuery := `
		UPDATE webhook_deliveries
		SET status = 'PENDING', attempt_count = attempt_count + 1, error_message = NULL, completed_at = NULL
		WHERE id = $1
	`
	_, err = m.store.ExecContext(ctx, updateQuery, deliveryID)
	if err != nil {
		return fmt.Errorf("reset delivery for retry: %w", err)
	}

	// Re-deliver
	go m.deliverer.Deliver(ctx, webhook, delivery.EventType, delivery.Payload)

	return nil
}

// getDelivery retrieves a single delivery record by ID.
func (m *DefaultManager) getDelivery(ctx context.Context, deliveryID string) (*models.WebhookDelivery, error) {
	query := `
		SELECT id, webhook_id, event_type, payload, status, http_status, response_body,
		       error_message, attempt_count, next_retry_at, completed_at, tenant_id, created_at
		FROM webhook_deliveries
		WHERE id = $1 AND tenant_id = $2
	`

	rows, err := m.store.QueryContext(ctx, query, deliveryID, tenant.TenantIDFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("query delivery: %w", err)
	}
	defer rows.Close()

	deliveries, err := scanDeliveries(rows)
	if err != nil {
		return nil, err
	}
	if len(deliveries) == 0 {
		return nil, fmt.Errorf("delivery not found: %s", deliveryID)
	}

	return deliveries[0], nil
}

// scanWebhooks scans rows into webhook models.
func scanWebhooks(rows *sql.Rows) ([]*models.Webhook, error) {
	var webhooks []*models.Webhook
	for rows.Next() {
		wh := &models.Webhook{}
		var apiID sql.NullString
		err := rows.Scan(
			&wh.ID, &apiID, &wh.Name, &wh.CallbackURL, &wh.Secret,
			&wh.EventTypes, &wh.Active, &wh.TenantID, &wh.CreatedAt, &wh.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan webhook: %w", err)
		}
		if apiID.Valid {
			wh.APIID = &apiID.String
		}
		webhooks = append(webhooks, wh)
	}
	return webhooks, rows.Err()
}

// scanDeliveries scans rows into delivery models.
func scanDeliveries(rows *sql.Rows) ([]*models.WebhookDelivery, error) {
	var deliveries []*models.WebhookDelivery
	for rows.Next() {
		d := &models.WebhookDelivery{}
		var httpStatus sql.NullInt32
		var responseBody sql.NullString
		var errorMsg sql.NullString
		var nextRetry sql.NullTime
		var completedAt sql.NullTime

		err := rows.Scan(
			&d.ID, &d.WebhookID, &d.EventType, &d.Payload,
			&d.Status, &httpStatus, &responseBody, &errorMsg,
			&d.AttemptCount, &nextRetry, &completedAt,
			&d.TenantID, &d.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		if httpStatus.Valid {
			s := int(httpStatus.Int32)
			d.HTTPStatus = &s
		}
		if responseBody.Valid {
			d.ResponseBody = &responseBody.String
		}
		if errorMsg.Valid {
			d.ErrorMessage = &errorMsg.String
		}
		if nextRetry.Valid {
			d.NextRetryAt = &nextRetry.Time
		}
		if completedAt.Valid {
			d.CompletedAt = &completedAt.Time
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}
