package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// Aggregator periodically aggregates analytics data into summary reports.
// It computes request counts, latency percentiles, error rates, and rankings.
type Aggregator struct {
	store       AnalyticsStore
	logger      *slog.Logger
	interval    time.Duration
	mu          sync.RWMutex
	stopCh      chan struct{}
	wg          sync.WaitGroup
	running     bool

	// Aggregation state
	windowData  map[string]*windowAggregation // key -> window aggregation
	reports     map[string]*models.AggregationReport

	// Callbacks for report publishing
	reportCallbacks []func(*models.AggregationReport)
}

// windowAggregation holds aggregated data for a single time window.
type windowAggregation struct {
	WindowStart time.Time
	WindowEnd   time.Time

	// API metrics
	apiCounts    map[string]int64   // apiID -> count
	apiLatencies map[string][]int64 // apiID -> latencies
	apiErrors    map[string]int64   // apiID -> error count

	// App metrics
	appCounts map[string]int64 // appID -> count
	appErrors map[string]int64 // appID -> error count

	// User metrics
	userCounts map[string]int64 // userID -> count
	userErrors map[string]int64 // userID -> error count

	// Overall
	totalRequests int64
	totalErrors   int64
	totalLatency  int64

	mu sync.Mutex
}

// AggregatorConfig configures the data aggregator.
type AggregatorConfig struct {
	AggregationInterval time.Duration // how often to flush windows and generate reports
	WindowSize          time.Duration // time window for aggregation
	MaxLatenciesPerKey  int           // max latencies to keep per key for percentile calculation
	TopN                int           // number of items in top-N reports
}

// DefaultAggregatorConfig returns sensible defaults.
func DefaultAggregatorConfig() AggregatorConfig {
	return AggregatorConfig{
		AggregationInterval: 1 * time.Minute,
		WindowSize:          1 * time.Minute,
		MaxLatenciesPerKey:  10000,
		TopN:                10,
	}
}

// NewAggregator creates a new data aggregator.
func NewAggregator(store AnalyticsStore, cfg AggregatorConfig, logger *slog.Logger) *Aggregator {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &Aggregator{
		store:           store,
		logger:          logger.With("component", "analytics-aggregator"),
		interval:        cfg.AggregationInterval,
		windowData:      make(map[string]*windowAggregation),
		reports:         make(map[string]*models.AggregationReport),
		reportCallbacks: make([]func(*models.AggregationReport), 0),
		stopCh:          make(chan struct{}),
	}
}

// OnReport registers a callback that is invoked when a new aggregation report
// is generated. Used to publish reports to external systems.
func (a *Aggregator) OnReport(cb func(*models.AggregationReport)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reportCallbacks = append(a.reportCallbacks, cb)
}

// --- Aggregation ---

// RecordEvent records a single analytics event for aggregation.
func (a *Aggregator) RecordEvent(event *models.AnalyticsEvent) {
	windowKey := a.currentWindowKey()

	a.mu.RLock()
	window, ok := a.windowData[windowKey]
	a.mu.RUnlock()

	if !ok {
		a.mu.Lock()
		window, ok = a.windowData[windowKey]
		if !ok {
			window = a.newWindowAggregation()
			a.windowData[windowKey] = window
		}
		a.mu.Unlock()
	}

	window.mu.Lock()
	defer window.mu.Unlock()

	// API metrics
	window.apiCounts[event.APIID]++
	if event.LatencyMs > 0 {
		latencies := window.apiLatencies[event.APIID]
		if len(latencies) < 10000 { // cap per-key latencies
			window.apiLatencies[event.APIID] = append(latencies, event.LatencyMs)
		}
	}
	if event.StatusCode >= 400 {
		window.apiErrors[event.APIID]++
	}

	// App metrics
	if event.AppID != "" {
		window.appCounts[event.AppID]++
		if event.StatusCode >= 400 {
			window.appErrors[event.AppID]++
		}
	}

	// User metrics
	if event.UserID != "" {
		window.userCounts[event.UserID]++
		if event.StatusCode >= 400 {
			window.userErrors[event.UserID]++
		}
	}

	window.totalRequests++
	if event.StatusCode >= 400 {
		window.totalErrors++
	}
	window.totalLatency += event.LatencyMs
}

// FlushWindow aggregates the current window and generates reports.
func (a *Aggregator) FlushWindow(ctx context.Context) (*models.AggregationReport, error) {
	windowKey := a.currentWindowKey()

	a.mu.Lock()
	window, ok := a.windowData[windowKey]
	if !ok {
		a.mu.Unlock()
		return nil, fmt.Errorf("no data for current window")
	}

	// Remove the window from active data
	delete(a.windowData, windowKey)
	a.mu.Unlock()

	window.mu.Lock()
	defer window.mu.Unlock()

	report := a.buildReport(window, windowKey)

	// Store the report
	a.mu.Lock()
	a.reports[windowKey] = report
	a.mu.Unlock()

	// Notify callbacks
	a.mu.RLock()
	callbacks := make([]func(*models.AggregationReport), len(a.reportCallbacks))
	copy(callbacks, a.reportCallbacks)
	a.mu.RUnlock()

	for _, cb := range callbacks {
		go cb(report)
	}

	return report, nil
}

func (a *Aggregator) buildReport(window *windowAggregation, windowKey string) *models.AggregationReport {
	now := time.Now().UTC()

	report := &models.AggregationReport{
		WindowKey:        windowKey,
		GeneratedAt:      now,
		TotalRequests:    window.totalRequests,
		TotalErrors:      window.totalErrors,
		TotalLatencyMs:   window.totalLatency,
		AvgLatencyMs:     0,
		ErrorRate:        0,
	}

	if window.totalRequests > 0 {
		report.AvgLatencyMs = float64(window.totalLatency) / float64(window.totalRequests)
		report.ErrorRate = float64(window.totalErrors) / float64(window.totalRequests) * 100
	}

	// Top APIs by request count
	report.TopAPIs = a.topNAPIs(window, 10)

	// Top Applications by request count
	report.TopApps = a.topNApps(window, 10)

	// Top Users by request count
	report.TopUsers = a.topNUsers(window, 10)

	// API latency percentiles
	report.APILatencyPercentiles = a.calculateLatencyPercentiles(window, 10)

	// API error rates
	report.APIErrorRates = a.calculateErrorRates(window, 10)

	return report
}

// --- Report Calculations ---

func (a *Aggregator) topNAPIs(window *windowAggregation, n int) []models.TopAPIEntry {
	type apiEntry struct {
		APIID string
		Count int64
	}

	entries := make([]apiEntry, 0, len(window.apiCounts))
	for apiID, count := range window.apiCounts {
		entries = append(entries, apiEntry{APIID: apiID, Count: count})
	}

	// Sort by count descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Count > entries[j].Count
	})

	// Take top N
	if len(entries) > n {
		entries = entries[:n]
	}

	result := make([]models.TopAPIEntry, len(entries))
	for i, e := range entries {
		errors := window.apiErrors[e.APIID]
		errorRate := 0.0
		if e.Count > 0 {
			errorRate = float64(errors) / float64(e.Count) * 100
		}

		result[i] = models.TopAPIEntry{
			APIID:     e.APIID,
			Count:     e.Count,
			Errors:    errors,
			ErrorRate: errorRate,
		}
	}

	return result
}

func (a *Aggregator) topNApps(window *windowAggregation, n int) []models.TopAppEntry {
	type appEntry struct {
		AppID string
		Count int64
	}

	entries := make([]appEntry, 0, len(window.appCounts))
	for appID, count := range window.appCounts {
		entries = append(entries, appEntry{AppID: appID, Count: count})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Count > entries[j].Count
	})

	if len(entries) > n {
		entries = entries[:n]
	}

	result := make([]models.TopAppEntry, len(entries))
	for i, e := range entries {
		errors := window.appErrors[e.AppID]
		errorRate := 0.0
		if e.Count > 0 {
			errorRate = float64(errors) / float64(e.Count) * 100
		}

		result[i] = models.TopAppEntry{
			AppID:     e.AppID,
			Count:     e.Count,
			Errors:    errors,
			ErrorRate: errorRate,
		}
	}

	return result
}

func (a *Aggregator) topNUsers(window *windowAggregation, n int) []models.TopUserEntry {
	type userEntry struct {
		UserID string
		Count  int64
	}

	entries := make([]userEntry, 0, len(window.userCounts))
	for userID, count := range window.userCounts {
		entries = append(entries, userEntry{UserID: userID, Count: count})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Count > entries[j].Count
	})

	if len(entries) > n {
		entries = entries[:n]
	}

	result := make([]models.TopUserEntry, len(entries))
	for i, e := range entries {
		errors := window.userErrors[e.UserID]
		errorRate := 0.0
		if e.Count > 0 {
			errorRate = float64(errors) / float64(e.Count) * 100
		}

		result[i] = models.TopUserEntry{
			UserID:    e.UserID,
			Count:     e.Count,
			Errors:    errors,
			ErrorRate: errorRate,
		}
	}

	return result
}

func (a *Aggregator) calculateLatencyPercentiles(window *windowAggregation, n int) []models.APILatencyPercentile {
	result := make([]models.APILatencyPercentile, 0, len(window.apiLatencies))

	for apiID, latencies := range window.apiLatencies {
		if len(latencies) == 0 {
			continue
		}

		p50 := percentileInt64(latencies, 50)
		p95 := percentileInt64(latencies, 95)
		p99 := percentileInt64(latencies, 99)

		var avg float64
		var sum int64
		for _, l := range latencies {
			sum += l
		}
		avg = float64(sum) / float64(len(latencies))

		result = append(result, models.APILatencyPercentile{
			APIID:      apiID,
			P50:        p50,
			P95:        p95,
			P99:        p99,
			Avg:        avg,
			Count:      int64(len(latencies)),
		})
	}

	// Sort by P95 descending (most latent first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].P95 > result[j].P95
	})

	if len(result) > n {
		result = result[:n]
	}

	return result
}

func (a *Aggregator) calculateErrorRates(window *windowAggregation, n int) []models.APIErrorRate {
	result := make([]models.APIErrorRate, 0, len(window.apiCounts))

	for apiID, count := range window.apiCounts {
		errors := window.apiErrors[apiID]
		rate := 0.0
		if count > 0 {
			rate = float64(errors) / float64(count) * 100
		}

		result = append(result, models.APIErrorRate{
			APIID:     apiID,
			Count:     count,
			Errors:    errors,
			ErrorRate: rate,
		})
	}

	// Sort by error rate descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].ErrorRate > result[j].ErrorRate
	})

	if len(result) > n {
		result = result[:n]
	}

	return result
}

// --- Background Processing ---

// Run starts the aggregation loop. It periodically flushes windows and
// generates reports.
func (a *Aggregator) Run() {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	a.mu.Unlock()

	a.wg.Add(1)
	go a.aggregationLoop()

	a.logger.Info("aggregator started", "interval", a.interval)
}

func (a *Aggregator) aggregationLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// Initial flush
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := a.FlushWindow(ctx); err != nil {
		a.logger.Debug("initial window flush skipped", "error", err)
	}
	cancel()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			report, err := a.FlushWindow(ctx)
			cancel()

			if err != nil {
				a.logger.Debug("window flush failed", "error", err)
				continue
			}

			a.logger.Info("aggregation report generated",
				"total_requests", report.TotalRequests,
				"total_errors", report.TotalErrors,
				"avg_latency_ms", report.AvgLatencyMs,
				"error_rate", report.ErrorRate,
				"top_apis", len(report.TopAPIs),
				"top_apps", len(report.TopApps),
				"top_users", len(report.TopUsers),
			)

		case <-a.stopCh:
			return
		}
	}
}

// Shutdown gracefully stops the aggregator.
func (a *Aggregator) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	a.mu.Unlock()

	close(a.stopCh)

	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Final flush
		flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		a.FlushWindow(flushCtx)
		cancel()

		a.logger.Info("aggregator stopped")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("aggregator shutdown timeout")
	}
}

// --- Query Methods ---

// GetLatestReport returns the most recent aggregation report.
func (a *Aggregator) GetLatestReport() *models.AggregationReport {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var latest *models.AggregationReport
	for _, report := range a.reports {
		if latest == nil || report.GeneratedAt.After(latest.GeneratedAt) {
			latest = report
		}
	}
	return latest
}

// GetReport returns a specific aggregation report by window key.
func (a *Aggregator) GetReport(windowKey string) *models.AggregationReport {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reports[windowKey]
}

// ListReports returns all stored aggregation reports, sorted by generation time.
func (a *Aggregator) ListReports(limit int) []*models.AggregationReport {
	a.mu.RLock()
	reports := make([]*models.AggregationReport, 0, len(a.reports))
	for _, r := range a.reports {
		reports = append(reports, r)
	}
	a.mu.RUnlock()

	// Sort by generation time descending
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].GeneratedAt.After(reports[j].GeneratedAt)
	})

	if limit > 0 && len(reports) > limit {
		reports = reports[:limit]
	}

	return reports
}

// --- Helpers ---

func (a *Aggregator) newWindowAggregation() *windowAggregation {
	return &windowAggregation{
		WindowStart:  time.Now().UTC(),
		apiCounts:    make(map[string]int64),
		apiLatencies: make(map[string][]int64),
		apiErrors:    make(map[string]int64),
		appCounts:    make(map[string]int64),
		appErrors:    make(map[string]int64),
		userCounts:   make(map[string]int64),
		userErrors:   make(map[string]int64),
	}
}

func (a *Aggregator) currentWindowKey() string {
	return time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339)
}

// percentileInt64 calculates the p-th percentile of a slice of int64 values.
func percentileInt64(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}

	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}

	// Use nearest-rank method
	rank := int(math.Ceil(p/100.0*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}

	return sorted[rank]
}

// min returns the minimum of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
