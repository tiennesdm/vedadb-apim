// Package config provides the complete configuration system for the VedaDB API Manager.
// It supports loading configuration from YAML files, environment variables, and
// provides hot-reload capability for dynamic configuration changes.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the complete configuration for the VedaDB API Manager.
// All fields include appropriate mapstructure tags for flexible unmarshaling.
type Config struct {
	Server     ServerConfig     `yaml:"server" mapstructure:"server"`
	Gateway    GatewayConfig    `yaml:"gateway" mapstructure:"gateway"`
	Database   DatabaseConfig   `yaml:"database" mapstructure:"database"`
	Auth       AuthConfig       `yaml:"auth" mapstructure:"auth"`
	Throttle   ThrottleConfig   `yaml:"throttle" mapstructure:"throttle"`
	Cache      CacheConfig      `yaml:"cache" mapstructure:"cache"`
	Analytics  AnalyticsConfig  `yaml:"analytics" mapstructure:"analytics"`
	CORS       CORSConfig       `yaml:"cors" mapstructure:"cors"`
	KeyManager KeyManagerConfig `yaml:"key_manager" mapstructure:"key_manager"`
	Publisher  PublisherConfig  `yaml:"publisher" mapstructure:"publisher"`
	Log        LogConfig        `yaml:"log" mapstructure:"log"`
	MutualTLS  MutualTLSConfig  `yaml:"mutual_tls,omitempty" mapstructure:"mutual_tls"`
	RateLimit  RateLimitConfig  `yaml:"rate_limit" mapstructure:"rate_limit"`
}

// ServerConfig defines the HTTP server settings.
type ServerConfig struct {
	Port            int           `yaml:"port" mapstructure:"port"`
	TLSEnabled      bool          `yaml:"tls_enabled" mapstructure:"tls_enabled"`
	TLSCertFile     string        `yaml:"tls_cert_file" mapstructure:"tls_cert_file"`
	TLSKeyFile      string        `yaml:"tls_key_file" mapstructure:"tls_key_file"`
	TLSMinVersion   string        `yaml:"tls_min_version" mapstructure:"tls_min_version"`
	ReadTimeout     time.Duration `yaml:"read_timeout" mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout" mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout" mapstructure:"idle_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" mapstructure:"shutdown_timeout"`
	MaxHeaderBytes  int           `yaml:"max_header_bytes" mapstructure:"max_header_bytes"`
	KeepAlive       bool          `yaml:"keep_alive" mapstructure:"keep_alive"`
}

// GatewayConfig defines the API gateway worker and buffer settings.
type GatewayConfig struct {
	WorkerPoolSize      int           `yaml:"worker_pool_size" mapstructure:"worker_pool_size"`
	RequestBufferSize   int           `yaml:"request_buffer_size" mapstructure:"request_buffer_size"`
	ResponseBufferSize  int           `yaml:"response_buffer_size" mapstructure:"response_buffer_size"`
	MaxRequestBodySize  int64         `yaml:"max_request_body_size" mapstructure:"max_request_body_size"`
	MaxResponseBodySize int64         `yaml:"max_response_body_size" mapstructure:"max_response_body_size"`
	RequestTimeout      time.Duration `yaml:"request_timeout" mapstructure:"request_timeout"`
	RetryCount          int           `yaml:"retry_count" mapstructure:"retry_count"`
	RetryBackoff        time.Duration `yaml:"retry_backoff" mapstructure:"retry_backoff"`
	CircuitBreaker      CircuitBreakerConfig `yaml:"circuit_breaker" mapstructure:"circuit_breaker"`
	ProxyPreserveHost   bool          `yaml:"proxy_preserve_host" mapstructure:"proxy_preserve_host"`
	ProxyReadTimeout    time.Duration `yaml:"proxy_read_timeout" mapstructure:"proxy_read_timeout"`
	ProxyWriteTimeout   time.Duration `yaml:"proxy_write_timeout" mapstructure:"proxy_write_timeout"`
	WebSocketEnabled    bool          `yaml:"websocket_enabled" mapstructure:"websocket_enabled"`
	SSEEnabled          bool          `yaml:"sse_enabled" mapstructure:"sse_enabled"`
	SchemaValidation    bool          `yaml:"schema_validation" mapstructure:"schema_validation"`
	EnforceHTTPS        bool          `yaml:"enforce_https" mapstructure:"enforce_https"`
}

// CircuitBreakerConfig defines circuit breaker settings for backend resilience.
type CircuitBreakerConfig struct {
	Enabled          bool          `yaml:"enabled" mapstructure:"enabled"`
	FailureThreshold int           `yaml:"failure_threshold" mapstructure:"failure_threshold"`
	SuccessThreshold int           `yaml:"success_threshold" mapstructure:"success_threshold"`
	Timeout          time.Duration `yaml:"timeout" mapstructure:"timeout"`
	HalfOpenMaxCalls int           `yaml:"half_open_max_calls" mapstructure:"half_open_max_calls"`
}

// DatabaseConfig defines the VedaDB connection parameters.
type DatabaseConfig struct {
	Host             string        `yaml:"host" mapstructure:"host"`
	Port             int           `yaml:"port" mapstructure:"port"`
	Username         string        `yaml:"username,omitempty" mapstructure:"username"`
	Password         string        `yaml:"password,omitempty" mapstructure:"password"`
	Database         string        `yaml:"database" mapstructure:"database"`
	Timeout          time.Duration `yaml:"timeout" mapstructure:"timeout"`
	MaxRetries       int           `yaml:"max_retries" mapstructure:"max_retries"`
	PoolSize         int           `yaml:"pool_size" mapstructure:"pool_size"`
	ConnectionLifetime time.Duration `yaml:"connection_lifetime" mapstructure:"connection_lifetime"`
	HealthCheckInterval time.Duration `yaml:"health_check_interval" mapstructure:"health_check_interval"`
	TLS              bool          `yaml:"tls" mapstructure:"tls"`
}

// AuthConfig defines authentication and authorization settings.
type AuthConfig struct {
	Enabled               bool          `yaml:"enabled" mapstructure:"enabled"`
	JWTSecret             string        `yaml:"jwt_secret" mapstructure:"jwt_secret"`
	JWTRefreshSecret      string        `yaml:"jwt_refresh_secret" mapstructure:"jwt_refresh_secret"`
	AccessTokenExpiry     time.Duration `yaml:"access_token_expiry" mapstructure:"access_token_expiry"`
	RefreshTokenExpiry    time.Duration `yaml:"refresh_token_expiry" mapstructure:"refresh_token_expiry"`
	TokenIssuer           string        `yaml:"token_issuer" mapstructure:"token_issuer"`
	TokenAudience         string        `yaml:"token_audience" mapstructure:"token_audience"`
	HeaderName            string        `yaml:"header_name" mapstructure:"header_name"`
	QueryParamName        string        `yaml:"query_param_name" mapstructure:"query_param_name"`
	SkipPaths             []string      `yaml:"skip_paths" mapstructure:"skip_paths"`
	SkipAuthForOptions    bool          `yaml:"skip_auth_for_options" mapstructure:"skip_auth_for_options"`
	RevocationCheck       bool          `yaml:"revocation_check" mapstructure:"revocation_check"`
	RevocationListRefresh time.Duration `yaml:"revocation_list_refresh" mapstructure:"revocation_list_refresh"`
	CacheTokens           bool          `yaml:"cache_tokens" mapstructure:"cache_tokens"`
	TokenCacheTTL         time.Duration `yaml:"token_cache_ttl" mapstructure:"token_cache_ttl"`
}

// ThrottleConfig defines default throttling parameters.
type ThrottleConfig struct {
	Enabled              bool          `yaml:"enabled" mapstructure:"enabled"`
	DefaultPolicy        string        `yaml:"default_policy" mapstructure:"default_policy"`
	HeaderEnabled        bool          `yaml:"header_enabled" mapstructure:"header_enabled"`
	UnauthenticatedTier  string        `yaml:"unauthenticated_tier" mapstructure:"unauthenticated_tier"`
	SpikeArrestEnabled   bool          `yaml:"spike_arrest_enabled" mapstructure:"spike_arrest_enabled"`
	SpikeArrestRate      int           `yaml:"spike_arrest_rate" mapstructure:"spike_arrest_rate"`
	ConditionGroupEnabled bool         `yaml:"condition_group_enabled" mapstructure:"condition_group_enabled"`
	JWTClaimEnabled      bool          `yaml:"jwt_claim_enabled" mapstructure:"jwt_claim_enabled"`
	IPBasedThrottling    bool          `yaml:"ip_based_throttling" mapstructure:"ip_based_throttling"`
	DataPublisherEnabled bool          `yaml:"data_publisher_enabled" mapstructure:"data_publisher_enabled"`
	DataPublisherBufferSize int        `yaml:"data_publisher_buffer_size" mapstructure:"data_publisher_buffer_size"`
}

// CacheConfig defines response caching settings.
type CacheConfig struct {
	Enabled          bool          `yaml:"enabled" mapstructure:"enabled"`
	DefaultTTL       time.Duration `yaml:"default_ttl" mapstructure:"default_ttl"`
	MaxSize          int           `yaml:"max_size" mapstructure:"max_size"`
	MaxEntrySize     int64         `yaml:"max_entry_size" mapstructure:"max_entry_size"`
	EvictionPolicy   string        `yaml:"eviction_policy" mapstructure:"eviction_policy"`
	CacheBypassHeader string       `yaml:"cache_bypass_header" mapstructure:"cache_bypass_header"`
	CacheBypassValue string        `yaml:"cache_bypass_value" mapstructure:"cache_bypass_value"`
	StaleWhileRevalidate time.Duration `yaml:"stale_while_revalidate" mapstructure:"stale_while_revalidate"`
	CacheableMethods []string      `yaml:"cacheable_methods" mapstructure:"cacheable_methods"`
	CacheableStatusCodes []int     `yaml:"cacheable_status_codes" mapstructure:"cacheable_status_codes"`
	VaryByHeaders    []string      `yaml:"vary_by_headers" mapstructure:"vary_by_headers"`
}

// AnalyticsConfig defines analytics and monitoring settings.
type AnalyticsConfig struct {
	Enabled             bool          `yaml:"enabled" mapstructure:"enabled"`
	RetentionPeriod     time.Duration `yaml:"retention_period" mapstructure:"retention_period"`
	BatchSize           int           `yaml:"batch_size" mapstructure:"batch_size"`
	FlushInterval       time.Duration `yaml:"flush_interval" mapstructure:"flush_interval"`
	BufferSize          int           `yaml:"buffer_size" mapstructure:"buffer_size"`
	StoreEnabled        bool          `yaml:"store_enabled" mapstructure:"store_enabled"`
	StoreType           string        `yaml:"store_type" mapstructure:"store_type"`
	StoreEndpoint       string        `yaml:"store_endpoint,omitempty" mapstructure:"store_endpoint"`
	GeoLocationEnabled  bool          `yaml:"geo_location_enabled" mapstructure:"geo_location_enabled"`
	UserAgentParsing    bool          `yaml:"user_agent_parsing" mapstructure:"user_agent_parsing"`
}

// CORSConfig defines global CORS settings.
type CORSConfig struct {
	Enabled          bool     `yaml:"enabled" mapstructure:"enabled"`
	AllowOrigins     []string `yaml:"allow_origins" mapstructure:"allow_origins"`
	AllowMethods     []string `yaml:"allow_methods" mapstructure:"allow_methods"`
	AllowHeaders     []string `yaml:"allow_headers" mapstructure:"allow_headers"`
	ExposeHeaders    []string `yaml:"expose_headers" mapstructure:"expose_headers"`
	AllowCredentials bool     `yaml:"allow_credentials" mapstructure:"allow_credentials"`
	MaxAge           int      `yaml:"max_age" mapstructure:"max_age"`
}

// KeyManagerConfig defines the embedded key manager settings.
type KeyManagerConfig struct {
	Enabled         bool     `yaml:"enabled" mapstructure:"enabled"`
	Port            int      `yaml:"port" mapstructure:"port"`
	SupportedGrants []string `yaml:"supported_grants" mapstructure:"supported_grants"`
	RevokeTokensEnabled bool   `yaml:"revoke_tokens_enabled" mapstructure:"revoke_tokens_enabled"`
}

// PublisherConfig defines the publisher API settings.
type PublisherConfig struct {
	Enabled bool `yaml:"enabled" mapstructure:"enabled"`
	Port    int  `yaml:"port" mapstructure:"port"`
}

// LogConfig defines logging settings.
type LogConfig struct {
	Level      string `yaml:"level" mapstructure:"level"`
	Format     string `yaml:"format" mapstructure:"format"`
	Output     string `yaml:"output" mapstructure:"output"`
	FilePath   string `yaml:"file_path,omitempty" mapstructure:"file_path"`
	MaxSize    int    `yaml:"max_size" mapstructure:"max_size"`
	MaxBackups int    `yaml:"max_backups" mapstructure:"max_backups"`
	MaxAge     int    `yaml:"max_age" mapstructure:"max_age"`
	Compress   bool   `yaml:"compress" mapstructure:"compress"`
}

// MutualTLSConfig defines mTLS settings.
type MutualTLSConfig struct {
	Enabled       bool     `yaml:"enabled" mapstructure:"enabled"`
	ClientCAFile  string   `yaml:"client_ca_file" mapstructure:"client_ca_file"`
	InsecureSkipVerify bool `yaml:"insecure_skip_verify" mapstructure:"insecure_skip_verify"`
}

// RateLimitConfig defines rate limiting settings.
type RateLimitConfig struct {
	Enabled             bool          `yaml:"enabled" mapstructure:"enabled"`
	DefaultLimit        int           `yaml:"default_limit" mapstructure:"default_limit"`
	DefaultWindow       time.Duration `yaml:"default_window" mapstructure:"default_window"`
	HeaderLimitName     string        `yaml:"header_limit_name" mapstructure:"header_limit_name"`
	HeaderRemainingName string        `yaml:"header_remaining_name" mapstructure:"header_remaining_name"`
	HeaderResetName     string        `yaml:"header_reset_name" mapstructure:"header_reset_name"`
	HeaderRetryAfterName string       `yaml:"header_retry_after_name" mapstructure:"header_retry_after_name"`
	RedisEnabled        bool          `yaml:"redis_enabled" mapstructure:"redis_enabled"`
	RedisAddr           string        `yaml:"redis_addr" mapstructure:"redis_addr"`
	RedisPassword       string        `yaml:"redis_password,omitempty" mapstructure:"redis_password"`
	RedisDB             int           `yaml:"redis_db" mapstructure:"redis_db"`
}

// ---------------------------------------------------------------------------
// Configuration Manager
// ---------------------------------------------------------------------------

// Manager handles configuration loading, access, and hot reload.
type Manager struct {
	config     *Config
	mu         sync.RWMutex
	listeners  []func(*Config)
	listenersMu sync.RWMutex
}

// NewManager creates a new configuration manager.
func NewManager() *Manager {
	return &Manager{
		config:    DefaultConfig(),
		listeners: make([]func(*Config), 0),
	}
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:            9443,
			TLSEnabled:      false,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     120 * time.Second,
			ShutdownTimeout: 30 * time.Second,
			MaxHeaderBytes:  1 << 20, // 1 MB
			KeepAlive:       true,
		},
		Gateway: GatewayConfig{
			WorkerPoolSize:      100,
			RequestBufferSize:   1000,
			ResponseBufferSize:  1000,
			MaxRequestBodySize:  10 << 20, // 10 MB
			MaxResponseBodySize: 10 << 20,
			RequestTimeout:      30 * time.Second,
			RetryCount:          3,
			RetryBackoff:        100 * time.Millisecond,
			CircuitBreaker: CircuitBreakerConfig{
				Enabled:          true,
				FailureThreshold: 5,
				SuccessThreshold: 2,
				Timeout:          30 * time.Second,
				HalfOpenMaxCalls: 3,
			},
			ProxyReadTimeout:  30 * time.Second,
			ProxyWriteTimeout: 30 * time.Second,
			WebSocketEnabled:  true,
			SSEEnabled:        true,
			SchemaValidation:  true,
			EnforceHTTPS:      false,
		},
		Database: DatabaseConfig{
			Host:                "localhost",
			Port:                6380,
			Database:            "apim",
			Timeout:             10 * time.Second,
			MaxRetries:          3,
			PoolSize:            10,
			ConnectionLifetime:  30 * time.Minute,
			HealthCheckInterval: 30 * time.Second,
			TLS:                 false,
		},
		Auth: AuthConfig{
			Enabled:            true,
			JWTSecret:          "vedadb-apim-jwt-secret-change-in-production",
			JWTRefreshSecret:   "vedadb-apim-refresh-secret-change-in-production",
			AccessTokenExpiry:  15 * time.Minute,
			RefreshTokenExpiry: 7 * 24 * time.Hour,
			TokenIssuer:        "vedadb-apim",
			TokenAudience:      "vedadb-apim-gateway",
			HeaderName:         "Authorization",
			QueryParamName:     "access_token",
			SkipAuthForOptions: true,
			RevocationCheck:    true,
			CacheTokens:        true,
			TokenCacheTTL:      5 * time.Minute,
		},
		Throttle: ThrottleConfig{
			Enabled:               true,
			DefaultPolicy:         "Unlimited",
			HeaderEnabled:         true,
			UnauthenticatedTier:   "Bronze",
			SpikeArrestEnabled:    true,
			SpikeArrestRate:       1000,
			IPBasedThrottling:     true,
			DataPublisherEnabled:  true,
			DataPublisherBufferSize: 10000,
		},
		Cache: CacheConfig{
			Enabled:              true,
			DefaultTTL:           5 * time.Minute,
			MaxSize:              10000,
			MaxEntrySize:         1 << 20, // 1 MB
			EvictionPolicy:       "lru",
			CacheBypassHeader:    "X-Cache-Bypass",
			CacheBypassValue:     "true",
			StaleWhileRevalidate: 1 * time.Minute,
			CacheableMethods:     []string{"GET", "HEAD"},
			CacheableStatusCodes: []int{200, 201, 204, 301, 302, 404},
		},
		Analytics: AnalyticsConfig{
			Enabled:            true,
			RetentionPeriod:    30 * 24 * time.Hour, // 30 days
			BatchSize:          100,
			FlushInterval:      10 * time.Second,
			BufferSize:         10000,
			StoreEnabled:       true,
			StoreType:          "vedadb",
			GeoLocationEnabled: false,
			UserAgentParsing:   true,
		},
		CORS: CORSConfig{
			Enabled:          true,
			AllowOrigins:     []string{"*"},
			AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID"},
			ExposeHeaders:    []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining"},
			AllowCredentials: true,
			MaxAge:           86400,
		},
		KeyManager: KeyManagerConfig{
			Enabled:             true,
			Port:                9444,
			SupportedGrants:     []string{"client_credentials", "password", "refresh_token", "authorization_code"},
			RevokeTokensEnabled: true,
		},
		Publisher: PublisherConfig{
			Enabled: true,
			Port:    9445,
		},
		Log: LogConfig{
			Level:      "info",
			Format:     "json",
			Output:     "stdout",
			MaxSize:    100,
			MaxBackups: 5,
			MaxAge:     30,
			Compress:   true,
		},
		RateLimit: RateLimitConfig{
			Enabled:              true,
			DefaultLimit:         100,
			DefaultWindow:        time.Minute,
			HeaderLimitName:      "X-RateLimit-Limit",
			HeaderRemainingName:  "X-RateLimit-Remaining",
			HeaderResetName:      "X-RateLimit-Reset",
			HeaderRetryAfterName: "Retry-After",
		},
	}
}

// Load reads configuration from a YAML file and overrides with environment variables.
func (m *Manager) Load(path string) error {
	cfg := DefaultConfig()

	// Load from YAML file if it exists
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read config file %s: %w", path, err)
			}
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return fmt.Errorf("failed to parse config file %s: %w", path, err)
			}
		}
	}

	// Override with environment variables
	overrideFromEnv(cfg)

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	m.mu.Lock()
	m.config = cfg
	m.mu.Unlock()

	// Notify listeners
	m.notifyListeners()

	return nil
}

// Get returns the current configuration (read-only copy).
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// Reload reloads the configuration from the given path.
func (m *Manager) Reload(path string) error {
	return m.Load(path)
}

// AddListener registers a callback that will be invoked when the config is reloaded.
func (m *Manager) AddListener(fn func(*Config)) {
	m.listenersMu.Lock()
	defer m.listenersMu.Unlock()
	m.listeners = append(m.listeners, fn)
}

// notifyListeners invokes all registered configuration change listeners.
func (m *Manager) notifyListeners() {
	m.listenersMu.RLock()
	listeners := make([]func(*Config), len(m.listeners))
	copy(listeners, m.listeners)
	m.listenersMu.RUnlock()

	for _, fn := range listeners {
		go fn(m.Get())
	}
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.Server.Port)
	}
	if c.Server.ReadTimeout <= 0 {
		return fmt.Errorf("server.read_timeout must be positive")
	}
	if c.Server.WriteTimeout <= 0 {
		return fmt.Errorf("server.write_timeout must be positive")
	}
	if c.Database.Host == "" {
		return fmt.Errorf("database.host is required")
	}
	if c.Database.Port <= 0 || c.Database.Port > 65535 {
		return fmt.Errorf("database.port must be between 1 and 65535")
	}
	if c.Auth.Enabled && c.Auth.JWTSecret == "vedadb-apim-jwt-secret-change-in-production" {
		// Warning only - don't fail in development
	}
	if c.Gateway.WorkerPoolSize <= 0 {
		c.Gateway.WorkerPoolSize = 100
	}
	if c.Gateway.RequestTimeout <= 0 {
		c.Gateway.RequestTimeout = 30 * time.Second
	}
	if c.Cache.MaxSize <= 0 {
		c.Cache.MaxSize = 10000
	}
	if c.Throttle.SpikeArrestRate <= 0 {
		c.Throttle.SpikeArrestRate = 1000
	}
	return nil
}

// overrideFromEnv overrides configuration values from environment variables.
func overrideFromEnv(cfg *Config) {
	if v := os.Getenv("VAPIM_SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("VAPIM_SERVER_TLS_ENABLED"); v != "" {
		cfg.Server.TLSEnabled = parseBool(v)
	}
	if v := os.Getenv("VAPIM_DATABASE_HOST"); v != "" {
		cfg.Database.Host = v
	}
	if v := os.Getenv("VAPIM_DATABASE_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Database.Port = port
		}
	}
	if v := os.Getenv("VAPIM_DATABASE_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("VAPIM_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("VAPIM_JWT_REFRESH_SECRET"); v != "" {
		cfg.Auth.JWTRefreshSecret = v
	}
	if v := os.Getenv("VAPIM_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("VAPIM_THROTTLE_ENABLED"); v != "" {
		cfg.Throttle.Enabled = parseBool(v)
	}
	if v := os.Getenv("VAPIM_CACHE_ENABLED"); v != "" {
		cfg.Cache.Enabled = parseBool(v)
	}
	if v := os.Getenv("VAPIM_ANALYTICS_ENABLED"); v != "" {
		cfg.Analytics.Enabled = parseBool(v)
	}
	if v := os.Getenv("VAPIM_GATEWAY_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Gateway.WorkerPoolSize = n
		}
	}
	if v := os.Getenv("VAPIM_AUTH_ENABLED"); v != "" {
		cfg.Auth.Enabled = parseBool(v)
	}
	if v := os.Getenv("VAPIM_RATE_LIMIT_ENABLED"); v != "" {
		cfg.RateLimit.Enabled = parseBool(v)
	}
}

// parseBool parses a string as a boolean value.
func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}

// LoadConfig is a convenience function to load configuration from a file.
func LoadConfig(path string) (*Config, error) {
	mgr := NewManager()
	if err := mgr.Load(path); err != nil {
		return nil, err
	}
	return mgr.Get(), nil
}

// MustLoadConfig loads configuration from a file and panics on error.
func MustLoadConfig(path string) *Config {
	cfg, err := LoadConfig(path)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	return cfg
}

// FindConfigFile searches for a config file in common locations.
func FindConfigFile() string {
	locations := []string{
		"configs/config.yaml",
		"config.yaml",
		"/etc/vedadb-apim/config.yaml",
		os.Getenv("VAPIM_CONFIG_PATH"),
	}
	for _, loc := range locations {
		if loc == "" {
			continue
		}
		if _, err := os.Stat(loc); err == nil {
			abs, err := filepath.Abs(loc)
			if err == nil {
				return abs
			}
			return loc
		}
	}
	return ""
}
