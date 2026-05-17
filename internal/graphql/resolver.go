// Package graphql provides GraphQL resolvers for the VedaDB API Manager.
// All resolvers perform real database queries via the store interface.
package graphql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/graphql-go/graphql"
	"github.com/vedadb/vapim/internal/audit"
	"github.com/vedadb/vapim/internal/tenant"
	"github.com/vedadb/vapim/internal/webhook"
	"github.com/vedadb/vapim/pkg/models"
)

// SQLStore defines the database interface used by resolvers.
type SQLStore interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// Resolver contains all GraphQL field resolvers.
type Resolver struct {
	store    SQLStore
	logger   *slog.Logger
	auditor  audit.AuditLogger
	events   webhook.EventPublisher
}

// NewResolver creates a new GraphQL resolver.
func NewResolver(store SQLStore, logger *slog.Logger, auditor audit.AuditLogger, events webhook.EventPublisher) *Resolver {
	if auditor == nil {
		auditor = &audit.NopLogger{}
	}
	if events == nil {
		events = &webhook.NopEventPublisher{}
	}
	return &Resolver{
		store:   store,
		logger:  logger,
		auditor: auditor,
		events:  events,
	}
}

// ---- Query Resolvers ----

// ResolveAPIs resolves the `apis` query field.
func (r *Resolver) ResolveAPIs(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	status, _ := p.Args["status"].(string)
	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)

	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, name, description, context, version, endpoint, auth_type, status, provider, tags, rating, tenant_id, created_at, updated_at
		FROM apis
		WHERE tenant_id = $1
	`
	args := []interface{}{tenantID}
	argIdx := 1

	if status != "" {
		argIdx++
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argIdx+1, argIdx+2)
	args = append(args, limit, offset)

	return r.queryAPIs(ctx, query, args...)
}

// ResolveAPI resolves the `api(id: ID!)` query field.
func (r *Resolver) ResolveAPI(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)

	api, err := r.getAPIByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return api, nil
}

// ResolvePublishedAPIs resolves the `publishedAPIs` query field.
func (r *Resolver) ResolvePublishedAPIs(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, name, description, context, version, endpoint, auth_type, status, provider, tags, rating, tenant_id, created_at, updated_at
		FROM apis
		WHERE tenant_id = $1 AND status = 'PUBLISHED'
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`

	return r.queryAPIs(ctx, query, tenantID, limit, offset)
}

// ResolveSearchAPIs resolves the `searchAPIs` query field.
func (r *Resolver) ResolveSearchAPIs(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	searchQuery, _ := p.Args["query"].(string)
	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	searchTerm := "%" + strings.ToLower(searchQuery) + "%"

	sqlQuery := `
		SELECT id, name, description, context, version, endpoint, auth_type, status, provider, tags, rating, tenant_id, created_at, updated_at
		FROM apis
		WHERE tenant_id = $1
		AND (
			LOWER(name) ILIKE $2
			OR LOWER(description) ILIKE $2
			OR LOWER(context) ILIKE $2
			OR LOWER(COALESCE(provider, '')) ILIKE $2
			OR EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(COALESCE(tags, '[]'::jsonb)) AS t
				WHERE LOWER(t) ILIKE $2
			)
		)
		ORDER BY created_at DESC LIMIT $3 OFFSET $4
	`

	return r.queryAPIs(ctx, sqlQuery, tenantID, searchTerm, limit, offset)
}

// ResolveApplications resolves the `applications` query field.
func (r *Resolver) ResolveApplications(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, name, description, owner_id, tier, status, tenant_id, created_at
		FROM applications
		WHERE tenant_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`

	rows, err := r.store.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query applications: %w", err)
	}
	defer rows.Close()

	var apps []*models.Application
	for rows.Next() {
		app := &models.Application{}
		var desc sql.NullString
		err := rows.Scan(&app.ID, &app.Name, &desc, &app.OwnerID, &app.Tier, &app.Status, &app.TenantID, &app.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan application: %w", err)
		}
		if desc.Valid {
			d := desc.String
			app.Description = &d
		}
		apps = append(apps, app)
	}

	return apps, rows.Err()
}

// ResolveApplication resolves the `application(id: ID!)` query field.
func (r *Resolver) ResolveApplication(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)

	query := `
		SELECT id, name, description, owner_id, tier, status, tenant_id, created_at
		FROM applications
		WHERE id = $1 AND tenant_id = $2
	`

	app := &models.Application{}
	var desc sql.NullString
	err := r.store.QueryRowContext(ctx, query, id, tenantID).Scan(
		&app.ID, &app.Name, &desc, &app.OwnerID, &app.Tier, &app.Status, &app.TenantID, &app.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get application: %w", err)
	}
	if desc.Valid {
		d := desc.String
		app.Description = &d
	}

	return app, nil
}

// ResolveSubscriptions resolves the `subscriptions` query field.
func (r *Resolver) ResolveSubscriptions(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	apiID, hasAPIID := p.Args["apiId"].(string)
	appID, hasAppID := p.Args["appId"].(string)

	query := `
		SELECT id, api_id, application_id, tier, status, tenant_id, created_at
		FROM subscriptions
		WHERE tenant_id = $1
	`
	args := []interface{}{tenantID}

	if hasAPIID && apiID != "" {
		query += " AND api_id = $2"
		args = append(args, apiID)
	}
	if hasAppID && appID != "" {
		argPos := len(args) + 1
		query += fmt.Sprintf(" AND application_id = $%d", argPos)
		args = append(args, appID)
	}

	query += " ORDER BY created_at DESC"

	rows, err := r.store.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions: %w", err)
	}
	defer rows.Close()

	return r.scanSubscriptions(rows)
}

// ResolveUsers resolves the `users` query field.
func (r *Resolver) ResolveUsers(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, username, email, role, status, tenant_id, created_at
		FROM users
		WHERE tenant_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`

	rows, err := r.store.QueryContext(ctx, query, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		user := &models.User{}
		err := rows.Scan(&user.ID, &user.Username, &user.Email, &user.Role, &user.Status, &user.TenantID, &user.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}

	return users, rows.Err()
}

// ResolveUser resolves the `user(id: ID!)` query field.
func (r *Resolver) ResolveUser(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)

	query := `
		SELECT id, username, email, role, status, tenant_id, created_at
		FROM users
		WHERE id = $1 AND tenant_id = $2
	`

	user := &models.User{}
	err := r.store.QueryRowContext(ctx, query, id, tenantID).Scan(
		&user.ID, &user.Username, &user.Email, &user.Role, &user.Status, &user.TenantID, &user.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	return user, nil
}

// ResolveAnalytics resolves the `analytics` query field.
func (r *Resolver) ResolveAnalytics(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	period, _ := p.Args["period"].(string)
	apiID, hasAPIID := p.Args["apiId"].(string)

	// Parse period to determine date range
	dateFrom, dateTo := parsePeriod(period)

	var query string
	var args []interface{}

	if hasAPIID && apiID != "" {
		query = `
			SELECT
				$1 as api_id,
				COALESCE(MAX(a.name), '') as api_name,
				COUNT(*)::int as request_count,
				COUNT(*) FILTER (WHERE al.status_code >= 400)::int as error_count,
				COALESCE(AVG(al.latency_ms)::int, 0) as avg_latency,
				COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY al.latency_ms)::int, 0) as p95_latency,
				COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY al.latency_ms)::int, 0) as p99_latency,
				COUNT(DISTINCT al.user_id)::int as unique_users
			FROM analytics_logs al
			JOIN apis a ON a.id = al.api_id
			WHERE al.api_id = $1 AND al.tenant_id = $2
			AND al.created_at BETWEEN $3 AND $4
		`
		args = []interface{}{apiID, tenantID, dateFrom, dateTo}
	} else {
		query = `
			SELECT
				'' as api_id,
				'' as api_name,
				COUNT(*)::int as request_count,
				COUNT(*) FILTER (WHERE status_code >= 400)::int as error_count,
				COALESCE(AVG(latency_ms)::int, 0) as avg_latency,
				COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms)::int, 0) as p95_latency,
				COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY latency_ms)::int, 0) as p99_latency,
				COUNT(DISTINCT user_id)::int as unique_users
			FROM analytics_logs
			WHERE tenant_id = $1
			AND created_at BETWEEN $2 AND $3
		`
		args = []interface{}{tenantID, dateFrom, dateTo}
	}

	summary := &models.AnalyticsSummary{}
	err := r.store.QueryRowContext(ctx, query, args...).Scan(
		&summary.APIID, &summary.APIName, &summary.RequestCount, &summary.ErrorCount,
		&summary.AvgLatency, &summary.P95Latency, &summary.P99Latency, &summary.UniqueUsers,
	)
	if err != nil {
		return nil, fmt.Errorf("query analytics: %w", err)
	}

	return summary, nil
}

// ResolveTopAPIs resolves the `topAPIs` query field.
func (r *Resolver) ResolveTopAPIs(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	limit, _ := p.Args["limit"].(int)
	if limit <= 0 || limit > 1000 {
		limit = 10
	}

	query := `
		SELECT
			al.api_id,
			MAX(a.name) as api_name,
			COUNT(*)::int as request_count,
			COUNT(*) FILTER (WHERE al.status_code >= 400)::int as error_count,
			COALESCE(AVG(al.latency_ms)::int, 0) as avg_latency,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY al.latency_ms)::int, 0) as p95_latency,
			COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY al.latency_ms)::int, 0) as p99_latency,
			COUNT(DISTINCT al.user_id)::int as unique_users
		FROM analytics_logs al
		JOIN apis a ON a.id = al.api_id
		WHERE al.tenant_id = $1
		GROUP BY al.api_id
		ORDER BY request_count DESC
		LIMIT $2
	`

	rows, err := r.store.QueryContext(ctx, query, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("query top APIs: %w", err)
	}
	defer rows.Close()

	var summaries []*models.AnalyticsSummary
	for rows.Next() {
		s := &models.AnalyticsSummary{}
		err := rows.Scan(
			&s.APIID, &s.APIName, &s.RequestCount, &s.ErrorCount,
			&s.AvgLatency, &s.P95Latency, &s.P99Latency, &s.UniqueUsers,
		)
		if err != nil {
			return nil, fmt.Errorf("scan analytics: %w", err)
		}
		summaries = append(summaries, s)
	}

	return summaries, rows.Err()
}

// ResolveThrottlePolicies resolves the `throttlePolicies` query field.
func (r *Resolver) ResolveThrottlePolicies(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	query := `
		SELECT id, name, description, quota_type, request_count, time_unit, rate_limit_count, rate_limit_unit, is_deployed, tenant_id, created_at, updated_at
		FROM throttle_policies
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.store.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query throttle policies: %w", err)
	}
	defer rows.Close()

	var policies []*models.ThrottlePolicy
	for rows.Next() {
		policy := &models.ThrottlePolicy{}
		var desc sql.NullString
		err := rows.Scan(
			&policy.ID, &policy.Name, &desc, &policy.QuotaType, &policy.RequestCount,
			&policy.TimeUnit, &policy.RateLimitCount, &policy.RateLimitUnit,
			&policy.IsDeployed, &policy.TenantID, &policy.CreatedAt, &policy.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan throttle policy: %w", err)
		}
		if desc.Valid {
			d := desc.String
			policy.Description = &d
		}
		policies = append(policies, policy)
	}

	return policies, rows.Err()
}

// ResolveAuditLogs resolves the `auditLogs` query field.
func (r *Resolver) ResolveAuditLogs(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	limit, _ := p.Args["limit"].(int)
	offset, _ := p.Args["offset"].(int)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	logs, _, err := r.auditor.GetLogs(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get audit logs: %w", err)
	}

	return logs, nil
}

// ---- Mutation Resolvers ----

// ResolveCreateAPI resolves the `createAPI` mutation field.
func (r *Resolver) ResolveCreateAPI(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	inputMap, _ := p.Args["input"].(map[string]interface{})

	id := uuid.New().String()
	name := safeString(inputMap["name"])
	contextVal := safeString(inputMap["context"])
	version := safeString(inputMap["version"])
	endpoint := safeString(inputMap["endpoint"])
	authType := safeString(inputMap["authType"])

	var desc, provider *string
	if v, ok := inputMap["description"].(string); ok && v != "" {
		desc = &v
	}
	if v, ok := inputMap["provider"].(string); ok && v != "" {
		provider = &v
	}

	var tags models.StringSlice
	if tagList, ok := inputMap["tags"].([]interface{}); ok {
		for _, t := range tagList {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	query := `
		INSERT INTO apis (id, name, description, context, version, endpoint, auth_type, status, provider, tags, tenant_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'CREATED', $8, $9, $10, NOW(), NOW())
		RETURNING id, name, description, context, version, endpoint, auth_type, status, provider, tags, rating, tenant_id, created_at, updated_at
	`

	api := &models.API{}
	var rating sql.NullFloat64
	err := r.store.QueryRowContext(ctx, query, id, name, desc, contextVal, version, endpoint, authType, provider, tags, tenantID).Scan(
		&api.ID, &api.Name, &api.Description, &api.Context, &api.Version,
		&api.Endpoint, &api.AuthType, &api.Status, &api.Provider,
		&api.Tags, &rating, &api.TenantID, &api.CreatedAt, &api.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert API: %w", err)
	}
	if rating.Valid {
		r := rating.Float64
		api.Rating = &r
	}

	// Audit log
	go r.auditor.LogAPIAction(ctx, audit.ActionAPICreate, api)

	// Webhook event
	go r.events.PublishAPIEvent(ctx, webhook.EventAPICreated, api)

	return api, nil
}

// ResolveUpdateAPI resolves the `updateAPI` mutation field.
func (r *Resolver) ResolveUpdateAPI(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)
	inputMap, _ := p.Args["input"].(map[string]interface{})

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argIdx := 2 // $1 is tenant_id

	if v, ok := inputMap["name"].(string); ok && v != "" {
		updates = append(updates, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if v, ok := inputMap["description"].(string); ok {
		updates = append(updates, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if v, ok := inputMap["context"].(string); ok && v != "" {
		updates = append(updates, fmt.Sprintf("context = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if v, ok := inputMap["version"].(string); ok && v != "" {
		updates = append(updates, fmt.Sprintf("version = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if v, ok := inputMap["endpoint"].(string); ok && v != "" {
		updates = append(updates, fmt.Sprintf("endpoint = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if v, ok := inputMap["authType"].(string); ok && v != "" {
		updates = append(updates, fmt.Sprintf("auth_type = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if v, ok := inputMap["provider"].(string); ok {
		updates = append(updates, fmt.Sprintf("provider = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if v, ok := inputMap["status"].(string); ok && v != "" {
		updates = append(updates, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, v)
		argIdx++
	}
	if tagList, ok := inputMap["tags"].([]interface{}); ok {
		var tags models.StringSlice
		for _, t := range tagList {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
		updates = append(updates, fmt.Sprintf("tags = $%d", argIdx))
		args = append(args, tags)
		argIdx++
	}

	if len(updates) == 0 {
		return r.getAPIByID(ctx, tenantID, id)
	}

	// Always update updated_at
	updates = append(updates, "updated_at = NOW()")

	query := fmt.Sprintf(`
		UPDATE apis SET %s
		WHERE id = $1 AND tenant_id = $%d
		RETURNING id, name, description, context, version, endpoint, auth_type, status, provider, tags, rating, tenant_id, created_at, updated_at
	`, strings.Join(updates, ", "), argIdx)

	args = append([]interface{}{id}, args...)
	args = append(args, tenantID)

	api := &models.API{}
	var rating sql.NullFloat64
	err := r.store.QueryRowContext(ctx, query, args...).Scan(
		&api.ID, &api.Name, &api.Description, &api.Context, &api.Version,
		&api.Endpoint, &api.AuthType, &api.Status, &api.Provider,
		&api.Tags, &rating, &api.TenantID, &api.CreatedAt, &api.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("API not found: %s", id)
		}
		return nil, fmt.Errorf("update API: %w", err)
	}
	if rating.Valid {
		r := rating.Float64
		api.Rating = &r
	}

	go r.auditor.LogAPIAction(ctx, audit.ActionAPIUpdate, api)
	go r.events.PublishAPIEvent(ctx, webhook.EventAPIUpdated, api)

	return api, nil
}

// ResolveDeleteAPI resolves the `deleteAPI` mutation field.
func (r *Resolver) ResolveDeleteAPI(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)

	// Get the API first for the event/audit
	api, _ := r.getAPIByID(ctx, tenantID, id)

	query := `DELETE FROM apis WHERE id = $1 AND tenant_id = $2`
	result, err := r.store.ExecContext(ctx, query, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("delete API: %w", err)
	}

	rows, _ := result.RowsAffected()
	deleted := rows > 0

	if deleted && api != nil {
		go r.auditor.LogAPIAction(ctx, audit.ActionAPIDelete, api)
		go r.events.PublishAPIEvent(ctx, webhook.EventAPIDeleted, api)
	}

	return deleted, nil
}

// ResolvePublishAPI resolves the `publishAPI` mutation field.
func (r *Resolver) ResolvePublishAPI(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)

	query := `
		UPDATE apis SET status = 'PUBLISHED', updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
		RETURNING id, name, description, context, version, endpoint, auth_type, status, provider, tags, rating, tenant_id, created_at, updated_at
	`

	api := &models.API{}
	var rating sql.NullFloat64
	err := r.store.QueryRowContext(ctx, query, id, tenantID).Scan(
		&api.ID, &api.Name, &api.Description, &api.Context, &api.Version,
		&api.Endpoint, &api.AuthType, &api.Status, &api.Provider,
		&api.Tags, &rating, &api.TenantID, &api.CreatedAt, &api.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("API not found: %s", id)
		}
		return nil, fmt.Errorf("publish API: %w", err)
	}
	if rating.Valid {
		r := rating.Float64
		api.Rating = &r
	}

	go r.auditor.LogAPIAction(ctx, audit.ActionAPIPublish, api)
	go r.events.PublishAPIEvent(ctx, webhook.EventAPIPublished, api)

	return api, nil
}

// ResolveCreateApplication resolves the `createApplication` mutation field.
func (r *Resolver) ResolveCreateApplication(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	inputMap, _ := p.Args["input"].(map[string]interface{})
	name := safeString(inputMap["name"])
	tier := safeString(inputMap["tier"])

	var desc *string
	if v, ok := inputMap["description"].(string); ok && v != "" {
		desc = &v
	}

	// Get current user ID from context
	ownerID := ""
	if v, ok := ctx.Value("user_id").(string); ok {
		ownerID = v
	}

	id := uuid.New().String()

	query := `
		INSERT INTO applications (id, name, description, owner_id, tier, status, tenant_id, created_at)
		VALUES ($1, $2, $3, $4, $5, 'ACTIVE', $6, NOW())
		RETURNING id, name, description, owner_id, tier, status, tenant_id, created_at
	`

	app := &models.Application{}
	var description sql.NullString
	err := r.store.QueryRowContext(ctx, query, id, name, desc, ownerID, tier, tenantID).Scan(
		&app.ID, &app.Name, &description, &app.OwnerID, &app.Tier, &app.Status, &app.TenantID, &app.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert application: %w", err)
	}
	if description.Valid {
		d := description.String
		app.Description = &d
	}

	go r.events.PublishApplicationEvent(ctx, webhook.EventAppCreated, app)

	return app, nil
}

// ResolveSubscribe resolves the `subscribe` mutation field.
func (r *Resolver) ResolveSubscribe(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	apiID, _ := p.Args["apiId"].(string)
	appID, _ := p.Args["appId"].(string)
	tier := "Unlimited"
	if v, ok := p.Args["tier"].(string); ok && v != "" {
		tier = v
	}

	id := uuid.New().String()

	query := `
		INSERT INTO subscriptions (id, api_id, application_id, tier, status, tenant_id, created_at)
		VALUES ($1, $2, $3, $4, 'ACTIVE', $5, NOW())
		RETURNING id, api_id, application_id, tier, status, tenant_id, created_at
	`

	sub := &models.Subscription{}
	err := r.store.QueryRowContext(ctx, query, id, apiID, appID, tier, tenantID).Scan(
		&sub.ID, &sub.APIID, &sub.ApplicationID, &sub.Tier, &sub.Status, &sub.TenantID, &sub.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	// Load related entities
	sub.API, _ = r.getAPIByID(ctx, tenantID, apiID)
	sub.Application, _ = r.getApplicationByID(ctx, tenantID, appID)

	go r.auditor.LogSubscriptionAction(ctx, audit.ActionSubscriptionCreate, sub)
	go r.events.PublishSubscriptionEvent(ctx, webhook.EventSubscriptionCreated, sub)

	return sub, nil
}

// ResolveUnsubscribe resolves the `unsubscribe` mutation field.
func (r *Resolver) ResolveUnsubscribe(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)

	// Get subscription before deleting
	sub, _ := r.getSubscriptionByID(ctx, tenantID, id)

	query := `DELETE FROM subscriptions WHERE id = $1 AND tenant_id = $2`
	result, err := r.store.ExecContext(ctx, query, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("unsubscribe: %w", err)
	}

	rows, _ := result.RowsAffected()
	deleted := rows > 0

	if deleted && sub != nil {
		go r.auditor.LogSubscriptionAction(ctx, audit.ActionSubscriptionCancel, sub)
		go r.events.PublishSubscriptionEvent(ctx, webhook.EventSubscriptionCancelled, sub)
	}

	return deleted, nil
}

// ResolveCreateUser resolves the `createUser` mutation field.
func (r *Resolver) ResolveCreateUser(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	inputMap, _ := p.Args["input"].(map[string]interface{})
	username := safeString(inputMap["username"])
	email := safeString(inputMap["email"])
	roleStr := safeString(inputMap["role"])
	role := models.UserRole(roleStr)

	id := uuid.New().String()

	query := `
		INSERT INTO users (id, username, email, role, status, tenant_id, created_at)
		VALUES ($1, $2, $3, $4, 'ACTIVE', $5, NOW())
		RETURNING id, username, email, role, status, tenant_id, created_at
	`

	user := &models.User{}
	err := r.store.QueryRowContext(ctx, query, id, username, email, role, tenantID).Scan(
		&user.ID, &user.Username, &user.Email, &user.Role, &user.Status, &user.TenantID, &user.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	go r.events.PublishUserEvent(ctx, webhook.EventUserRegistered, user)

	return user, nil
}

// ResolveCreateThrottlePolicy resolves the `createThrottlePolicy` mutation field.
func (r *Resolver) ResolveCreateThrottlePolicy(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	inputMap, _ := p.Args["input"].(map[string]interface{})
	name := safeString(inputMap["name"])
	quotaType := safeString(inputMap["quotaType"])
	timeUnit := safeString(inputMap["timeUnit"])
	rateLimitUnit := safeString(inputMap["rateLimitUnit"])

	var desc *string
	if v, ok := inputMap["description"].(string); ok && v != "" {
		desc = &v
	}

	requestCount := int64(safeInt(inputMap["requestCount"]))
	rateLimitCount := int64(safeInt(inputMap["rateLimitCount"]))

	id := uuid.New().String()

	query := `
		INSERT INTO throttle_policies (id, name, description, quota_type, request_count, time_unit, rate_limit_count, rate_limit_unit, is_deployed, tenant_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false, $9, NOW(), NOW())
		RETURNING id, name, description, quota_type, request_count, time_unit, rate_limit_count, rate_limit_unit, is_deployed, tenant_id, created_at, updated_at
	`

	policy := &models.ThrottlePolicy{}
	var description sql.NullString
	err := r.store.QueryRowContext(ctx, query,
		id, name, desc, quotaType, requestCount, timeUnit, rateLimitCount, rateLimitUnit, tenantID,
	).Scan(
		&policy.ID, &policy.Name, &description, &policy.QuotaType, &policy.RequestCount,
		&policy.TimeUnit, &policy.RateLimitCount, &policy.RateLimitUnit,
		&policy.IsDeployed, &policy.TenantID, &policy.CreatedAt, &policy.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert throttle policy: %w", err)
	}
	if description.Valid {
		d := description.String
		policy.Description = &d
	}

	go r.events.PublishPolicyEvent(ctx, webhook.EventPolicyCreated, policy)

	return policy, nil
}

// ResolveCreateWebhook resolves the `createWebhook` mutation field.
func (r *Resolver) ResolveCreateWebhook(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)

	inputMap, _ := p.Args["input"].(map[string]interface{})
	name := safeString(inputMap["name"])
	callbackURL := safeString(inputMap["callbackUrl"])
	secret := safeString(inputMap["secret"])

	var apiID *string
	if v, ok := inputMap["apiId"].(string); ok && v != "" {
		apiID = &v
	}

	var eventTypes models.StringSlice
	if etList, ok := inputMap["eventTypes"].([]interface{}); ok {
		for _, et := range etList {
			if s, ok := et.(string); ok {
				eventTypes = append(eventTypes, s)
			}
		}
	}

	id := uuid.New().String()

	query := `
		INSERT INTO webhooks (id, api_id, name, callback_url, secret, event_types, active, tenant_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, $7, NOW(), NOW())
		RETURNING id, api_id, name, callback_url, secret, event_types, active, tenant_id, created_at, updated_at
	`

	webhook := &models.Webhook{}
	var apiIDNull sql.NullString
	err := r.store.QueryRowContext(ctx, query,
		id, apiID, name, callbackURL, secret, eventTypes, tenantID,
	).Scan(
		&webhook.ID, &apiIDNull, &webhook.Name, &webhook.CallbackURL,
		&webhook.Secret, &webhook.EventTypes, &webhook.Active,
		&webhook.TenantID, &webhook.CreatedAt, &webhook.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert webhook: %w", err)
	}
	if apiIDNull.Valid {
		s := apiIDNull.String
		webhook.APIID = &s
	}

	go r.auditor.LogAdminAction(ctx, audit.ActionWebhookCreate, map[string]interface{}{
		"webhook_id":   webhook.ID,
		"webhook_name": webhook.Name,
		"callback_url": webhook.CallbackURL,
		"event_types":  webhook.EventTypes,
	})

	return webhook, nil
}

// ResolveDeleteWebhook resolves the `deleteWebhook` mutation field.
func (r *Resolver) ResolveDeleteWebhook(p graphql.ResolveParams) (interface{}, error) {
	ctx := p.Context
	tenantID := tenant.TenantIDFromContext(ctx)
	id, _ := p.Args["id"].(string)

	query := `UPDATE webhooks SET active = false, updated_at = NOW() WHERE id = $1 AND tenant_id = $2`
	result, err := r.store.ExecContext(ctx, query, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("delete webhook: %w", err)
	}

	rows, _ := result.RowsAffected()
	deleted := rows > 0

	if deleted {
		go r.auditor.LogAdminAction(ctx, audit.ActionWebhookDelete, map[string]interface{}{
			"webhook_id": id,
		})
	}

	return deleted, nil
}

// ---- Helper Methods ----

// getAPIByID retrieves a single API by ID and tenant.
func (r *Resolver) getAPIByID(ctx context.Context, tenantID, id string) (*models.API, error) {
	query := `
		SELECT id, name, description, context, version, endpoint, auth_type, status, provider, tags, rating, tenant_id, created_at, updated_at
		FROM apis
		WHERE id = $1 AND tenant_id = $2
	`

	api := &models.API{}
	var rating sql.NullFloat64
	err := r.store.QueryRowContext(ctx, query, id, tenantID).Scan(
		&api.ID, &api.Name, &api.Description, &api.Context, &api.Version,
		&api.Endpoint, &api.AuthType, &api.Status, &api.Provider,
		&api.Tags, &rating, &api.TenantID, &api.CreatedAt, &api.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get API: %w", err)
	}
	if rating.Valid {
		r := rating.Float64
		api.Rating = &r
	}

	return api, nil
}

// getApplicationByID retrieves a single application by ID and tenant.
func (r *Resolver) getApplicationByID(ctx context.Context, tenantID, id string) (*models.Application, error) {
	query := `
		SELECT id, name, description, owner_id, tier, status, tenant_id, created_at
		FROM applications
		WHERE id = $1 AND tenant_id = $2
	`

	app := &models.Application{}
	var desc sql.NullString
	err := r.store.QueryRowContext(ctx, query, id, tenantID).Scan(
		&app.ID, &app.Name, &desc, &app.OwnerID, &app.Tier, &app.Status, &app.TenantID, &app.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get application: %w", err)
	}
	if desc.Valid {
		d := desc.String
		app.Description = &d
	}

	return app, nil
}

// getSubscriptionByID retrieves a single subscription by ID and tenant.
func (r *Resolver) getSubscriptionByID(ctx context.Context, tenantID, id string) (*models.Subscription, error) {
	query := `
		SELECT id, api_id, application_id, tier, status, tenant_id, created_at
		FROM subscriptions
		WHERE id = $1 AND tenant_id = $2
	`

	sub := &models.Subscription{}
	err := r.store.QueryRowContext(ctx, query, id, tenantID).Scan(
		&sub.ID, &sub.APIID, &sub.ApplicationID, &sub.Tier, &sub.Status, &sub.TenantID, &sub.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get subscription: %w", err)
	}

	return sub, nil
}

// queryAPIs executes an API query and scans the results.
func (r *Resolver) queryAPIs(ctx context.Context, query string, args ...interface{}) ([]*models.API, error) {
	rows, err := r.store.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query APIs: %w", err)
	}
	defer rows.Close()

	var apis []*models.API
	for rows.Next() {
		api := &models.API{}
		var rating sql.NullFloat64
		err := rows.Scan(
			&api.ID, &api.Name, &api.Description, &api.Context, &api.Version,
			&api.Endpoint, &api.AuthType, &api.Status, &api.Provider,
			&api.Tags, &rating, &api.TenantID, &api.CreatedAt, &api.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan API: %w", err)
		}
		if rating.Valid {
			r := rating.Float64
			api.Rating = &r
		}
		apis = append(apis, api)
	}

	return apis, rows.Err()
}

// scanSubscriptions scans subscription rows into models.
func (r *Resolver) scanSubscriptions(rows *sql.Rows) ([]*models.Subscription, error) {
	var subs []*models.Subscription
	for rows.Next() {
		sub := &models.Subscription{}
		err := rows.Scan(&sub.ID, &sub.APIID, &sub.ApplicationID, &sub.Tier, &sub.Status, &sub.TenantID, &sub.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// safeString safely extracts a string value from an interface{}.
func safeString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// safeInt safely extracts an int value from an interface{}.
func safeInt(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	default:
		return 0
	}
}

// parsePeriod converts a period string (e.g., "24h", "7d", "30d") to date range.
func parsePeriod(period string) (from interface{}, to interface{}) {
	now := "NOW()"
	switch strings.ToLower(period) {
	case "1h":
		return "NOW() - INTERVAL '1 hour'", now
	case "24h", "1d":
		return "NOW() - INTERVAL '24 hours'", now
	case "7d":
		return "NOW() - INTERVAL '7 days'", now
	case "30d":
		return "NOW() - INTERVAL '30 days'", now
	case "90d":
		return "NOW() - INTERVAL '90 days'", now
	case "1y":
		return "NOW() - INTERVAL '1 year'", now
	default:
		return "NOW() - INTERVAL '24 hours'", now
	}
}

// ---- NopEventPublisher is a no-op event publisher for testing ----

// NopEventPublisher is a no-op implementation of EventPublisher.
type NopEventPublisher struct{}

// Publish implements EventPublisher as a no-op.
func (n *NopEventPublisher) Publish(ctx context.Context, eventType string, payload map[string]interface{}) error {
	return nil
}

// PublishAPIEvent implements EventPublisher as a no-op.
func (n *NopEventPublisher) PublishAPIEvent(ctx context.Context, eventType string, api *models.API) error {
	return nil
}

// PublishSubscriptionEvent implements EventPublisher as a no-op.
func (n *NopEventPublisher) PublishSubscriptionEvent(ctx context.Context, eventType string, sub *models.Subscription) error {
	return nil
}

// PublishApplicationEvent implements EventPublisher as a no-op.
func (n *NopEventPublisher) PublishApplicationEvent(ctx context.Context, eventType string, app *models.Application) error {
	return nil
}

// PublishUserEvent implements EventPublisher as a no-op.
func (n *NopEventPublisher) PublishUserEvent(ctx context.Context, eventType string, user *models.User) error {
	return nil
}

// PublishPolicyEvent implements EventPublisher as a no-op.
func (n *NopEventPublisher) PublishPolicyEvent(ctx context.Context, eventType string, policy *models.ThrottlePolicy) error {
	return nil
}

// Ensure NopEventPublisher implements EventPublisher
var _ webhook.EventPublisher = (*NopEventPublisher)(nil)
