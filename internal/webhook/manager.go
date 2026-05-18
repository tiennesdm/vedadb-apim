// Package webhook provides webhook registration, event delivery, and
// subscription management for the VedaDB API Manager.
package webhook

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/vedadb/vapim/pkg/models"
	"github.com/vedadb/vapim/pkg/store"
)

// Manager handles the lifecycle of webhooks: registration, unregistration,
// and event delivery. It uses a Deliverer for reliable HTTP transmission and
// a Store for persistence.
type Manager struct {
	store     store.Store
	deliverer *Deliverer
	logger    *zap.Logger
}

// NewManager creates a new Manager backed by the given store and logger. An
// internal Deliverer is created automatically.
func NewManager(st store.Store, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		store:     st,
		deliverer: NewDeliverer(st, logger),
		logger:    logger,
	}
}

// Register creates a new webhook subscription for the given API. The webhook
// will receive HTTP POST requests for each event type listed in Events.
func (m *Manager) Register(apiID, name, url string, events []string, secret string, headers map[string]string) (*models.Webhook, error) {
	if apiID == "" {
		return nil, fmt.Errorf("apiID is required")
	}
	if url == "" {
		return nil, fmt.Errorf("webhook URL is required")
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("at least one event type is required")
	}

	wh := &models.WebhookDB{
		ID:        generateID(),
		APIID:     apiID,
		URL:       url,
		Events:    strings.Join(events, ","),
		Secret:    secret,
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := m.store.CreateWebhook(wh); err != nil {
		m.logger.Error("failed to persist webhook",
			zap.String("api_id", apiID),
			zap.String("url", url),
			zap.Error(err),
		)
		return nil, fmt.Errorf("create webhook in store: %w", err)
	}

	result := dbToWebhook(wh)
	result.Name = name
	result.Headers = headers
	m.logger.Info("webhook registered",
		zap.String("webhook_id", result.ID),
		zap.String("api_id", apiID),
		zap.String("url", url),
		zap.Strings("events", events),
	)
	return result, nil
}

// Unregister deletes a webhook by its ID. Returns an error if the webhook
// does not exist or the store operation fails.
func (m *Manager) Unregister(webhookID string) error {
	if webhookID == "" {
		return fmt.Errorf("webhookID is required")
	}
	if err := m.store.DeleteWebhook(webhookID); err != nil {
		m.logger.Error("failed to delete webhook",
			zap.String("webhook_id", webhookID),
			zap.Error(err),
		)
		return fmt.Errorf("delete webhook %s: %w", webhookID, err)
	}
	m.logger.Info("webhook unregistered", zap.String("webhook_id", webhookID))
	return nil
}

// List returns all webhooks for a given tenant.
func (m *Manager) List(tenantID string) ([]*models.Webhook, error) {
	dbHooks, err := m.store.ListWebhooks(tenantID)
	if err != nil {
		m.logger.Error("failed to list webhooks",
			zap.String("tenant_id", tenantID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	result := make([]*models.Webhook, 0, len(dbHooks))
	for _, h := range dbHooks {
		result = append(result, dbToWebhook(h))
	}
	return result, nil
}

// ListByAPI returns all webhooks registered for a specific API.
func (m *Manager) ListByAPI(apiID string) ([]*models.Webhook, error) {
	dbHooks, err := m.store.GetWebhooksByAPI(apiID)
	if err != nil {
		m.logger.Error("failed to list webhooks by API",
			zap.String("api_id", apiID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("list webhooks by API %s: %w", apiID, err)
	}
	result := make([]*models.Webhook, 0, len(dbHooks))
	for _, h := range dbHooks {
		result = append(result, dbToWebhook(h))
	}
	return result, nil
}

// Deliver looks up all active webhooks registered for the given API and
// delivers the event payload to each one asynchronously via the Deliverer.
// Returns the number of webhooks that the event was dispatched to.
func (m *Manager) Deliver(apiID, eventType string, payload map[string]interface{}) (int, error) {
	if apiID == "" {
		return 0, fmt.Errorf("apiID is required")
	}

	hooks, err := m.store.GetWebhooksByAPI(apiID)
	if err != nil {
		m.logger.Error("failed to fetch webhooks for delivery",
			zap.String("api_id", apiID),
			zap.String("event_type", eventType),
			zap.Error(err),
		)
		return 0, fmt.Errorf("fetch webhooks for API %s: %w", apiID, err)
	}

	dispatched := 0
	for _, h := range hooks {
		// Skip inactive webhooks.
		if h.Status != "active" {
			continue
		}

		wh := dbToWebhook(h)

		// Filter by event type if the webhook specifies events.
		if !shouldDeliver(wh, eventType) {
			continue
		}

		if err := m.deliverer.Deliver(wh, eventType, payload); err != nil {
			m.logger.Error("failed to dispatch webhook event",
				zap.String("webhook_id", wh.ID),
				zap.String("event_type", eventType),
				zap.Error(err),
			)
			continue
		}
		dispatched++
	}

	m.logger.Debug("webhook delivery completed",
		zap.String("api_id", apiID),
		zap.String("event_type", eventType),
		zap.Int("dispatched", dispatched),
		zap.Int("total_hooks", len(hooks)),
	)
	return dispatched, nil
}

// DeliverWithContext delivers a webhook event using context for cancellation.
// It behaves identically to Deliver but accepts a context for potential
// future use (e.g. timeouts or tracing).
func (m *Manager) DeliverWithContext(_ context.Context, apiID, eventType string, payload map[string]interface{}) (int, error) {
	return m.Deliver(apiID, eventType, payload)
}

// SetDeliverer replaces the default Deliverer. Useful for tests.
func (m *Manager) SetDeliverer(d *Deliverer) {
	m.deliverer = d
}

// Shutdown waits for all in-flight deliveries to complete.
func (m *Manager) Shutdown() {
	if m.deliverer != nil {
		m.deliverer.Wait()
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// shouldDeliver returns true if the webhook should receive the given event
// type. An empty event list on the webhook means it subscribes to all events.
func shouldDeliver(wh *models.Webhook, eventType string) bool {
	if len(wh.Events) == 0 {
		return true
	}
	for _, e := range wh.Events {
		if e == eventType || e == "*" {
			return true
		}
	}
	return false
}

// dbToWebhook converts a database webhook model to the business model.
func dbToWebhook(db *models.WebhookDB) *models.Webhook {
	events := []string{}
	if db.Events != "" {
		events = strings.Split(db.Events, ",")
	}
	active := db.Status == "active"
	return &models.Webhook{
		ID:        db.ID,
		URL:       db.URL,
		Events:    events,
		Active:    active,
		Secret:    db.Secret,
		CreatedAt: db.CreatedAt,
		UpdatedAt: db.UpdatedAt,
	}
}

// generateID creates a short unique identifier for webhook records.
func generateID() string {
	return fmt.Sprintf("wh_%d", time.Now().UnixNano())
}
