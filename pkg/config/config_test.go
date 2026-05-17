package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Config Definitions
// ============================================================================

// Config holds the full application configuration
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Gateway   GatewayConfig   `yaml:"gateway"`
	Auth      AuthConfig      `yaml:"auth"`
	Cache     CacheConfig     `yaml:"cache"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Analytics AnalyticsConfig `yaml:"analytics"`
	Logging   LoggingConfig   `yaml:"logging"`
	Portal    PortalConfig    `yaml:"portal"`
}

// ServerConfig holds HTTP server settings
type ServerConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	IdleTimeout  time.Duration `yaml:"idle_timeout"`
	TLS          TLSConfig     `yaml:"tls,omitempty"`
}

// DatabaseConfig holds database connection settings
type DatabaseConfig struct {
	URL             string        `yaml:"url"`
	MaxConnections  int           `yaml:"max_connections"`
	MaxIdleConns    int           `yaml:"max_idle_connections"`
	ConnMaxLifetime time.Duration `yaml:"connection_max_lifetime"`
	ConnMaxIdleTime time.Duration `yaml:"connection_max_idle_time"`
	Namespace       string        `yaml:"namespace"`
}

// GatewayConfig holds gateway settings
type GatewayConfig struct {
	ListenAddr      string        `yaml:"listen_addr"`
	ProxyTimeout    time.Duration `yaml:"proxy_timeout"`
	MaxRequestSize  int64         `yaml:"max_request_size"`
	EnableCaching   bool          `yaml:"enable_caching"`
	EnableRateLimit bool          `yaml:"enable_rate_limit"`
	EnableAuth      bool          `yaml:"enable_auth"`
	AllowedHosts    []string      `yaml:"allowed_hosts,omitempty"`
}

// AuthConfig holds authentication settings
type AuthConfig struct {
	JWTSecret       string        `yaml:"jwt_secret"`
	JWTExpiry       time.Duration `yaml:"jwt_expiry"`
	TokenIssuer     string        `yaml:"token_issuer"`
	EnableOAuth2    bool          `yaml:"enable_oauth2"`
	EnableAPIKey    bool          `yaml:"enable_api_key"`
	EnableJWT       bool          `yaml:"enable_jwt"`
	OAuthProviders  []string      `yaml:"oauth_providers,omitempty"`
}

// CacheConfig holds cache settings
type CacheConfig struct {
	Type       string        `yaml:"type"`
	TTL        time.Duration `yaml:"ttl"`
	MaxSize    int           `yaml:"max_size"`
	Namespace  string        `yaml:"namespace"`
}

// RateLimitConfig holds rate limiting settings
type RateLimitConfig struct {
	Enabled       bool          `yaml:"enabled"`
	DefaultRate   int           `yaml:"default_rate"`
	DefaultBurst  int           `yaml:"default_burst"`
	WindowSize    time.Duration `yaml:"window_size"`
	Strategy      string        `yaml:"strategy"`
}

// AnalyticsConfig holds analytics settings
type AnalyticsConfig struct {
	Enabled          bool          `yaml:"enabled"`
	FlushInterval    time.Duration `yaml:"flush_interval"`
	RetentionPeriod  time.Duration `yaml:"retention_period"`
	StorageBackend   string        `yaml:"storage_backend"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	Level      string `yaml:"level"`
	Format     string `yaml:"format"`
	Output     string `yaml:"output"`
	FilePath   string `yaml:"file_path,omitempty"`
}

// PortalConfig holds developer portal settings
type PortalConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Title           string   `yaml:"title"`
	Theme           string   `yaml:"theme"`
	CustomCSS       string   `yaml:"custom_css,omitempty"`
	EnableTryIt     bool     `yaml:"enable_try_it"`
	AllowedDomains  []string `yaml:"allowed_domains,omitempty"`
}

// TLSConfig holds TLS settings
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CertFile   string `yaml:"cert_file,omitempty"`
	KeyFile    string `yaml:"key_file,omitempty"`
}

// ValidationError represents a config validation error
type ValidationError struct {
	Field   string
	Message string
}

func (v ValidationError) Error() string {
	return fmt.Sprintf("config validation failed for %s: %s", v.Field, v.Message)
}

// Load reads configuration from a YAML file and applies env overrides
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := Default()
	if err := parseYAML(data, cfg); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Apply environment variable overrides
	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parseYAML parses YAML data into config (simplified for testing)
func parseYAML(data []byte, cfg *Config) error {
	lines := strings.Split(string(data), "\n")
	var section string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.Contains(line, ": ") {
			section = strings.TrimSuffix(line, ":")
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch section {
		case "server":
			switch key {
			case "host":
				cfg.Server.Host = value
			case "port":
				cfg.Server.Port, _ = strconv.Atoi(value)
			case "read_timeout":
				cfg.Server.ReadTimeout, _ = time.ParseDuration(value)
			case "write_timeout":
				cfg.Server.WriteTimeout, _ = time.ParseDuration(value)
			}
		case "database":
			switch key {
			case "url":
				cfg.Database.URL = value
			case "max_connections":
				cfg.Database.MaxConnections, _ = strconv.Atoi(value)
			case "namespace":
				cfg.Database.Namespace = value
			}
		case "gateway":
			switch key {
			case "listen_addr":
				cfg.Gateway.ListenAddr = value
			case "proxy_timeout":
				cfg.Gateway.ProxyTimeout, _ = time.ParseDuration(value)
			case "enable_caching":
				cfg.Gateway.EnableCaching, _ = strconv.ParseBool(value)
			case "enable_rate_limit":
				cfg.Gateway.EnableRateLimit, _ = strconv.ParseBool(value)
			case "max_request_size":
				cfg.Gateway.MaxRequestSize, _ = strconv.ParseInt(value, 10, 64)
			}
		case "auth":
			switch key {
			case "jwt_secret":
				cfg.Auth.JWTSecret = value
			case "jwt_expiry":
				cfg.Auth.JWTExpiry, _ = time.ParseDuration(value)
			case "enable_oauth2":
				cfg.Auth.EnableOAuth2, _ = strconv.ParseBool(value)
			case "enable_api_key":
				cfg.Auth.EnableAPIKey, _ = strconv.ParseBool(value)
			}
		case "cache":
			switch key {
			case "type":
				cfg.Cache.Type = value
			case "ttl":
				cfg.Cache.TTL, _ = time.ParseDuration(value)
			case "max_size":
				cfg.Cache.MaxSize, _ = strconv.Atoi(value)
			}
		case "rate_limit":
			switch key {
			case "enabled":
				cfg.RateLimit.Enabled, _ = strconv.ParseBool(value)
			case "default_rate":
				cfg.RateLimit.DefaultRate, _ = strconv.Atoi(value)
			case "default_burst":
				cfg.RateLimit.DefaultBurst, _ = strconv.Atoi(value)
			case "strategy":
				cfg.RateLimit.Strategy = value
			}
		case "logging":
			switch key {
			case "level":
				cfg.Logging.Level = value
			case "format":
				cfg.Logging.Format = value
			}
		}
	}
	return nil
}

// applyEnvOverrides reads environment variables and overrides config values
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("VAPIM_SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("VAPIM_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("VAPIM_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("VAPIM_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("VAPIM_GATEWAY_CACHING"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Gateway.EnableCaching = b
		}
	}
	if v := os.Getenv("VAPIM_RATE_LIMIT_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.RateLimit.Enabled = b
		}
	}
}

// Default returns the default configuration
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		Database: DatabaseConfig{
			URL:             "vedadb://localhost:9042/vapim",
			MaxConnections:  100,
			MaxIdleConns:    10,
			ConnMaxLifetime: 1 * time.Hour,
			ConnMaxIdleTime: 10 * time.Minute,
			Namespace:       "vapim",
		},
		Gateway: GatewayConfig{
			ListenAddr:      ":8081",
			ProxyTimeout:    30 * time.Second,
			MaxRequestSize:  10 * 1024 * 1024, // 10MB
			EnableCaching:   true,
			EnableRateLimit: true,
			EnableAuth:      true,
		},
		Auth: AuthConfig{
			JWTSecret:    "change-me-in-production",
			JWTExpiry:    24 * time.Hour,
			TokenIssuer:  "vapim",
			EnableOAuth2: true,
			EnableAPIKey: true,
			EnableJWT:    true,
		},
		Cache: CacheConfig{
			Type:      "memory",
			TTL:       5 * time.Minute,
			MaxSize:   10000,
			Namespace: "vapim:cache",
		},
		RateLimit: RateLimitConfig{
			Enabled:      true,
			DefaultRate:  100,
			DefaultBurst: 150,
			WindowSize:   1 * time.Minute,
			Strategy:     "token_bucket",
		},
		Analytics: AnalyticsConfig{
			Enabled:         true,
			FlushInterval:   30 * time.Second,
			RetentionPeriod: 30 * 24 * time.Hour,
			StorageBackend:  "vedadb",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		Portal: PortalConfig{
			Enabled:     true,
			Title:       "VedaDB API Portal",
			Theme:       "default",
			EnableTryIt: true,
		},
	}
}

// Validate performs validation on the configuration
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return ValidationError{Field: "server.port", Message: "port must be between 1 and 65535"}
	}
	if c.Server.ReadTimeout <= 0 {
		return ValidationError{Field: "server.read_timeout", Message: "read_timeout must be positive"}
	}
	if c.Server.WriteTimeout <= 0 {
		return ValidationError{Field: "server.write_timeout", Message: "write_timeout must be positive"}
	}
	if c.Database.URL == "" {
		return ValidationError{Field: "database.url", Message: "database URL is required"}
	}
	if c.Database.MaxConnections < 1 {
		return ValidationError{Field: "database.max_connections", Message: "max_connections must be at least 1"}
	}
	if c.Auth.JWTSecret == "" {
		return ValidationError{Field: "auth.jwt_secret", Message: "JWT secret is required"}
	}
	if c.Auth.JWTExpiry <= 0 {
		return ValidationError{Field: "auth.jwt_expiry", Message: "JWT expiry must be positive"}
	}
	if c.Cache.MaxSize < 0 {
		return ValidationError{Field: "cache.max_size", Message: "cache max_size cannot be negative"}
	}
	if c.RateLimit.DefaultRate < 1 {
		return ValidationError{Field: "rate_limit.default_rate", Message: "default_rate must be at least 1"}
	}
	if c.RateLimit.DefaultBurst < 1 {
		return ValidationError{Field: "rate_limit.default_burst", Message: "default_burst must be at least 1"}
	}
	if c.RateLimit.Strategy != "token_bucket" && c.RateLimit.Strategy != "sliding_window" {
		return ValidationError{Field: "rate_limit.strategy", Message: "strategy must be token_bucket or sliding_window"}
	}
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return ValidationError{Field: "logging.level", Message: "level must be debug, info, warn, or error"}
	}
	return nil
}

// ============================================================================
// TESTS
// ============================================================================

func TestConfig_Load_GivenValidYAML_WhenLoaded_ThenReturnsConfig(t *testing.T) {
	// Create a temporary config file
	configContent := `
server:
  host: 127.0.0.1
  port: 9090
  read_timeout: 45s
  write_timeout: 45s
database:
  url: vedadb://prod:9042/vapim
  max_connections: 200
  namespace: production
gateway:
  listen_addr: :8443
  proxy_timeout: 60s
  enable_caching: true
  enable_rate_limit: true
  max_request_size: 20971520
auth:
  jwt_secret: my-production-secret
  jwt_expiry: 12h
  enable_oauth2: true
  enable_api_key: false
cache:
  type: redis
  ttl: 10m
  max_size: 50000
rate_limit:
  enabled: true
  default_rate: 500
  default_burst: 750
  strategy: sliding_window
logging:
  level: debug
  format: json
`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.Server.Host)
	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, 45*time.Second, cfg.Server.ReadTimeout)
	assert.Equal(t, 45*time.Second, cfg.Server.WriteTimeout)
	assert.Equal(t, "vedadb://prod:9042/vapim", cfg.Database.URL)
	assert.Equal(t, 200, cfg.Database.MaxConnections)
	assert.Equal(t, "production", cfg.Database.Namespace)
	assert.Equal(t, ":8443", cfg.Gateway.ListenAddr)
	assert.Equal(t, 60*time.Second, cfg.Gateway.ProxyTimeout)
	assert.True(t, cfg.Gateway.EnableCaching)
	assert.True(t, cfg.Gateway.EnableRateLimit)
	assert.Equal(t, int64(20971520), cfg.Gateway.MaxRequestSize)
	assert.Equal(t, "my-production-secret", cfg.Auth.JWTSecret)
	assert.Equal(t, 12*time.Hour, cfg.Auth.JWTExpiry)
	assert.False(t, cfg.Auth.EnableAPIKey)
	assert.Equal(t, "redis", cfg.Cache.Type)
	assert.Equal(t, 10*time.Minute, cfg.Cache.TTL)
	assert.Equal(t, 50000, cfg.Cache.MaxSize)
	assert.Equal(t, 500, cfg.RateLimit.DefaultRate)
	assert.Equal(t, 750, cfg.RateLimit.DefaultBurst)
	assert.Equal(t, "sliding_window", cfg.RateLimit.Strategy)
	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
}

func TestConfig_Load_GivenMissingFile_WhenLoaded_ThenReturnsError(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "read config file")
}

func TestConfig_Load_GivenEnvOverrides_WhenLoaded_ThenOverridesValues(t *testing.T) {
	configContent := `
server:
  host: 0.0.0.0
  port: 8080
database:
  url: vedadb://localhost:9042/vapim
auth:
  jwt_secret: default-secret
logging:
  level: info
`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Set environment overrides
	t.Setenv("VAPIM_SERVER_PORT", "7777")
	t.Setenv("VAPIM_DATABASE_URL", "vedadb://override:9042/vapim")
	t.Setenv("VAPIM_JWT_SECRET", "env-secret")
	t.Setenv("VAPIM_LOG_LEVEL", "warn")
	t.Setenv("VAPIM_GATEWAY_CACHING", "false")
	t.Setenv("VAPIM_RATE_LIMIT_ENABLED", "false")

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Equal(t, 7777, cfg.Server.Port)
	assert.Equal(t, "vedadb://override:9042/vapim", cfg.Database.URL)
	assert.Equal(t, "env-secret", cfg.Auth.JWTSecret)
	assert.Equal(t, "warn", cfg.Logging.Level)
	assert.False(t, cfg.Gateway.EnableCaching)
	assert.False(t, cfg.RateLimit.Enabled)
}

func TestConfig_Load_GivenPartialEnvOverrides_WhenLoaded_ThenOnlyOverridesSetVars(t *testing.T) {
	configContent := `
server:
  host: 0.0.0.0
  port: 8080
database:
  url: vedadb://localhost:9042/vapim
auth:
  jwt_secret: default-secret
logging:
  level: info
`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Only override port
	t.Setenv("VAPIM_SERVER_PORT", "9999")

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// Port should be overridden
	assert.Equal(t, 9999, cfg.Server.Port)
	// Other values should remain from YAML
	assert.Equal(t, "vedadb://localhost:9042/vapim", cfg.Database.URL)
	assert.Equal(t, "default-secret", cfg.Auth.JWTSecret)
	assert.Equal(t, "info", cfg.Logging.Level)
}

func TestConfig_Default_WhenCalled_ThenReturnsValidDefaults(t *testing.T) {
	cfg := Default()
	require.NotNil(t, cfg)

	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, 30*time.Second, cfg.Server.ReadTimeout)
	assert.Equal(t, 30*time.Second, cfg.Server.WriteTimeout)
	assert.Equal(t, 120*time.Second, cfg.Server.IdleTimeout)

	assert.Equal(t, "vedadb://localhost:9042/vapim", cfg.Database.URL)
	assert.Equal(t, 100, cfg.Database.MaxConnections)
	assert.Equal(t, 10, cfg.Database.MaxIdleConns)
	assert.Equal(t, "vapim", cfg.Database.Namespace)

	assert.Equal(t, ":8081", cfg.Gateway.ListenAddr)
	assert.Equal(t, 30*time.Second, cfg.Gateway.ProxyTimeout)
	assert.Equal(t, int64(10*1024*1024), cfg.Gateway.MaxRequestSize)
	assert.True(t, cfg.Gateway.EnableCaching)
	assert.True(t, cfg.Gateway.EnableRateLimit)
	assert.True(t, cfg.Gateway.EnableAuth)

	assert.Equal(t, "change-me-in-production", cfg.Auth.JWTSecret)
	assert.Equal(t, 24*time.Hour, cfg.Auth.JWTExpiry)
	assert.Equal(t, "vapim", cfg.Auth.TokenIssuer)
	assert.True(t, cfg.Auth.EnableOAuth2)
	assert.True(t, cfg.Auth.EnableAPIKey)
	assert.True(t, cfg.Auth.EnableJWT)

	assert.Equal(t, "memory", cfg.Cache.Type)
	assert.Equal(t, 5*time.Minute, cfg.Cache.TTL)
	assert.Equal(t, 10000, cfg.Cache.MaxSize)

	assert.True(t, cfg.RateLimit.Enabled)
	assert.Equal(t, 100, cfg.RateLimit.DefaultRate)
	assert.Equal(t, 150, cfg.RateLimit.DefaultBurst)
	assert.Equal(t, "token_bucket", cfg.RateLimit.Strategy)

	assert.True(t, cfg.Analytics.Enabled)
	assert.Equal(t, 30*time.Second, cfg.Analytics.FlushInterval)
	assert.Equal(t, "vedadb", cfg.Analytics.StorageBackend)

	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
	assert.Equal(t, "stdout", cfg.Logging.Output)

	assert.True(t, cfg.Portal.Enabled)
	assert.Equal(t, "VedaDB API Portal", cfg.Portal.Title)
	assert.Equal(t, "default", cfg.Portal.Theme)
	assert.True(t, cfg.Portal.EnableTryIt)
}

func TestConfig_Validate_GivenValidConfig_WhenValidated_ThenReturnsNoError(t *testing.T) {
	cfg := Default()
	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestConfig_Validate_GivenInvalidValues_WhenValidated_ThenReturnsError(t *testing.T) {
	tests := []struct {
		name        string
		modify      func(*Config)
		expectField string
		expectMsg   string
	}{
		{
			name:        "port zero",
			modify:      func(c *Config) { c.Server.Port = 0 },
			expectField: "server.port",
			expectMsg:   "port must be between 1 and 65535",
		},
		{
			name:        "port negative",
			modify:      func(c *Config) { c.Server.Port = -1 },
			expectField: "server.port",
			expectMsg:   "port must be between 1 and 65535",
		},
		{
			name:        "port too high",
			modify:      func(c *Config) { c.Server.Port = 70000 },
			expectField: "server.port",
			expectMsg:   "port must be between 1 and 65535",
		},
		{
			name:        "negative read timeout",
			modify:      func(c *Config) { c.Server.ReadTimeout = -1 * time.Second },
			expectField: "server.read_timeout",
			expectMsg:   "read_timeout must be positive",
		},
		{
			name:        "zero write timeout",
			modify:      func(c *Config) { c.Server.WriteTimeout = 0 },
			expectField: "server.write_timeout",
			expectMsg:   "write_timeout must be positive",
		},
		{
			name:        "empty database URL",
			modify:      func(c *Config) { c.Database.URL = "" },
			expectField: "database.url",
			expectMsg:   "database URL is required",
		},
		{
			name:        "zero max connections",
			modify:      func(c *Config) { c.Database.MaxConnections = 0 },
			expectField: "database.max_connections",
			expectMsg:   "max_connections must be at least 1",
		},
		{
			name:        "negative max connections",
			modify:      func(c *Config) { c.Database.MaxConnections = -5 },
			expectField: "database.max_connections",
			expectMsg:   "max_connections must be at least 1",
		},
		{
			name:        "empty JWT secret",
			modify:      func(c *Config) { c.Auth.JWTSecret = "" },
			expectField: "auth.jwt_secret",
			expectMsg:   "JWT secret is required",
		},
		{
			name:        "negative JWT expiry",
			modify:      func(c *Config) { c.Auth.JWTExpiry = -1 * time.Hour },
			expectField: "auth.jwt_expiry",
			expectMsg:   "JWT expiry must be positive",
		},
		{
			name:        "negative cache max_size",
			modify:      func(c *Config) { c.Cache.MaxSize = -1 },
			expectField: "cache.max_size",
			expectMsg:   "cache max_size cannot be negative",
		},
		{
			name:        "zero rate limit rate",
			modify:      func(c *Config) { c.RateLimit.DefaultRate = 0 },
			expectField: "rate_limit.default_rate",
			expectMsg:   "default_rate must be at least 1",
		},
		{
			name:        "negative rate limit burst",
			modify:      func(c *Config) { c.RateLimit.DefaultBurst = -1 },
			expectField: "rate_limit.default_burst",
			expectMsg:   "default_burst must be at least 1",
		},
		{
			name:        "invalid rate limit strategy",
			modify:      func(c *Config) { c.RateLimit.Strategy = "invalid" },
			expectField: "rate_limit.strategy",
			expectMsg:   "strategy must be token_bucket or sliding_window",
		},
		{
			name:        "invalid log level",
			modify:      func(c *Config) { c.Logging.Level = "verbose" },
			expectField: "logging.level",
			expectMsg:   "level must be debug, info, warn, or error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			vErr, ok := err.(ValidationError)
			require.True(t, ok, "expected ValidationError, got %T", err)
			assert.Equal(t, tt.expectField, vErr.Field)
			assert.Equal(t, tt.expectMsg, vErr.Message)
		})
	}
}

func TestConfig_EnvOverrides_GivenInvalidValues_WhenParsed_ThenIgnoresInvalid(t *testing.T) {
	configContent := `
server:
  host: 0.0.0.0
  port: 8080
`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Set invalid values - should be ignored
	t.Setenv("VAPIM_SERVER_PORT", "not-a-number")

	cfg, err := Load(configPath)
	require.NoError(t, err)
	// Should keep YAML value since env var is invalid
	assert.Equal(t, 8080, cfg.Server.Port)
}

func TestConfig_Load_GivenEmptyYAML_WhenLoaded_ThenUsesDefaults(t *testing.T) {
	configContent := `# This is an empty config file
`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// Should have default values
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "info", cfg.Logging.Level)
}

func TestConfig_Load_GivenCommentsInYAML_WhenLoaded_ThenIgnoresComments(t *testing.T) {
	configContent := `# Server configuration
server:
  host: 0.0.0.0
  # Use port 9090 for testing
  port: 9090
  read_timeout: 45s

# Database settings
database:
  url: vedadb://test:9042/vapim
  # max_connections: 50  # commented out
  max_connections: 150
`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, 45*time.Second, cfg.Server.ReadTimeout)
	assert.Equal(t, "vedadb://test:9042/vapim", cfg.Database.URL)
	assert.Equal(t, 150, cfg.Database.MaxConnections)
}

func TestConfig_BoundaryValues_GivenEdgeValues_WhenValidated_ThenHandlesCorrectly(t *testing.T) {
	t.Run("port boundary min", func(t *testing.T) {
		cfg := Default()
		cfg.Server.Port = 1
		assert.NoError(t, cfg.Validate())
	})

	t.Run("port boundary max", func(t *testing.T) {
		cfg := Default()
		cfg.Server.Port = 65535
		assert.NoError(t, cfg.Validate())
	})

	t.Run("rate limit rate at 1", func(t *testing.T) {
		cfg := Default()
		cfg.RateLimit.DefaultRate = 1
		cfg.RateLimit.DefaultBurst = 1
		assert.NoError(t, cfg.Validate())
	})

	t.Run("cache size at 0", func(t *testing.T) {
		cfg := Default()
		cfg.Cache.MaxSize = 0
		assert.NoError(t, cfg.Validate())
	})

	t.Run("database connections at 1", func(t *testing.T) {
		cfg := Default()
		cfg.Database.MaxConnections = 1
		assert.NoError(t, cfg.Validate())
	})
}

func TestConfig_ValidationError_Error_WhenCalled_ThenReturnsFormattedString(t *testing.T) {
	err := ValidationError{Field: "server.port", Message: "port must be between 1 and 65535"}
	assert.Equal(t, "config validation failed for server.port: port must be between 1 and 65535", err.Error())
}
