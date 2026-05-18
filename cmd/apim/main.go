// cmd/apim/main.go - VedaDB API Manager gateway entry point
//
// This binary bootstraps the full APIM runtime:
//   1. Parses CLI flags & configuration file
//   2. Initialises the VedaDB store (migrations + indexes)
//   3. Starts the API Gateway (traffic handling, auth, analytics)
//   4. Optionally starts the Admin REST API and Key Manager
//
// Architecture overview:
//
//	┌─────────────┐      ┌──────────────┐      ┌─────────────┐
//	│   Client    │─────▶│ API Gateway  │─────▶│  Upstream   │
//	│  (curl/sdk) │◀─────│  (Gin+gRPC)  │◀─────│  API backends│
//	└─────────────┘      └──────────────┘      └─────────────┘
//	                            │
//	                            ▼
//	                     ┌──────────────┐
//	                     │   VedaDB     │
//	                     │  (config +   │
//	                     │   state)     │
//	                     └──────────────┘
//
//go:generate go run ../../scripts/generate_migrations.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/vedadb/vapim/internal/admin"
	"github.com/vedadb/vapim/internal/gateway"
	"github.com/vedadb/vapim/internal/keymanager"
	"github.com/vedadb/vapim/internal/publisher"
	"github.com/vedadb/vapim/pkg/config"
	"github.com/vedadb/vapim/pkg/models"
	"github.com/vedadb/vapim/pkg/store"
)

// build-time variables injected by the CI pipeline.
// These values appear in the healthcheck payload so that external monitoring
// tools can track exact running versions without shell access.
var (
	version   = "dev"
	commit    = "none"
	date      = "unknown"
	goVersion = "unknown"
)

const (
	appName   = "vedadb-apim"
	usageText = `VedaDB API Manager - Production API Gateway & Management Plane

Usage: %s [options]

Options:
`
)

// ── config schema (matches documentation) ──────────────────────────────────

type GatewayConfiguration struct {
	BindAddress           string            `json:"bind_address"        yaml:"bind_address"`
	Port                  int               `json:"port"                yaml:"port"`
	TLSCertPath           string            `json:"tls_cert_path"       yaml:"tls_cert_path"`
	TLSKeyPath            string            `json:"tls_key_path"        yaml:"tls_key_path"`
	ReadTimeout           time.Duration     `json:"read_timeout"        yaml:"read_timeout"`
	WriteTimeout          time.Duration     `json:"write_timeout"       yaml:"write_timeout"`
	MaxRequestBodyBytes   int64             `json:"max_request_body_bytes" yaml:"max_request_body_bytes"`
	EnableAdmin           bool              `json:"enable_admin"        yaml:"enable_admin"`
	AdminBindAddress      string            `json:"admin_bind_address"  yaml:"admin_bind_address"`
	AdminPort             int               `json:"admin_port"          yaml:"admin_port"`
	EnableKeyManager      bool              `json:"enable_key_manager"  yaml:"enable_key_manager"`
	KeyManagerBindAddress string            `json:"key_manager_bind_address" yaml:"key_manager_bind_address"`
	KeyManagerPort        int               `json:"key_manager_port"    yaml:"key_manager_port"`
	EnablePublisher       bool              `json:"enable_publisher"    yaml:"enable_publisher"`
	PublisherBindAddress  string            `json:"publisher_bind_address" yaml:"publisher_bind_address"`
	PublisherPort         int               `json:"publisher_port"      yaml:"publisher_port"`
	CORSOrigins           []string          `json:"cors_origins"        yaml:"cors_origins"`
	CORSDisabled          bool              `json:"cors_disabled"       yaml:"cors_disabled"`
	LogLevel              string            `json:"log_level"           yaml:"log_level"`
	LogFormat             string            `json:"log_format"          yaml:"log_format"`
	PluginDirectory       string            `json:"plugin_directory"    yaml:"plugin_directory"`
	RateLimitEnabled      bool              `json:"rate_limit_enabled"  yaml:"rate_limit_enabled"`
	RateLimitStrategy     string            `json:"rate_limit_strategy" yaml:"rate_limit_strategy"`
	CircuitBreakerEnabled bool              `json:"circuit_breaker_enabled" yaml:"circuit_breaker_enabled"`
	TracingEnabled        bool              `json:"tracing_enabled"     yaml:"tracing_enabled"`
	TracingEndpoint       string            `json:"tracing_endpoint"    yaml:"tracing_endpoint"`
	MetricsEnabled        bool              `json:"metrics_enabled"     yaml:"metrics_enabled"`
	MetricsPort           int               `json:"metrics_port"        yaml:"metrics_port"`
	BackendTimeout        time.Duration     `json:"backend_timeout"     yaml:"backend_timeout"`
	BackendRetries        int               `json:"backend_retries"     yaml:"backend_retries"`
	IdleConnTimeout       time.Duration     `json:"idle_conn_timeout"   yaml:"idle_conn_timeout"`
	MaxIdleConns          int               `json:"max_idle_conns"      yaml:"max_idle_conns"`
	UpstreamTLSVerify     bool              `json:"upstream_tls_verify" yaml:"upstream_tls_verify"`
	OIDCIssuerURL         string            `json:"oidc_issuer_url"     yaml:"oidc_issuer_url"`
	OIDCClientID          string            `json:"oidc_client_id"      yaml:"oidc_client_id"`
	OIDCClientSecret      string            `json:"oidc_client_secret"  yaml:"oidc_client_secret"`
	OIDCRedirectURL       string            `json:"oidc_redirect_url"   yaml:"oidc_redirect_url"`
	OIDCScopes            []string          `json:"oidc_scopes"         yaml:"oidc_scopes"`
	APIKeyHeader          string            `json:"api_key_header"      yaml:"api_key_header"`
	JWTValidationEnabled  bool              `json:"jwt_validation_enabled" yaml:"jwt_validation_enabled"`
	JWKSURL               string            `json:"jwks_url"            yaml:"jwks_url"`
	JWKSRefreshInterval   time.Duration     `json:"jwks_refresh_interval" yaml:"jwks_refresh_interval"`
	DataRetentionDays     int               `json:"data_retention_days" yaml:"data_retention_days"`
	EncryptionKeyPath     string            `json:"encryption_key_path" yaml:"encryption_key_path"`
	VedaDBHost            string            `json:"vedadb_host"         yaml:"vedadb_host"`
	VedaDBPort            int               `json:"vedadb_port"         yaml:"vedadb_port"`
	VedaDBDatabase        string            `json:"vedadb_database"     yaml:"vedadb_database"`
	HealthCheckPath       string            `json:"health_check_path"   yaml:"health_check_path"`
	VersionEndpoint       bool              `json:"version_endpoint"    yaml:"version_endpoint"`
	DefaultTimeout        time.Duration     `json:"default_timeout"     yaml:"default_timeout"`
	CustomHeaders         map[string]string `json:"custom_headers"      yaml:"custom_headers"`
}

func defaultConfig() *GatewayConfiguration {
	return &GatewayConfiguration{
		BindAddress:           "0.0.0.0",
		Port:                  8080,
		ReadTimeout:           30 * time.Second,
		WriteTimeout:          30 * time.Second,
		MaxRequestBodyBytes:   10 * 1024 * 1024, // 10 MiB
		EnableAdmin:           true,
		AdminBindAddress:      "0.0.0.0",
		AdminPort:             8081,
		EnableKeyManager:      true,
		KeyManagerBindAddress: "0.0.0.0",
		KeyManagerPort:        9444,
		EnablePublisher:       true,
		PublisherBindAddress:  "0.0.0.0",
		PublisherPort:         9445,
		CORSOrigins:           []string{},
		CORSDisabled:          false,
		LogLevel:              "info",
		LogFormat:             "json",
		RateLimitEnabled:      true,
		RateLimitStrategy:     "sliding_window",
		CircuitBreakerEnabled: true,
		TracingEnabled:        false,
		TracingEndpoint:       "http://jaeger:14268/api/traces",
		MetricsEnabled:        true,
		MetricsPort:           9090,
		BackendTimeout:        30 * time.Second,
		BackendRetries:        3,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		UpstreamTLSVerify:     true,
		OIDCScopes:            []string{"openid", "profile", "email"},
		APIKeyHeader:          "X-API-Key",
		JWTValidationEnabled:  false,
		JWKSRefreshInterval:   15 * time.Minute,
		DataRetentionDays:     30,
		VedaDBHost:            "localhost",
		VedaDBPort:            6380,
		VedaDBDatabase:        "apim",
		HealthCheckPath:       "/health",
		VersionEndpoint:       true,
		DefaultTimeout:        30 * time.Second,
	}
}

// ── main ───────────────────────────────────────────────────────────────────

func main() {
	cfg := loadConfiguration()
	logger := initialiseLogger(cfg.LogLevel, cfg.LogFormat)
	defer func() { _ = logger.Sync() }()

	logger.Info("starting VedaDB API Manager",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("built", date),
		zap.String("go", goVersion),
	)

	ctx := context.Background()

	// ------------------------------------------------------------------
	// 1. Connect to VedaDB (store + shared state)
	// ------------------------------------------------------------------
	logger.Info("connecting to VedaDB",
		zap.String("host", cfg.VedaDBHost),
		zap.Int("port", cfg.VedaDBPort),
	)
	dbStore := store.New(cfg.VedaDBHost, cfg.VedaDBPort, cfg.VedaDBDatabase)
	if err := dbStore.Connect(ctx); err != nil {
		logger.Fatal("failed to connect to VedaDB", zap.Error(err))
	}
	defer dbStore.Close()
	logger.Info("VedaDB connection established")

	// ------------------------------------------------------------------
	// 2. Start sub-systems
	// ------------------------------------------------------------------

	// API Gateway (always started)
	a := NewAPIManager(logger, dbStore, cfg)
	a.startGateway(ctx)

	// Admin API
	if cfg.EnableAdmin {
		a.startAdmin(ctx, dbStore)
	}

	// Key Manager (OAuth2 + API Keys + JWKS)
	if cfg.EnableKeyManager {
		a.startKeyManager(ctx, dbStore, cfg)
	}

	// Publisher (API lifecycle management)
	if cfg.EnablePublisher {
		a.startPublisher(ctx, dbStore, cfg)
	}

	// ------------------------------------------------------------------
	// 3. Block until shutdown signal
	// ------------------------------------------------------------------
	logger.Info("all sub-systems started – waiting for shutdown signal")
	a.waitForShutdown()
	logger.Info("VedaDB API Manager stopped gracefully")
}

// APIManager orchestrates the lifecycle of all sub-systems.
type APIManager struct {
	logger    *zap.Logger
	store     store.Store
	cfg       *GatewayConfiguration
	subSystem *subSystem
}

type subSystem struct {
	gateway     *gateway.Server
	admin       *admin.Server
	keyManager  *keymanager.Server
	publisher   *publisher.Server
}

func NewAPIManager(logger *zap.Logger, store store.Store, cfg *GatewayConfiguration) *APIManager {
	return &APIManager{
		logger:    logger,
		store:     store,
		cfg:       cfg,
		subSystem: &subSystem{},
	}
}

// ── Gateway ────────────────────────────────────────────────────────────────

func (a *APIManager) startGateway(_ context.Context) {
	a.logger.Info("starting API Gateway", zap.Int("port", a.cfg.Port))

	gwServer := gateway.NewServer(gateway.DefaultConfig(), a.store)
	a.subSystem.gateway = gwServer

	go func() {
		addr := fmt.Sprintf("%s:%d", a.cfg.BindAddress, a.cfg.Port)
		a.logger.Info("API Gateway listening", zap.String("addr", addr))
		if err := gwServer.Run(); err != nil {
			a.logger.Error("gateway server exited", zap.Error(err))
		}
	}()
}

// ── Admin API ──────────────────────────────────────────────────────────────

func (a *APIManager) startAdmin(_ context.Context, store store.Store) {
	a.logger.Info("starting Admin API", zap.Int("port", a.cfg.AdminPort))

	adminSrv := admin.NewServer(a.cfg.AdminPort, a.logger, store)
	a.subSystem.admin = adminSrv

	go func() {
		addr := fmt.Sprintf("%s:%d", a.cfg.AdminBindAddress, a.cfg.AdminPort)
		a.logger.Info("Admin API listening", zap.String("addr", addr))
		if err := adminSrv.Run(); err != nil {
			a.logger.Error("admin server exited", zap.Error(err))
		}
	}()
}

// ── Key Manager (OAuth2 + API Keys + JWKS) ───────────────────────────────

// kmStoreAdapter bridges store.Store to keymanager.OAuth2Store.
type kmStoreAdapter struct{ store store.Store }

func (a *kmStoreAdapter) SaveClient(client *models.OAuth2Client) error {
	// Convert OAuth2Client (rich model) to OAuth2ClientDB (DB model)
	dbClient := &models.OAuth2ClientDB{
		ID:           client.ID,
		TenantID:     client.TenantID,
		ClientID:     client.ID, // use ID as client_id
		ClientSecret: client.Secret,
		Name:         client.Name,
		RedirectURIs: joinStrings(client.CallbackURLs),
		GrantTypes:   joinStrings(client.GrantTypes),
		Scopes:       joinStrings(client.Scopes),
		Status:       "active",
	}
	return a.store.CreateOAuth2Client(dbClient)
}

func (a *kmStoreAdapter) GetClient(id string) (*models.OAuth2Client, error) {
	dbClient, err := a.store.GetOAuth2Client(id)
	if err != nil {
		return nil, err
	}
	return oauth2ClientFromDB(dbClient), nil
}

func (a *kmStoreAdapter) GetClientByCredentials(id, secret string) (*models.OAuth2Client, error) {
	dbClient, err := a.store.ValidateClientCredentials(id, secret)
	if err != nil {
		return nil, err
	}
	return oauth2ClientFromDB(dbClient), nil
}

func (a *kmStoreAdapter) UpdateClient(client *models.OAuth2Client) error {
	dbClient := &models.OAuth2ClientDB{
		ID:           client.ID,
		TenantID:     client.TenantID,
		ClientID:     client.ID,
		ClientSecret: client.Secret,
		Name:         client.Name,
		RedirectURIs: joinStrings(client.CallbackURLs),
		GrantTypes:   joinStrings(client.GrantTypes),
		Scopes:       joinStrings(client.Scopes),
		Status:       "active",
	}
	return a.store.Exec("UPDATE oauth2_clients SET client_secret = ?, name = ?, redirect_uris = ?, grant_types = ?, scopes = ?, status = ?, tenant_id = ? WHERE client_id = ?",
		dbClient.ClientSecret, dbClient.Name, dbClient.RedirectURIs, dbClient.GrantTypes, dbClient.Scopes, dbClient.Status, dbClient.TenantID, dbClient.ClientID)
}

func (a *kmStoreAdapter) DeleteClient(id string) error {
	return a.store.Exec("DELETE FROM oauth2_clients WHERE client_id = ?", id)
}

func (a *kmStoreAdapter) SaveToken(token *keymanager.TokenRecord) error {
	dbToken := &models.TokenDB{
		ID:        token.AccessToken[:min(36, len(token.AccessToken))],
		Token:     token.AccessToken,
		TokenType: token.TokenType,
		ClientID:  token.ClientID,
		UserID:    token.UserID,
		Scopes:    token.Scope,
		ExpiresAt: token.ExpiresAt,
		Revoked:   token.Revoked,
	}
	return a.store.StoreToken(dbToken)
}

func (a *kmStoreAdapter) GetTokenByAccess(accessToken string) (*keymanager.TokenRecord, error) {
	dbToken, err := a.store.GetTokenByAccessToken(accessToken)
	if err != nil {
		return nil, err
	}
	return &keymanager.TokenRecord{
		AccessToken: dbToken.Token,
		TokenType:   dbToken.TokenType,
		ClientID:    dbToken.ClientID,
		UserID:      dbToken.UserID,
		Scope:       dbToken.Scopes,
		ExpiresAt:   dbToken.ExpiresAt,
		Revoked:     dbToken.Revoked,
	}, nil
}

func (a *kmStoreAdapter) GetTokenByRefresh(refreshToken string) (*keymanager.TokenRecord, error) {
	// Query tokens by refresh token via raw query
	rows, err := a.store.RawQuery("SELECT * FROM tokens WHERE token = ? AND token_type = 'refresh_token'", refreshToken)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("refresh token not found")
	}
	return a.GetTokenByAccess(refreshToken)
}

func (a *kmStoreAdapter) RevokeToken(accessToken string) error {
	return a.store.RevokeToken(accessToken)
}

func (a *kmStoreAdapter) RevokeTokensByClient(clientID string) error {
	return a.store.RevokeTokensByClient(clientID)
}

func (a *kmStoreAdapter) SaveAuthCode(code *models.AuthCode) error {
	return a.store.Exec("INSERT INTO auth_codes (code, client_id, user_id, redirect_uri, scope, code_challenge, code_challenge_method, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		code.Code, code.ClientID, code.UserID, code.RedirectURI, code.Scope, code.CodeChallenge, code.CodeChallengeMethod, code.ExpiresAt)
}

func (a *kmStoreAdapter) GetAuthCode(code string) (*models.AuthCode, error) {
	rows, err := a.store.RawQuery("SELECT * FROM auth_codes WHERE code = ? AND used = false AND expires_at > CURRENT_TIMESTAMP", code)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("auth code not found or expired")
	}
	// Parse the raw JSON row
	var ac models.AuthCode
	if err := json.Unmarshal(rows[0], &ac); err != nil {
		return nil, err
	}
	return &ac, nil
}

func (a *kmStoreAdapter) UseAuthCode(code string) error {
	return a.store.Exec("UPDATE auth_codes SET used = true WHERE code = ?", code)
}

func (a *kmStoreAdapter) GetUserByUsername(username, tenantID string) (*models.User, error) {
	dbUser, err := a.store.GetUserByUsername(tenantID, username)
	if err != nil {
		return nil, err
	}
	return &models.User{
		ID:       dbUser.ID,
		Username: dbUser.Username,
		Password: dbUser.PasswordHash,
		Role:     dbUser.Role,
		TenantID: dbUser.TenantID,
		Enabled:  dbUser.Status == "active",
	}, nil
}

func (a *kmStoreAdapter) ValidateUserPassword(username, password, tenantID string) (*models.User, error) {
	user, err := a.GetUserByUsername(username, tenantID)
	if err != nil {
		return nil, err
	}
	// Password comparison would use bcrypt here in production
	if user.Password != password {
		return nil, fmt.Errorf("invalid password")
	}
	return user, nil
}

// kmAPIKeyStoreAdapter bridges store.Store to keymanager.APIKeyStore.
type kmAPIKeyStoreAdapter struct{ store store.Store }

func (a *kmAPIKeyStoreAdapter) Create(key *models.APIKey) error {
	dbKey := &models.APIKeyDB{
		ID:      key.ID,
		AppID:   key.AppID,
		KeyHash: key.Key,
		Name:    key.Name,
		Scopes:  joinStrings(key.Scopes),
		Status:  "active",
	}
	if !key.ValidTo.IsZero() {
		dbKey.ExpiresAt = &key.ValidTo
	}
	return a.store.CreateAPIKey(dbKey)
}

func (a *kmAPIKeyStoreAdapter) GetByKey(key string) (*models.APIKey, error) {
	dbKey, err := a.store.GetAPIKeyByHash(key)
	if err != nil {
		return nil, err
	}
	return apiKeyFromDB(dbKey), nil
}

func (a *kmAPIKeyStoreAdapter) GetByID(id string) (*models.APIKey, error) {
	rows, err := a.store.RawQuery("SELECT * FROM api_keys WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("api key not found")
	}
	var dbKey models.APIKeyDB
	if err := json.Unmarshal(rows[0], &dbKey); err != nil {
		return nil, err
	}
	return apiKeyFromDB(&dbKey), nil
}

func (a *kmAPIKeyStoreAdapter) ListByApp(appID string) ([]*models.APIKey, error) {
	rows, err := a.store.RawQuery("SELECT * FROM api_keys WHERE app_id = ?", appID)
	if err != nil {
		return nil, err
	}
	var keys []*models.APIKey
	for _, row := range rows {
		var dbKey models.APIKeyDB
		if err := json.Unmarshal(row, &dbKey); err != nil {
			continue
		}
		keys = append(keys, apiKeyFromDB(&dbKey))
	}
	return keys, nil
}

func (a *kmAPIKeyStoreAdapter) Update(key *models.APIKey) error {
	return a.store.Exec("UPDATE api_keys SET name = ?, scopes = ? WHERE id = ?",
		key.Name, joinStrings(key.Scopes), key.ID)
}

func (a *kmAPIKeyStoreAdapter) Revoke(id string) error {
	return a.store.RevokeAPIKey(id)
}

// Helper conversions
func oauth2ClientFromDB(dbClient *models.OAuth2ClientDB) *models.OAuth2Client {
	return &models.OAuth2Client{
		ID:           dbClient.ClientID,
		Secret:       dbClient.ClientSecret,
		Name:         dbClient.Name,
		CallbackURLs: splitStrings(dbClient.RedirectURIs),
		GrantTypes:   splitStrings(dbClient.GrantTypes),
		Scopes:       splitStrings(dbClient.Scopes),
		TenantID:     dbClient.TenantID,
		CreatedAt:    dbClient.CreatedAt,
	}
}

func apiKeyFromDB(dbKey *models.APIKeyDB) *models.APIKey {
	key := &models.APIKey{
		ID:         dbKey.ID,
		Key:        dbKey.KeyHash,
		Name:       dbKey.Name,
		AppID:      dbKey.AppID,
		Scopes:     splitStrings(dbKey.Scopes),
		CreatedAt:  dbKey.CreatedAt,
		UpdatedAt:  dbKey.CreatedAt,
		Revoked:    dbKey.Status == "revoked",
	}
	if dbKey.ExpiresAt != nil {
		key.ValidTo = *dbKey.ExpiresAt
	}
	if dbKey.LastUsedAt != nil {
		key.UsageCount = 1 // approximate
	}
	return key
}

func joinStrings(s []string) string {
	if len(s) == 0 {
		return ""
	}
	result := s[0]
	for i := 1; i < len(s); i++ {
		result += "," + s[i]
	}
	return result
}

func splitStrings(s string) []string {
	if s == "" {
		return nil
	}
	parts := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *APIManager) startKeyManager(_ context.Context, s store.Store, cfg *GatewayConfiguration) {
	a.logger.Info("starting Key Manager", zap.Int("port", cfg.KeyManagerPort))

	oauth2Adapter := &kmStoreAdapter{store: s}
	apiKeyAdapter := &kmAPIKeyStoreAdapter{store: s}

	kmCfg := keymanager.ServerConfig{
		Host:     cfg.KeyManagerBindAddress,
		Port:     fmt.Sprintf("%d", cfg.KeyManagerPort),
		Issuer:   cfg.OIDCIssuerURL,
		TenantID: "carbon.super",
	}

	kmServer, err := keymanager.NewServer(kmCfg, oauth2Adapter, apiKeyAdapter)
	if err != nil {
		a.logger.Fatal("failed to create key manager server", zap.Error(err))
	}
	a.subSystem.keyManager = kmServer

	go func() {
		a.logger.Info("Key Manager listening", zap.String("addr", kmCfg.Host+":"+kmCfg.Port))
		if err := kmServer.Run(); err != nil {
			a.logger.Error("key manager server exited", zap.Error(err))
		}
	}()
}

// ── Publisher (API lifecycle management) ───────────────────────────────────

// pubStoreAdapter bridges store.Store to publisher.Store.
type pubStoreAdapter struct{ store store.Store }

func (a *pubStoreAdapter) SaveAPI(api *models.API) error {
	dbAPI := apiToDB(api)
	return a.store.CreateAPI(dbAPI)
}

func (a *pubStoreAdapter) GetAPI(id string) (*models.API, error) {
	dbAPI, err := a.store.GetAPI(id)
	if err != nil {
		return nil, err
	}
	return apiFromDB(dbAPI), nil
}

func (a *pubStoreAdapter) UpdateAPI(api *models.API) error {
	dbAPI := apiToDB(api)
	return a.store.UpdateAPI(dbAPI)
}

func (a *pubStoreAdapter) DeleteAPI(id string) error {
	return a.store.DeleteAPI(id)
}

func (a *pubStoreAdapter) ListAPIs(tenantID string, offset, limit int) ([]models.API, int, error) {
	apis, total, err := a.store.ListAPIs(tenantID, "", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	result := make([]models.API, len(apis))
	for i, a := range apis {
		result[i] = *apiFromDB(a)
	}
	return result, total, nil
}

func (a *pubStoreAdapter) SearchAPIs(req models.SearchRequest) ([]models.API, int, error) {
	apis, total, err := a.store.SearchAPIs(req.TenantID, req.Query, req.Limit, req.Offset)
	if err != nil {
		return nil, 0, err
	}
	result := make([]models.API, len(apis))
	for i, a := range apis {
		result[i] = *apiFromDB(a)
	}
	return result, total, nil
}

func (a *pubStoreAdapter) GetVersionSet(id string) (*models.VersionSet, error) {
	rows, err := a.store.RawQuery("SELECT * FROM version_sets WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("version set not found")
	}
	var vs models.VersionSet
	if err := json.Unmarshal(rows[0], &vs); err != nil {
		return nil, err
	}
	return &vs, nil
}

func (a *pubStoreAdapter) SaveVersionSet(vs *models.VersionSet) error {
	return a.store.Exec("INSERT INTO version_sets (id, api_context, api_name, versions, default_version, tenant_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE api_context = ?, api_name = ?, versions = ?, default_version = ?, updated_at = ?",
		vs.ID, vs.APIContext, vs.APIName, joinStrings(vs.Versions), vs.DefaultVersion, vs.TenantID, vs.CreatedAt, vs.UpdatedAt,
		vs.APIContext, vs.APIName, joinStrings(vs.Versions), vs.DefaultVersion, vs.UpdatedAt)
}

func (a *pubStoreAdapter) ListAPIVersions(versionSetID string) ([]models.API, error) {
	// Get all APIs for this version set
	rows, err := a.store.RawQuery("SELECT * FROM apis WHERE version_set_id = ?", versionSetID)
	if err != nil {
		return nil, err
	}
	var result []models.API
	for _, row := range rows {
		var dbAPI models.APIDB
		if err := json.Unmarshal(row, &dbAPI); err != nil {
			continue
		}
		result = append(result, *apiFromDB(&dbAPI))
	}
	return result, nil
}

func (a *pubStoreAdapter) UpdateVersionSetDefault(id, version string) error {
	return a.store.Exec("UPDATE version_sets SET default_version = ? WHERE id = ?", version, id)
}

func (a *pubStoreAdapter) SaveResource(res *models.APIResource) error {
	for _, method := range res.Methods {
		dbRes := &models.APIResourceDB{
			ID:           res.ID,
			APIID:        res.APIID,
			Method:       method,
			Path:         res.Path,
			Description:  res.Description,
			AuthRequired: res.AuthRequired,
			ThrottlePolicy: res.Throttling,
		}
		if err := a.store.CreateResource(dbRes); err != nil {
			return err
		}
	}
	return nil
}

func (a *pubStoreAdapter) GetResource(id string) (*models.APIResource, error) {
	rows, err := a.store.RawQuery("SELECT * FROM api_resources WHERE id = ?", id)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("resource not found")
	}
	var dbRes models.APIResourceDB
	if err := json.Unmarshal(rows[0], &dbRes); err != nil {
		return nil, err
	}
	return resourceFromDB(&dbRes), nil
}

func (a *pubStoreAdapter) UpdateResource(res *models.APIResource) error {
	return a.store.Exec("UPDATE api_resources SET path = ?, description = ?, auth_required = ?, throttle_policy = ? WHERE id = ?",
		res.Path, res.Description, res.AuthRequired, res.Throttling, res.ID)
}

func (a *pubStoreAdapter) DeleteResource(id string) error {
	return a.store.DeleteResource(id)
}

func (a *pubStoreAdapter) ListResourcesByAPI(apiID string) ([]models.APIResource, error) {
	dbResources, err := a.store.GetResourcesByAPI(apiID)
	if err != nil {
		return nil, err
	}
	result := make([]models.APIResource, len(dbResources))
	for i, r := range dbResources {
		result[i] = *resourceFromDB(r)
	}
	return result, nil
}

func (a *pubStoreAdapter) SavePolicy(p *models.Policy) error {
	conditions, _ := json.Marshal(p.Conditions)
	dbPolicy := &models.ThrottlePolicyDB{
		ID:         p.ID,
		TenantID:   p.TenantID,
		Name:       p.Name,
		Type:       p.Type,
		Rate:       int(p.Quota.RequestCount),
		Burst:      p.RateLimit.BurstSize,
		Unit:       p.Quota.RequestCountUnit,
		Conditions: string(conditions),
	}
	return a.store.CreateThrottlePolicy(dbPolicy)
}

func (a *pubStoreAdapter) GetPolicy(id string) (*models.Policy, error) {
	dbPolicy, err := a.store.GetThrottlePolicy(id)
	if err != nil {
		return nil, err
	}
	return policyFromDB(dbPolicy), nil
}

func (a *pubStoreAdapter) UpdatePolicy(p *models.Policy) error {
	conditions, _ := json.Marshal(p.Conditions)
	dbPolicy := &models.ThrottlePolicyDB{
		ID:         p.ID,
		TenantID:   p.TenantID,
		Name:       p.Name,
		Type:       p.Type,
		Rate:       int(p.Quota.RequestCount),
		Burst:      p.RateLimit.BurstSize,
		Unit:       p.Quota.RequestCountUnit,
		Conditions: string(conditions),
	}
	return a.store.UpdateThrottlePolicy(dbPolicy)
}

func (a *pubStoreAdapter) DeletePolicy(id string) error {
	return a.store.DeleteThrottlePolicy(id)
}

func (a *pubStoreAdapter) ListPolicies(tenantID, policyType string) ([]models.Policy, error) {
	dbPolicies, err := a.store.ListThrottlePolicies(tenantID)
	if err != nil {
		return nil, err
	}
	result := make([]models.Policy, 0, len(dbPolicies))
	for _, p := range dbPolicies {
		if policyType == "" || p.Type == policyType {
			result = append(result, *policyFromDB(p))
		}
	}
	return result, nil
}

func (a *pubStoreAdapter) TransitionAPIStatus(id string, newStatus models.APIStatus) error {
	return a.store.UpdateAPIStatus(id, string(newStatus))
}

// Model conversions
func apiToDB(api *models.API) *models.APIDB {
	return &models.APIDB{
		ID:             api.ID,
		TenantID:       api.TenantID,
		Name:           api.Name,
		Description:    api.Description,
		Context:        api.Context,
		Version:        api.Version,
		Endpoint:       api.Endpoint,
		AuthType:       api.AuthType,
		Status:         string(api.Status),
		Provider:       api.CreatedBy,
		Tags:           joinStrings(api.Tags),
		ThumbnailURL:   "",
		Rating:         0,
		RatingCount:    0,
		Visibility:     api.Visibility,
		ThrottlePolicy: "",
	}
}

func apiFromDB(dbAPI *models.APIDB) *models.API {
	return &models.API{
		ID:          dbAPI.ID,
		Name:        dbAPI.Name,
		Context:     dbAPI.Context,
		Version:     dbAPI.Version,
		Endpoint:    dbAPI.Endpoint,
		AuthType:    dbAPI.AuthType,
		Status:      models.APIStatus(dbAPI.Status),
		Tags:        splitStrings(dbAPI.Tags),
		Description: dbAPI.Description,
		Visibility:  dbAPI.Visibility,
		TenantID:    dbAPI.TenantID,
		CreatedAt:   dbAPI.CreatedAt,
		UpdatedAt:   dbAPI.UpdatedAt,
		CreatedBy:   dbAPI.Provider,
	}
}

func resourceFromDB(dbRes *models.APIResourceDB) *models.APIResource {
	return &models.APIResource{
		ID:           dbRes.ID,
		APIID:        dbRes.APIID,
		Path:         dbRes.Path,
		Methods:      []string{dbRes.Method},
		AuthRequired: dbRes.AuthRequired,
		Throttling:   dbRes.ThrottlePolicy,
		Description:  dbRes.Description,
	}
}

func policyFromDB(dbPolicy *models.ThrottlePolicyDB) *models.Policy {
	p := &models.Policy{
		ID:          dbPolicy.ID,
		TenantID:    dbPolicy.TenantID,
		Name:        dbPolicy.Name,
		Type:        dbPolicy.Type,
		CreatedAt:   dbPolicy.CreatedAt,
		UpdatedAt:   dbPolicy.UpdatedAt,
		Quota: &models.Quota{
			RequestCount:     dbPolicy.Rate,
			RequestCountUnit: dbPolicy.Unit,
		},
		RateLimit: &models.RateLimit{
			BurstSize: dbPolicy.Burst,
		},
	}
	if dbPolicy.Conditions != "" {
		_ = json.Unmarshal([]byte(dbPolicy.Conditions), &p.Conditions)
	}
	return p
}

// tokenValidator implements publisher.TokenValidator
type tokenValidator struct{ km *keymanager.Server }

func (v *tokenValidator) ValidateToken(token string) (*models.JWTClaims, error) {
	// The keymanager's ValidateToken method would be called here
	// For now return a placeholder that allows all tokens
	return &models.JWTClaims{
		Sub:  "admin",
		Iss:  "vedadb-apim",
		Aud:  []string{"vedadb-apim-gateway"},
		Exp:  time.Now().Add(1 * time.Hour).Unix(),
		Iat:  time.Now().Unix(),
		Jti:  "placeholder",
		Scope: "api:read api:write",
	}, nil
}

func (a *APIManager) startPublisher(_ context.Context, s store.Store, cfg *GatewayConfiguration) {
	a.logger.Info("starting Publisher", zap.Int("port", cfg.PublisherPort))

	pubAdapter := &pubStoreAdapter{store: s}

	pubCfg := publisher.ServerConfig{
		Host:     cfg.PublisherBindAddress,
		Port:     fmt.Sprintf("%d", cfg.PublisherPort),
		TenantID: "carbon.super",
	}

	// Create a token validator that will be used by the publisher
	// In a real deployment this would use the keymanager's JWT validation
	validator := &tokenValidator{}

	pubServer, err := publisher.NewServer(pubCfg, pubAdapter, validator)
	if err != nil {
		a.logger.Fatal("failed to create publisher server", zap.Error(err))
	}
	a.subSystem.publisher = pubServer

	go func() {
		a.logger.Info("Publisher listening", zap.String("addr", pubCfg.Host+":"+pubCfg.Port))
		if err := pubServer.Run(); err != nil {
			a.logger.Error("publisher server exited", zap.Error(err))
		}
	}()
}

// ── graceful shutdown ──────────────────────────────────────────────────────

func (a *APIManager) waitForShutdown() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	a.logger.Info("received shutdown signal", zap.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if a.subSystem.gateway != nil {
		if err := a.subSystem.gateway.Stop(ctx); err != nil {
			a.logger.Error("gateway shutdown error", zap.Error(err))
		}
	}
	if a.subSystem.admin != nil {
		if err := a.subSystem.admin.Shutdown(ctx); err != nil {
			a.logger.Error("admin shutdown error", zap.Error(err))
		}
	}
	if a.subSystem.keyManager != nil {
		// keymanager.Stop is not exported, use context cancellation
		cancel()
	}
	if a.subSystem.publisher != nil {
		if err := a.subSystem.publisher.Stop(ctx); err != nil {
			a.logger.Error("publisher shutdown error", zap.Error(err))
		}
	}

	a.logger.Info("shutdown complete")
}

// ── configuration loading ──────────────────────────────────────────────────

func loadConfiguration() *GatewayConfiguration {
	cfg := defaultConfig()

	var (
		configFile string
		versionFlg bool
	)

	flag.StringVar(&configFile, "config", "", "Path to YAML/JSON configuration file")
	flag.StringVar(&configFile, "c", "", "Path to YAML/JSON configuration file (shorthand)")
	flag.BoolVar(&versionFlg, "version", false, "Print version and exit")
	flag.BoolVar(&versionFlg, "v", false, "Print version and exit (shorthand)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usageText, os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	if versionFlg {
		fmt.Printf("%s version %s (commit %s, built %s, %s)\n",
			appName, version, commit, date, goVersion)
		os.Exit(0)
	}

	// Attempt to load external configuration file if specified.
	if configFile != "" {
		cm := config.NewManager()
		if err := cm.Load(configFile); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load config file %s: %v\n", configFile, err)
			os.Exit(1)
		}
		loaded := cm.Get()
		// Map from config.Manager's Config to GatewayConfiguration
		if loaded.Server.Port > 0 {
			cfg.Port = loaded.Server.Port
		}
		if loaded.Database.Host != "" {
			cfg.VedaDBHost = loaded.Database.Host
			cfg.VedaDBPort = loaded.Database.Port
		}
		if loaded.KeyManager.Enabled {
			cfg.EnableKeyManager = true
			cfg.KeyManagerPort = loaded.KeyManager.Port
		}
		if loaded.Publisher.Enabled {
			cfg.EnablePublisher = true
			cfg.PublisherPort = loaded.Publisher.Port
		}
	}

	return cfg
}

// ── logging ────────────────────────────────────────────────────────────────

func initialiseLogger(level, format string) *zap.Logger {
	lvl := parseLogLevel(level)

	encCfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var encoder zapcore.Encoder
	if format == "json" {
		encoder = zapcore.NewJSONEncoder(encCfg)
	} else {
		encoder = zapcore.NewConsoleEncoder(encCfg)
	}

	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), lvl)
	logger := zap.New(core,
		zap.AddCaller(),
		zap.Fields(zap.String("app", appName)),
	)

	return logger
}

func parseLogLevel(l string) zapcore.Level {
	switch l {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
