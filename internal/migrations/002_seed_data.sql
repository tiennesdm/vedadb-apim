-- VedaDB API Manager (VAPIM) v2.0 Seed Data

-- Default tenant
INSERT OR IGNORE INTO tenants (id, name, domain, tier, status) VALUES
('tenant-default-001', 'Default Tenant', 'default.local', 'standard', 'active');

-- Admin user (password: admin123, bcrypt hash)
INSERT OR IGNORE INTO users (id, tenant_id, username, email, password_hash, role, status) VALUES
('user-admin-001', 'tenant-default-001', 'admin', 'admin@default.local', '$2a$10$N9qo8uLOickgx2ZMRZoMy.MqrqxmOBWvEK9K2YZp.j3B.z.FzXj.a', 'super_admin', 'active');

-- Throttle policies: Bronze, Silver, Gold, Unlimited
INSERT OR IGNORE INTO throttle_policies (id, tenant_id, name, type, rate, burst, unit, conditions) VALUES
('policy-bronze-001', 'tenant-default-001', 'Bronze', 'subscription', 100, 10, 'minute', NULL);

INSERT OR IGNORE INTO throttle_policies (id, tenant_id, name, type, rate, burst, unit, conditions) VALUES
('policy-silver-001', 'tenant-default-001', 'Silver', 'subscription', 1000, 50, 'minute', NULL);

INSERT OR IGNORE INTO throttle_policies (id, tenant_id, name, type, rate, burst, unit, conditions) VALUES
('policy-gold-001', 'tenant-default-001', 'Gold', 'subscription', 10000, 200, 'minute', NULL);

INSERT OR IGNORE INTO throttle_policies (id, tenant_id, name, type, rate, burst, unit, conditions) VALUES
('policy-unlimited-001', 'tenant-default-001', 'Unlimited', 'subscription', 1000000, 10000, 'minute', NULL);

-- Sample APIs
INSERT OR IGNORE INTO apis (id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy) VALUES
('api-user-001', 'tenant-default-001', 'User API', 'User management API for creating, updating, and querying users', '/users', 'v1', 'http://user-service:8080', 'oauth2', 'PUBLISHED', 'System', 'users,identity', NULL, 4.5, 12, 'public', 'policy-silver-001');

INSERT OR IGNORE INTO apis (id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy) VALUES
('api-product-001', 'tenant-default-001', 'Product API', 'Product catalog API for managing products and inventory', '/products', 'v1', 'http://product-service:8080', 'oauth2', 'PUBLISHED', 'System', 'products,catalog', NULL, 4.2, 8, 'public', 'policy-silver-001');

INSERT OR IGNORE INTO apis (id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy) VALUES
('api-order-001', 'tenant-default-001', 'Order API', 'Order management API for creating and tracking orders', '/orders', 'v1', 'http://order-service:8080', 'apikey', 'PUBLISHED', 'System', 'orders,ecommerce', NULL, 4.7, 15, 'public', 'policy-gold-001');

INSERT OR IGNORE INTO apis (id, tenant_id, name, description, context, version, endpoint, auth_type, status, provider, tags, thumbnail_url, rating, rating_count, visibility, throttle_policy) VALUES
('api-payment-001', 'tenant-default-001', 'Payment API', 'Payment processing API for handling transactions', '/payments', 'v1', 'http://payment-service:8080', 'mutualtls', 'CREATED', 'System', 'payments,finance', NULL, 0, 0, 'private', 'policy-gold-001');

-- API Resources for User API
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-user-001', 'api-user-001', 'GET', '/users', 'List all users', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-user-002', 'api-user-001', 'POST', '/users', 'Create a new user', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-user-003', 'api-user-001', 'GET', '/users/{id}', 'Get user by ID', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-user-004', 'api-user-001', 'PUT', '/users/{id}', 'Update user', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-user-005', 'api-user-001', 'DELETE', '/users/{id}', 'Delete user', true);

-- API Resources for Product API
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-product-001', 'api-product-001', 'GET', '/products', 'List all products', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-product-002', 'api-product-001', 'POST', '/products', 'Create a new product', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-product-003', 'api-product-001', 'GET', '/products/{id}', 'Get product by ID', false);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-product-004', 'api-product-001', 'PUT', '/products/{id}', 'Update product', true);

-- API Resources for Order API
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-order-001', 'api-order-001', 'GET', '/orders', 'List all orders', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-order-002', 'api-order-001', 'POST', '/orders', 'Create a new order', true);
INSERT OR IGNORE INTO api_resources (id, api_id, method, path, description, auth_required) VALUES
('res-order-003', 'api-order-001', 'GET', '/orders/{id}', 'Get order by ID', true);

-- API Versions
INSERT OR IGNORE INTO api_versions (id, api_id, version, definition, status, is_default) VALUES
('ver-user-001', 'api-user-001', 'v1', '{"openapi":"3.0.0","info":{"title":"User API","version":"1.0.0"}}', 'PUBLISHED', true);
INSERT OR IGNORE INTO api_versions (id, api_id, version, definition, status, is_default) VALUES
('ver-product-001', 'api-product-001', 'v1', '{"openapi":"3.0.0","info":{"title":"Product API","version":"1.0.0"}}', 'PUBLISHED', true);
INSERT OR IGNORE INTO api_versions (id, api_id, version, definition, status, is_default) VALUES
('ver-order-001', 'api-order-001', 'v1', '{"openapi":"3.0.0","info":{"title":"Order API","version":"1.0.0"}}', 'PUBLISHED', true);
INSERT OR IGNORE INTO api_versions (id, api_id, version, definition, status, is_default) VALUES
('ver-payment-001', 'api-payment-001', 'v1', '{"openapi":"3.0.0","info":{"title":"Payment API","version":"1.0.0"}}', 'CREATED', true);

-- Sample OAuth2 client
INSERT OR IGNORE INTO oauth2_clients (id, tenant_id, client_id, client_secret, name, redirect_uris, grant_types, scopes, status) VALUES
('client-default-001', 'tenant-default-001', 'default_client', 'default_secret_12345', 'Default Application Client', 'http://localhost/callback', 'client_credentials,password,authorization_code,refresh_token', 'read,write,admin', 'active');
