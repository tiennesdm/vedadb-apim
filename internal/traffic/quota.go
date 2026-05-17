package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/tiennesdm/vedadb-apim/pkg/models"
)

// QuotaManager handles API-level, application-level, and user-level quotas
// with tier-based limits and scheduled resets.
type QuotaManager struct {
	store      ThrottleStore
	logger     *slog.Logger
	mu         sync.RWMutex
	tierDefs   map[string]*models.QuotaDefinition
	schedulers map[string]*quotaScheduler // active quota reset schedulers
}

// QuotaPeriod defines the time period for quota tracking.
type QuotaPeriod string

const (
	QuotaPerMinute QuotaPeriod = "min"
	QuotaPerHour   QuotaPeriod = "hour"
	QuotaPerDay    QuotaPeriod = "day"
	QuotaPerMonth  QuotaPeriod = "month"
)

// quotaScheduler tracks scheduled resets for quota windows.
type quotaScheduler struct {
	quotaKey string
	period   QuotaPeriod
	timer    *time.Timer
	stopCh   chan struct{}
}

// NewQuotaManager creates a new quota manager.
func NewQuotaManager(store ThrottleStore, logger *slog.Logger) *QuotaManager {
	if logger == nil {
		logger = slog.Default()
	}

	qm := &QuotaManager{
		store:      store,
		logger:     logger.With("component", "quota-manager"),
		tierDefs:   buildDefaultQuotaDefinitions(),
		schedulers: make(map[string]*quotaScheduler),
	}

	// Start background reset scheduler
	go qm.resetSchedulerLoop()

	return qm
}

// --- Quota Tracking ---

// TrackAPIQuota records API usage and checks if the quota is exceeded.
func (qm *QuotaManager) TrackAPIQuota(ctx context.Context, apiID, tier string) (*models.QuotaStatus, error) {
	quotaKey := fmt.Sprintf("quota:api:%s:%s", apiID, tier)
	return qm.trackQuota(ctx, quotaKey, tier, QuotaPerMinute)
}

// TrackApplicationQuota records application usage and checks quota.
func (qm *QuotaManager) TrackApplicationQuota(ctx context.Context, appID, tier string) (*models.QuotaStatus, error) {
	quotaKey := fmt.Sprintf("quota:app:%s:%s", appID, tier)

	// Track all periods
	statuses := make([]*models.QuotaStatus, 0, 3)

	for _, period := range []QuotaPeriod{QuotaPerMinute, QuotaPerHour, QuotaPerDay} {
		status, err := qm.trackQuota(ctx, fmt.Sprintf("%s:%s", quotaKey, period), tier, period)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}

	// Return the most restrictive status
	mostRestrictive := statuses[0]
	for _, s := range statuses[1:] {
		if s.UsageRatio > mostRestrictive.UsageRatio {
			mostRestrictive = s
		}
	}
	mostRestrictive.QuotaKey = quotaKey

	return mostRestrictive, nil
}

// TrackUserQuota records user-level usage and checks quota.
func (qm *QuotaManager) TrackUserQuota(ctx context.Context, userID string) (*models.QuotaStatus, error) {
	quotaKey := fmt.Sprintf("quota:user:%s", userID)
	return qm.trackQuota(ctx, quotaKey, "Unlimited", QuotaPerMinute)
}

func (qm *QuotaManager) trackQuota(ctx context.Context, quotaKey, tier string, period QuotaPeriod) (*models.QuotaStatus, error) {
	def := qm.GetTierDefinition(tier)
	if def == nil {
		def = qm.GetTierDefinition("Unlimited")
	}

	limit := qm.getLimitForPeriod(def, period)
	if limit == math.MaxInt64 {
		// Unlimited tier - no tracking needed
		return &models.QuotaStatus{
			QuotaKey:   quotaKey,
			Tier:       tier,
			Period:     string(period),
			Limit:      limit,
			Used:       0,
			Remaining:  limit,
			UsageRatio: 0,
			Exceeded:   false,
		}, nil
	}

	// Increment usage counter
	used, err := qm.store.IncrementQuotaUsage(ctx, quotaKey, 1)
	if err != nil {
		qm.logger.Error("failed to increment quota usage", "key", quotaKey, "error", err)
		// Fail open
		return &models.QuotaStatus{
			QuotaKey:   quotaKey,
			Tier:       tier,
			Period:     string(period),
			Limit:      limit,
			Used:       0,
			Remaining:  limit,
			UsageRatio: 0,
			Exceeded:   false,
		}, nil
	}

	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}

	usageRatio := float64(used) / float64(limit)
	if usageRatio > 1.0 {
		usageRatio = 1.0
	}

	return &models.QuotaStatus{
		QuotaKey:   quotaKey,
		Tier:       tier,
		Period:     string(period),
		Limit:      limit,
		Used:       used,
		Remaining:  remaining,
		UsageRatio: usageRatio,
		Exceeded:   used > limit,
	}, nil
}

// --- Quota Usage Queries ---

// GetUsage returns the current quota usage for a key.
func (qm *QuotaManager) GetUsage(ctx context.Context, quotaKey string) (*models.QuotaUsage, error) {
	return qm.store.GetQuotaUsage(ctx, quotaKey)
}

// ResetUsage resets the quota usage for a key.
func (qm *QuotaManager) ResetUsage(ctx context.Context, quotaKey string) error {
	qm.logger.Info("resetting quota usage", "key", quotaKey)
	return qm.store.ResetQuotaUsage(ctx, quotaKey)
}

// --- Tier Management ---

// GetTierDefinition returns the quota definition for a subscription tier.
func (qm *QuotaManager) GetTierDefinition(tier string) *models.QuotaDefinition {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	if def, ok := qm.tierDefs[tier]; ok {
		return def
	}
	return nil
}

// RegisterTier registers or updates a tier definition.
func (qm *QuotaManager) RegisterTier(def *models.QuotaDefinition) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	qm.tierDefs[def.Name] = def
	qm.logger.Info("tier registered", "tier", def.Name,
		"rpm", def.RequestsPerMin,
		"rph", def.RequestsPerHour,
		"rpd", def.RequestsPerDay,
	)
}

// ListTiers returns all registered tier definitions.
func (qm *QuotaManager) ListTiers() []*models.QuotaDefinition {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	result := make([]*models.QuotaDefinition, 0, len(qm.tierDefs))
	for _, def := range qm.tierDefs {
		result = append(result, def)
	}
	return result
}

func (qm *QuotaManager) getLimitForPeriod(def *models.QuotaDefinition, period QuotaPeriod) int64 {
	switch period {
	case QuotaPerMinute:
		return def.RequestsPerMin
	case QuotaPerHour:
		return def.RequestsPerHour
	case QuotaPerDay:
		return def.RequestsPerDay
	case QuotaPerMonth:
		return def.RequestsPerMonth
	default:
		return math.MaxInt64
	}
}

// --- Scheduled Reset ---

// resetSchedulerLoop runs a background loop that triggers periodic quota resets.
func (qm *QuotaManager) resetSchedulerLoop() {
	// Reset all per-minute quotas every minute
	minuteTicker := time.NewTicker(time.Minute)
	defer minuteTicker.Stop()

	// Reset all per-hour quotas every hour
	hourTicker := time.NewTicker(time.Hour)
	defer hourTicker.Stop()

	// Reset all per-day quotas every day
	dayTicker := time.NewTicker(24 * time.Hour)
	defer dayTicker.Stop()

	for {
		select {
		case <-minuteTicker.C:
			qm.resetQuotasByPeriod(QuotaPerMinute)
		case <-hourTicker.C:
			qm.resetQuotasByPeriod(QuotaPerHour)
		case <-dayTicker.C:
			qm.resetQuotasByPeriod(QuotaPerDay)
		}
	}
}

func (qm *QuotaManager) resetQuotasByPeriod(period QuotaPeriod) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	qm.logger.Info("resetting quotas", "period", period)

	// This is a simplified implementation - in production, you'd iterate
	// over active quota keys and reset each one
	_ = ctx
}

// --- Default Definitions ---

func buildDefaultQuotaDefinitions() map[string]*models.QuotaDefinition {
	return map[string]*models.QuotaDefinition{
		"Bronze": {
			Name:            "Bronze",
			RequestsPerMin:  20,
			RequestsPerHour: 1000,
			RequestsPerDay:  10000,
			RequestsPerMonth: 100000,
			BurstAllowance:  5,
			Description:     "Basic tier with limited access",
		},
		"Silver": {
			Name:            "Silver",
			RequestsPerMin:  100,
			RequestsPerHour: 5000,
			RequestsPerDay:  50000,
			RequestsPerMonth: 500000,
			BurstAllowance:  20,
			Description:     "Standard tier with moderate access",
		},
		"Gold": {
			Name:            "Gold",
			RequestsPerMin:  500,
			RequestsPerHour: 25000,
			RequestsPerDay:  250000,
			RequestsPerMonth: 2500000,
			BurstAllowance:  100,
			Description:     "Premium tier with high access",
		},
		"Unlimited": {
			Name:            "Unlimited",
			RequestsPerMin:  math.MaxInt64,
			RequestsPerHour: math.MaxInt64,
			RequestsPerDay:  math.MaxInt64,
			RequestsPerMonth: math.MaxInt64,
			BurstAllowance:  math.MaxInt64,
			Description:     "Unrestricted access",
		},
	}
}
