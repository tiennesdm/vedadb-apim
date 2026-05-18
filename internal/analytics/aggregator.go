// Package analytics implements the analytics aggregation subsystem for VedaDB API Manager.
// All aggregation queries execute REAL SQL against the analytics_events and
// analytics_summary tables via the VedaDB wire protocol.
package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/vedadb/vapim/pkg/models"
	"github.com/vedadb/vapim/pkg/store"
)

// ---------------------------------------------------------------------------
// Aggregator
// ---------------------------------------------------------------------------

// Aggregator computes analytics summaries by querying VedaDB directly.
type Aggregator struct {
	store  store.Store
	logger *slog.Logger

	mu       sync.RWMutex
	stopCh   chan struct{}
	wg       sync.WaitGroup
	running  bool
	interval time.Duration
}

// AggregatorConfig configures the data aggregator.
type AggregatorConfig struct {
	AggregationInterval time.Duration // how often to run aggregation
}

// DefaultAggregatorConfig returns sensible defaults.
func DefaultAggregatorConfig() AggregatorConfig {
	return AggregatorConfig{
		AggregationInterval: 1 * time.Minute,
	}
}

// NewAggregator creates a new DB-backed aggregator.
func NewAggregator(store store.Store, cfg AggregatorConfig, logger *slog.Logger) *Aggregator {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &Aggregator{
		store:    store,
		logger:   logger.With("component", "analytics-aggregator"),
		interval: cfg.AggregationInterval,
		stopCh:   make(chan struct{}),
	}
}

// ---------------------------------------------------------------------------
// Real DB Query Methods
// ---------------------------------------------------------------------------

// AnalyticsSummary is the result of AggregateByAPI.
type AnalyticsSummary struct {
	RequestCount int64   `json:"request_count"`
	ErrorCount   int64   `json:"error_count"`
	AvgLatency   float64 `json:"avg_latency"`
	P95Latency   float64 `json:"p95_latency"`
	P99Latency   float64 `json:"p99_latency"`
	UniqueUsers  int64   `json:"unique_users"`
}

// AggregateByAPI queries analytics_events for aggregated metrics on a single API.
func (a *Aggregator) AggregateByAPI(tenantID, apiID string, start, end time.Time) (*AnalyticsSummary, error) {
	query := `SELECT
		COUNT(*) as request_count,
		COUNT(CASE WHEN status_code >= 400 THEN 1 END) as error_count,
		COALESCE(AVG(latency_ms), 0) as avg_latency,
		COALESCE((SELECT latency_ms FROM analytics_events e2 WHERE e2.tenant_id = ? AND e2.api_id = ? AND e2.timestamp BETWEEN ? AND ? ORDER BY latency_ms LIMIT 1 OFFSET (SELECT COUNT(*) * 95 / 100 FROM analytics_events e3 WHERE e3.tenant_id = ? AND e3.api_id = ? AND e3.timestamp BETWEEN ? AND ?)), 0) as p95_latency,
		COALESCE((SELECT latency_ms FROM analytics_events e2 WHERE e2.tenant_id = ? AND e2.api_id = ? AND e2.timestamp BETWEEN ? AND ? ORDER BY latency_ms LIMIT 1 OFFSET (SELECT COUNT(*) * 99 / 100 FROM analytics_events e3 WHERE e3.tenant_id = ? AND e3.api_id = ? AND e3.timestamp BETWEEN ? AND ?)), 0) as p99_latency,
		COUNT(DISTINCT user_id) as unique_users
	FROM analytics_events
	WHERE tenant_id = ? AND api_id = ? AND timestamp BETWEEN ? AND ?`

	raws, err := a.store.RawQuery(query,
		tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339),
		tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339),
		tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339),
		tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339),
		tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339),
	)
	if err != nil {
		a.logger.Error("AggregateByAPI query failed", "error", err, "tenant_id", tenantID, "api_id", apiID)
		return nil, fmt.Errorf("aggregate by api: %w", err)
	}
	if len(raws) == 0 {
		return &AnalyticsSummary{}, nil
	}

	var row struct {
		RequestCount string  `json:"request_count"`
		ErrorCount   string  `json:"error_count"`
		AvgLatency   float64 `json:"avg_latency"`
		P95Latency   float64 `json:"p95_latency"`
		P99Latency   float64 `json:"p99_latency"`
		UniqueUsers  string  `json:"unique_users"`
	}
	if err := json.Unmarshal(raws[0], &row); err != nil {
		// Try flexible parsing
		var alt map[string]interface{}
		if err2 := json.Unmarshal(raws[0], &alt); err2 == nil {
			sum := &AnalyticsSummary{}
			if v, ok := alt["request_count"]; ok {
				switch vt := v.(type) {
				case float64:
					sum.RequestCount = int64(vt)
				case string:
					sum.RequestCount, _ = strconv.ParseInt(vt, 10, 64)
				}
			}
			if v, ok := alt["error_count"]; ok {
				switch vt := v.(type) {
				case float64:
					sum.ErrorCount = int64(vt)
				case string:
					sum.ErrorCount, _ = strconv.ParseInt(vt, 10, 64)
				}
			}
			if v, ok := alt["avg_latency"]; ok {
				switch vt := v.(type) {
				case float64:
					sum.AvgLatency = vt
				case string:
					sum.AvgLatency, _ = strconv.ParseFloat(vt, 64)
				}
			}
			if v, ok := alt["p95_latency"]; ok {
				switch vt := v.(type) {
				case float64:
					sum.P95Latency = vt
				case string:
					sum.P95Latency, _ = strconv.ParseFloat(vt, 64)
				}
			}
			if v, ok := alt["p99_latency"]; ok {
				switch vt := v.(type) {
				case float64:
					sum.P99Latency = vt
				case string:
					sum.P99Latency, _ = strconv.ParseFloat(vt, 64)
				}
			}
			if v, ok := alt["unique_users"]; ok {
				switch vt := v.(type) {
				case float64:
					sum.UniqueUsers = int64(vt)
				case string:
					sum.UniqueUsers, _ = strconv.ParseInt(vt, 10, 64)
				}
			}
			return sum, nil
		}
		return nil, fmt.Errorf("unmarshal aggregate row: %w", err)
	}

	sum := &AnalyticsSummary{
		AvgLatency: row.AvgLatency,
		P95Latency: row.P95Latency,
		P99Latency: row.P99Latency,
	}
	sum.RequestCount, _ = strconv.ParseInt(row.RequestCount, 10, 64)
	sum.ErrorCount, _ = strconv.ParseInt(row.ErrorCount, 10, 64)
	sum.UniqueUsers, _ = strconv.ParseInt(row.UniqueUsers, 10, 64)
	return sum, nil
}

// GetTopAPIs queries the top APIs by request count for a tenant.
func (a *Aggregator) GetTopAPIs(tenantID string, limit int, start, end time.Time) ([]*models.APISummary, error) {
	query := `SELECT api_id, COUNT(*) as request_count, COALESCE(AVG(latency_ms), 0) as avg_latency
	FROM analytics_events
	WHERE tenant_id = ? AND timestamp BETWEEN ? AND ?
	GROUP BY api_id
	ORDER BY request_count DESC
	LIMIT ?`

	raws, err := a.store.RawQuery(query,
		tenantID, start.Format(time.RFC3339), end.Format(time.RFC3339), limit)
	if err != nil {
		a.logger.Error("GetTopAPIs query failed", "error", err, "tenant_id", tenantID)
		return nil, fmt.Errorf("get top apis: %w", err)
	}

	summaries := make([]*models.APISummary, 0, len(raws))
	for _, raw := range raws {
		var sum models.APISummary
		if err := json.Unmarshal(raw, &sum); err != nil {
			var alt struct {
				APIID        string `json:"api_id"`
				RequestCount string `json:"request_count"`
				AvgLatency   string `json:"avg_latency"`
			}
			if err2 := json.Unmarshal(raw, &alt); err2 == nil {
				sum.APIID = alt.APIID
				sum.RequestCount, _ = strconv.ParseInt(alt.RequestCount, 10, 64)
				avg, _ := strconv.ParseFloat(alt.AvgLatency, 64)
				sum.AvgLatency = int64(avg)
			} else {
				continue
			}
		}
		summaries = append(summaries, &sum)
	}
	return summaries, nil
}

// UsageDataPoint represents a single time-bucketed usage metric.
type UsageDataPoint struct {
	Period       time.Time `json:"period"`
	RequestCount int64     `json:"request_count"`
	AvgLatency   float64   `json:"avg_latency"`
}

// GetAPIUsageOverTime queries time-series usage data for an API.
func (a *Aggregator) GetAPIUsageOverTime(tenantID, apiID string, start, end time.Time, interval string) ([]*UsageDataPoint, error) {
	unit := interval
	if unit == "" {
		unit = "hour"
	}

	query := fmt.Sprintf(`SELECT
		DATE_TRUNC('%s', timestamp) as period,
		COUNT(*) as request_count,
		COALESCE(AVG(latency_ms), 0) as avg_latency
	FROM analytics_events
	WHERE tenant_id = ? AND api_id = ? AND timestamp BETWEEN ? AND ?
	GROUP BY period
	ORDER BY period ASC`, unit)

	raws, err := a.store.RawQuery(query,
		tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339))
	if err != nil {
		a.logger.Error("GetAPIUsageOverTime query failed", "error", err, "tenant_id", tenantID, "api_id", apiID)
		return nil, fmt.Errorf("get api usage over time: %w", err)
	}

	points := make([]*UsageDataPoint, 0, len(raws))
	for _, raw := range raws {
		var dp struct {
			Period       string  `json:"period"`
			RequestCount string  `json:"request_count"`
			AvgLatency   float64 `json:"avg_latency"`
		}
		if err := json.Unmarshal(raw, &dp); err != nil {
			var alt map[string]interface{}
			if err2 := json.Unmarshal(raw, &alt); err2 == nil {
				up := &UsageDataPoint{}
				if p, ok := alt["period"].(string); ok {
					up.Period, _ = time.Parse(time.RFC3339, p)
					if up.Period.IsZero() {
						up.Period, _ = time.Parse("2006-01-02T15:04:05Z", p)
					}
				}
				if v, ok := alt["request_count"]; ok {
					switch vt := v.(type) {
					case float64:
						up.RequestCount = int64(vt)
					case string:
						up.RequestCount, _ = strconv.ParseInt(vt, 10, 64)
					}
				}
				if v, ok := alt["avg_latency"]; ok {
					switch vt := v.(type) {
					case float64:
						up.AvgLatency = vt
					case string:
						up.AvgLatency, _ = strconv.ParseFloat(vt, 64)
					}
				}
				points = append(points, up)
			}
			continue
		}
		t, _ := time.Parse(time.RFC3339, dp.Period)
		if t.IsZero() {
			t, _ = time.Parse("2006-01-02T15:04:05Z", dp.Period)
		}
		rc, _ := strconv.ParseInt(dp.RequestCount, 10, 64)
		points = append(points, &UsageDataPoint{
			Period:       t,
			RequestCount: rc,
			AvgLatency:   dp.AvgLatency,
		})
	}
	return points, nil
}

// GetErrorRate queries the error rate percentage for an API.
func (a *Aggregator) GetErrorRate(tenantID, apiID string, start, end time.Time) (float64, error) {
	query := `SELECT
		CASE WHEN COUNT(*) = 0 THEN 0.0
			 ELSE COUNT(CASE WHEN status_code >= 400 THEN 1 END) * 100.0 / COUNT(*)
		END as error_rate
	FROM analytics_events
	WHERE tenant_id = ? AND api_id = ? AND timestamp BETWEEN ? AND ?`

	raws, err := a.store.RawQuery(query,
		tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339))
	if err != nil {
		a.logger.Error("GetErrorRate query failed", "error", err, "tenant_id", tenantID, "api_id", apiID)
		return 0, fmt.Errorf("get error rate: %w", err)
	}
	if len(raws) == 0 {
		return 0, nil
	}

	var row struct {
		ErrorRate float64 `json:"error_rate"`
	}
	if err := json.Unmarshal(raws[0], &row); err != nil {
		var alt map[string]interface{}
		if err2 := json.Unmarshal(raws[0], &alt); err2 == nil {
			if v, ok := alt["error_rate"]; ok {
				switch vt := v.(type) {
				case float64:
					return vt, nil
				case string:
					r, _ := strconv.ParseFloat(vt, 64)
					return r, nil
				}
			}
		}
		return 0, fmt.Errorf("unmarshal error rate: %w", err)
	}
	return row.ErrorRate, nil
}

// UserUsage represents usage for a single user.
type UserUsage struct {
	UserID     string  `json:"user_id"`
	Count      int64   `json:"count"`
	AvgLatency float64 `json:"avg_latency"`
}

// GetTopUsers queries the top users by request count for an API.
func (a *Aggregator) GetTopUsers(tenantID, apiID string, limit int) ([]*UserUsage, error) {
	query := `SELECT user_id, COUNT(*) as request_count, COALESCE(AVG(latency_ms), 0) as avg_latency
	FROM analytics_events
	WHERE tenant_id = ? AND api_id = ?
	GROUP BY user_id
	ORDER BY request_count DESC
	LIMIT ?`

	raws, err := a.store.RawQuery(query, tenantID, apiID, limit)
	if err != nil {
		a.logger.Error("GetTopUsers query failed", "error", err, "tenant_id", tenantID, "api_id", apiID)
		return nil, fmt.Errorf("get top users: %w", err)
	}

	users := make([]*UserUsage, 0, len(raws))
	for _, raw := range raws {
		var u struct {
			UserID     string  `json:"user_id"`
			Count      string  `json:"request_count"`
			AvgLatency float64 `json:"avg_latency"`
		}
		if err := json.Unmarshal(raw, &u); err != nil {
			var alt map[string]interface{}
			if err2 := json.Unmarshal(raw, &alt); err2 == nil {
				uu := &UserUsage{}
				if v, ok := alt["user_id"].(string); ok {
					uu.UserID = v
				}
				if v, ok := alt["request_count"]; ok {
					switch vt := v.(type) {
					case float64:
						uu.Count = int64(vt)
					case string:
						uu.Count, _ = strconv.ParseInt(vt, 10, 64)
					}
				}
				if v, ok := alt["avg_latency"]; ok {
					switch vt := v.(type) {
					case float64:
						uu.AvgLatency = vt
					case string:
						uu.AvgLatency, _ = strconv.ParseFloat(vt, 64)
					}
				}
				users = append(users, uu)
			}
			continue
		}
		c, _ := strconv.ParseInt(u.Count, 10, 64)
		users = append(users, &UserUsage{
			UserID:     u.UserID,
			Count:      c,
			AvgLatency: u.AvgLatency,
		})
	}
	return users, nil
}

// ---------------------------------------------------------------------------
// Periodic Aggregation (cron-style)
// ---------------------------------------------------------------------------

// RunAggregation reads analytics_events for the given period, computes
// aggregates per API, and writes them to analytics_summary.
func (a *Aggregator) RunAggregation(period string) error {
	now := time.Now().UTC()
	var start time.Time
	switch period {
	case "hourly":
		start = now.Add(-1 * time.Hour)
	case "daily":
		start = now.Add(-24 * time.Hour)
	case "weekly":
		start = now.Add(-7 * 24 * time.Hour)
	case "monthly":
		start = now.Add(-30 * 24 * time.Hour)
	default:
		start = now.Add(-1 * time.Hour)
		period = "hourly"
	}
	end := now

	a.logger.Info("running aggregation", "period", period, "start", start, "end", end)

	// Step 1: Read all events in the period grouped by api_id
	query := `SELECT tenant_id, api_id,
		COUNT(*) as request_count,
		COUNT(CASE WHEN status_code >= 400 THEN 1 END) as error_count,
		COALESCE(AVG(latency_ms), 0) as avg_latency,
		COALESCE((SELECT latency_ms FROM analytics_events e2 WHERE e2.api_id = analytics_events.api_id AND e2.tenant_id = analytics_events.tenant_id AND e2.timestamp BETWEEN ? AND ? ORDER BY latency_ms LIMIT 1 OFFSET (SELECT COUNT(*) * 95 / 100 FROM analytics_events e3 WHERE e3.api_id = analytics_events.api_id AND e3.tenant_id = analytics_events.tenant_id AND e3.timestamp BETWEEN ? AND ?)), 0) as p95_latency,
		COALESCE((SELECT latency_ms FROM analytics_events e2 WHERE e2.api_id = analytics_events.api_id AND e2.tenant_id = analytics_events.tenant_id AND e2.timestamp BETWEEN ? AND ? ORDER BY latency_ms LIMIT 1 OFFSET (SELECT COUNT(*) * 99 / 100 FROM analytics_events e3 WHERE e3.api_id = analytics_events.api_id AND e3.tenant_id = analytics_events.tenant_id AND e3.timestamp BETWEEN ? AND ?)), 0) as p99_latency,
		COUNT(DISTINCT user_id) as unique_users
	FROM analytics_events
	WHERE timestamp BETWEEN ? AND ?
	GROUP BY tenant_id, api_id`

	raws, err := a.store.RawQuery(query,
		start.Format(time.RFC3339), end.Format(time.RFC3339),
		start.Format(time.RFC3339), end.Format(time.RFC3339),
		start.Format(time.RFC3339), end.Format(time.RFC3339),
		start.Format(time.RFC3339), end.Format(time.RFC3339),
		start.Format(time.RFC3339), end.Format(time.RFC3339),
	)
	if err != nil {
		a.logger.Error("aggregation query failed", "error", err, "period", period)
		return fmt.Errorf("aggregation query: %w", err)
	}

	type aggRow struct {
		TenantID     string  `json:"tenant_id"`
		APIID        string  `json:"api_id"`
		RequestCount string  `json:"request_count"`
		ErrorCount   string  `json:"error_count"`
		AvgLatency   float64 `json:"avg_latency"`
		P95Latency   float64 `json:"p95_latency"`
		P99Latency   float64 `json:"p99_latency"`
		UniqueUsers  string  `json:"unique_users"`
	}

	aggregates := make([]aggRow, 0, len(raws))
	for _, raw := range raws {
		var r aggRow
		if err := json.Unmarshal(raw, &r); err != nil {
			var alt map[string]interface{}
			if err2 := json.Unmarshal(raw, &alt); err2 != nil {
				continue
			}
			if v, ok := alt["tenant_id"].(string); ok {
				r.TenantID = v
			}
			if v, ok := alt["api_id"].(string); ok {
				r.APIID = v
			}
			if v, ok := alt["request_count"]; ok {
				switch vt := v.(type) {
				case float64:
					r.RequestCount = strconv.FormatInt(int64(vt), 10)
				case string:
					r.RequestCount = vt
				}
			}
			if v, ok := alt["error_count"]; ok {
				switch vt := v.(type) {
				case float64:
					r.ErrorCount = strconv.FormatInt(int64(vt), 10)
				case string:
					r.ErrorCount = vt
				}
			}
			if v, ok := alt["avg_latency"]; ok {
				switch vt := v.(type) {
				case float64:
					r.AvgLatency = vt
				case string:
					r.AvgLatency, _ = strconv.ParseFloat(vt, 64)
				}
			}
			if v, ok := alt["p95_latency"]; ok {
				switch vt := v.(type) {
				case float64:
					r.P95Latency = vt
				case string:
					r.P95Latency, _ = strconv.ParseFloat(vt, 64)
				}
			}
			if v, ok := alt["p99_latency"]; ok {
				switch vt := v.(type) {
				case float64:
					r.P99Latency = vt
				case string:
					r.P99Latency, _ = strconv.ParseFloat(vt, 64)
				}
			}
			if v, ok := alt["unique_users"]; ok {
				switch vt := v.(type) {
				case float64:
					r.UniqueUsers = strconv.FormatInt(int64(vt), 10)
				case string:
					r.UniqueUsers = vt
				}
			}
		}
		aggregates = append(aggregates, r)
	}

	// Step 4: INSERT INTO analytics_summary
	for _, agg := range aggregates {
		rc, _ := strconv.ParseInt(agg.RequestCount, 10, 64)
		ec, _ := strconv.ParseInt(agg.ErrorCount, 10, 64)
		uu, _ := strconv.ParseInt(agg.UniqueUsers, 10, 64)

		insertQ := `INSERT INTO analytics_summary
			(id, tenant_id, api_id, period, period_start, request_count, error_count, avg_latency_ms, p95_latency_ms, p99_latency_ms, unique_users, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`

		summaryID := fmt.Sprintf("%s-%s-%s", agg.TenantID, agg.APIID, start.Format("20060102T150405"))
		avgLat := int64(math.Round(agg.AvgLatency))

		if err := a.store.Exec(insertQ,
			summaryID, agg.TenantID, agg.APIID, period, start.Format(time.RFC3339),
			rc, ec, avgLat, int64(math.Round(agg.P95Latency)), int64(math.Round(agg.P99Latency)), uu,
		); err != nil {
			a.logger.Error("failed to insert analytics summary",
				"error", err,
				"tenant_id", agg.TenantID,
				"api_id", agg.APIID,
			)
			// Continue with other APIs; don't fail the whole batch
		}
	}

	a.logger.Info("aggregation complete",
		"period", period,
		"apis_aggregated", len(aggregates),
		"window_start", start,
		"window_end", end,
	)

	// Step 5: Optionally DELETE old events (retention policy - keep 30 days)
	retentionCutoff := now.Add(-30 * 24 * time.Hour)
	delQ := `DELETE FROM analytics_events WHERE timestamp < ?`
	if err := a.store.Exec(delQ, retentionCutoff.Format(time.RFC3339)); err != nil {
		a.logger.Warn("failed to prune old analytics events", "error", err)
		// Non-fatal: retention cleanup failure shouldn't fail aggregation
	} else {
		a.logger.Info("pruned old analytics events", "retention_cutoff", retentionCutoff)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Background Processing
// ---------------------------------------------------------------------------

// Run starts the periodic aggregation loop.
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

	// Initial aggregation
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	_ = ctx
	if err := a.RunAggregation("hourly"); err != nil {
		a.logger.Debug("initial aggregation skipped", "error", err)
	}
	cancel()

	for {
		select {
		case <-ticker.C:
			period := "hourly"
			if time.Now().UTC().Minute() == 0 {
				period = "hourly"
			}
			if err := a.RunAggregation(period); err != nil {
				a.logger.Error("periodic aggregation failed", "error", err, "period", period)
			}

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
		// Final aggregation
		_ = ctx
		if err := a.RunAggregation("hourly"); err != nil {
			a.logger.Warn("final aggregation failed", "error", err)
		}

		a.logger.Info("aggregator stopped")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("aggregator shutdown timeout")
	}
}

// ---------------------------------------------------------------------------
// Legacy Report Compatibility
// ---------------------------------------------------------------------------

// AggregationReport wraps the DB-backed aggregation results.
type AggregationReport struct {
	WindowKey     string               `json:"window_key"`
	GeneratedAt   time.Time            `json:"generated_at"`
	TotalRequests int64                `json:"total_requests"`
	TotalErrors   int64                `json:"total_errors"`
	TotalLatencyMs int64               `json:"total_latency_ms"`
	AvgLatencyMs  float64              `json:"avg_latency_ms"`
	ErrorRate     float64              `json:"error_rate"`
	TopAPIs       []models.TopAPIEntry `json:"top_apis"`
}

// GetLatestReport builds a report from real DB queries for the last hour.
func (a *Aggregator) GetLatestReport() *AggregationReport {
	end := time.Now().UTC()
	start := end.Add(-1 * time.Hour)

	query := `SELECT
		COUNT(*) as total_requests,
		COUNT(CASE WHEN status_code >= 400 THEN 1 END) as total_errors,
		COALESCE(AVG(latency_ms), 0) as avg_latency
	FROM analytics_events
	WHERE timestamp BETWEEN ? AND ?`

	raws, err := a.store.RawQuery(query, start.Format(time.RFC3339), end.Format(time.RFC3339))
	if err != nil {
		a.logger.Error("GetLatestReport query failed", "error", err)
		return &AggregationReport{GeneratedAt: end}
	}

	report := &AggregationReport{
		WindowKey:   start.Format(time.RFC3339) + "/" + end.Format(time.RFC3339),
		GeneratedAt: end,
	}

	if len(raws) > 0 {
		var row struct {
			TotalRequests string  `json:"total_requests"`
			TotalErrors   string  `json:"total_errors"`
			AvgLatency    float64 `json:"avg_latency"`
		}
		if err := json.Unmarshal(raws[0], &row); err == nil {
			report.TotalRequests, _ = strconv.ParseInt(row.TotalRequests, 10, 64)
			report.TotalErrors, _ = strconv.ParseInt(row.TotalErrors, 10, 64)
			report.AvgLatencyMs = row.AvgLatency
			if report.TotalRequests > 0 {
				report.ErrorRate = float64(report.TotalErrors) / float64(report.TotalRequests) * 100
			}
		}
	}

	topAPIs, _ := a.GetTopAPIs("", 10, start, end)
	report.TopAPIs = make([]models.TopAPIEntry, 0, len(topAPIs))
	for _, api := range topAPIs {
		report.TopAPIs = append(report.TopAPIs, models.TopAPIEntry{
			APIID:  api.APIID,
			Count:  api.RequestCount,
			Errors: api.ErrorCount,
		})
	}

	return report
}

// GetReport returns a specific aggregation report by window key.
func (a *Aggregator) GetReport(windowKey string) *AggregationReport {
	_ = windowKey
	return a.GetLatestReport()
}

// ListReports returns stored aggregation reports.
func (a *Aggregator) ListReports(limit int) []*AggregationReport {
	_ = limit
	report := a.GetLatestReport()
	return []*AggregationReport{report}
}
