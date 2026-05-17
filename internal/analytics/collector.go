// Package analytics implements the analytics collection and aggregation
// subsystem for VedaDB API Manager.
package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// AnalyticsStore defines the persistence interface for analytics events.
type AnalyticsStore interface {
	// Event storage
	PublishEvent(ctx context.Context, event *models.AnalyticsEvent) error
	PublishEvents(ctx context.Context, events []*models.AnalyticsEvent) error

	// Throttle events
	PublishThrottleEvent(ctx context.Context, event *models.ThrottleEvent) error

	// Counter operations for aggregation
	IncrementAPIMetric(ctx context.Context, metric *models.APIMetric) error
	IncrementAppMetric(ctx context.Context, metric *models.AppMetric) error
	IncrementUserMetric(ctx context.Context, metric *models.UserMetric) error
}

// Collector collects API invocation analytics events and publishes them
// to VedaDB with batching and async processing.
type Collector struct {
	store       AnalyticsStore
	logger      *slog.Logger
	eventCh     chan *models.AnalyticsEvent
	throttleCh  chan *models.ThrottleEvent
	batchSize   int
	flushInterval time.Duration
	wg          sync.WaitGroup
	stopCh      chan struct{}
	stopped     bool
	mu          sync.Mutex

	// Metrics
	eventsCollected   int64
	eventsPublished   int64
	eventsDropped     int64
	batchesPublished  int64
}

// CollectorConfig configures the analytics collector.
type CollectorConfig struct {
	BufferSize      int
	BatchSize       int
	FlushInterval   time.Duration
	MaxRetries      int
	RetryBackoff    time.Duration
	DropOnFull      bool // if true, drop events when buffer is full; if false, block
}

// DefaultCollectorConfig returns sensible defaults.
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		BufferSize:    10000,
		BatchSize:     100,
		FlushInterval: 5 * time.Second,
		MaxRetries:    3,
		RetryBackoff:  1 * time.Second,
		DropOnFull:    true,
	}
}

// NewCollector creates a new analytics collector.
func NewCollector(store AnalyticsStore, cfg CollectorConfig, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	c := &Collector{
		store:         store,
		logger:        logger.With("component", "analytics-collector"),
		eventCh:       make(chan *models.AnalyticsEvent, cfg.BufferSize),
		throttleCh:    make(chan *models.ThrottleEvent, cfg.BufferSize/10),
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval,
		stopCh:        make(chan struct{}),
	}

	// Start background processors
	c.wg.Add(2)
	go c.eventProcessor()
	go c.throttleProcessor()

	return c
}

// --- Event Collection ---

// CollectRequestEvent collects an API request event.
func (c *Collector) CollectRequestEvent(ctx context.Context, event *models.AnalyticsEvent) error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return fmt.Errorf("collector is stopped")
	}
	c.mu.Unlock()

	event.EventType = models.EventTypeRequest
	event.Timestamp = time.Now().UTC()

	select {
	case c.eventCh <- event:
		c.eventsCollected++
		return nil
	default:
		c.eventsDropped++
		c.logger.Warn("analytics event buffer full, dropping event",
			"api_id", event.APIID,
			"app_id", event.AppID,
		)
		return fmt.Errorf("analytics buffer full, event dropped")
	}
}

// CollectResponseEvent collects an API response event with latency.
func (c *Collector) CollectResponseEvent(ctx context.Context, event *models.AnalyticsEvent) error {
	event.EventType = models.EventTypeResponse
	event.Timestamp = time.Now().UTC()
	return c.CollectRequestEvent(ctx, event)
}

// CollectFaultEvent collects an API fault/error event.
func (c *Collector) CollectFaultEvent(ctx context.Context, event *models.AnalyticsEvent) error {
	event.EventType = models.EventTypeFault
	event.Timestamp = time.Now().UTC()
	return c.CollectRequestEvent(ctx, event)
}

// CollectThrottleEvent collects a throttling event.
func (c *Collector) CollectThrottleEvent(ctx context.Context, event *models.ThrottleEvent) error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return fmt.Errorf("collector is stopped")
	}
	c.mu.Unlock()

	event.Timestamp = time.Now().UTC()

	select {
	case c.throttleCh <- event:
		return nil
	default:
		c.logger.Warn("throttle event buffer full, dropping event",
			"api_id", event.APIID,
			"level", event.Level,
		)
		return fmt.Errorf("throttle buffer full, event dropped")
	}
}

// --- Background Processors ---

// eventProcessor batches and publishes analytics events.
func (c *Collector) eventProcessor() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	batch := make([]*models.AnalyticsEvent, 0, c.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := c.publishBatch(ctx, batch); err != nil {
			c.logger.Error("failed to publish analytics batch", "count", len(batch), "error", err)
		}
		cancel()

		batch = batch[:0]
	}

	for {
		select {
		case event := <-c.eventCh:
			batch = append(batch, event)
			if len(batch) >= c.batchSize {
				flush()
			}

		case <-ticker.C:
			flush()

		case <-c.stopCh:
			// Drain remaining events
			drainLoop := true
			for drainLoop {
				select {
				case event := <-c.eventCh:
					batch = append(batch, event)
					if len(batch) >= c.batchSize {
						flush()
					}
				default:
					drainLoop = false
				}
			}
			flush()
			return
		}
	}
}

// throttleProcessor publishes throttle events.
func (c *Collector) throttleProcessor() {
	defer c.wg.Done()

	for {
		select {
		case event := <-c.throttleCh:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := c.store.PublishThrottleEvent(ctx, event); err != nil {
				c.logger.Error("failed to publish throttle event",
					"api_id", event.APIID,
					"level", event.Level,
					"error", err,
				)
			}
			cancel()

		case <-c.stopCh:
			// Drain remaining throttle events
			for {
				select {
				case event := <-c.throttleCh:
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = c.store.PublishThrottleEvent(ctx, event)
					cancel()
				default:
					return
				}
			}
		}
	}
}

// --- Publishing ---

func (c *Collector) publishBatch(ctx context.Context, events []*models.AnalyticsEvent) error {
	if len(events) == 0 {
		return nil
	}

	// Publish to VedaDB
	if err := c.store.PublishEvents(ctx, events); err != nil {
		return fmt.Errorf("failed to publish %d events: %w", len(events), err)
	}

	c.eventsPublished += int64(len(events))
	c.batchesPublished++

	c.logger.Debug("published analytics batch", "count", len(events))
	return nil
}

// --- Lifecycle ---

// Shutdown gracefully shuts down the collector, flushing all pending events.
func (c *Collector) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	c.mu.Unlock()

	c.logger.Info("analytics collector shutting down")
	close(c.stopCh)

	// Wait for processors to finish with timeout
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Info("analytics collector stopped gracefully",
			"events_collected", c.eventsCollected,
			"events_published", c.eventsPublished,
			"events_dropped", c.eventsDropped,
			"batches", c.batchesPublished,
		)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("analytics collector shutdown timeout")
	}
}

// --- Statistics ---

// Stats returns current collector statistics.
func (c *Collector) Stats() models.CollectorStats {
	return models.CollectorStats{
		EventsCollected:   c.eventsCollected,
		EventsPublished:   c.eventsPublished,
		EventsDropped:     c.eventsDropped,
		BatchesPublished:  c.batchesPublished,
		BufferSize:        int64(cap(c.eventCh)),
		BufferUsed:        int64(len(c.eventCh)),
	}
}

// --- Convenience Helpers for Gateway Integration ---

// RecordInvocation is a convenience method that records a complete API invocation
// from request to response in a single call.
func (c *Collector) RecordInvocation(ctx context.Context, req *models.InvocationRecord) error {
	now := time.Now().UTC()

	// Determine event type based on response status
	eventType := models.EventTypeResponse
	if req.StatusCode >= 500 {
		eventType = models.EventTypeFault
	} else if req.StatusCode >= 400 {
		eventType = models.EventTypeFault
	}

	event := &models.AnalyticsEvent{
		EventType:        eventType,
		APIID:            req.APIID,
		APIName:          req.APIName,
		APIVersion:       req.APIVersion,
		AppID:            req.AppID,
		AppName:          req.AppName,
		UserID:           req.UserID,
		Tenant:           req.Tenant,
		Method:           req.Method,
		Path:             req.Path,
		StatusCode:       req.StatusCode,
		ResponseSize:     req.ResponseSize,
		RequestSize:      req.RequestSize,
		LatencyMs:        req.LatencyMs,
		BackendLatencyMs: req.BackendLatencyMs,
		ClientIP:         req.ClientIP,
		UserAgent:        req.UserAgent,
		Timestamp:        now,
		GatewayNode:      req.GatewayNode,
		CorrelationID:    req.CorrelationID,
	}

	if eventType == models.EventTypeFault {
		event.ErrorCode = req.ErrorCode
		event.ErrorMessage = req.ErrorMessage
	}

	return c.CollectRequestEvent(ctx, event)
}
