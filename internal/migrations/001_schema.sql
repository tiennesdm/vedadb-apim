-- VedaDB API Manager (VAPIM) v2.0 Schema
-- Compatible with VedaDB SQL interface

-- Tenants
CREATE TABLE IF NOT EXISTS tenants (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    domain VARCHAR(255) UNIQUE NOT NULL,
    tier VARCHAR(50) DEFAULT 'standard',
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Users
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) REFERENCES tenants(id),
    username VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(50) DEFAULT 'subscriber',
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, username)
);

-- APIs
CREATE TABLE IF NOT EXISTS apis (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    context VARCHAR(255) NOT NULL,
    version VARCHAR(50) NOT NULL,
    endpoint VARCHAR(500) NOT NULL,
    auth_type VARCHAR(50) DEFAULT 'oauth2',
    status VARCHAR(20) DEFAULT 'CREATED',
    provider VARCHAR(255),
    tags TEXT,
    thumbnail_url VARCHAR(500),
    rating DECIMAL(3,2) DEFAULT 0,
    rating_count INT DEFAULT 0,
    visibility VARCHAR(20) DEFAULT 'public',
    throttle_policy VARCHAR(100),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, context, version)
);

-- API Resources
CREATE TABLE IF NOT EXISTS api_resources (
    id VARCHAR(36) PRIMARY KEY,
    api_id VARCHAR(36) REFERENCES apis(id),
    method VARCHAR(10) NOT NULL,
    path VARCHAR(255) NOT NULL,
    description TEXT,
    auth_required BOOLEAN DEFAULT true,
    throttle_policy VARCHAR(100),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(api_id, method, path)
);

-- API Versions
CREATE TABLE IF NOT EXISTS api_versions (
    id VARCHAR(36) PRIMARY KEY,
    api_id VARCHAR(36) REFERENCES apis(id),
    version VARCHAR(50) NOT NULL,
    definition TEXT,
    status VARCHAR(20) DEFAULT 'CREATED',
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(api_id, version)
);

-- Applications
CREATE TABLE IF NOT EXISTS applications (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) REFERENCES users(id),
    tier VARCHAR(50) DEFAULT 'Bronze',
    status VARCHAR(20) DEFAULT 'active',
    callback_url VARCHAR(500),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Application Keys
CREATE TABLE IF NOT EXISTS application_keys (
    id VARCHAR(36) PRIMARY KEY,
    app_id VARCHAR(36) REFERENCES applications(id),
    key_type VARCHAR(20) NOT NULL,
    consumer_key VARCHAR(255) NOT NULL,
    consumer_secret VARCHAR(255),
    status VARCHAR(20) DEFAULT 'active',
    expires_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Subscriptions
CREATE TABLE IF NOT EXISTS subscriptions (
    id VARCHAR(36) PRIMARY KEY,
    api_id VARCHAR(36) REFERENCES apis(id),
    app_id VARCHAR(36) REFERENCES applications(id),
    tier VARCHAR(50) DEFAULT 'Bronze',
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(api_id, app_id)
);

-- OAuth2 Clients
CREATE TABLE IF NOT EXISTS oauth2_clients (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) REFERENCES tenants(id),
    client_id VARCHAR(255) UNIQUE NOT NULL,
    client_secret VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    redirect_uris TEXT,
    grant_types TEXT,
    scopes TEXT,
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Tokens
CREATE TABLE IF NOT EXISTS tokens (
    id VARCHAR(36) PRIMARY KEY,
    token VARCHAR(500) NOT NULL,
    token_type VARCHAR(20) NOT NULL,
    client_id VARCHAR(36) REFERENCES oauth2_clients(id),
    user_id VARCHAR(36) REFERENCES users(id),
    scopes TEXT,
    expires_at TIMESTAMP NOT NULL,
    revoked BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- API Keys (generated keys for apps)
CREATE TABLE IF NOT EXISTS api_keys (
    id VARCHAR(36) PRIMARY KEY,
    app_id VARCHAR(36) REFERENCES applications(id),
    key_hash VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    scopes TEXT,
    status VARCHAR(20) DEFAULT 'active',
    expires_at TIMESTAMP,
    last_used_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Throttle Policies
CREATE TABLE IF NOT EXISTS throttle_policies (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) REFERENCES tenants(id),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    rate INT NOT NULL,
    burst INT NOT NULL,
    unit VARCHAR(20) NOT NULL,
    conditions TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Analytics Events
CREATE TABLE IF NOT EXISTS analytics_events (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36),
    request_id VARCHAR(36),
    api_id VARCHAR(36),
    app_id VARCHAR(36),
    user_id VARCHAR(36),
    method VARCHAR(10),
    path VARCHAR(255),
    status_code INT,
    latency_ms INT,
    error_message TEXT,
    user_agent VARCHAR(500),
    client_ip VARCHAR(50),
    country VARCHAR(100),
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Analytics Summary (aggregated)
CREATE TABLE IF NOT EXISTS analytics_summary (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36),
    api_id VARCHAR(36),
    period VARCHAR(20) NOT NULL,
    period_start TIMESTAMP NOT NULL,
    request_count INT DEFAULT 0,
    error_count INT DEFAULT 0,
    avg_latency_ms INT DEFAULT 0,
    p95_latency_ms INT DEFAULT 0,
    p99_latency_ms INT DEFAULT 0,
    unique_users INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tenant_id, api_id, period, period_start)
);

-- Audit Log
CREATE TABLE IF NOT EXISTS audit_log (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36),
    user_id VARCHAR(36),
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    resource_id VARCHAR(36),
    details TEXT,
    ip_address VARCHAR(50),
    user_agent VARCHAR(500),
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Webhooks
CREATE TABLE IF NOT EXISTS webhooks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36),
    api_id VARCHAR(36),
    url VARCHAR(500) NOT NULL,
    events TEXT NOT NULL,
    secret VARCHAR(255),
    status VARCHAR(20) DEFAULT 'active',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Webhook Deliveries
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id VARCHAR(36) PRIMARY KEY,
    webhook_id VARCHAR(36) REFERENCES webhooks(id),
    event_type VARCHAR(100) NOT NULL,
    payload TEXT NOT NULL,
    response_status INT,
    response_body TEXT,
    attempt_count INT DEFAULT 1,
    status VARCHAR(20) DEFAULT 'pending',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Migrations tracking table
CREATE TABLE IF NOT EXISTS schema_migrations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) UNIQUE NOT NULL,
    applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_apis_tenant ON apis(tenant_id);
CREATE INDEX IF NOT EXISTS idx_apis_status ON apis(status);
CREATE INDEX IF NOT EXISTS idx_apis_context ON apis(context);
CREATE INDEX IF NOT EXISTS idx_subscriptions_api ON subscriptions(api_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_app ON subscriptions(app_id);
CREATE INDEX IF NOT EXISTS idx_apps_owner ON applications(owner_id);
CREATE INDEX IF NOT EXISTS idx_apps_tenant ON applications(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tokens_expires ON tokens(expires_at);
CREATE INDEX IF NOT EXISTS idx_analytics_timestamp ON analytics_events(timestamp);
CREATE INDEX IF NOT EXISTS idx_analytics_api ON analytics_events(api_id);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp);
