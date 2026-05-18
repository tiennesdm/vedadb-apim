// Package store provides the complete database abstraction layer for VedaDB API Manager.
// All CRUD operations use real SQL queries sent via the VedaDB wire protocol (JSON over TCP).
package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vedadb/vapim/pkg/errors"
	"github.com/vedadb/vapim/pkg/models"
)

// ---------------------------------------------------------------------------
// Store interface
// ---------------------------------------------------------------------------

// Store defines the complete database contract for VAPIM.
type Store interface {
	// Tenants
	CreateTenant(t *models.TenantDB) error
	GetTenant(id string) (*models.TenantDB, error)
	GetTenantByDomain(domain string) (*models.TenantDB, error)
	ListTenants(limit, offset int) ([]*models.TenantDB, error)

	// Users
	CreateUser(u *models.UserDB) error
	GetUser(id string) (*models.UserDB, error)
	GetUserByEmail(email string) (*models.UserDB, error)
	GetUserByUsername(tenantID, username string) (*models.UserDB, error)
	ListUsers(tenantID string, limit, offset int) ([]*models.UserDB, error)
	UpdateUser(u *models.UserDB) error
	DeleteUser(id string) error

	// APIs
	CreateAPI(api *models.APIDB) error
	GetAPI(id string) (*models.APIDB, error)
	GetAPIByContext(tenantID, context, version string) (*models.APIDB, error)
	ListAPIs(tenantID string, status string, limit, offset int) ([]*models.APIDB, int, error)
	SearchAPIs(tenantID, query string, limit, offset int) ([]*models.APIDB, int, error)
	UpdateAPI(api *models.APIDB) error
	DeleteAPI(id string) error
	UpdateAPIStatus(id, status string) error
	ListPublishedAPIs(tenantID string, limit, offset int) ([]*models.APIDB, int, error)

	// API Resources
	CreateResource(r *models.APIResourceDB) error
	GetResourcesByAPI(apiID string) ([]*models.APIResourceDB, error)
	DeleteResource(id string) error

	// API Versions
	CreateVersion(v *models.APIVersionDB) error
	GetVersionsByAPI(apiID string) ([]*models.APIVersionDB, error)
	SetDefaultVersion(apiID, versionID string) error

	// Applications
	CreateApp(app *models.ApplicationDB) error
	GetApp(id string) (*models.ApplicationDB, error)
	ListAppsByOwner(ownerID string, limit, offset int) ([]*models.ApplicationDB, error)
	ListAppsByTenant(tenantID string, limit, offset int) ([]*models.ApplicationDB, int, error)
	UpdateApp(app *models.ApplicationDB) error
	DeleteApp(id string) error

	// Application Keys
	CreateAppKey(key *models.ApplicationKeyDB) error
	GetAppKeys(appID string) ([]*models.ApplicationKeyDB, error)
	GetAppKeyByConsumerKey(consumerKey string) (*models.ApplicationKeyDB, error)
	UpdateAppKeyStatus(id, status string) error

	// Subscriptions
	CreateSubscription(s *models.SubscriptionDB) error
	GetSubscription(id string) (*models.SubscriptionDB, error)
	ListSubscriptionsByApp(appID string) ([]*models.SubscriptionDB, error)
	ListSubscriptionsByAPI(apiID string) ([]*models.SubscriptionDB, error)
	ListSubscriptionsByTenant(tenantID string, limit, offset int) ([]*models.SubscriptionDB, int, error)
	UpdateSubscriptionStatus(id, status string) error
	DeleteSubscription(id string) error
	ValidateSubscription(apiID, appID string) (bool, error)

	// OAuth2
	CreateOAuth2Client(c *models.OAuth2ClientDB) error
	GetOAuth2Client(clientID string) (*models.OAuth2ClientDB, error)
	ValidateClientCredentials(clientID, clientSecret string) (*models.OAuth2ClientDB, error)

	// Tokens
	StoreToken(t *models.TokenDB) error
	GetTokenByAccessToken(token string) (*models.TokenDB, error)
	RevokeToken(token string) error
	RevokeTokensByClient(clientID string) error

	// API Keys
	CreateAPIKey(key *models.APIKeyDB) error
	GetAPIKeyByHash(hash string) (*models.APIKeyDB, error)
	UpdateAPIKeyLastUsed(id string) error
	RevokeAPIKey(id string) error

	// Throttle Policies
	CreateThrottlePolicy(p *models.ThrottlePolicyDB) error
	GetThrottlePolicy(id string) (*models.ThrottlePolicyDB, error)
	GetThrottlePolicyByName(tenantID, name string) (*models.ThrottlePolicyDB, error)
	ListThrottlePolicies(tenantID string) ([]*models.ThrottlePolicyDB, error)
	UpdateThrottlePolicy(p *models.ThrottlePolicyDB) error
	DeleteThrottlePolicy(id string) error

	// Analytics
	InsertAnalyticsEvent(e *models.AnalyticsEventDB) error
	GetAnalyticsSummary(tenantID, apiID, period string, start, end time.Time) ([]*models.AnalyticsSummaryDB, error)
	GetTopAPIs(tenantID string, limit int) ([]*models.APISummary, error)
	GetAPIUsage(tenantID, apiID string, start, end time.Time) ([]*models.UsageDataPoint, error)

	// Audit
	InsertAuditLog(entry *models.AuditLogDB) error
	GetAuditLogs(tenantID string, limit, offset int) ([]*models.AuditLogDB, int, error)

	// Webhooks
	CreateWebhook(w *models.WebhookDB) error
	GetWebhooksByAPI(apiID string) ([]*models.WebhookDB, error)
	ListWebhooks(tenantID string) ([]*models.WebhookDB, error)
	DeleteWebhook(id string) error

	// Webhook Deliveries
	CreateWebhookDelivery(d *models.WebhookDeliveryDB) error
	UpdateWebhookDelivery(d *models.WebhookDeliveryDB) error

	// Throttle Counters (for distributed rate limiting)
	IncrementThrottleCounter(key string, window time.Time) error
	GetThrottleCounter(key string, window time.Time) (int, error)
	ResetThrottleCounter(key string) error

	// API Mocks
	CreateMock(m *models.APIMockDB) error
	GetMock(apiID, method, path string) (*models.APIMockDB, error)
	GetMockByID(id string) (*models.APIMockDB, error)
	ListMocks(apiID string) ([]*models.APIMockDB, error)
	UpdateMock(m *models.APIMockDB) error
	DeleteMock(id string) error
	DeleteAllMocksForAPI(apiID string) error

	// API Changelog
	InsertChangelog(entry *models.APIChangeDB) error
	GetChangelog(apiID string) ([]*models.APIChangeDB, error)

	// Raw query execution (for analytics aggregation, custom queries)
	RawQuery(query string, args ...interface{}) ([]json.RawMessage, error)
	Exec(query string, args ...interface{}) error

	// Request/Response Schemas
	CreateSchema(schema *models.APISchemaDB) error
	GetSchema(resourceID, schemaType string) (*models.APISchemaDB, error)

	// Migrations
	RunMigration(name, sql string) error
	GetAppliedMigrations() ([]string, error)

	// Connection management
	Ping(ctx context.Context) error
	Close() error
}

// ---------------------------------------------------------------------------
// VedaDB wire protocol types
// ---------------------------------------------------------------------------

type vedadbRequest struct {
	Type   string        `json:"type"`
	Query  string        `json:"query"`
	Params []interface{} `json:"params,omitempty"`
}

type vedadbResponse struct {
	Status   string          `json:"status"`
	Rows     []rowWrapper    `json:"rows,omitempty"`
	RowCount int             `json:"row_count,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// rowWrapper wraps json.RawMessage for row scanning.
type rowWrapper struct {
	data json.RawMessage
}

func (r *rowWrapper) UnmarshalJSON(data []byte) error {
	r.data = data
	return nil
}

func (r *rowWrapper) MarshalJSON() ([]byte, error) {
	return r.data, nil
}

// VedaDBStore is the concrete Store implementation using the VedaDB wire protocol.
type VedaDBStore struct {
	addr       string
	database   string
	timeout    time.Duration
	maxRetries int
	pool       chan *vedaConn
	poolSize   int
	closed     bool
	mu         sync.Mutex
	// The migrator table is managed by the migrator package;
	// we only run migrations here.
}

type vedaConn struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	mu     sync.Mutex
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// New creates a new VedaDB store with default configuration.
func New(host string, port int, database string) *VedaDBStore {
	return &VedaDBStore{
		addr:       fmt.Sprintf("%s:%d", host, port),
		database:   database,
		timeout:    10 * time.Second,
		maxRetries: 3,
		poolSize:   10,
		pool:       make(chan *vedaConn, 10),
	}
}

// WithTimeout sets the operation timeout.
func (s *VedaDBStore) WithTimeout(d time.Duration) *VedaDBStore {
	s.timeout = d
	return s
}

// WithPoolSize sets the connection pool size.
func (s *VedaDBStore) WithPoolSize(size int) *VedaDBStore {
	s.poolSize = size
	s.pool = make(chan *vedaConn, size)
	return s
}

// WithMaxRetries sets the max retry count.
func (s *VedaDBStore) WithMaxRetries(n int) *VedaDBStore {
	s.maxRetries = n
	return s
}

// Connect initializes the connection pool.
func (s *VedaDBStore) Connect(ctx context.Context) error {
	for i := 0; i < s.poolSize; i++ {
		conn, err := s.createConnection()
		if err != nil {
			return fmt.Errorf("create connection %d: %w", i, err)
		}
		s.pool <- conn
	}
	return s.Ping(ctx)
}

// Close shuts down all connections.
func (s *VedaDBStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	close(s.pool)
	for conn := range s.pool {
		conn.conn.Close()
	}
	return nil
}

// Ping checks connectivity.
func (s *VedaDBStore) Ping(ctx context.Context) error {
	_, err := s.exec(ctx, "SELECT 1")
	return err
}

// ---------------------------------------------------------------------------
// Internal: connection management
// ---------------------------------------------------------------------------

func (s *VedaDBStore) createConnection() (*vedaConn, error) {
	conn, err := net.DialTimeout("tcp", s.addr, s.timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", s.addr, err)
	}
	return &vedaConn{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
	}, nil
}

func (s *VedaDBStore) acquireConn() (*vedaConn, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.ErrDatabaseError.WithMessage("store is closed")
	}
	s.mu.Unlock()

	select {
	case conn := <-s.pool:
		return conn, nil
	case <-time.After(s.timeout):
		return nil, errors.ErrDatabaseError.WithMessage("connection pool exhausted")
	}
}

func (s *VedaDBStore) releaseConn(conn *vedaConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		conn.conn.Close()
		return
	}
	select {
	case s.pool <- conn:
	default:
		conn.conn.Close()
	}
}

// ---------------------------------------------------------------------------
// Internal: query execution
// ---------------------------------------------------------------------------

func (s *VedaDBStore) exec(ctx context.Context, query string, args ...interface{}) (*vedadbResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond * time.Duration(attempt))
		}

		conn, err := s.acquireConn()
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := s.execWithConn(ctx, conn, query, args)
		s.releaseConn(conn)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, errors.ErrDatabaseError.WithCause(fmt.Errorf("after %d attempts: %w", s.maxRetries+1, lastErr))
}

func (s *VedaDBStore) execWithConn(ctx context.Context, conn *vedaConn, query string, args []interface{}) (*vedadbResponse, error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	deadline := time.Now().Add(s.timeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	conn.conn.SetDeadline(deadline)
	defer conn.conn.SetDeadline(time.Time{})

	req := vedadbRequest{
		Type:   "query",
		Query:  query,
		Params: args,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	data = append(data, '\n')
	if _, err := conn.writer.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	if err := conn.writer.Flush(); err != nil {
		return nil, fmt.Errorf("flush request: %w", err)
	}

	line, err := conn.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp vedadbResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Status != "ok" {
		return &resp, fmt.Errorf("vedadb error: %s", resp.Error)
	}
	return &resp, nil
}

// queryOne executes a query that returns a single row, scanning into dest.
func (s *VedaDBStore) queryOne(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	resp, err := s.exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if resp.RowCount == 0 {
		return sql.ErrNoRows
	}
	if len(resp.Rows) == 0 {
		return sql.ErrNoRows
	}
	if err := json.Unmarshal(resp.Rows[0].data, dest); err != nil {
		return fmt.Errorf("unmarshal row: %w", err)
	}
	return nil
}

// queryMany executes a query and returns all rows as raw JSON.
func (s *VedaDBStore) queryMany(ctx context.Context, query string, args ...interface{}) ([]json.RawMessage, *vedadbResponse, error) {
	resp, err := s.exec(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	raws := make([]json.RawMessage, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		raws = append(raws, r.data)
	}
	return raws, resp, nil
}

// ---------------------------------------------------------------------------
// Tenants
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateTenant(t *models.TenantDB) error {
	q := `INSERT INTO tenants (id, name, domain, tier, status) VALUES (?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, t.ID, t.Name, t.Domain, t.Tier, t.Status)
	return err
}

func (s *VedaDBStore) GetTenant(id string) (*models.TenantDB, error) {
	var t models.TenantDB
	q := `SELECT id, name, domain, tier, status, created_at, updated_at FROM tenants WHERE id = ?`
	if err := s.queryOne(context.Background(), &t, q, id); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *VedaDBStore) GetTenantByDomain(domain string) (*models.TenantDB, error) {
	var t models.TenantDB
	q := `SELECT id, name, domain, tier, status, created_at, updated_at FROM tenants WHERE domain = ?`
	if err := s.queryOne(context.Background(), &t, q, domain); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *VedaDBStore) ListTenants(limit, offset int) ([]*models.TenantDB, error) {
	q := `SELECT id, name, domain, tier, status, created_at, updated_at FROM tenants ORDER BY created_at DESC LIMIT ? OFFSET ?`
	raws, _, err := s.queryMany(context.Background(), q, limit, offset)
	if err != nil {
		return nil, err
	}
	tenants := make([]*models.TenantDB, 0, len(raws))
	for _, raw := range raws {
		var t models.TenantDB
		if err := json.Unmarshal(raw, &t); err != nil {
			continue
		}
		tenants = append(tenants, &t)
	}
	return tenants, nil
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateUser(u *models.UserDB) error {
	q := `INSERT INTO users (id, tenant_id, username, email, password_hash, role, status) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, u.ID, u.TenantID, u.Username, u.Email, u.PasswordHash, u.Role, u.Status)
	return err
}

func (s *VedaDBStore) GetUser(id string) (*models.UserDB, error) {
	var u models.UserDB
	q := `SELECT id, tenant_id, username, email, password_hash, role, status, created_at, updated_at FROM users WHERE id = ?`
	if err := s.queryOne(context.Background(), &u, q, id); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *VedaDBStore) GetUserByEmail(email string) (*models.UserDB, error) {
	var u models.UserDB
	q := `SELECT id, tenant_id, username, email, password_hash, role, status, created_at, updated_at FROM users WHERE email = ?`
	if err := s.queryOne(context.Background(), &u, q, email); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *VedaDBStore) GetUserByUsername(tenantID, username string) (*models.UserDB, error) {
	var u models.UserDB
	q := `SELECT id, tenant_id, username, email, password_hash, role, status, created_at, updated_at FROM users WHERE tenant_id = ? AND username = ?`
	if err := s.queryOne(context.Background(), &u, q, tenantID, username); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *VedaDBStore) ListUsers(tenantID string, limit, offset int) ([]*models.UserDB, error) {
	q := `SELECT id, tenant_id, username, email, password_hash, role, status, created_at, updated_at FROM users WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`
	raws, _, err := s.queryMany(context.Background(), q, tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	users := make([]*models.UserDB, 0, len(raws))
	for _, raw := range raws {
		var u models.UserDB
		if err := json.Unmarshal(raw, &u); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

func (s *VedaDBStore) UpdateUser(u *models.UserDB) error {
	q := `UPDATE users SET username = ?, email = ?, password_hash = ?, role = ?, status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, u.Username, u.Email, u.PasswordHash, u.Role, u.Status, u.ID)
	return err
}

func (s *VedaDBStore) DeleteUser(id string) error {
	q := `DELETE FROM users WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

// ---------------------------------------------------------------------------
// APIs
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateAPI(api *models.APIDB) error {
	q := `INSERT INTO apis (id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, api.ID, api.TenantID, api.Name, api.Description, api.Context, api.Version, api.Endpoint, api.AuthType, api.Status, api.Provider, api.Tags, api.ThumbnailURL, api.Rating, api.RatingCount, api.Visibility, api.ThrottlePolicy)
	return err
}

func (s *VedaDBStore) GetAPI(id string) (*models.APIDB, error) {
	var a models.APIDB
	q := `SELECT id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy, created_at, updated_at FROM apis WHERE id = ?`
	if err := s.queryOne(context.Background(), &a, q, id); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *VedaDBStore) GetAPIByContext(tenantID, ctx, version string) (*models.APIDB, error) {
	var a models.APIDB
	q := `SELECT id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy, created_at, updated_at FROM apis WHERE tenant_id = ? AND context = ? AND version = ?`
	if err := s.queryOne(context.Background(), &a, q, tenantID, ctx, version); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *VedaDBStore) ListAPIs(tenantID string, status string, limit, offset int) ([]*models.APIDB, int, error) {
	ctx := context.Background()
	var q string
	var args []interface{}

	if status != "" {
		q = `SELECT id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy, created_at, updated_at FROM apis WHERE tenant_id = ? AND status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`
		args = []interface{}{tenantID, status, limit, offset}
	} else {
		q = `SELECT id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy, created_at, updated_at FROM apis WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`
		args = []interface{}{tenantID, limit, offset}
	}

	raws, _, err := s.queryMany(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}

	apis := make([]*models.APIDB, 0, len(raws))
	for _, raw := range raws {
		var a models.APIDB
		if err := json.Unmarshal(raw, &a); err != nil {
			continue
		}
		apis = append(apis, &a)
	}

	// Count total
	var countQ string
	var countArgs []interface{}
	if status != "" {
		countQ = `SELECT COUNT(*) as count FROM apis WHERE tenant_id = ? AND status = ?`
		countArgs = []interface{}{tenantID, status}
	} else {
		countQ = `SELECT COUNT(*) as count FROM apis WHERE tenant_id = ?`
		countArgs = []interface{}{tenantID}
	}

	var countRow struct {
		Count int `json:"count"`
	}
	if err := s.queryOne(ctx, &countRow, countQ, countArgs...); err != nil {
		// If count fails, just return 0
		return apis, len(apis), nil
	}

	return apis, countRow.Count, nil
}

func (s *VedaDBStore) SearchAPIs(tenantID, query string, limit, offset int) ([]*models.APIDB, int, error) {
	ctx := context.Background()
	searchPattern := "%" + query + "%"

	q := `SELECT id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy, created_at, updated_at FROM apis WHERE tenant_id = ? AND (name LIKE ? OR description LIKE ? OR context LIKE ? OR tags LIKE ?) ORDER BY created_at DESC LIMIT ? OFFSET ?`
	raws, _, err := s.queryMany(ctx, q, tenantID, searchPattern, searchPattern, searchPattern, searchPattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	apis := make([]*models.APIDB, 0, len(raws))
	for _, raw := range raws {
		var a models.APIDB
		if err := json.Unmarshal(raw, &a); err != nil {
			continue
		}
		apis = append(apis, &a)
	}

	// Count
	countQ := `SELECT COUNT(*) as count FROM apis WHERE tenant_id = ? AND (name LIKE ? OR description LIKE ? OR context LIKE ? OR tags LIKE ?)`
	var countRow struct {
		Count int `json:"count"`
	}
	if err := s.queryOne(ctx, &countRow, countQ, tenantID, searchPattern, searchPattern, searchPattern, searchPattern); err != nil {
		return apis, len(apis), nil
	}

	return apis, countRow.Count, nil
}

func (s *VedaDBStore) UpdateAPI(api *models.APIDB) error {
	q := `UPDATE apis SET name = ?, description = ?, context = ?, version = ?, endpoint = ?, auth_type = ?, status = ?, provider = ?, tags = ?, thumbnail_url = ?, rating = ?, rating_count = ?, visibility = ?, throttle_policy = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, api.Name, api.Description, api.Context, api.Version, api.Endpoint, api.AuthType, api.Status, api.Provider, api.Tags, api.ThumbnailURL, api.Rating, api.RatingCount, api.Visibility, api.ThrottlePolicy, api.ID)
	return err
}

func (s *VedaDBStore) DeleteAPI(id string) error {
	q := `DELETE FROM apis WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

func (s *VedaDBStore) UpdateAPIStatus(id, status string) error {
	q := `UPDATE apis SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, status, id)
	return err
}

func (s *VedaDBStore) ListPublishedAPIs(tenantID string, limit, offset int) ([]*models.APIDB, int, error) {
	return s.ListAPIs(tenantID, "PUBLISHED", limit, offset)
}

// ---------------------------------------------------------------------------
// API Resources
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateResource(r *models.APIResourceDB) error {
	q := `INSERT INTO api_resources (id, api_id, method, path, description, auth_required, throttle_policy) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, r.ID, r.APIID, r.Method, r.Path, r.Description, r.AuthRequired, r.ThrottlePolicy)
	return err
}

func (s *VedaDBStore) GetResourcesByAPI(apiID string) ([]*models.APIResourceDB, error) {
	q := `SELECT id, api_id, method, path, description, auth_required, throttle_policy, created_at FROM api_resources WHERE api_id = ? ORDER BY path, method`
	raws, _, err := s.queryMany(context.Background(), q, apiID)
	if err != nil {
		return nil, err
	}
	resources := make([]*models.APIResourceDB, 0, len(raws))
	for _, raw := range raws {
		var r models.APIResourceDB
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		resources = append(resources, &r)
	}
	return resources, nil
}

func (s *VedaDBStore) DeleteResource(id string) error {
	q := `DELETE FROM api_resources WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

// ---------------------------------------------------------------------------
// API Versions
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateVersion(v *models.APIVersionDB) error {
	q := `INSERT INTO api_versions (id, api_id, version, definition, status, is_default) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, v.ID, v.APIID, v.Version, v.Definition, v.Status, v.IsDefault)
	return err
}

func (s *VedaDBStore) GetVersionsByAPI(apiID string) ([]*models.APIVersionDB, error) {
	q := `SELECT id, api_id, version, definition, status, is_default, created_at FROM api_versions WHERE api_id = ? ORDER BY created_at DESC`
	raws, _, err := s.queryMany(context.Background(), q, apiID)
	if err != nil {
		return nil, err
	}
	versions := make([]*models.APIVersionDB, 0, len(raws))
	for _, raw := range raws {
		var v models.APIVersionDB
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		versions = append(versions, &v)
	}
	return versions, nil
}

func (s *VedaDBStore) SetDefaultVersion(apiID, versionID string) error {
	// First unset any existing default
	q1 := `UPDATE api_versions SET is_default = false WHERE api_id = ?`
	_, _ = s.exec(context.Background(), q1, apiID) // ignore error if none set

	// Then set the new default
	q2 := `UPDATE api_versions SET is_default = true WHERE id = ? AND api_id = ?`
	_, err := s.exec(context.Background(), q2, versionID, apiID)
	return err
}

// ---------------------------------------------------------------------------
// Applications
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateApp(app *models.ApplicationDB) error {
	q := `INSERT INTO applications (id, tenant_id, name, description, owner_id, tier, status, callback_url) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, app.ID, app.TenantID, app.Name, app.Description, app.OwnerID, app.Tier, app.Status, app.CallbackURL)
	return err
}

func (s *VedaDBStore) GetApp(id string) (*models.ApplicationDB, error) {
	var a models.ApplicationDB
	q := `SELECT id, tenant_id, name, description, owner_id, tier, status, callback_url, created_at, updated_at FROM applications WHERE id = ?`
	if err := s.queryOne(context.Background(), &a, q, id); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *VedaDBStore) ListAppsByOwner(ownerID string, limit, offset int) ([]*models.ApplicationDB, error) {
	q := `SELECT id, tenant_id, name, description, owner_id, tier, status, callback_url, created_at, updated_at FROM applications WHERE owner_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`
	raws, _, err := s.queryMany(context.Background(), q, ownerID, limit, offset)
	if err != nil {
		return nil, err
	}
	apps := make([]*models.ApplicationDB, 0, len(raws))
	for _, raw := range raws {
		var a models.ApplicationDB
		if err := json.Unmarshal(raw, &a); err != nil {
			continue
		}
		apps = append(apps, &a)
	}
	return apps, nil
}

func (s *VedaDBStore) ListAppsByTenant(tenantID string, limit, offset int) ([]*models.ApplicationDB, int, error) {
	ctx := context.Background()
	q := `SELECT id, tenant_id, name, description, owner_id, tier, status, callback_url, created_at, updated_at FROM applications WHERE tenant_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`
	raws, _, err := s.queryMany(ctx, q, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	apps := make([]*models.ApplicationDB, 0, len(raws))
	for _, raw := range raws {
		var a models.ApplicationDB
		if err := json.Unmarshal(raw, &a); err != nil {
			continue
		}
		apps = append(apps, &a)
	}

	// Count
	var countRow struct {
		Count int `json:"count"`
	}
	if err := s.queryOne(ctx, &countRow, `SELECT COUNT(*) as count FROM applications WHERE tenant_id = ?`, tenantID); err != nil {
		return apps, len(apps), nil
	}
	return apps, countRow.Count, nil
}

func (s *VedaDBStore) UpdateApp(app *models.ApplicationDB) error {
	q := `UPDATE applications SET name = ?, description = ?, owner_id = ?, tier = ?, status = ?, callback_url = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, app.Name, app.Description, app.OwnerID, app.Tier, app.Status, app.CallbackURL, app.ID)
	return err
}

func (s *VedaDBStore) DeleteApp(id string) error {
	q := `DELETE FROM applications WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

// ---------------------------------------------------------------------------
// Application Keys
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateAppKey(key *models.ApplicationKeyDB) error {
	q := `INSERT INTO application_keys (id, app_id, key_type, consumer_key, consumer_secret, status, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, key.ID, key.AppID, key.KeyType, key.ConsumerKey, key.ConsumerSecret, key.Status, key.ExpiresAt)
	return err
}

func (s *VedaDBStore) GetAppKeys(appID string) ([]*models.ApplicationKeyDB, error) {
	q := `SELECT id, app_id, key_type, consumer_key, consumer_secret, status, expires_at, created_at FROM application_keys WHERE app_id = ? ORDER BY created_at DESC`
	raws, _, err := s.queryMany(context.Background(), q, appID)
	if err != nil {
		return nil, err
	}
	keys := make([]*models.ApplicationKeyDB, 0, len(raws))
	for _, raw := range raws {
		var k models.ApplicationKeyDB
		if err := json.Unmarshal(raw, &k); err != nil {
			continue
		}
		keys = append(keys, &k)
	}
	return keys, nil
}

func (s *VedaDBStore) GetAppKeyByConsumerKey(consumerKey string) (*models.ApplicationKeyDB, error) {
	var k models.ApplicationKeyDB
	q := `SELECT id, app_id, key_type, consumer_key, consumer_secret, status, expires_at, created_at FROM application_keys WHERE consumer_key = ?`
	if err := s.queryOne(context.Background(), &k, q, consumerKey); err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *VedaDBStore) UpdateAppKeyStatus(id, status string) error {
	q := `UPDATE application_keys SET status = ? WHERE id = ?`
	_, err := s.exec(context.Background(), q, status, id)
	return err
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateSubscription(sub *models.SubscriptionDB) error {
	q := `INSERT INTO subscriptions (id, api_id, app_id, tier, status) VALUES (?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, sub.ID, sub.APIID, sub.AppID, sub.Tier, sub.Status)
	return err
}

func (s *VedaDBStore) GetSubscription(id string) (*models.SubscriptionDB, error) {
	var sub models.SubscriptionDB
	q := `SELECT id, api_id, app_id, tier, status, created_at, updated_at FROM subscriptions WHERE id = ?`
	if err := s.queryOne(context.Background(), &sub, q, id); err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *VedaDBStore) ListSubscriptionsByApp(appID string) ([]*models.SubscriptionDB, error) {
	q := `SELECT id, api_id, app_id, tier, status, created_at, updated_at FROM subscriptions WHERE app_id = ? ORDER BY created_at DESC`
	raws, _, err := s.queryMany(context.Background(), q, appID)
	if err != nil {
		return nil, err
	}
	subs := make([]*models.SubscriptionDB, 0, len(raws))
	for _, raw := range raws {
		var sub models.SubscriptionDB
		if err := json.Unmarshal(raw, &sub); err != nil {
			continue
		}
		subs = append(subs, &sub)
	}
	return subs, nil
}

func (s *VedaDBStore) ListSubscriptionsByAPI(apiID string) ([]*models.SubscriptionDB, error) {
	q := `SELECT id, api_id, app_id, tier, status, created_at, updated_at FROM subscriptions WHERE api_id = ? ORDER BY created_at DESC`
	raws, _, err := s.queryMany(context.Background(), q, apiID)
	if err != nil {
		return nil, err
	}
	subs := make([]*models.SubscriptionDB, 0, len(raws))
	for _, raw := range raws {
		var sub models.SubscriptionDB
		if err := json.Unmarshal(raw, &sub); err != nil {
			continue
		}
		subs = append(subs, &sub)
	}
	return subs, nil
}

func (s *VedaDBStore) ListSubscriptionsByTenant(tenantID string, limit, offset int) ([]*models.SubscriptionDB, int, error) {
	ctx := context.Background()
	q := `SELECT s.id, s.api_id, s.app_id, s.tier, s.status, s.created_at, s.updated_at FROM subscriptions s JOIN apis a ON s.api_id = a.id WHERE a.tenant_id = ? ORDER BY s.created_at DESC LIMIT ? OFFSET ?`
	raws, _, err := s.queryMany(ctx, q, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	subs := make([]*models.SubscriptionDB, 0, len(raws))
	for _, raw := range raws {
		var sub models.SubscriptionDB
		if err := json.Unmarshal(raw, &sub); err != nil {
			continue
		}
		subs = append(subs, &sub)
	}

	var countRow struct {
		Count int `json:"count"`
	}
	if err := s.queryOne(ctx, &countRow, `SELECT COUNT(*) as count FROM subscriptions s JOIN apis a ON s.api_id = a.id WHERE a.tenant_id = ?`, tenantID); err != nil {
		return subs, len(subs), nil
	}
	return subs, countRow.Count, nil
}

func (s *VedaDBStore) UpdateSubscriptionStatus(id, status string) error {
	q := `UPDATE subscriptions SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, status, id)
	return err
}

func (s *VedaDBStore) DeleteSubscription(id string) error {
	q := `DELETE FROM subscriptions WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

func (s *VedaDBStore) ValidateSubscription(apiID, appID string) (bool, error) {
	q := `SELECT id FROM subscriptions WHERE api_id = ? AND app_id = ? AND status = 'active'`
	var row struct {
		ID string `json:"id"`
	}
	if err := s.queryOne(context.Background(), &row, q, apiID, appID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// OAuth2 Clients
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateOAuth2Client(c *models.OAuth2ClientDB) error {
	q := `INSERT INTO oauth2_clients (id, tenant_id, client_id, client_secret, name, redirect_uris, grant_types, scopes, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, c.ID, c.TenantID, c.ClientID, c.ClientSecret, c.Name, c.RedirectURIs, c.GrantTypes, c.Scopes, c.Status)
	return err
}

func (s *VedaDBStore) GetOAuth2Client(clientID string) (*models.OAuth2ClientDB, error) {
	var c models.OAuth2ClientDB
	q := `SELECT id, tenant_id, client_id, client_secret, name, redirect_uris, grant_types, scopes, status, created_at FROM oauth2_clients WHERE client_id = ?`
	if err := s.queryOne(context.Background(), &c, q, clientID); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *VedaDBStore) ValidateClientCredentials(clientID, clientSecret string) (*models.OAuth2ClientDB, error) {
	var c models.OAuth2ClientDB
	q := `SELECT id, tenant_id, client_id, client_secret, name, redirect_uris, grant_types, scopes, status, created_at FROM oauth2_clients WHERE client_id = ? AND client_secret = ? AND status = 'active'`
	if err := s.queryOne(context.Background(), &c, q, clientID, clientSecret); err != nil {
		return nil, err
	}
	return &c, nil
}

// ---------------------------------------------------------------------------
// Tokens
// ---------------------------------------------------------------------------

func (s *VedaDBStore) StoreToken(t *models.TokenDB) error {
	q := `INSERT INTO tokens (id, token, token_type, client_id, user_id, scopes, expires_at, revoked) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, t.ID, t.Token, t.TokenType, t.ClientID, t.UserID, t.Scopes, t.ExpiresAt, t.Revoked)
	return err
}

func (s *VedaDBStore) GetTokenByAccessToken(token string) (*models.TokenDB, error) {
	var t models.TokenDB
	q := `SELECT id, token, token_type, client_id, user_id, scopes, expires_at, revoked, created_at FROM tokens WHERE token = ? AND revoked = false AND expires_at > CURRENT_TIMESTAMP`
	if err := s.queryOne(context.Background(), &t, q, token); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *VedaDBStore) RevokeToken(token string) error {
	q := `UPDATE tokens SET revoked = true WHERE token = ?`
	_, err := s.exec(context.Background(), q, token)
	return err
}

func (s *VedaDBStore) RevokeTokensByClient(clientID string) error {
	q := `UPDATE tokens SET revoked = true WHERE client_id = ?`
	_, err := s.exec(context.Background(), q, clientID)
	return err
}

// ---------------------------------------------------------------------------
// API Keys
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateAPIKey(key *models.APIKeyDB) error {
	q := `INSERT INTO api_keys (id, app_id, key_hash, name, scopes, status, expires_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, key.ID, key.AppID, key.KeyHash, key.Name, key.Scopes, key.Status, key.ExpiresAt, key.LastUsedAt)
	return err
}

func (s *VedaDBStore) GetAPIKeyByHash(hash string) (*models.APIKeyDB, error) {
	var k models.APIKeyDB
	q := `SELECT id, app_id, key_hash, name, scopes, status, expires_at, last_used_at, created_at FROM api_keys WHERE key_hash = ? AND status = 'active' AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`
	if err := s.queryOne(context.Background(), &k, q, hash); err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *VedaDBStore) UpdateAPIKeyLastUsed(id string) error {
	q := `UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

func (s *VedaDBStore) RevokeAPIKey(id string) error {
	q := `UPDATE api_keys SET status = 'revoked' WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

// ---------------------------------------------------------------------------
// Throttle Policies
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateThrottlePolicy(p *models.ThrottlePolicyDB) error {
	q := `INSERT INTO throttle_policies (id, tenant_id, name, type, rate, burst, unit, conditions) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, p.ID, p.TenantID, p.Name, p.Type, p.Rate, p.Burst, p.Unit, p.Conditions)
	return err
}

func (s *VedaDBStore) GetThrottlePolicy(id string) (*models.ThrottlePolicyDB, error) {
	var p models.ThrottlePolicyDB
	q := `SELECT id, tenant_id, name, type, rate, burst, unit, conditions, created_at, updated_at FROM throttle_policies WHERE id = ?`
	if err := s.queryOne(context.Background(), &p, q, id); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *VedaDBStore) GetThrottlePolicyByName(tenantID, name string) (*models.ThrottlePolicyDB, error) {
	var p models.ThrottlePolicyDB
	q := `SELECT id, tenant_id, name, type, rate, burst, unit, conditions, created_at, updated_at FROM throttle_policies WHERE tenant_id = ? AND name = ?`
	if err := s.queryOne(context.Background(), &p, q, tenantID, name); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *VedaDBStore) ListThrottlePolicies(tenantID string) ([]*models.ThrottlePolicyDB, error) {
	q := `SELECT id, tenant_id, name, type, rate, burst, unit, conditions, created_at, updated_at FROM throttle_policies WHERE tenant_id = ? ORDER BY name`
	raws, _, err := s.queryMany(context.Background(), q, tenantID)
	if err != nil {
		return nil, err
	}
	policies := make([]*models.ThrottlePolicyDB, 0, len(raws))
	for _, raw := range raws {
		var p models.ThrottlePolicyDB
		if err := json.Unmarshal(raw, &p); err != nil {
			continue
		}
		policies = append(policies, &p)
	}
	return policies, nil
}

func (s *VedaDBStore) UpdateThrottlePolicy(p *models.ThrottlePolicyDB) error {
	q := `UPDATE throttle_policies SET name = ?, type = ?, rate = ?, burst = ?, unit = ?, conditions = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, p.Name, p.Type, p.Rate, p.Burst, p.Unit, p.Conditions, p.ID)
	return err
}

func (s *VedaDBStore) DeleteThrottlePolicy(id string) error {
	q := `DELETE FROM throttle_policies WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

// ---------------------------------------------------------------------------
// Analytics
// ---------------------------------------------------------------------------

func (s *VedaDBStore) InsertAnalyticsEvent(e *models.AnalyticsEventDB) error {
	q := `INSERT INTO analytics_events (id, tenant_id, request_id, api_id, app_id, user_id, method, path, status_code, latency_ms, error_message, user_agent, client_ip, country) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, e.ID, e.TenantID, e.RequestID, e.APIID, e.AppID, e.UserID, e.Method, e.Path, e.StatusCode, e.LatencyMs, e.ErrorMessage, e.UserAgent, e.ClientIP, e.Country)
	return err
}

func (s *VedaDBStore) GetAnalyticsSummary(tenantID, apiID, period string, start, end time.Time) ([]*models.AnalyticsSummaryDB, error) {
	q := `SELECT id, tenant_id, api_id, period, period_start, request_count, error_count, avg_latency_ms, p95_latency_ms, p99_latency_ms, unique_users, created_at FROM analytics_summary WHERE tenant_id = ? AND api_id = ? AND period = ? AND period_start >= ? AND period_start <= ? ORDER BY period_start DESC`
	raws, _, err := s.queryMany(context.Background(), q, tenantID, apiID, period, start.Format(time.RFC3339), end.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	summaries := make([]*models.AnalyticsSummaryDB, 0, len(raws))
	for _, raw := range raws {
		var sum models.AnalyticsSummaryDB
		if err := json.Unmarshal(raw, &sum); err != nil {
			continue
		}
		summaries = append(summaries, &sum)
	}
	return summaries, nil
}

func (s *VedaDBStore) GetTopAPIs(tenantID string, limit int) ([]*models.APISummary, error) {
	q := `SELECT api_id, COUNT(*) as request_count, SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) as error_count, AVG(latency_ms) as avg_latency FROM analytics_events WHERE tenant_id = ? GROUP BY api_id ORDER BY request_count DESC LIMIT ?`
	raws, _, err := s.queryMany(context.Background(), q, tenantID, limit)
	if err != nil {
		return nil, err
	}
	summaries := make([]*models.APISummary, 0, len(raws))
	for _, raw := range raws {
		var sum models.APISummary
		if err := json.Unmarshal(raw, &sum); err != nil {
			// Try alternative parsing for numeric types that may come as strings
			var alt struct {
				APIID        string `json:"api_id"`
				RequestCount string `json:"request_count"`
				ErrorCount   string `json:"error_count"`
				AvgLatency   string `json:"avg_latency"`
			}
			if err2 := json.Unmarshal(raw, &alt); err2 == nil {
				sum.APIID = alt.APIID
				sum.RequestCount, _ = strconv.ParseInt(alt.RequestCount, 10, 64)
				sum.ErrorCount, _ = strconv.ParseInt(alt.ErrorCount, 10, 64)
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

func (s *VedaDBStore) GetAPIUsage(tenantID, apiID string, start, end time.Time) ([]*models.UsageDataPoint, error) {
	q := `SELECT DATE(timestamp) as date, COUNT(*) as request_count, SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) as error_count, AVG(latency_ms) as avg_latency_ms FROM analytics_events WHERE tenant_id = ? AND api_id = ? AND timestamp >= ? AND timestamp <= ? GROUP BY DATE(timestamp) ORDER BY date ASC`
	raws, _, err := s.queryMany(context.Background(), q, tenantID, apiID, start.Format(time.RFC3339), end.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	points := make([]*models.UsageDataPoint, 0, len(raws))
	for _, raw := range raws {
		var dp models.UsageDataPoint
		if err := json.Unmarshal(raw, &dp); err != nil {
			// Try alternative parsing
			var alt struct {
				Timestamp    string `json:"date"`
				RequestCount string `json:"request_count"`
				ErrorCount   string `json:"error_count"`
				AvgLatencyMs string `json:"avg_latency_ms"`
			}
			if err2 := json.Unmarshal(raw, &alt); err2 == nil {
				dp.Timestamp, _ = time.Parse("2006-01-02", alt.Timestamp)
				dp.RequestCount, _ = strconv.ParseInt(alt.RequestCount, 10, 64)
				dp.ErrorCount, _ = strconv.ParseInt(alt.ErrorCount, 10, 64)
				avg, _ := strconv.ParseFloat(alt.AvgLatencyMs, 64)
				dp.AvgLatencyMs = int64(avg)
			} else {
				continue
			}
		}
		points = append(points, &dp)
	}
	return points, nil
}

// ---------------------------------------------------------------------------
// Audit Log
// ---------------------------------------------------------------------------

func (s *VedaDBStore) InsertAuditLog(entry *models.AuditLogDB) error {
	q := `INSERT INTO audit_log (id, tenant_id, user_id, action, resource_type, resource_id, details, ip_address, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, entry.ID, entry.TenantID, entry.UserID, entry.Action, entry.ResourceType, entry.ResourceID, entry.Details, entry.IPAddress, entry.UserAgent)
	return err
}

func (s *VedaDBStore) GetAuditLogs(tenantID string, limit, offset int) ([]*models.AuditLogDB, int, error) {
	ctx := context.Background()
	q := `SELECT id, tenant_id, user_id, action, resource_type, resource_id, details, ip_address, user_agent, timestamp FROM audit_log WHERE tenant_id = ? ORDER BY timestamp DESC LIMIT ? OFFSET ?`
	raws, _, err := s.queryMany(ctx, q, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	entries := make([]*models.AuditLogDB, 0, len(raws))
	for _, raw := range raws {
		var e models.AuditLogDB
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		entries = append(entries, &e)
	}

	var countRow struct {
		Count int `json:"count"`
	}
	if err := s.queryOne(ctx, &countRow, `SELECT COUNT(*) as count FROM audit_log WHERE tenant_id = ?`, tenantID); err != nil {
		return entries, len(entries), nil
	}
	return entries, countRow.Count, nil
}

// ---------------------------------------------------------------------------
// Webhooks
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateWebhook(w *models.WebhookDB) error {
	q := `INSERT INTO webhooks (id, tenant_id, api_id, url, events, secret, status) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, w.ID, w.TenantID, w.APIID, w.URL, w.Events, w.Secret, w.Status)
	return err
}

func (s *VedaDBStore) GetWebhooksByAPI(apiID string) ([]*models.WebhookDB, error) {
	q := `SELECT id, tenant_id, api_id, url, events, secret, status, created_at, updated_at FROM webhooks WHERE api_id = ? AND status = 'active' ORDER BY created_at DESC`
	raws, _, err := s.queryMany(context.Background(), q, apiID)
	if err != nil {
		return nil, err
	}
	webhooks := make([]*models.WebhookDB, 0, len(raws))
	for _, raw := range raws {
		var w models.WebhookDB
		if err := json.Unmarshal(raw, &w); err != nil {
			continue
		}
		webhooks = append(webhooks, &w)
	}
	return webhooks, nil
}

func (s *VedaDBStore) ListWebhooks(tenantID string) ([]*models.WebhookDB, error) {
	q := `SELECT id, tenant_id, api_id, url, events, secret, status, created_at, updated_at FROM webhooks WHERE tenant_id = ? ORDER BY created_at DESC`
	raws, _, err := s.queryMany(context.Background(), q, tenantID)
	if err != nil {
		return nil, err
	}
	webhooks := make([]*models.WebhookDB, 0, len(raws))
	for _, raw := range raws {
		var w models.WebhookDB
		if err := json.Unmarshal(raw, &w); err != nil {
			continue
		}
		webhooks = append(webhooks, &w)
	}
	return webhooks, nil
}

func (s *VedaDBStore) DeleteWebhook(id string) error {
	q := `DELETE FROM webhooks WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

// ---------------------------------------------------------------------------
// Webhook Deliveries
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateWebhookDelivery(d *models.WebhookDeliveryDB) error {
	q := `INSERT INTO webhook_deliveries (id, webhook_id, event_type, payload, response_status, response_body, attempt_count, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q,
		d.ID, d.WebhookID, d.EventType, d.Payload, d.ResponseStatus, d.ResponseBody,
		d.AttemptCount, d.Status, d.CreatedAt)
	return err
}

func (s *VedaDBStore) UpdateWebhookDelivery(d *models.WebhookDeliveryDB) error {
	q := `UPDATE webhook_deliveries SET response_status = ?, response_body = ?, attempt_count = ?, status = ? WHERE id = ?`
	_, err := s.exec(context.Background(), q,
		d.ResponseStatus, d.ResponseBody, d.AttemptCount, d.Status, d.ID)
	return err
}

// ---------------------------------------------------------------------------
// Throttle Counters (for distributed rate limiting)
// ---------------------------------------------------------------------------

func (s *VedaDBStore) IncrementThrottleCounter(key string, window time.Time) error {
	q := `INSERT INTO throttle_counters (id, counter_key, window_start, count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT (counter_key, window_start) DO UPDATE SET count = count + 1, updated_at = ?`
	_, err := s.exec(context.Background(), q, uuid.New().String(), key, window, time.Now())
	return err
}

func (s *VedaDBStore) GetThrottleCounter(key string, window time.Time) (int, error) {
	var row struct {
		Count int `json:"count"`
	}
	q := `SELECT count FROM throttle_counters WHERE counter_key = ? AND window_start = ?`
	if err := s.queryOne(context.Background(), &row, q, key, window); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return row.Count, nil
}

// ---------------------------------------------------------------------------
// API Mocks
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateMock(m *models.APIMockDB) error {
	q := `INSERT INTO api_mocks (id, api_id, method, path, status_code, headers, body, delay_ms, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active')`
	_, err := s.exec(context.Background(), q, m.ID, m.APIID, m.Method, m.Path, m.StatusCode, m.Headers, m.Body, m.DelayMS)
	return err
}

func (s *VedaDBStore) GetMock(apiID, method, path string) (*models.APIMockDB, error) {
	var m models.APIMockDB
	q := `SELECT id, api_id, method, path, status_code, headers, body, delay_ms
		FROM api_mocks WHERE api_id = ? AND method = ? AND path = ? AND status = 'active'`
	if err := s.queryOne(context.Background(), &m, q, apiID, method, path); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *VedaDBStore) ListMocks(apiID string) ([]*models.APIMockDB, error) {
	q := `SELECT id, api_id, method, path, status_code, headers, body, delay_ms
		FROM api_mocks WHERE api_id = ? AND status = 'active'`
	raws, _, err := s.queryMany(context.Background(), q, apiID)
	if err != nil {
		return nil, err
	}
	mocks := make([]*models.APIMockDB, 0, len(raws))
	for _, raw := range raws {
		var m models.APIMockDB
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		mocks = append(mocks, &m)
	}
	return mocks, nil
}

func (s *VedaDBStore) GetMockByID(id string) (*models.APIMockDB, error) {
	var m models.APIMockDB
	q := `SELECT id, api_id, method, path, status_code, headers, body, delay_ms, status, created_at
		FROM api_mocks WHERE id = ?`
	if err := s.queryOne(context.Background(), &m, q, id); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *VedaDBStore) UpdateMock(m *models.APIMockDB) error {
	q := `UPDATE api_mocks SET status_code = ?, headers = ?, body = ?, delay_ms = ?, status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := s.exec(context.Background(), q, m.StatusCode, m.Headers, m.Body, m.DelayMS, m.Status, m.ID)
	return err
}

func (s *VedaDBStore) DeleteMock(id string) error {
	q := `DELETE FROM api_mocks WHERE id = ?`
	_, err := s.exec(context.Background(), q, id)
	return err
}

func (s *VedaDBStore) DeleteAllMocksForAPI(apiID string) error {
	q := `DELETE FROM api_mocks WHERE api_id = ?`
	_, err := s.exec(context.Background(), q, apiID)
	return err
}

func (s *VedaDBStore) ResetThrottleCounter(key string) error {
	q := `DELETE FROM throttle_counters WHERE counter_key = ?`
	_, err := s.exec(context.Background(), q, key)
	return err
}

// ---------------------------------------------------------------------------
// API Changelog
// ---------------------------------------------------------------------------

func (s *VedaDBStore) InsertChangelog(entry *models.APIChangeDB) error {
	q := `INSERT INTO api_changelog (id, api_id, change_type, description, changed_by, changed_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, entry.ID, entry.APIID, entry.ChangeType, entry.Description, entry.ChangedBy, entry.ChangedAt)
	return err
}

func (s *VedaDBStore) GetChangelog(apiID string) ([]*models.APIChangeDB, error) {
	q := `SELECT id, api_id, change_type, description, changed_by, changed_at
		FROM api_changelog WHERE api_id = ? ORDER BY changed_at DESC`
	raws, _, err := s.queryMany(context.Background(), q, apiID)
	if err != nil {
		return nil, err
	}
	changes := make([]*models.APIChangeDB, 0, len(raws))
	for _, raw := range raws {
		var c models.APIChangeDB
		if err := json.Unmarshal(raw, &c); err != nil {
			continue
		}
		changes = append(changes, &c)
	}
	return changes, nil
}

// ---------------------------------------------------------------------------
// Request/Response Schemas
// ---------------------------------------------------------------------------

func (s *VedaDBStore) CreateSchema(schema *models.APISchemaDB) error {
	q := `INSERT INTO api_schemas (id, resource_id, schema_type, schema_json) VALUES (?, ?, ?, ?)`
	_, err := s.exec(context.Background(), q, schema.ID, schema.ResourceID, schema.SchemaType, schema.SchemaJSON)
	return err
}

func (s *VedaDBStore) GetSchema(resourceID, schemaType string) (*models.APISchemaDB, error) {
	var sch models.APISchemaDB
	q := `SELECT id, resource_id, schema_type, schema_json
		FROM api_schemas WHERE resource_id = ? AND schema_type = ?`
	if err := s.queryOne(context.Background(), &sch, q, resourceID, schemaType); err != nil {
		return nil, err
	}
	return &sch, nil
}

// ---------------------------------------------------------------------------
// Migrations
// ---------------------------------------------------------------------------

func (s *VedaDBStore) RunMigration(name, sql string) error {
	statements := splitMigrationStatements(sql)
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		_, err := s.exec(context.Background(), stmt)
		if err != nil {
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "already exists") || strings.Contains(errMsg, "duplicate") {
				continue
			}
			return fmt.Errorf("migration statement failed: %w", err)
		}
	}
	// Record as applied
	_, err := s.exec(context.Background(), "INSERT OR IGNORE INTO schema_migrations (name) VALUES (?)", name)
	return err
}

func (s *VedaDBStore) GetAppliedMigrations() ([]string, error) {
	q := `SELECT name FROM schema_migrations ORDER BY id ASC`
	raws, _, err := s.queryMany(context.Background(), q)
	if err != nil {
		// If migrations table doesn't exist, return empty
		if strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "not found") {
			return []string{}, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(raws))
	for _, raw := range raws {
		var r struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		names = append(names, r.Name)
	}
	return names, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func splitMigrationStatements(sql string) []string {
	var statements []string
	var buf strings.Builder
	inString := false
	stringChar := rune(0)
	escaped := false

	for _, ch := range sql {
		if escaped {
			buf.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			buf.WriteRune(ch)
			escaped = true
			continue
		}
		if inString {
			buf.WriteRune(ch)
			if ch == stringChar {
				inString = false
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			inString = true
			stringChar = ch
			buf.WriteRune(ch)
			continue
		}
		if ch == ';' {
			buf.WriteRune(ch)
			statements = append(statements, buf.String())
			buf.Reset()
			continue
		}
		buf.WriteRune(ch)
	}

	remaining := strings.TrimSpace(buf.String())
	if remaining != "" {
		statements = append(statements, remaining)
	}
	return statements
}

// ---------------------------------------------------------------------------
// Raw query execution (exported for analytics, custom queries)
// ---------------------------------------------------------------------------

// RawQuery executes a raw SQL query and returns the rows as raw JSON messages.
func (s *VedaDBStore) RawQuery(query string, args ...interface{}) ([]json.RawMessage, error) {
	raws, _, err := s.queryMany(context.Background(), query, args...)
	return raws, err
}

// Exec executes a raw SQL statement (INSERT, UPDATE, DELETE) and returns an error if any.
func (s *VedaDBStore) Exec(query string, args ...interface{}) error {
	_, err := s.exec(context.Background(), query, args...)
	return err
}
