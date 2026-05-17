// Package sdk provides SDK code templates for multiple languages.
package sdk

// JavaScriptTemplate is the SDK template for JavaScript (axios/fetch).
const JavaScriptTemplate = `/**
 * {{.APIName}} Client SDK (v{{.APIVersion}})
 * Generated: {{.GeneratedAt}}
 * @vedadata/vapim-sdk
 */

{{$pkg := .PackageName}}
{{$endpoint := .EndpointURL}}
{{$sandbox := .SandboxURL}}
{{$ctx := .Context}}

// Configuration for the API client
class ClientConfig {
  constructor(options = {}) {
    this.baseURL = options.baseURL || '{{$endpoint}}';
    this.sandboxURL = options.sandboxURL || '{{$sandbox}}';
    this.apiKey = options.apiKey || '';
    this.accessToken = options.accessToken || '';
    this.timeout = options.timeout || 30000;
    this.sandbox = options.sandbox || false;
    this.headers = {
      'Content-Type': 'application/json',
      ...options.headers
    };
  }

  getUrl() {
    return this.sandbox ? this.sandboxURL : this.baseURL;
  }

  getHeaders() {
    const headers = { ...this.headers };
    if (this.apiKey) headers['X-API-Key'] = this.apiKey;
    if (this.accessToken) headers['Authorization'] = ` + "`Bearer ${this.accessToken}`" + `;
    return headers;
  }
}

/**
 * {{.APIName}} API Client
 */
class {{pascalCase .APIName}}Client {
  constructor(config = {}) {
    this.config = new ClientConfig(config);
  }

  /**
   * Update the client configuration
   */
  configure(options) {
    this.config = new ClientConfig({ ...this.config, ...options });
    return this;
  }

  /**
   * Set authentication token
   */
  setToken(token) {
    this.config.accessToken = token;
    return this;
  }

  /**
   * Enable sandbox mode
   */
  useSandbox(enabled = true) {
    this.config.sandbox = enabled;
    return this;
  }

  /**
   * Make an HTTP request
   */
  async request(method, path, options = {}) {
    const url = `${this.config.getUrl()}{{$ctx}}${path}`;
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), this.config.timeout);

    try {
      const fetchOptions = {
        method: method.toUpperCase(),
        headers: { ...this.config.getHeaders(), ...options.headers },
        signal: controller.signal,
      };

      if (options.body) {
        fetchOptions.body = typeof options.body === 'string' 
          ? options.body 
          : JSON.stringify(options.body);
      }

      const response = await fetch(url, fetchOptions);
      clearTimeout(timeoutId);

      if (!response.ok) {
        const errorBody = await response.text();
        throw new APIError(response.status, response.statusText, errorBody);
      }

      const contentType = response.headers.get('content-type') || '';
      if (contentType.includes('application/json')) {
        return await response.json();
      }
      return await response.text();
    } catch (error) {
      clearTimeout(timeoutId);
      if (error.name === 'AbortError') {
        throw new APIError(408, 'Request Timeout', 'Request timed out after ' + this.config.timeout + 'ms');
      }
      throw error;
    }
  }

  // Helper methods
  async get(path, options = {}) { return this.request('GET', path, options); }
  async post(path, body, options = {}) { return this.request('POST', path, { ...options, body }); }
  async put(path, body, options = {}) { return this.request('PUT', path, { ...options, body }); }
  async patch(path, body, options = {}) { return this.request('PATCH', path, { ...options, body }); }
  async delete(path, options = {}) { return this.request('DELETE', path, options); }

  // --- API Resources ---
{{range .Resources}}
  /**
   * {{if .Summary}}{{.Summary}}{{else}}{{.MethodUpper}} {{.Path}}{{end}}
   * Path: {{.Path}}
   * Method: {{.MethodUpper}}
   {{if .AuthRequired}}* @auth_required{{end}}
   {{range .Parameters}}* @param {{.JSType}} {{.Name}} - {{.Description}}{{if .Required}} (required){{end}}
   {{end}}*/
  async {{.FunctionName}}({{range $i, $p := .Parameters}}{{if $i}}, {{end}}{{.Name}}{{end}}) {
    let path = '{{.Path}}';
    const queryParams = new URLSearchParams();
    {{range .Parameters}}
    {{if eq .In "path"}}path = path.replace('{{.Name}}', encodeURIComponent(String({{stripBraces .Name}})));{{end}}
    {{if eq .In "query"}}if ({{.Name}} !== undefined) queryParams.set('{{.Name}}', String({{.Name}}));{{end}}
    {{end}}
    const queryStr = queryParams.toString();
    if (queryStr) path += '?' + queryStr;
    {{if .HasBody}}
    return this.{{lower .Method}}(path, body);
    {{else}}
    return this.{{lower .Method}}(path);
    {{end}}
  }
{{end}}
}

/**
 * API Error class
 */
class APIError extends Error {
  constructor(status, statusText, body) {
    super(`${status} ${statusText}: ${body}`);
    this.status = status;
    this.statusText = statusText;
    this.body = body;
    this.name = 'APIError';
  }
}

// Export
{{if eq .PackageName "api_client"}}module.exports = { {{pascalCase .APIName}}Client, ClientConfig, APIError };
{{else}}module.exports = { {{pascalCase .APIName}}Client, ClientConfig, APIError };
{{end}}

// ESM export
export { {{pascalCase .APIName}}Client, ClientConfig, APIError };
export default {{pascalCase .APIName}}Client;
`

// PythonTemplate is the SDK template for Python (requests).
const PythonTemplate = `#!/usr/bin/env python3
"""
{{.APIName}} Client SDK (v{{.APIVersion}})
Generated: {{.GeneratedAt}}
Python 3.7+ required
"""

import json
import time
from typing import Optional, Dict, Any, List, Union
from urllib.parse import urljoin, urlencode

# Try to import requests
try:
    import requests
    from requests.adapters import HTTPAdapter
    from urllib3.util.retry import Retry
except ImportError:
    raise ImportError("The 'requests' package is required. Install it with: pip install requests")


class APIError(Exception):
    """Raised when the API returns an error response."""

    def __init__(self, status_code: int, message: str, response_body: str = None):
        self.status_code = status_code
        self.message = message
        self.response_body = response_body
        super().__init__(f"[{status_code}] {message}")


class ClientConfig:
    """Configuration for the API client."""

    def __init__(self,
                 base_url: str = '{{.EndpointURL}}',
                 sandbox_url: str = '{{.SandboxURL}}',
                 api_key: Optional[str] = None,
                 access_token: Optional[str] = None,
                 timeout: int = 30,
                 sandbox: bool = False,
                 headers: Optional[Dict[str, str]] = None):
        self.base_url = base_url.rstrip('/')
        self.sandbox_url = sandbox_url.rstrip('/')
        self.api_key = api_key
        self.access_token = access_token
        self.timeout = timeout
        self.sandbox = sandbox
        self.headers = {
            'Content-Type': 'application/json',
            'User-Agent': 'vapim-python-sdk/{{.APIVersion}}',
            **(headers or {})
        }

    def get_url(self) -> str:
        return self.sandbox_url if self.sandbox else self.base_url

    def get_headers(self) -> Dict[str, str]:
        headers = dict(self.headers)
        if self.api_key:
            headers['X-API-Key'] = self.api_key
        if self.access_token:
            headers['Authorization'] = f'Bearer {self.access_token}'
        return headers


class {{pascalCase .APIName}}Client:
    """
    {{.APIName}} API Client

    Generated SDK for {{.APIName}} API v{{.APIVersion}}.
    Context: {{.Context}}
    """

    def __init__(self, config: Optional[ClientConfig] = None):
        self.config = config or ClientConfig()
        self.session = requests.Session()

        # Configure retries
        retry_strategy = Retry(
            total=3,
            backoff_factor=1,
            status_forcelist=[429, 500, 502, 503, 504],
        )
        adapter = HTTPAdapter(max_retries=retry_strategy)
        self.session.mount("http://", adapter)
        self.session.mount("https://", adapter)

    def configure(self, **kwargs) -> '{{pascalCase .APIName}}Client':
        """Update client configuration."""
        for key, value in kwargs.items():
            if hasattr(self.config, key):
                setattr(self.config, key, value)
        return self

    def set_token(self, token: str) -> '{{pascalCase .APIName}}Client':
        """Set the authentication token."""
        self.config.access_token = token
        return self

    def use_sandbox(self, enabled: bool = True) -> '{{pascalCase .APIName}}Client':
        """Enable or disable sandbox mode."""
        self.config.sandbox = enabled
        return self

    def request(self,
                method: str,
                path: str,
                body: Optional[Any] = None,
                params: Optional[Dict[str, Any]] = None,
                headers: Optional[Dict[str, str]] = None) -> Any:
        """Make an HTTP request."""
        url = f"{self.config.get_url()}{{.Context}}{path}"
        request_headers = {**self.config.get_headers(), **(headers or {})}

        kwargs = {
            'headers': request_headers,
            'timeout': self.config.timeout,
        }
        if params:
            kwargs['params'] = params
        if body is not None:
            kwargs['json'] = body

        response = self.session.request(method.upper(), url, **kwargs)

        if not response.ok:
            raise APIError(response.status_code, response.reason, response.text)

        content_type = response.headers.get('content-type', '')
        if 'application/json' in content_type:
            return response.json()
        return response.text

    # Convenience methods
    def get(self, path: str, **kwargs) -> Any:
        return self.request('GET', path, **kwargs)

    def post(self, path: str, body: Any = None, **kwargs) -> Any:
        return self.request('POST', path, body=body, **kwargs)

    def put(self, path: str, body: Any = None, **kwargs) -> Any:
        return self.request('PUT', path, body=body, **kwargs)

    def patch(self, path: str, body: Any = None, **kwargs) -> Any:
        return self.request('PATCH', path, body=body, **kwargs)

    def delete(self, path: str, **kwargs) -> Any:
        return self.request('DELETE', path, **kwargs)

    # --- API Resources ---
{{range .Resources}}
    def {{.FunctionName}}(self{{range .Parameters}}, {{.Name}}: Optional[{{.PythonType}}] = None{{end}}) -> Any:
        """
        {{if .Summary}}{{.Summary}}{{else}}{{.MethodUpper}} {{.Path}}{{end}}

        Method: {{.MethodUpper}}
        Path: {{.Path}}
        {{if .AuthRequired}}Authentication required.{{end}}
        {{range .Parameters}}
        :param {{.Name}}: {{.Description}}{{if .Required}} (required){{end}}
        {{end}}"""
        path = '{{.Path}}'
        params = {}
        {{range .Parameters}}
        {{if eq .In "path"}}path = path.replace('{{.Name}}', str({{stripBraces .Name}})){{end}}
        {{if eq .In "query"}}if {{.Name}} is not None:
            params['{{.Name}}'] = {{.Name}}{{end}}
        {{end}}
        {{if .HasBody}}
        return self.{{lower .Method}}(path, body=body, params=params if params else None)
        {{else}}
        return self.{{lower .Method}}(path, params=params if params else None)
        {{end}}
{{end}}
`

// GoTemplate is the SDK template for Go (net/http).
const GoTemplate = `// Code generated by VAPIM SDK Generator. DO NOT EDIT.
// {{.APIName}} Client SDK (v{{.APIVersion}})
// Generated: {{.GeneratedAt}}

package {{.PackageName}}

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientConfig holds configuration for the API client.
type ClientConfig struct {
	BaseURL     string
	SandboxURL  string
	APIKey      string
	AccessToken string
	Timeout     time.Duration
	Sandbox     bool
	Headers     map[string]string
	HTTPClient  *http.Client
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *ClientConfig {
	return &ClientConfig{
		BaseURL:    "{{.EndpointURL}}",
		SandboxURL: "{{.SandboxURL}}",
		Timeout:    30 * time.Second,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"User-Agent":   "vapim-go-sdk/{{.APIVersion}}",
		},
	}
}

// Client is the {{.APIName}} API client.
type Client struct {
	config *ClientConfig
}

// NewClient creates a new API client.
func NewClient(config *ClientConfig) *Client {
	if config == nil {
		config = DefaultConfig()
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: config.Timeout,
		}
	}
	if config.Headers == nil {
		config.Headers = make(map[string]string)
	}
	return &Client{config: config}
}

// SetToken sets the authentication token.
func (c *Client) SetToken(token string) *Client {
	c.config.AccessToken = token
	return c
}

// UseSandbox enables or disables sandbox mode.
func (c *Client) UseSandbox(enabled bool) *Client {
	c.config.Sandbox = enabled
	return c
}

// BaseURL returns the current base URL.
func (c *Client) BaseURL() string {
	if c.config.Sandbox {
		return c.config.SandboxURL
	}
	return c.config.BaseURL
}

// Request makes an HTTP request.
func (c *Client) Request(ctx context.Context, method, path string, body interface{}, queryParams map[string]string) (*http.Response, error) {
	base := strings.TrimSuffix(c.BaseURL(), "/")
	ctxPath := strings.TrimPrefix("{{.Context}}", "/")
	path = strings.TrimPrefix(path, "/")
	
	urlStr := fmt.Sprintf("%s/%s/%s", base, ctxPath, path)
	
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Add query params
	if len(queryParams) > 0 {
		q := u.Query()
		for k, v := range queryParams {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	// Serialize body
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), u.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	for k, v := range c.config.Headers {
		req.Header.Set(k, v)
	}
	if c.config.APIKey != "" {
		req.Header.Set("X-API-Key", c.config.APIKey)
	}
	if c.config.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	}

	resp, err := c.config.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API error [%d]: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// Get sends a GET request.
func (c *Client) Get(ctx context.Context, path string, params map[string]string) (*http.Response, error) {
	return c.Request(ctx, "GET", path, nil, params)
}

// Post sends a POST request.
func (c *Client) Post(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	return c.Request(ctx, "POST", path, body, nil)
}

// Put sends a PUT request.
func (c *Client) Put(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	return c.Request(ctx, "PUT", path, body, nil)
}

// Patch sends a PATCH request.
func (c *Client) Patch(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	return c.Request(ctx, "PATCH", path, body, nil)
}

// Delete sends a DELETE request.
func (c *Client) Delete(ctx context.Context, path string) (*http.Response, error) {
	return c.Request(ctx, "DELETE", path, nil, nil)
}

// --- API Resources ---
{{range .Resources}}
// {{.FunctionName}} - {{if .Summary}}{{.Summary}}{{else}}{{.MethodUpper}} {{.Path}}{{end}}
func (c *Client) {{.FunctionName}}(ctx context.Context{{range .Parameters}}, {{.Name}} {{.GoType}}{{end}}) (*http.Response, error) {
	path := "{{.Path}}"
	{{range .Parameters}}
	{{if eq .In "path"}}path = strings.Replace(path, "{{.Name}}", url.QueryEscape(fmt.Sprint({{stripBraces .Name}})), 1){{end}}
	{{end}}
	params := map[string]string{}
	{{range .Parameters}}
	{{if eq .In "query"}}if {{.Name}} != "" {
		params["{{.Name}}"] = {{.Name}}
	}{{end}}
	{{end}}
	{{if .HasBody}}
	return c.{{title .Method}}(ctx, path, body)
	{{else}}
	return c.{{title .Method}}(ctx, path{{if eq .Method "get"}}, params{{end}})
	{{end}}
}
{{end}}

// APIError represents an error returned by the API.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error [%d]: %s - %s", e.StatusCode, e.Message, e.Body)
}
`

// JavaTemplate is the SDK template for Java (OkHttp).
const JavaTemplate = `package com.vedadata.{{snakeCase .APIName}};

import okhttp3.*;
import com.google.gson.Gson;
import com.google.gson.JsonObject;
import java.io.IOException;
import java.util.concurrent.TimeUnit;
import java.util.Map;
import java.util.HashMap;

/**
 * {{.APIName}} Client SDK (v{{.APIVersion}})
 * Generated: {{.GeneratedAt}}
 */
public class {{pascalCase .APIName}}Client {

    private final ClientConfig config;
    private final OkHttpClient httpClient;
    private final Gson gson;

    public {{pascalCase .APIName}}Client() {
        this(new ClientConfig());
    }

    public {{pascalCase .APIName}}Client(ClientConfig config) {
        this.config = config;
        this.gson = new Gson();
        this.httpClient = new OkHttpClient.Builder()
            .connectTimeout(config.getTimeout(), TimeUnit.SECONDS)
            .readTimeout(config.getTimeout(), TimeUnit.SECONDS)
            .writeTimeout(config.getTimeout(), TimeUnit.SECONDS)
            .build();
    }

    public {{pascalCase .APIName}}Client setToken(String token) {
        this.config.setAccessToken(token);
        return this;
    }

    public {{pascalCase .APIName}}Client useSandbox(boolean enabled) {
        this.config.setSandbox(enabled);
        return this;
    }

    private String getBaseUrl() {
        return config.isSandbox() ? config.getSandboxUrl() : config.getBaseUrl();
    }

    private Request.Builder buildRequest(String method, String path) {
        String url = getBaseUrl() + "{{.Context}}" + path;
        Request.Builder builder = new Request.Builder().url(url);

        // Add default headers
        for (Map.Entry<String, String> entry : config.getHeaders().entrySet()) {
            builder.header(entry.getKey(), entry.getValue());
        }

        if (config.getAccessToken() != null && !config.getAccessToken().isEmpty()) {
            builder.header("Authorization", "Bearer " + config.getAccessToken());
        }
        if (config.getApiKey() != null && !config.getApiKey().isEmpty()) {
            builder.header("X-API-Key", config.getApiKey());
        }

        return builder;
    }

    public Response request(String method, String path, Object body, Map<String, String> queryParams) throws IOException {
        Request.Builder builder = buildRequest(method, path);

        RequestBody requestBody = null;
        if (body != null) {
            String jsonBody = gson.toJson(body);
            requestBody = RequestBody.create(jsonBody, MediaType.parse("application/json"));
        }

        builder.method(method.toUpperCase(), requestBody);
        Request request = builder.build();
        return httpClient.newCall(request).execute();
    }

    // Convenience methods
    public Response get(String path) throws IOException {
        return request("GET", path, null, null);
    }

    public Response post(String path, Object body) throws IOException {
        return request("POST", path, body, null);
    }

    public Response put(String path, Object body) throws IOException {
        return request("PUT", path, body, null);
    }

    public Response patch(String path, Object body) throws IOException {
        return request("PATCH", path, body, null);
    }

    public Response delete(String path) throws IOException {
        return request("DELETE", path, null, null);
    }

    // --- API Resources ---
{{range .Resources}}
    /**
     * {{if .Summary}}{{.Summary}}{{else}}{{.MethodUpper}} {{.Path}}{{end}}
     * Method: {{.MethodUpper}}
     {{if .AuthRequired}}* Authentication required.{{end}}
     {{range .Parameters}}* @param {{.JavaType}} {{.Name}} {{.Description}}{{if .Required}} (required){{end}}
     {{end}}*/
    public Response {{.FunctionName}}({{range $i, $p := .Parameters}}{{if $i}}, {{end}}{{if eq .In "body"}}Object body{{else}}{{.JavaType}} {{.Name}}{{end}}{{end}}) throws IOException {
        String path = "{{.Path}}";
        {{range .Parameters}}
        {{if eq .In "path"}}path = path.replace("{{.Name}}", String.valueOf({{stripBraces .Name}}));{{end}}
        {{end}}
        {{if .HasBody}}
        return {{lower .Method}}(path, body);
        {{else}}
        return {{lower .Method}}(path);
        {{end}}
    }
{{end}}

    public static class ClientConfig {
        private String baseUrl = "{{.EndpointURL}}";
        private String sandboxUrl = "{{.SandboxURL}}";
        private String apiKey;
        private String accessToken;
        private long timeout = 30;
        private boolean sandbox;
        private Map<String, String> headers = new HashMap<>();

        public ClientConfig() {
            headers.put("Content-Type", "application/json");
            headers.put("User-Agent", "vapim-java-sdk/{{.APIVersion}}");
        }

        // Getters and setters
        public String getBaseUrl() { return baseUrl; }
        public void setBaseUrl(String baseUrl) { this.baseUrl = baseUrl; }
        public String getSandboxUrl() { return sandboxUrl; }
        public void setSandboxUrl(String sandboxUrl) { this.sandboxUrl = sandboxUrl; }
        public String getApiKey() { return apiKey; }
        public void setApiKey(String apiKey) { this.apiKey = apiKey; }
        public String getAccessToken() { return accessToken; }
        public void setAccessToken(String accessToken) { this.accessToken = accessToken; }
        public long getTimeout() { return timeout; }
        public void setTimeout(long timeout) { this.timeout = timeout; }
        public boolean isSandbox() { return sandbox; }
        public void setSandbox(boolean sandbox) { this.sandbox = sandbox; }
        public Map<String, String> getHeaders() { return headers; }
        public void setHeaders(Map<String, String> headers) { this.headers = headers; }
    }

    public static class APIException extends IOException {
        private final int statusCode;
        private final String body;

        public APIException(int statusCode, String message, String body) {
            super(message);
            this.statusCode = statusCode;
            this.body = body;
        }

        public int getStatusCode() { return statusCode; }
        public String getBody() { return body; }
    }
}
`

// CurlTemplate is the SDK template for curl command examples.
const CurlTemplate = `#!/bin/bash
# {{.APIName}} API - curl Examples (v{{.APIVersion}})
# Generated: {{.GeneratedAt}}
#
# Usage:
#   chmod +x {{.PackageName}}.sh
#   source {{.PackageName}}.sh
#
# Environment variables:
#   BASE_URL    - API base URL (default: {{.EndpointURL}})
#   API_KEY     - API key for authentication
#   TOKEN       - Bearer token for authentication
#   SANDBOX     - Set to "true" to use sandbox URL

set -e

# --- Configuration ---
BASE_URL="${BASE_URL:-{{.EndpointURL}}}"
SANDBOX_URL="${SANDBOX_URL:-{{.SandboxURL}}}"
API_KEY="${API_KEY:-}"
TOKEN="${TOKEN:-}"
SANDBOX="${SANDBOX:-false}"
CONTEXT="{{.Context}}"

# Determine actual URL
if [ "$SANDBOX" = "true" ]; then
    URL="$SANDBOX_URL"
else
    URL="$BASE_URL"
fi

# Remove trailing slash
URL="${URL%/}"

# Build auth headers
AUTH_HEADERS=""
if [ -n "$TOKEN" ]; then
    AUTH_HEADERS="-H \"Authorization: Bearer $TOKEN\""
fi
if [ -n "$API_KEY" ]; then
    AUTH_HEADERS="$AUTH_HEADERS -H \"X-API-Key: $API_KEY\""
fi

# --- Helper Functions ---

# Make an HTTP request
api_request() {
    local method="$1"
    local path="$2"
    local body="$3"
    local extra_headers="${4:-}"

    local full_url="${URL}${CONTEXT}${path}"
    local curl_cmd="curl -s -w \"\\nHTTP_CODE:%{http_code}\" -X ${method}"

    if [ -n "$AUTH_HEADERS" ]; then
        curl_cmd="$curl_cmd $AUTH_HEADERS"
    fi

    curl_cmd="$curl_cmd -H \"Content-Type: application/json\""

    if [ -n "$extra_headers" ]; then
        curl_cmd="$curl_cmd $extra_headers"
    fi

    if [ -n "$body" ]; then
        curl_cmd="$curl_cmd -d '$body'"
    fi

    curl_cmd="$curl_cmd \"$full_url\""

    echo "=== REQUEST ==="
    echo "$method $full_url"
    [ -n "$body" ] && echo "Body: $body"
    echo ""
    echo "=== RESPONSE ==="
    eval "$curl_cmd" | while read -r line; do
        if [[ "$line" == HTTP_CODE:* ]]; then
            echo ""
            echo "Status: ${line#HTTP_CODE:}"
        else
            echo "$line"
        fi
    done
    echo ""
}

# Pretty print JSON
pretty_json() {
    if command -v python3 &> /dev/null; then
        python3 -m json.tool
    elif command -v jq &> /dev/null; then
        jq .
    else
        cat
    fi
}

# --- API Endpoints ---
{{range .Resources}}
# {{.MethodUpper}} {{.Path}}
# {{if .Summary}}{{.Summary}}{{else}}{{.MethodUpper}} {{.Path}}{{end}}
{{if .AuthRequired}}# Requires authentication{{end}}
{{pascalCase .APIName}}_{{.FunctionName}}() {
    local path="{{.Path}}"
    {{range .Parameters}}
    {{if eq .In "path"}}local {{.Name}}="${1:-example_{{.Name}}}"
    path="${path//\{{{stripBraces .Name}}\}/${{stripBraces .Name}}}"
    shift{{end}}
    {{if eq .In "query"}}local {{.Name}}="${1:-}"
    [ -n "${{.Name}}" ] && path="${path}?{{.Name}}=${{stripBraces .Name}}"
    shift{{end}}
    {{if eq .In "body"}}local body='{ \"key\": \"value\" }'{{end}}
    {{end}}
    api_request "{{.MethodUpper}}" "$path" "${body:-}" ""
}
{{end}}

# --- Usage Examples ---
echo "{{.APIName}} curl SDK loaded."
echo "Available functions:"
echo ""
{{range .Resources}}echo "  {{pascalCase $.APIName}}_{{.FunctionName}}"
{{end}}echo ""
echo "Set BASE_URL, API_KEY, or TOKEN environment variables to configure."
echo "Set SANDBOX=true to use sandbox environment."
`
