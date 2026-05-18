// Package store provides the VedaDB (multi-model database) client and CRUD operations
// for all entities in the VedaDB API Manager. VedaDB is accessed via TCP at localhost:6380
// using a JSON-based protocol.
package store

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/vedadb/vapim/pkg/models"
)

const (
	// defaultTimeout for VedaDB operations.
	defaultTimeout = 10 * time.Second
	// defaultMaxRetries for transient failures.
	defaultMaxRetries = 3
	// retryBackoff base delay between retries.
	retryBackoff = 100 * time.Millisecond
)

// VedaDBProtocol defines the JSON protocol commands sent to VedaDB.
type VedaDBProtocol struct {
	Command   string          `json:"cmd"`
	Namespace string          `json:"ns,omitempty"`
	Key       string          `json:"key,omitempty"`
	Value     json.RawMessage `json:"val,omitempty"`
	Query     string          `json:"q,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	RequestID string          `json:"rid,omitempty"`
}

// VedaDBResponse is the response from VedaDB.
type VedaDBResponse struct {
	Status  string          `json:"status"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Count   int64           `json:"count,omitempty"`
	RequestID string        `json:"rid,omitempty"`
}

// VedaDBClient manages the connection to VedaDB.
type VedaDBClient struct {
	addr     string
	database string
	timeout  time.Duration
	maxRetries int

	// Connection pool
	pool     chan *vedaConn
	poolSize int
	poolMu   sync.Mutex
	closed   int32

	healthCheckInterval time.Duration
	healthStatus        int32 // 0 = unhealthy, 1 = healthy

	mu sync.RWMutex
}

type vedaConn struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	mu     sync.Mutex
	inUse  int32
}

// NewVedaDBClient creates a new VedaDB client with the given configuration.
func NewVedaDBClient(host string, port int, database string) *VedaDBClient {
	return &VedaDBClient{
		addr:                fmt.Sprintf("%s:%d", host, port),
		database:            database,
		timeout:             defaultTimeout,
		maxRetries:          defaultMaxRetries,
		poolSize:            10,
		pool:                make(chan *vedaConn, 10),
		healthCheckInterval: 30 * time.Second,
		healthStatus:        0,
	}
}

// WithTimeout sets the operation timeout.
func (c *VedaDBClient) WithTimeout(d time.Duration) *VedaDBClient {
	c.timeout = d
	return c
}

// WithPoolSize sets the connection pool size.
func (c *VedaDBClient) WithPoolSize(size int) *VedaDBClient {
	c.poolSize = size
	c.pool = make(chan *vedaConn, size)
	return c
}

// Connect establishes the connection pool and verifies connectivity to VedaDB.
func (c *VedaDBClient) Connect(ctx context.Context) error {
	// Create initial connections
	for i := 0; i < c.poolSize; i++ {
		conn, err := c.createConnection()
		if err != nil {
			return fmt.Errorf("failed to create connection %d: %w", i, err)
		}
		c.pool <- conn
	}

	// Perform health check
	if err := c.Ping(ctx); err != nil {
		return fmt.Errorf("vedadb health check failed: %w", err)
	}

	atomic.StoreInt32(&c.healthStatus, 1)

	// Start background health checker
	go c.healthChecker()

	return nil
}

// Close closes all connections in the pool.
func (c *VedaDBClient) Close() error {
	atomic.StoreInt32(&c.closed, 1)
	close(c.pool)
	for conn := range c.pool {
		conn.conn.Close()
	}
	return nil
}

// IsHealthy returns true if the VedaDB connection is healthy.
func (c *VedaDBClient) IsHealthy() bool {
	return atomic.LoadInt32(&c.healthStatus) == 1
}

// Ping sends a ping command to VedaDB to verify connectivity.
func (c *VedaDBClient) Ping(ctx context.Context) error {
	resp, err := c.execute(ctx, VedaDBProtocol{
		Command:   "PING",
		RequestID: uuid.New().String(),
	})
	if err != nil {
		return err
	}
	if resp.Status != "OK" {
		return fmt.Errorf("vedadb ping failed: %s", resp.Error)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Generic CRUD Operations
// ---------------------------------------------------------------------------

// Set stores a value in VedaDB with the given namespace and key.
func (c *VedaDBClient) Set(ctx context.Context, namespace, key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}

	resp, err := c.execute(ctx, VedaDBProtocol{
		Command:   "SET",
		Namespace: namespace,
		Key:       key,
		Value:     data,
		RequestID: uuid.New().String(),
	})
	if err != nil {
		return err
	}
	if resp.Status != "OK" {
		return fmt.Errorf("vedadb SET failed: %s", resp.Error)
	}
	return nil
}

// Get retrieves a value from VedaDB by namespace and key.
func (c *VedaDBClient) Get(ctx context.Context, namespace, key string, dest interface{}) error {
	resp, err := c.execute(ctx, VedaDBProtocol{
		Command:   "GET",
		Namespace: namespace,
		Key:       key,
		RequestID: uuid.New().String(),
	})
	if err != nil {
		return err
	}
	if resp.Status != "OK" {
		if resp.Error == "not found" || resp.Error == "key not found" {
			return fmt.Errorf("key not found: %s/%s", namespace, key)
		}
		return fmt.Errorf("vedadb GET failed: %s", resp.Error)
	}
	return json.Unmarshal(resp.Data, dest)
}

// Delete removes a value from VedaDB by namespace and key.
func (c *VedaDBClient) Delete(ctx context.Context, namespace, key string) error {
	resp, err := c.execute(ctx, VedaDBProtocol{
		Command:   "DEL",
		Namespace: namespace,
		Key:       key,
		RequestID: uuid.New().String(),
	})
	if err != nil {
		return err
	}
	if resp.Status != "OK" {
		return fmt.Errorf("vedadb DEL failed: %s", resp.Error)
	}
	return nil
}

// List retrieves all keys in a namespace.
func (c *VedaDBClient) List(ctx context.Context, namespace string) ([]string, error) {
	resp, err := c.execute(ctx, VedaDBProtocol{
		Command:   "LIST",
		Namespace: namespace,
		RequestID: uuid.New().String(),
	})
	if err != nil {
		return nil, err
	}
	if resp.Status != "OK" {
		return nil, fmt.Errorf("vedadb LIST failed: %s", resp.Error)
	}
	var keys []string
	if err := json.Unmarshal(resp.Data, &keys); err != nil {
		return nil, fmt.Errorf("unmarshal list result: %w", err)
	}
	return keys, nil
}

// Query executes a query against VedaDB.
func (c *VedaDBClient) Query(ctx context.Context, namespace, query string, params interface{}) (json.RawMessage, error) {
	var paramData json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal query params: %w", err)
		}
		paramData = data
	}

	resp, err := c.execute(ctx, VedaDBProtocol{
		Command:   "QUERY",
		Namespace: namespace,
		Query:     query,
		Params:    paramData,
		RequestID: uuid.New().String(),
	})
	if err != nil {
		return nil, err
	}
	if resp.Status != "OK" {
		return nil, fmt.Errorf("vedadb QUERY failed: %s", resp.Error)
	}
	return resp.Data, nil
}

// ---------------------------------------------------------------------------
// API CRUD
// ---------------------------------------------------------------------------

const nsAPIs = "apis"

// StoreAPI stores an API in VedaDB.
func (c *VedaDBClient) StoreAPI(ctx context.Context, api *models.API) error {
	if api.ID == uuid.Nil {
		api.ID = uuid.New()
	}
	now := time.Now()
	if api.CreatedAt.IsZero() {
		api.CreatedAt = now
	}
	api.UpdatedAt = now
	return c.Set(ctx, nsAPIs, api.ID.String(), api)
}

// GetAPI retrieves an API by ID.
func (c *VedaDBClient) GetAPI(ctx context.Context, id uuid.UUID) (*models.API, error) {
	var api models.API
	if err := c.Get(ctx, nsAPIs, id.String(), &api); err != nil {
		return nil, err
	}
	return &api, nil
}

// GetAPIByContext retrieves an API by its context path.
func (c *VedaDBClient) GetAPIByContext(ctx context.Context, context string) (*models.API, error) {
	keys, err := c.List(ctx, nsAPIs)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		var api models.API
		if err := c.Get(ctx, nsAPIs, key, &api); err != nil {
			continue
		}
		if api.Context == context {
			return &api, nil
		}
	}
	return nil, fmt.Errorf("api with context %s not found", context)
}

// DeleteAPI removes an API by ID.
func (c *VedaDBClient) DeleteAPI(ctx context.Context, id uuid.UUID) error {
	return c.Delete(ctx, nsAPIs, id.String())
}

// ListAPIs retrieves all APIs with optional filtering.
func (c *VedaDBClient) ListAPIs(ctx context.Context) ([]*models.API, error) {
	keys, err := c.List(ctx, nsAPIs)
	if err != nil {
		return nil, err
	}
	apis := make([]*models.API, 0, len(keys))
	for _, key := range keys {
		var api models.API
		if err := c.Get(ctx, nsAPIs, key, &api); err != nil {
			continue
		}
		apis = append(apis, &api)
	}
	return apis, nil
}

// ---------------------------------------------------------------------------
// Application CRUD
// ---------------------------------------------------------------------------

const nsApps = "applications"

// StoreApplication stores an application in VedaDB.
func (c *VedaDBClient) StoreApplication(ctx context.Context, app *models.Application) error {
	if app.ID == uuid.Nil {
		app.ID = uuid.New()
	}
	now := time.Now()
	if app.CreatedAt.IsZero() {
		app.CreatedAt = now
	}
	app.UpdatedAt = now
	return c.Set(ctx, nsApps, app.ID.String(), app)
}

// GetApplication retrieves an application by ID.
func (c *VedaDBClient) GetApplication(ctx context.Context, id uuid.UUID) (*models.Application, error) {
	var app models.Application
	if err := c.Get(ctx, nsApps, id.String(), &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// GetApplicationByName retrieves an application by name.
func (c *VedaDBClient) GetApplicationByName(ctx context.Context, name string) (*models.Application, error) {
	keys, err := c.List(ctx, nsApps)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		var app models.Application
		if err := c.Get(ctx, nsApps, key, &app); err != nil {
			continue
		}
		if app.Name == name {
			return &app, nil
		}
	}
	return nil, fmt.Errorf("application with name %s not found", name)
}

// DeleteApplication removes an application by ID.
func (c *VedaDBClient) DeleteApplication(ctx context.Context, id uuid.UUID) error {
	return c.Delete(ctx, nsApps, id.String())
}

// ListApplications retrieves all applications.
func (c *VedaDBClient) ListApplications(ctx context.Context) ([]*models.Application, error) {
	keys, err := c.List(ctx, nsApps)
	if err != nil {
		return nil, err
	}
	apps := make([]*models.Application, 0, len(keys))
	for _, key := range keys {
		var app models.Application
		if err := c.Get(ctx, nsApps, key, &app); err != nil {
			continue
		}
		apps = append(apps, &app)
	}
	return apps, nil
}

// ---------------------------------------------------------------------------
// Subscription CRUD
// ---------------------------------------------------------------------------

const nsSubs = "subscriptions"

// StoreSubscription stores a subscription in VedaDB.
func (c *VedaDBClient) StoreSubscription(ctx context.Context, sub *models.Subscription) error {
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}
	now := time.Now()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	sub.UpdatedAt = now
	return c.Set(ctx, nsSubs, sub.ID.String(), sub)
}

// GetSubscription retrieves a subscription by ID.
func (c *VedaDBClient) GetSubscription(ctx context.Context, id uuid.UUID) (*models.Subscription, error) {
	var sub models.Subscription
	if err := c.Get(ctx, nsSubs, id.String(), &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// GetSubscriptionsByApp retrieves all subscriptions for an application.
func (c *VedaDBClient) GetSubscriptionsByApp(ctx context.Context, appID uuid.UUID) ([]*models.Subscription, error) {
	keys, err := c.List(ctx, nsSubs)
	if err != nil {
		return nil, err
	}
	subs := make([]*models.Subscription, 0)
	for _, key := range keys {
		var sub models.Subscription
		if err := c.Get(ctx, nsSubs, key, &sub); err != nil {
			continue
		}
		if sub.AppID == appID {
			subs = append(subs, &sub)
		}
	}
	return subs, nil
}

// GetSubscriptionByAPIAndApp retrieves a subscription by API and Application.
func (c *VedaDBClient) GetSubscriptionByAPIAndApp(ctx context.Context, apiID, appID uuid.UUID) (*models.Subscription, error) {
	keys, err := c.List(ctx, nsSubs)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		var sub models.Subscription
		if err := c.Get(ctx, nsSubs, key, &sub); err != nil {
			continue
		}
		if sub.APIID == apiID && sub.AppID == appID {
			return &sub, nil
		}
	}
	return nil, fmt.Errorf("subscription not found for api %s and app %s", apiID, appID)
}

// DeleteSubscription removes a subscription by ID.
func (c *VedaDBClient) DeleteSubscription(ctx context.Context, id uuid.UUID) error {
	return c.Delete(ctx, nsSubs, id.String())
}

// ---------------------------------------------------------------------------
// User CRUD
// ---------------------------------------------------------------------------

const nsUsers = "users"

// StoreUser stores a user in VedaDB.
func (c *VedaDBClient) StoreUser(ctx context.Context, user *models.User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	now := time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now
	return c.Set(ctx, nsUsers, user.ID.String(), user)
}

// GetUser retrieves a user by ID.
func (c *VedaDBClient) GetUser(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := c.Get(ctx, nsUsers, id.String(), &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByUsername retrieves a user by username.
func (c *VedaDBClient) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	keys, err := c.List(ctx, nsUsers)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		var user models.User
		if err := c.Get(ctx, nsUsers, key, &user); err != nil {
			continue
		}
		if user.Username == username {
			return &user, nil
		}
	}
	return nil, fmt.Errorf("user with username %s not found", username)
}

// GetUserByEmail retrieves a user by email.
func (c *VedaDBClient) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	keys, err := c.List(ctx, nsUsers)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		var user models.User
		if err := c.Get(ctx, nsUsers, key, &user); err != nil {
			continue
		}
		if user.Email == email {
			return &user, nil
		}
	}
	return nil, fmt.Errorf("user with email %s not found", email)
}

// DeleteUser removes a user by ID.
func (c *VedaDBClient) DeleteUser(ctx context.Context, id uuid.UUID) error {
	return c.Delete(ctx, nsUsers, id.String())
}

// ---------------------------------------------------------------------------
// Token CRUD
// ---------------------------------------------------------------------------

const nsTokens = "tokens"

// StoreToken stores a token in VedaDB.
func (c *VedaDBClient) StoreToken(ctx context.Context, token *models.Token) error {
	if token.ID == uuid.Nil {
		token.ID = uuid.New()
	}
	if token.IssuedAt.IsZero() {
		token.IssuedAt = time.Now()
	}
	return c.Set(ctx, nsTokens, token.ID.String(), token)
}

// GetToken retrieves a token by ID.
func (c *VedaDBClient) GetToken(ctx context.Context, id uuid.UUID) (*models.Token, error) {
	var token models.Token
	if err := c.Get(ctx, nsTokens, id.String(), &token); err != nil {
		return nil, err
	}
	return &token, nil
}

// GetTokenByAccessToken retrieves a token by its access token string.
func (c *VedaDBClient) GetTokenByAccessToken(ctx context.Context, accessToken string) (*models.Token, error) {
	keys, err := c.List(ctx, nsTokens)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		var token models.Token
		if err := c.Get(ctx, nsTokens, key, &token); err != nil {
			continue
		}
		if token.AccessToken == accessToken {
			return &token, nil
		}
	}
	return nil, fmt.Errorf("token not found")
}

// RevokeToken marks a token as revoked.
func (c *VedaDBClient) RevokeToken(ctx context.Context, id uuid.UUID) error {
	token, err := c.GetToken(ctx, id)
	if err != nil {
		return err
	}
	token.Revoked = true
	token.RevokedAt = time.Now()
	return c.StoreToken(ctx, token)
}

// ---------------------------------------------------------------------------
// Throttle Policy CRUD
// ---------------------------------------------------------------------------

const nsPolicies = "policies"

// StorePolicy stores a throttle policy in VedaDB.
func (c *VedaDBClient) StorePolicy(ctx context.Context, policy *models.ThrottlePolicy) error {
	if policy.ID == uuid.Nil {
		policy.ID = uuid.New()
	}
	now := time.Now()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	return c.Set(ctx, nsPolicies, policy.ID.String(), policy)
}

// GetPolicy retrieves a throttle policy by ID.
func (c *VedaDBClient) GetPolicy(ctx context.Context, id uuid.UUID) (*models.ThrottlePolicy, error) {
	var policy models.ThrottlePolicy
	if err := c.Get(ctx, nsPolicies, id.String(), &policy); err != nil {
		return nil, err
	}
	return &policy, nil
}

// GetPolicyByName retrieves a throttle policy by name.
func (c *VedaDBClient) GetPolicyByName(ctx context.Context, name string) (*models.ThrottlePolicy, error) {
	keys, err := c.List(ctx, nsPolicies)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		var policy models.ThrottlePolicy
		if err := c.Get(ctx, nsPolicies, key, &policy); err != nil {
			continue
		}
		if policy.Name == name {
			return &policy, nil
		}
	}
	return nil, fmt.Errorf("policy with name %s not found", name)
}

// ListPolicies retrieves all throttle policies.
func (c *VedaDBClient) ListPolicies(ctx context.Context) ([]*models.ThrottlePolicy, error) {
	keys, err := c.List(ctx, nsPolicies)
	if err != nil {
		return nil, err
	}
	policies := make([]*models.ThrottlePolicy, 0, len(keys))
	for _, key := range keys {
		var policy models.ThrottlePolicy
		if err := c.Get(ctx, nsPolicies, key, &policy); err != nil {
			continue
		}
		policies = append(policies, &policy)
	}
	return policies, nil
}

// ---------------------------------------------------------------------------
// Analytics CRUD
// ---------------------------------------------------------------------------

const nsAnalytics = "analytics"

// StoreAnalyticsEvent stores an analytics event in VedaDB.
func (c *VedaDBClient) StoreAnalyticsEvent(ctx context.Context, event *models.AnalyticsEvent) error {
	key := fmt.Sprintf("%s_%d", event.RequestID, event.Timestamp.UnixNano())
	return c.Set(ctx, nsAnalytics, key, event)
}

// QueryAnalytics executes an analytics query.
func (c *VedaDBClient) QueryAnalytics(ctx context.Context, query string) (json.RawMessage, error) {
	return c.Query(ctx, nsAnalytics, query, nil)
}

// ---------------------------------------------------------------------------
// Internal connection management
// ---------------------------------------------------------------------------

func (c *VedaDBClient) createConnection() (*vedaConn, error) {
	conn, err := net.DialTimeout("tcp", c.addr, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.addr, err)
	}
	return &vedaConn{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
	}, nil
}

func (c *VedaDBClient) acquireConn() (*vedaConn, error) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return nil, fmt.Errorf("client is closed")
	}
	select {
	case conn := <-c.pool:
		atomic.StoreInt32(&conn.inUse, 1)
		return conn, nil
	case <-time.After(c.timeout):
		return nil, fmt.Errorf("connection pool exhausted")
	}
}

func (c *VedaDBClient) releaseConn(conn *vedaConn) {
	atomic.StoreInt32(&conn.inUse, 0)
	if atomic.LoadInt32(&c.closed) == 1 {
		conn.conn.Close()
		return
	}
	select {
	case c.pool <- conn:
	default:
		conn.conn.Close()
	}
}

func (c *VedaDBClient) execute(ctx context.Context, req VedaDBProtocol) (*VedaDBResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(retryBackoff * time.Duration(attempt))
		}

		conn, err := c.acquireConn()
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := c.executeWithConn(conn, req)
		c.releaseConn(conn)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("after %d attempts: %w", c.maxRetries+1, lastErr)
}

func (c *VedaDBClient) executeWithConn(conn *vedaConn, req VedaDBProtocol) (*VedaDBResponse, error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	// Set deadline based on timeout
	deadline := time.Now().Add(c.timeout)
	conn.conn.SetDeadline(deadline)
	defer conn.conn.SetDeadline(time.Time{})

	// Send request as JSON line
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

	// Read response line
	line, err := conn.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp VedaDBResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}

func (c *VedaDBClient) healthChecker() {
	ticker := time.NewTicker(c.healthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if atomic.LoadInt32(&c.closed) == 1 {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := c.Ping(ctx)
			cancel()
			if err != nil {
				atomic.StoreInt32(&c.healthStatus, 0)
			} else {
				atomic.StoreInt32(&c.healthStatus, 1)
			}
		}
	}
}

// Stats returns client statistics.
func (c *VedaDBClient) Stats() map[string]interface{} {
	return map[string]interface{}{
		"addr":         c.addr,
		"database":     c.database,
		"pool_size":    c.poolSize,
		"healthy":      c.IsHealthy(),
		"timeout_ms":   c.timeout.Milliseconds(),
		"max_retries":  c.maxRetries,
	}
}
