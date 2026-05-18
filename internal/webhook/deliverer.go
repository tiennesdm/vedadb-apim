// Package webhook provides reliable webhook delivery with retry, exponential
// backoff, HMAC signatures, and persistent delivery tracking.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/vedadb/vapim/pkg/models"
	"github.com/vedadb/vapim/pkg/store"
)

// Deliverer sends webhook events over HTTP with configurable retry, HMAC
// signing, and per-delivery database tracking.
type Deliverer struct {
	httpClient *http.Client
	store      store.Store
	logger     *zap.Logger
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	batchSize  int
	wg         sync.WaitGroup
}

// NewDeliverer creates a Deliverer with sensible defaults for timeout, retry,
// and connection pooling.
func NewDeliverer(st store.Store, logger *zap.Logger) *Deliverer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Deliverer{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		store:      st,
		logger:     logger,
		maxRetries: 3,
		baseDelay:  1 * time.Second,
		maxDelay:   30 * time.Second,
		batchSize:  10,
	}
}

// Deliver sends a webhook event to the configured URL. A delivery record is
// created in the database and the actual HTTP POST is performed asynchronously
// so the caller is never blocked waiting for network I/O.
func (d *Deliverer) Deliver(webhook *models.Webhook, eventType string, payload map[string]interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	// Create a persistent delivery record in the database.
	delivery := &models.WebhookDeliveryDB{
		ID:           uuid.New().String(),
		WebhookID:    webhook.ID,
		EventType:    eventType,
		Payload:      string(payloadBytes),
		ResponseStatus: 0,
		ResponseBody:   "",
		AttemptCount:   0,
		Status:         "pending",
		CreatedAt:      time.Now(),
	}
	if err := d.store.CreateWebhookDelivery(delivery); err != nil {
		d.logger.Error("failed to create webhook delivery record",
			zap.String("webhook_id", webhook.ID),
			zap.String("event_type", eventType),
			zap.Error(err),
		)
	}

	d.wg.Add(1)
	go d.deliverWithRetry(webhook, delivery, payloadBytes)
	return nil
}

// deliverWithRetry performs the HTTP delivery with exponential backoff. It
// updates the delivery record in the database after each attempt and on final
// success or failure.
func (d *Deliverer) deliverWithRetry(webhook *models.Webhook, delivery *models.WebhookDeliveryDB, payload []byte) {
	defer d.wg.Done()

	var lastErr error
	for attempt := 1; attempt <= d.maxRetries; attempt++ {
		// Build request.
		req, err := http.NewRequest("POST", webhook.URL, bytes.NewReader(payload))
		if err != nil {
			lastErr = err
			d.logger.Error("failed to build webhook request",
				zap.String("url", webhook.URL),
				zap.Error(err),
			)
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "VedaDB-APIM-Webhook/1.0")
		req.Header.Set("X-Webhook-ID", webhook.ID)
		req.Header.Set("X-Delivery-ID", delivery.ID)
		req.Header.Set("X-Event-Type", delivery.EventType)
		req.Header.Set("X-Attempt", fmt.Sprintf("%d", attempt))

		// Add custom headers configured on the webhook.
		for k, v := range webhook.Headers {
			req.Header.Set(k, v)
		}

		// Sign payload with HMAC-SHA256 if a secret is configured.
		if webhook.Secret != "" {
			sig := d.generateSignature(payload, webhook.Secret)
			req.Header.Set("X-Webhook-Signature", sig)
		}

		// Execute HTTP request.
		resp, err := d.httpClient.Do(req)
		if err != nil {
			lastErr = err
			delivery.AttemptCount = attempt
			delivery.Status = "retrying"
			if updateErr := d.store.UpdateWebhookDelivery(delivery); updateErr != nil {
				d.logger.Warn("failed to update delivery record after error",
					zap.Error(updateErr),
				)
			}
			d.logger.Warn("webhook delivery failed",
				zap.String("url", webhook.URL),
				zap.String("delivery_id", delivery.ID),
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			if attempt < d.maxRetries {
				time.Sleep(d.calculateBackoff(attempt))
			}
			continue
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()

		delivery.ResponseStatus = resp.StatusCode
		delivery.ResponseBody = string(body)
		delivery.AttemptCount = attempt

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			delivery.Status = "delivered"
			if err := d.store.UpdateWebhookDelivery(delivery); err != nil {
				d.logger.Error("failed to update delivery status to delivered",
					zap.Error(err),
				)
			}
			d.logger.Info("webhook delivered",
				zap.String("url", webhook.URL),
				zap.String("delivery_id", delivery.ID),
				zap.Int("status", resp.StatusCode),
				zap.Int("attempt", attempt),
			)
			return
		}

		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		d.logger.Warn("webhook returned non-2xx",
			zap.String("url", webhook.URL),
			zap.String("delivery_id", delivery.ID),
			zap.Int("status", resp.StatusCode),
			zap.Int("attempt", attempt),
		)

		if attempt < d.maxRetries {
			time.Sleep(d.calculateBackoff(attempt))
		}
	}

	// All retries exhausted.
	delivery.Status = "failed"
	if err := d.store.UpdateWebhookDelivery(delivery); err != nil {
		d.logger.Error("failed to update delivery status to failed",
			zap.Error(err),
		)
	}
	d.logger.Error("webhook delivery failed after all retries",
		zap.String("url", webhook.URL),
		zap.String("delivery_id", delivery.ID),
		zap.Int("attempts", d.maxRetries),
		zap.Error(lastErr),
	)
}

// generateSignature creates an HMAC-SHA256 hex signature of the payload using
// the provided secret.
func (d *Deliverer) generateSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// calculateBackoff computes an exponential delay with jitter for the given
// attempt number. Jitter is 0-30% of the computed delay.
func (d *Deliverer) calculateBackoff(attempt int) time.Duration {
	delay := d.baseDelay * time.Duration(1<<(attempt-1))
	if delay > d.maxDelay {
		delay = d.maxDelay
	}
	jitter := time.Duration(float64(delay) * 0.3 * rand.Float64())
	return delay + jitter
}

// VerifySignature checks whether the provided signature matches the expected
// HMAC-SHA256 signature for the given payload and secret.
func (d *Deliverer) VerifySignature(payload []byte, secret, signature string) bool {
	expected := d.generateSignature(payload, secret)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// Wait blocks until all in-flight webhook deliveries complete. It should be
// called during graceful shutdown.
func (d *Deliverer) Wait() {
	d.wg.Wait()
}
