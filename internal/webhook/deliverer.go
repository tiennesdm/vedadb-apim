// Package webhook provides the webhook delivery engine for the VedaDB API Manager.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vedadb/vapim/internal/tenant"
	"github.com/vedadb/vapim/pkg/models"
)

// Deliverer defines the interface for delivering webhook payloads.
type Deliverer interface {
	// Deliver sends the payload to the webhook endpoint.
	Deliver(ctx context.Context, webhook *models.Webhook, eventType string, payload map[string]interface{}) error
}

// Config configures the webhook deliverer.
type Config struct {
	// Store is the database handle for persisting delivery records.
	Store SQLStore
	// HTTPClient is the HTTP client for making webhook requests. Uses default if nil.
	HTTPClient *http.Client
	// MaxRetries is the maximum number of delivery attempts. Defaults to 3.
	MaxRetries int
	// InitialBackoff is the initial retry backoff duration. Defaults to 5s.
	InitialBackoff time.Duration
	// MaxBackoff is the maximum retry backoff duration. Defaults to 5m.
	MaxBackoff time.Duration
	// BackoffMultiplier is the exponential backoff multiplier. Defaults to 2.
	BackoffMultiplier float64
	// RequestTimeout is the HTTP request timeout. Defaults to 30s.
	RequestTimeout time.Duration
	// BatchSize is the number of events to batch before delivery. Defaults to 1 (no batching).
	BatchSize int
	// BatchInterval is the maximum time to wait before flushing a batch. Defaults to 5s.
	BatchInterval time.Duration
	// EnableDLQ if true, sends permanently failed deliveries to a dead letter queue.
	EnableDLQ bool
	// DLQTable is the database table for dead letter queue. Defaults to "webhook_dlq".
	DLQTable string
}

// DefaultConfig returns a default deliverer configuration.
func DefaultConfig(store SQLStore) Config {
	return Config{
		Store:             store,
		HTTPClient:        &http.Client{Timeout: 30 * time.Second},
		MaxRetries:        3,
		InitialBackoff:    5 * time.Second,
		MaxBackoff:        5 * time.Minute,
		BackoffMultiplier: 2.0,
		RequestTimeout:    30 * time.Second,
		BatchSize:         1,
		BatchInterval:     5 * time.Second,
		EnableDLQ:         true,
		DLQTable:          "webhook_dlq",
	}
}

// DefaultDeliverer is the production webhook delivery engine.
type DefaultDeliverer struct {
	store             SQLStore
	httpClient        *http.Client
	maxRetries        int
	initialBackoff    time.Duration
	maxBackoff        time.Duration
	backoffMultiplier float64
	requestTimeout    time.Duration
	enableDLQ         bool
	dlqTable          string

	// Batching
	batchSize     int
	batchInterval time.Duration
	batchMu       sync.Mutex
	batchBuffers  map[string][]*batchEntry
	batchTimers   map[string]*time.Timer
}

type batchEntry struct {
	webhook   *models.Webhook
	eventType string
	payload   map[string]interface{}
	delivery  *models.WebhookDelivery
}

// NewDeliverer creates a new webhook deliverer.
func NewDeliverer(cfg Config) *DefaultDeliverer {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	initialBackoff := cfg.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = 5 * time.Second
	}

	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 5 * time.Minute
	}

	backoffMultiplier := cfg.BackoffMultiplier
	if backoffMultiplier <= 0 {
		backoffMultiplier = 2.0
	}

	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 1
	}

	batchInterval := cfg.BatchInterval
	if batchInterval <= 0 {
		batchInterval = 5 * time.Second
	}

	dlqTable := cfg.DLQTable
	if dlqTable == "" {
		dlqTable = "webhook_dlq"
	}

	d := &DefaultDeliverer{
		store:             cfg.Store,
		httpClient:        httpClient,
		maxRetries:        maxRetries,
		initialBackoff:    initialBackoff,
		maxBackoff:        maxBackoff,
		backoffMultiplier: backoffMultiplier,
		requestTimeout:    requestTimeout,
		enableDLQ:         cfg.EnableDLQ,
		dlqTable:          dlqTable,
		batchSize:         batchSize,
		batchInterval:     batchInterval,
		batchBuffers:      make(map[string][]*batchEntry),
		batchTimers:       make(map[string]*time.Timer),
	}

	return d
}

// Deliver sends the event payload to the webhook endpoint with retry logic.
func (d *DefaultDeliverer) Deliver(ctx context.Context, webhook *models.Webhook, eventType string, payload map[string]interface{}) error {
	if !webhook.Active {
		return fmt.Errorf("webhook %s is inactive", webhook.ID)
	}

	// Create delivery record
	delivery := &models.WebhookDelivery{
		ID:           uuid.New().String(),
		WebhookID:    webhook.ID,
		EventType:    eventType,
		Payload:      payload,
		Status:       "PENDING",
		AttemptCount: 0,
		TenantID:     webhook.TenantID,
		CreatedAt:    time.Now().UTC(),
	}

	// Persist delivery record
	if err := d.insertDelivery(ctx, delivery); err != nil {
		return fmt.Errorf("insert delivery record: %w", err)
	}

	// If batching is enabled and batch size > 1
	if d.batchSize > 1 {
		return d.enqueueBatch(webhook, eventType, payload, delivery)
	}

	// Deliver immediately (no batching)
	return d.attemptDelivery(ctx, webhook, eventType, payload, delivery)
}

// enqueueBatch adds the event to the batch buffer and flushes if needed.
func (d *DefaultDeliverer) enqueueBatch(webhook *models.Webhook, eventType string, payload map[string]interface{}, delivery *models.WebhookDelivery) error {
	d.batchMu.Lock()
	defer d.batchMu.Unlock()

	batchKey := webhook.ID
	entry := &batchEntry{
		webhook:   webhook,
		eventType: eventType,
		payload:   payload,
		delivery:  delivery,
	}

	d.batchBuffers[batchKey] = append(d.batchBuffers[batchKey], entry)

	// Flush immediately if batch is full
	if len(d.batchBuffers[batchKey]) >= d.batchSize {
		return d.flushBatchLocked(batchKey)
	}

	// Set up a flush timer if not already set
	if d.batchTimers[batchKey] == nil {
		d.batchTimers[batchKey] = time.AfterFunc(d.batchInterval, func() {
			d.batchMu.Lock()
			defer d.batchMu.Unlock()
			_ = d.flushBatchLocked(batchKey)
		})
	}

	return nil
}

// flushBatchLocked flushes all batched events for a webhook. Must be called with batchMu held.
func (d *DefaultDeliverer) flushBatchLocked(batchKey string) error {
	entries := d.batchBuffers[batchKey]
	if len(entries) == 0 {
		return nil
	}

	// Clear the buffer and timer
	d.batchBuffers[batchKey] = nil
	if t, ok := d.batchTimers[batchKey]; ok {
		t.Stop()
		delete(d.batchTimers, batchKey)
	}

	// Build batched payload
	batchedPayload := map[string]interface{}{
		"events": make([]map[string]interface{}, 0, len(entries)),
	}

	for _, e := range entries {
		eventData := map[string]interface{}{
			"eventType":   e.eventType,
			"payload":     e.payload,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
			"deliveryId":  e.delivery.ID,
		}
		batchedPayload["events"] = append(batchedPayload["events"].([]map[string]interface{}), eventData)
	}

	// Use the first webhook as the target for the batch
	if len(entries) > 0 {
		ctx := context.Background()
		go d.attemptDelivery(ctx, entries[0].webhook, "BATCHED", batchedPayload, entries[0].delivery)
	}

	return nil
}

// attemptDelivery performs the actual HTTP delivery with retries.
func (d *DefaultDeliverer) attemptDelivery(ctx context.Context, webhook *models.Webhook, eventType string, payload map[string]interface{}, delivery *models.WebhookDelivery) error {
	backoff := d.initialBackoff

	for attempt := 1; attempt <= d.maxRetries; attempt++ {
		delivery.AttemptCount = attempt

		err := d.doHTTPRequest(ctx, webhook, eventType, payload, delivery)
		if err == nil {
			// Success - mark delivery as completed
			now := time.Now().UTC()
			delivery.Status = "DELIVERED"
			delivery.CompletedAt = &now
			_ = d.updateDelivery(ctx, delivery)
			return nil
		}

		// Update delivery record with error
		errMsg := err.Error()
		delivery.ErrorMessage = &errMsg
		delivery.Status = fmt.Sprintf("FAILED_ATTEMPT_%d", attempt)
		_ = d.updateDelivery(ctx, delivery)

		// Don't retry on the last attempt
		if attempt < d.maxRetries {
			time.Sleep(backoff)
			backoff = calculateBackoff(backoff, d.maxBackoff, d.backoffMultiplier)
		}
	}

	// All retries exhausted - mark as permanently failed
	delivery.Status = "FAILED"
	now := time.Now().UTC()
	delivery.CompletedAt = &now
	_ = d.updateDelivery(ctx, delivery)

	// Send to dead letter queue if enabled
	if d.enableDLQ {
		_ = d.sendToDLQ(ctx, delivery)
	}

	return fmt.Errorf("webhook delivery failed after %d attempts for webhook %s", d.maxRetries, webhook.ID)
}

// doHTTPRequest performs a single HTTP POST to the webhook endpoint.
func (d *DefaultDeliverer) doHTTPRequest(ctx context.Context, webhook *models.Webhook, eventType string, payload map[string]interface{}, delivery *models.WebhookDelivery) error {
	// Build request body
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, d.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, webhook.CallbackURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "VedaDB-Webhook/2.0")
	req.Header.Set("X-Webhook-ID", webhook.ID)
	req.Header.Set("X-Event-Type", eventType)
	req.Header.Set("X-Delivery-ID", delivery.ID)
	req.Header.Set("X-Webhook-Timestamp", time.Now().UTC().Format(time.RFC3339))

	// Add HMAC-SHA256 signature
	if webhook.Secret != "" {
		signature := generateHMACSignature(bodyBytes, webhook.Secret)
		req.Header.Set("X-Webhook-Signature", "sha256="+signature)
	}

	// Execute request
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	respBodyStr := string(respBody)
	httpStatus := resp.StatusCode
	delivery.HTTPStatus = &httpStatus
	delivery.ResponseBody = &respBodyStr

	// Check for success status codes (2xx)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, respBodyStr)
	}

	return nil
}

// insertDelivery persists a new delivery record.
func (d *DefaultDeliverer) insertDelivery(ctx context.Context, delivery *models.WebhookDelivery) error {
	query := `
		INSERT INTO webhook_deliveries
		(id, webhook_id, event_type, payload, status, tenant_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	payloadJSON, _ := json.Marshal(delivery.Payload)
	_, err := d.store.ExecContext(ctx, query,
		delivery.ID, delivery.WebhookID, delivery.EventType,
		payloadJSON, delivery.Status, delivery.TenantID, delivery.CreatedAt,
	)
	return err
}

// updateDelivery updates the delivery record in the database.
func (d *DefaultDeliverer) updateDelivery(ctx context.Context, delivery *models.WebhookDelivery) error {
	query := `
		UPDATE webhook_deliveries SET
			status = $1,
			http_status = $2,
			response_body = $3,
			error_message = $4,
			attempt_count = $5,
			next_retry_at = $6,
			completed_at = $7
		WHERE id = $8
	`

	var nextRetry interface{}
	if delivery.NextRetryAt != nil {
		nextRetry = *delivery.NextRetryAt
	}

	var completed interface{}
	if delivery.CompletedAt != nil {
		completed = *delivery.CompletedAt
	}

	_, err := d.store.ExecContext(ctx, query,
		delivery.Status, delivery.HTTPStatus, delivery.ResponseBody,
		delivery.ErrorMessage, delivery.AttemptCount,
		nextRetry, completed, delivery.ID,
	)
	return err
}

// sendToDLQ writes a failed delivery to the dead letter queue.
func (d *DefaultDeliverer) sendToDLQ(ctx context.Context, delivery *models.WebhookDelivery) error {
	query := fmt.Sprintf(`
		INSERT INTO %s (id, webhook_id, event_type, payload, status, error_message, tenant_id, failed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, d.dlqTable)

	payloadJSON, _ := json.Marshal(delivery.Payload)
	_, err := d.store.ExecContext(ctx, query,
		delivery.ID, delivery.WebhookID, delivery.EventType,
		payloadJSON, delivery.Status, delivery.ErrorMessage,
		delivery.TenantID, time.Now().UTC(),
	)
	return err
}

// generateHMACSignature creates an HMAC-SHA256 signature of the payload using the secret.
func generateHMACSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// calculateBackoff computes the next backoff duration using exponential backoff.
func calculateBackoff(current, maxBackoff time.Duration, multiplier float64) time.Duration {
	next := time.Duration(float64(current) * multiplier)
	if next > maxBackoff {
		return maxBackoff
	}
	// Add jitter (±25%)
	jitter := time.Duration(float64(next) * 0.25)
	// Deterministic for simplicity - in production use crypto/rand
	return next + (jitter / 2)
}

// VerifySignature verifies the HMAC-SHA256 signature of a payload.
func VerifySignature(payload []byte, secret, signature string) bool {
	expected := generateHMACSignature(payload, secret)
	return hmac.Equal([]byte(expected), []byte(signature))
}
