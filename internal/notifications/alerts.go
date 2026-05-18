// Package notifications provides API usage alerting for the VedaDB API Manager.
package notifications

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vedadb/vapim/pkg/store"
)

// ---------------------------------------------------------------------------
// AlertManager
// ---------------------------------------------------------------------------

// AlertManager monitors API usage against throttle policies and sends
// email alerts when thresholds are breached.
type AlertManager struct {
	store        store.Store
	email        *EmailNotifier
	mu           sync.Mutex
	lastAlerted  map[string]time.Time // key: "apiID:alertType" -> last alert time
	cooldown     time.Duration        // minimum time between repeat alerts
}

// NewAlertManager creates an AlertManager with the given dependencies.
func NewAlertManager(store store.Store, email *EmailNotifier) *AlertManager {
	return &AlertManager{
		store:       store,
		email:       email,
		lastAlerted: make(map[string]time.Time),
		cooldown:    1 * time.Hour, // default: alert at most once per hour
	}
}

// WithCooldown sets the minimum duration between repeated alerts of the
// same type for the same API.
func (a *AlertManager) WithCooldown(d time.Duration) *AlertManager {
	a.cooldown = d
	return a
}

// ---------------------------------------------------------------------------
// Quota threshold checking
// ---------------------------------------------------------------------------

// QuotaAlertThreshold is the percentage of quota usage at which an alert
// is triggered.
const QuotaAlertThreshold = 80.0

// CheckAndAlert evaluates the current request volume for the given API
// against its throttle policy. If usage exceeds the configured threshold,
// it looks up all active subscriptions, resolves subscriber emails, and
// sends a quota alert to each unique address.
func (a *AlertManager) CheckAndAlert(apiID string) {
	if apiID == "" {
		return
	}

	// Respect cooldown to avoid spam.
	key := apiID + ":quota"
	a.mu.Lock()
	if last, ok := a.lastAlerted[key]; ok && time.Since(last) < a.cooldown {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	// Get the API to resolve tenant and policy.
	api, err := a.store.GetAPI(apiID)
	if err != nil || api == nil {
		return
	}

	// If no throttle policy is attached, nothing to alert on.
	if api.ThrottlePolicy == "" {
		return
	}

	// Fetch the throttle policy to obtain the rate limit.
	policy, err := a.store.GetThrottlePolicyByName(api.TenantID, api.ThrottlePolicy)
	if err != nil || policy == nil || policy.Rate <= 0 {
		return
	}

	// Pull the latest analytics summary for this API in the current hour.
	now := time.Now()
	summary, err := a.store.GetAnalyticsSummary(api.TenantID, apiID, "hour", now.Add(-time.Hour), now)
	if err != nil || len(summary) == 0 {
		return
	}

	current := summary[0]

	// Calculate usage percentage against the policy rate.
	usagePercent := float64(current.RequestCount) / float64(policy.Rate) * 100
	if usagePercent < QuotaAlertThreshold {
		return
	}

	// Find all active subscribers to notify.
	subs, err := a.store.ListSubscriptionsByAPI(apiID)
	if err != nil {
		return
	}

	// Collect unique email addresses.
	emails := make(map[string]struct{})
	for _, sub := range subs {
		if sub.Status != "ACTIVE" {
			continue
		}
		app, err := a.store.GetApp(sub.AppID)
		if err != nil || app == nil {
			continue
		}
		user, err := a.store.GetUser(app.OwnerID)
		if err != nil || user == nil || user.Email == "" {
			continue
		}
		emails[user.Email] = struct{}{}
	}

	// Send alerts.
	sent := 0
	for email := range emails {
		if err := a.email.SendQuotaAlert(email, api.Name, current.RequestCount, policy.Rate); err == nil {
			sent++
		}
	}

	if sent > 0 {
		a.mu.Lock()
		a.lastAlerted[key] = time.Now()
		a.mu.Unlock()
	}
}

// CheckAndAlertAsync is a fire-and-forget wrapper around CheckAndAlert.
func (a *AlertManager) CheckAndAlertAsync(apiID string) {
	go a.CheckAndAlert(apiID)
}

// ---------------------------------------------------------------------------
// Periodic scanner
// ---------------------------------------------------------------------------

// RunPeriodicScanner starts a background goroutine that scans all APIs
// every interval and triggers alerts for those exceeding thresholds.
// The returned cancel function stops the scanner.
func (a *AlertManager) RunPeriodicScanner(ctx context.Context, interval time.Duration) context.CancelFunc {
	scanCtx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-scanCtx.Done():
				return
			case <-ticker.C:
				a.scanAllAPIs(scanCtx)
			}
		}
	}()

	return cancel
}

// scanAllAPIs enumerates APIs and checks each one for threshold breaches.
func (a *AlertManager) scanAllAPIs(ctx context.Context) {
	// List all published APIs across tenants (empty tenant = all).
	// We scan with a large limit; pagination is omitted for simplicity.
	// In production this should use a cursor or worker pool.
	// The store.ListAPIs signature is: (tenantID, status, limit, offset)
	// We iterate tenant by tenant is not available here, so we pass empty
	// tenantID which the store layer treats as cross-tenant.
	apis, _, err := a.store.ListAPIs("", "PUBLISHED", 10000, 0)
	if err != nil {
		return
	}

	for _, api := range apis {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if api == nil {
			continue
		}
		a.CheckAndAlert(api.ID)
	}
}

// ---------------------------------------------------------------------------
// Direct alert helpers (bypass threshold logic)
// ---------------------------------------------------------------------------

// AlertSubscriptionApproved sends a subscription approval email to the
// application owner.
func (a *AlertManager) AlertSubscriptionApproved(apiID, appID string) error {
	api, err := a.store.GetAPI(apiID)
	if err != nil || api == nil {
		return fmt.Errorf("cannot find API %s: %w", apiID, err)
	}
	app, err := a.store.GetApp(appID)
	if err != nil || app == nil {
		return fmt.Errorf("cannot find app %s: %w", appID, err)
	}
	user, err := a.store.GetUser(app.OwnerID)
	if err != nil || user == nil || user.Email == "" {
		return fmt.Errorf("cannot resolve owner email for app %s: %w", appID, err)
	}
	return a.email.SendSubscriptionApproval(user.Email, api.Name)
}

// AlertSubscriptionRejected sends a subscription rejection email.
func (a *AlertManager) AlertSubscriptionRejected(apiID, appID, reason string) error {
	api, err := a.store.GetAPI(apiID)
	if err != nil || api == nil {
		return fmt.Errorf("cannot find API %s: %w", apiID, err)
	}
	app, err := a.store.GetApp(appID)
	if err != nil || app == nil {
		return fmt.Errorf("cannot find app %s: %w", appID, err)
	}
	user, err := a.store.GetUser(app.OwnerID)
	if err != nil || user == nil || user.Email == "" {
		return fmt.Errorf("cannot resolve owner email for app %s: %w", appID, err)
	}
	return a.email.SendSubscriptionRejection(user.Email, api.Name, reason)
}

// AlertAPIDeprecation sends a deprecation notice to all active subscribers.
func (a *AlertManager) AlertAPIDeprecation(apiID string, deprecationDate time.Time) error {
	api, err := a.store.GetAPI(apiID)
	if err != nil || api == nil {
		return fmt.Errorf("cannot find API %s: %w", apiID, err)
	}

	subs, err := a.store.ListSubscriptionsByAPI(apiID)
	if err != nil {
		return fmt.Errorf("cannot list subscriptions: %w", err)
	}

	var lastErr error
	sent := 0
	for _, sub := range subs {
		app, err := a.store.GetApp(sub.AppID)
		if err != nil || app == nil {
			continue
		}
		user, err := a.store.GetUser(app.OwnerID)
		if err != nil || user == nil || user.Email == "" {
			continue
		}
		if err := a.email.SendAPIDeprecationNotice(user.Email, api.Name, deprecationDate); err != nil {
			lastErr = err
		} else {
			sent++
		}
	}

	if sent == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

// ResetCooldown clears the alert cooldown for the given API, allowing
// immediate re-alerting. Useful in tests.
func (a *AlertManager) ResetCooldown(apiID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k := range a.lastAlerted {
		if k == apiID+":quota" || k == apiID+":deprecation" {
			delete(a.lastAlerted, k)
		}
	}
}
