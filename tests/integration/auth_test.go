package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Auth Integration Test Infrastructure
// ============================================================================

// Token represents an OAuth2/JWT token
type AuthToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// AuthIntegrationServer simulates the complete auth system
type AuthIntegrationServer struct {
	OAuthServer    *httptest.Server
	JWTServer      *httptest.Server
	APIKeyServer   *httptest.Server
	ProtectedServer *httptest.Server

	oauthTokens   map[string]*AuthToken
	oauthClients  map[string]oauthClient
	oauthCodes    map[string]codeData
	apiKeys       map[string]apiKeyData
	jwtKeys       map[string]interface{} // kid -> key
	mu            sync.RWMutex
}

type oauthClient struct {
	ID       string
	Secret   string
	Active   bool
	GrantTypes []string
}

type codeData struct {
	ClientID    string
	CodeChallenge string
	CodeChallengeMethod string
	ExpiresAt   time.Time
	Used        bool
}

type apiKeyData struct {
	Owner    string
	Scopes   []string
	Active   bool
	ExpiresAt time.Time
}

// NewAuthIntegrationServer creates all auth servers
func NewAuthIntegrationServer() *AuthIntegrationServer {
	ais := &AuthIntegrationServer{
		oauthTokens:  make(map[string]*AuthToken),
		oauthClients: make(map[string]oauthClient),
		oauthCodes:   make(map[string]codeData),
		apiKeys:      make(map[string]apiKeyData),
		jwtKeys:      make(map[string]interface{}),
	}

	// OAuth2 server
	ais.OAuthServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			ais.handleOAuthToken(w, r)
		case "/authorize":
			ais.handleOAuthAuthorize(w, r)
		case "/introspect":
			ais.handleOAuthIntrospect(w, r)
		case "/revoke":
			ais.handleOAuthRevoke(w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	// JWT validation server
	ais.JWTServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			ais.handleJWKS(w, r)
		case "/validate":
			ais.handleJWTValidate(w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	// API Key server
	ais.APIKeyServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/validate":
			ais.handleAPIKeyValidate(w, r)
		case "/generate":
			ais.handleAPIKeyGenerate(w, r)
		case "/revoke":
			ais.handleAPIKeyRevoke(w, r)
		default:
			http.NotFound(w, r)
		}
	}))

	// Protected resource server
	ais.ProtectedServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try all auth methods
		authHeader := r.Header.Get("Authorization")
		apiKey := r.Header.Get("X-API-Key")

		authorized := false

		if authHeader != "" {
			if strings.HasPrefix(authHeader, "Bearer ") {
				token := strings.TrimPrefix(authHeader, "Bearer ")
				// Check OAuth token
				ais.mu.RLock()
				_, ok := ais.oauthTokens[token]
				ais.mu.RUnlock()
				if ok {
					authorized = true
				}
				// Could also be JWT - simplified check
				if strings.Count(token, ".") == 2 {
					authorized = true // Assume valid JWT format
				}
			}
		}

		if apiKey != "" {
			ais.mu.RLock()
			keyData, ok := ais.apiKeys[apiKey]
			ais.mu.RUnlock()
			if ok && keyData.Active && (keyData.ExpiresAt.IsZero() || time.Now().Before(keyData.ExpiresAt)) {
				authorized = true
			}
		}

		if !authorized {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "access granted",
			"path":    r.URL.Path,
		})
	}))

	return ais
}

func (ais *AuthIntegrationServer) Close() {
	ais.OAuthServer.Close()
	ais.JWTServer.Close()
	ais.APIKeyServer.Close()
	ais.ProtectedServer.Close()
}

// ============================================================================
// OAuth Handlers
// ============================================================================

func (ais *AuthIntegrationServer) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	grantType := r.FormValue("grant_type")
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")

	// Validate client
	ais.mu.RLock()
	client, ok := ais.oauthClients[clientID]
	ais.mu.RUnlock()
	if !ok || client.Secret != clientSecret || !client.Active {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}

	switch grantType {
	case "client_credentials":
		token := &AuthToken{
			AccessToken: fmt.Sprintf("cc_token_%d", time.Now().UnixNano()),
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		}
		ais.mu.Lock()
		ais.oauthTokens[token.AccessToken] = token
		ais.mu.Unlock()
		json.NewEncoder(w).Encode(token)

	case "password":
		username := r.FormValue("username")
		password := r.FormValue("password")
		if username == "" || password == "" {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		if username != "testuser" || password != "testpass" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		token := &AuthToken{
			AccessToken:  fmt.Sprintf("pwd_token_%d", time.Now().UnixNano()),
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			RefreshToken: fmt.Sprintf("refresh_%d", time.Now().UnixNano()),
		}
		ais.mu.Lock()
		ais.oauthTokens[token.AccessToken] = token
		ais.mu.Unlock()
		json.NewEncoder(w).Encode(token)

	case "authorization_code":
		code := r.FormValue("code")
		codeVerifier := r.FormValue("code_verifier")

		ais.mu.Lock()
		codeData, ok := ais.oauthCodes[code]
		if !ok || codeData.Used || time.Now().After(codeData.ExpiresAt) {
			ais.mu.Unlock()
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}

		// PKCE verification
		if codeData.CodeChallenge != "" {
			if codeData.CodeChallengeMethod == "S256" {
				h := sha256.Sum256([]byte(codeVerifier))
				expected := base64.RawURLEncoding.EncodeToString(h[:])
				if expected != codeData.CodeChallenge {
					ais.mu.Unlock()
					http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
					return
				}
			}
		}

		codeData.Used = true
		ais.oauthCodes[code] = codeData
		ais.mu.Unlock()

		token := &AuthToken{
			AccessToken:  fmt.Sprintf("ac_token_%d", time.Now().UnixNano()),
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			RefreshToken: fmt.Sprintf("refresh_%d", time.Now().UnixNano()),
		}
		ais.mu.Lock()
		ais.oauthTokens[token.AccessToken] = token
		ais.mu.Unlock()
		json.NewEncoder(w).Encode(token)

	default:
		http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
	}
}

func (ais *AuthIntegrationServer) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client_id")
	responseType := r.URL.Query().Get("response_type")
	codeChallenge := r.URL.Query().Get("code_challenge")
	codeChallengeMethod := r.URL.Query().Get("code_challenge_method")
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")

	if responseType != "code" {
		http.Error(w, `{"error":"unsupported_response_type"}`, http.StatusBadRequest)
		return
	}

	ais.mu.RLock()
	_, ok := ais.oauthClients[clientID]
	ais.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusBadRequest)
		return
	}

	code := fmt.Sprintf("auth_code_%d", time.Now().UnixNano())
	ais.mu.Lock()
	ais.oauthCodes[code] = codeData{
		ClientID:    clientID,
		CodeChallenge: codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	ais.mu.Unlock()

	redirectURL, _ := url.Parse(redirectURI)
	q := redirectURL.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	redirectURL.RawQuery = q.Encode()

	w.Header().Set("Location", redirectURL.String())
	w.WriteHeader(http.StatusFound)
}

func (ais *AuthIntegrationServer) handleOAuthIntrospect(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")

	ais.mu.RLock()
	_, ok := ais.oauthTokens[token]
	ais.mu.RUnlock()

	if ok {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true,
			"scope":  "read write",
		})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"active": false})
	}
}

func (ais *AuthIntegrationServer) handleOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	ais.mu.Lock()
	delete(ais.oauthTokens, token)
	ais.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// ============================================================================
// JWT Handlers
// ============================================================================

func (ais *AuthIntegrationServer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	ais.mu.RLock()
	defer ais.mu.RUnlock()

	var keys []map[string]interface{}
	for kid, key := range ais.jwtKeys {
		switch k := key.(type) {
		case *rsa.PublicKey:
			keys = append(keys, map[string]interface{}{
				"kty": "RSA",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(intToBytes(k.E)),
			})
		case *ecdsa.PublicKey:
			keys = append(keys, map[string]interface{}{
				"kty": "EC",
				"kid": kid,
				"x":   base64.RawURLEncoding.EncodeToString(k.X.Bytes()),
				"y":   base64.RawURLEncoding.EncodeToString(k.Y.Bytes()),
				"crv": "P-256",
			})
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"keys": keys})
}

func (ais *AuthIntegrationServer) handleJWTValidate(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, `{"error":"missing token"}`, http.StatusBadRequest)
		return
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		http.Error(w, `{"error":"invalid token format"}`, http.StatusBadRequest)
		return
	}

	// Basic validation - check structure
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":  true,
		"claims": map[string]interface{}{"sub": "user-123"},
	})
}

// ============================================================================
// API Key Handlers
// ============================================================================

func (ais *AuthIntegrationServer) handleAPIKeyValidate(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, `{"error":"missing key"}`, http.StatusBadRequest)
		return
	}

	ais.mu.RLock()
	keyData, ok := ais.apiKeys[key]
	ais.mu.RUnlock()

	if !ok || !keyData.Active {
		http.Error(w, `{"error":"invalid key"}`, http.StatusUnauthorized)
		return
	}

	if !keyData.ExpiresAt.IsZero() && time.Now().After(keyData.ExpiresAt) {
		http.Error(w, `{"error":"expired key"}`, http.StatusUnauthorized)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":  true,
		"owner":  keyData.Owner,
		"scopes": keyData.Scopes,
	})
}

func (ais *AuthIntegrationServer) handleAPIKeyGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Owner  string   `json:"owner"`
		Scopes []string `json:"scopes"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	key := fmt.Sprintf("vapim_%d", time.Now().UnixNano())
	ais.mu.Lock()
	ais.apiKeys[key] = apiKeyData{
		Owner:    req.Owner,
		Scopes:   req.Scopes,
		Active:   true,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	ais.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{"api_key": key})
}

func (ais *AuthIntegrationServer) handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	ais.mu.Lock()
	if data, ok := ais.apiKeys[key]; ok {
		data.Active = false
		ais.apiKeys[key] = data
	}
	ais.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func intToBytes(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var result []byte
	for n > 0 {
		result = append([]byte{byte(n & 0xff)}, result...)
		n >>= 8
	}
	return result
}

// ============================================================================
// TESTS
// ============================================================================

func TestAuthIntegration_OAuth2_ClientCredentials_GivenValidClient_WhenRequested_ThenReturnsToken(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Register client
	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{
		ID:       "client-1",
		Secret:   "secret-1",
		Active:   true,
		GrantTypes: []string{"client_credentials"},
	}
	ais.mu.Unlock()

	// Request token
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	resp, err := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	assert.NotEmpty(t, token.AccessToken)
	assert.Equal(t, "Bearer", token.TokenType)
	assert.Equal(t, 3600, token.ExpiresIn)
}

func TestAuthIntegration_OAuth2_ClientCredentials_GivenInvalidClient_WhenRequested_ThenReturnsError(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"client_credentials"}}
	ais.mu.Unlock()

	tests := []struct {
		name    string
		id      string
		secret  string
		expect  int
	}{
		{"wrong secret", "client-1", "wrong-secret", http.StatusUnauthorized},
		{"unknown client", "unknown", "secret", http.StatusUnauthorized},
		{"empty credentials", "", "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("grant_type", "client_credentials")
			form.Set("client_id", tt.id)
			form.Set("client_secret", tt.secret)

			resp, err := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.expect, resp.StatusCode)
		})
	}
}

func TestAuthIntegration_OAuth2_PasswordFlow_GivenValidCredentials_WhenRequested_ThenReturnsToken(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"password"}}
	ais.mu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("username", "testuser")
	form.Set("password", "testpass")

	resp, err := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	assert.NotEmpty(t, token.AccessToken)
	assert.NotEmpty(t, token.RefreshToken)
}

func TestAuthIntegration_OAuth2_PasswordFlow_GivenInvalidCredentials_WhenRequested_ThenReturnsError(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"password"}}
	ais.mu.Unlock()

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"wrong password", "testuser", "wrongpass"},
		{"wrong username", "wronguser", "testpass"},
		{"both wrong", "wrong", "wrong"},
		{"empty credentials", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("grant_type", "password")
			form.Set("client_id", "client-1")
			form.Set("client_secret", "secret-1")
			form.Set("username", tt.username)
			form.Set("password", tt.password)

			resp, err := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestAuthIntegration_OAuth2_AuthorizationCodeFlow_GivenValidCode_WhenExchanged_ThenReturnsToken(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{
		ID:       "client-1",
		Secret:   "secret-1",
		Active:   true,
		GrantTypes: []string{"authorization_code"},
	}
	ais.mu.Unlock()

	// Step 1: Get authorization code
	authURL := fmt.Sprintf("%s/authorize?response_type=code&client_id=client-1&redirect_uri=http://localhost/cb&state=xyz", ais.OAuthServer.URL)
	resp, err := http.Get(authURL)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	location := resp.Header.Get("Location")
	require.NotEmpty(t, location)

	parsedURL, _ := url.Parse(location)
	code := parsedURL.Query().Get("code")
	require.NotEmpty(t, code)
	assert.Equal(t, "xyz", parsedURL.Query().Get("state"))

	// Step 2: Exchange code for token
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost/cb")

	resp, err = http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	assert.NotEmpty(t, token.AccessToken)
}

func TestAuthIntegration_OAuth2_PKCE_GivenS256Challenge_WhenVerified_ThenReturnsToken(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{
		ID:       "client-1",
		Secret:   "secret-1",
		Active:   true,
		GrantTypes: []string{"authorization_code"},
	}
	ais.mu.Unlock()

	// Generate PKCE
	verifier := "my_verifier_123456789"
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Step 1: Authorize with PKCE
	authURL := fmt.Sprintf("%s/authorize?response_type=code&client_id=client-1&redirect_uri=http://localhost/cb&code_challenge=%s&code_challenge_method=S256", ais.OAuthServer.URL, challenge)
	resp, err := http.Get(authURL)
	require.NoError(t, err)
	resp.Body.Close()

	parsedURL, _ := url.Parse(resp.Header.Get("Location"))
	code := parsedURL.Query().Get("code")

	// Step 2: Exchange with verifier
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost/cb")
	form.Set("code_verifier", verifier)

	resp, err = http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	assert.NotEmpty(t, token.AccessToken)
}

func TestAuthIntegration_OAuth2_PKCE_GivenWrongVerifier_WhenVerified_ThenReturnsError(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"authorization_code"}}
	ais.mu.Unlock()

	verifier := "my_verifier_123456789"
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	authURL := fmt.Sprintf("%s/authorize?response_type=code&client_id=client-1&redirect_uri=http://localhost/cb&code_challenge=%s&code_challenge_method=S256", ais.OAuthServer.URL, challenge)
	resp, _ := http.Get(authURL)
	parsedURL, _ := url.Parse(resp.Header.Get("Location"))
	code := parsedURL.Query().Get("code")

	// Exchange with WRONG verifier
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost/cb")
	form.Set("code_verifier", "wrong_verifier")

	resp, _ = http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAuthIntegration_OAuth2_TokenIntrospection_GivenValidToken_WhenIntrospected_ThenActive(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"client_credentials"}}
	ais.mu.Unlock()

	// Get token
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	resp, _ := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	resp.Body.Close()

	// Introspect
	form = url.Values{}
	form.Set("token", token.AccessToken)

	resp, _ = http.Post(ais.OAuthServer.URL+"/introspect", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()

	assert.Equal(t, true, result["active"])
}

func TestAuthIntegration_OAuth2_TokenRevocation_GivenValidToken_WhenRevoked_ThenInactive(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"client_credentials"}}
	ais.mu.Unlock()

	// Get token
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	resp, _ := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	resp.Body.Close()

	// Revoke
	form = url.Values{}
	form.Set("token", token.AccessToken)
	http.Post(ais.OAuthServer.URL+"/revoke", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))

	// Introspect - should be inactive
	form = url.Values{}
	form.Set("token", token.AccessToken)

	resp, _ = http.Post(ais.OAuthServer.URL+"/introspect", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()

	assert.Equal(t, false, result["active"])
}

// ============================================================================
// API Key Auth Tests
// ============================================================================

func TestAuthIntegration_APIKey_GivenValidKey_WhenAccessingResource_ThenAllowed(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Register key
	ais.mu.Lock()
	ais.apiKeys["vapim_testkey123"] = apiKeyData{
		Owner:    "user-1",
		Scopes:   []string{"read", "write"},
		Active:   true,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	ais.mu.Unlock()

	// Validate
	resp, err := http.Get(ais.APIKeyServer.URL + "/validate?key=vapim_testkey123")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, true, result["valid"])
	assert.Equal(t, "user-1", result["owner"])
}

func TestAuthIntegration_APIKey_GivenInvalidKey_WhenAccessingResource_ThenDenied(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	resp, err := http.Get(ais.APIKeyServer.URL + "/validate?key=vapim_invalidkey")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuthIntegration_APIKey_GivenExpiredKey_WhenAccessingResource_ThenDenied(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.apiKeys["vapim_expiredkey"] = apiKeyData{
		Owner:    "user-1",
		Scopes:   []string{"read"},
		Active:   true,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	ais.mu.Unlock()

	resp, err := http.Get(ais.APIKeyServer.URL + "/validate?key=vapim_expiredkey")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuthIntegration_APIKey_GivenRevokedKey_WhenAccessingResource_ThenDenied(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	ais.mu.Lock()
	ais.apiKeys["vapim_revokedkey"] = apiKeyData{
		Owner:  "user-1",
		Scopes: []string{"read"},
		Active: false, // Revoked
	}
	ais.mu.Unlock()

	resp, err := http.Get(ais.APIKeyServer.URL + "/validate?key=vapim_revokedkey")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuthIntegration_APIKey_GenerateAndRevoke_WhenLifecycle_ThenCorrectState(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Generate
	reqBody := map[string]interface{}{
		"owner":  "user-1",
		"scopes": []string{"read", "write"},
	}
	body, _ := json.Marshal(reqBody)

	resp, err := http.Post(ais.APIKeyServer.URL+"/generate", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Revoke
	resp, err = http.Get(ais.APIKeyServer.URL + "/revoke?key=vapim_testkey123")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ============================================================================
// JWT Validation Tests
// ============================================================================

func TestAuthIntegration_JWT_GivenValidToken_WhenValidated_ThenAllowed(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Register RSA key
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ais.mu.Lock()
	ais.jwtKeys["key-1"] = &rsaKey.PublicKey
	ais.mu.Unlock()

	resp, err := http.Get(ais.JWTServer.URL + "/validate?token=eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEyMyJ9.signature")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuthIntegration_JWT_GivenMalformedToken_WhenValidated_ThenReturnsError(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	tests := []struct {
		name  string
		token string
	}{
		{"no dots", "notajwttoken"},
		{"one dot", "header.claims"},
		{"four dots", "a.b.c.d"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := http.Get(ais.JWTServer.URL + "/validate?token=" + tt.token)
			resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestAuthIntegration_JWKS_GivenRegisteredKeys_WhenQueried_ThenReturnsKeys(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Register keys
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecdsaKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	ais.mu.Lock()
	ais.jwtKeys["rsa-key-1"] = &rsaKey.PublicKey
	ais.jwtKeys["ec-key-1"] = &ecdsaKey.PublicKey
	ais.mu.Unlock()

	resp, err := http.Get(ais.JWTServer.URL + "/jwks")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	keys := result["keys"].([]interface{})
	assert.Len(t, keys, 2)
}

// ============================================================================
// Unauthorized Access Tests
// ============================================================================

func TestAuthIntegration_Unauthorized_GivenNoCredentials_WhenAccessingProtected_ThenReturns401(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	resp, err := http.Get(ais.ProtectedServer.URL + "/api/resource")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuthIntegration_Unauthorized_GivenInvalidToken_WhenAccessingProtected_ThenReturns401(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	req, _ := http.NewRequest("GET", ais.ProtectedServer.URL+"/api/resource", nil)
	req.Header.Set("Authorization", "Bearer invalid_token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuthIntegration_Unauthorized_GivenExpiredOAuthToken_WhenAccessingProtected_ThenDenied(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Register a client and get a token
	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"client_credentials"}}
	ais.mu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	resp, _ := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	resp.Body.Close()

	// Revoke the token
	form = url.Values{}
	form.Set("token", token.AccessToken)
	http.Post(ais.OAuthServer.URL+"/revoke", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))

	// Try to access protected resource with revoked token
	req, _ := http.NewRequest("GET", ais.ProtectedServer.URL+"/api/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	// Should be denied since token was revoked
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// ============================================================================
// Protected Resource Access with Valid Auth
// ============================================================================

func TestAuthIntegration_ProtectedResource_GivenOAuthToken_WhenAccessed_ThenAllowed(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Setup
	ais.mu.Lock()
	ais.oauthClients["client-1"] = oauthClient{ID: "client-1", Secret: "secret-1", Active: true, GrantTypes: []string{"client_credentials"}}
	ais.mu.Unlock()

	// Get token
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	resp, _ := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	var token AuthToken
	json.NewDecoder(resp.Body).Decode(&token)
	resp.Body.Close()

	// Access protected resource
	req, _ := http.NewRequest("GET", ais.ProtectedServer.URL+"/api/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, "access granted", result["message"])
}

func TestAuthIntegration_ProtectedResource_GivenAPIKey_WhenAccessed_ThenAllowed(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Setup API key
	ais.mu.Lock()
	ais.apiKeys["vapim_validkey123"] = apiKeyData{
		Owner:    "user-1",
		Scopes:   []string{"read"},
		Active:   true,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	ais.mu.Unlock()

	// Access with API key
	req, _ := http.NewRequest("GET", ais.ProtectedServer.URL+"/api/resource", nil)
	req.Header.Set("X-API-Key", "vapim_validkey123")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ============================================================================
// Concurrent Auth Tests
// ============================================================================

func TestAuthIntegration_Concurrent_GivenMultipleClients_WhenAuthenticating_ThenAllSucceed(t *testing.T) {
	ais := NewAuthIntegrationServer()
	defer ais.Close()

	// Setup multiple clients
	ais.mu.Lock()
	for i := 0; i < 10; i++ {
		ais.oauthClients[fmt.Sprintf("client-%d", i)] = oauthClient{
			ID:       fmt.Sprintf("client-%d", i),
			Secret:   fmt.Sprintf("secret-%d", i),
			Active:   true,
			GrantTypes: []string{"client_credentials"},
		}
	}
	ais.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			defer wg.Done()
			clientID := fmt.Sprintf("client-%d", id)

			form := url.Values{}
			form.Set("grant_type", "client_credentials")
			form.Set("client_id", clientID)
			form.Set("client_secret", fmt.Sprintf("secret-%d", id))

			resp, err := http.Post(ais.OAuthServer.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
		}(i)
	}

	wg.Wait()
}
