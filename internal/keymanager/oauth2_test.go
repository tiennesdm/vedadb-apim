package keymanager

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
// OAuth2 Implementation
// ============================================================================

// Token represents an OAuth2 token
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	ClientID     string    `json:"client_id"`
}

// IsExpired checks if the token is expired
func (t *Token) IsExpired() bool {
	return time.Now().After(t.CreatedAt.Add(time.Duration(t.ExpiresIn) * time.Second))
}

// Client represents an OAuth2 client
type Client struct {
	ID           string   `json:"id"`
	Secret       string   `json:"secret"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
	GrantTypes   []string `json:"grant_types"`
	Scopes       []string `json:"scopes"`
	Active       bool     `json:"active"`
}

// HasGrantType checks if client supports a grant type
func (c *Client) HasGrantType(gt string) bool {
	for _, g := range c.GrantTypes {
		if g == gt {
			return true
		}
	}
	return false
}

// HasScope checks if client has a scope
func (c *Client) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// PKCEData holds PKCE challenge data
type PKCEData struct {
	Challenge       string `json:"challenge"`
	ChallengeMethod string `json:"challenge_method"` // "S256" or "plain"
	Code            string `json:"code"`
	Used            bool   `json:"used"`
}

// Verify verifies a PKCE verifier
func (p *PKCEData) Verify(verifier string) bool {
	switch p.ChallengeMethod {
	case "S256":
		h := sha256.Sum256([]byte(verifier))
		expected := base64.RawURLEncoding.EncodeToString(h[:])
		return subtle.ConstantTimeCompare([]byte(p.Challenge), []byte(expected)) == 1
	case "plain":
		return subtle.ConstantTimeCompare([]byte(p.Challenge), []byte(verifier)) == 1
	default:
		return false
	}
}

// OAuth2Server implements OAuth2 flows
type OAuth2Server struct {
	clients     map[string]*Client
	tokens      map[string]*Token
	codes       map[string]*PKCEData
	refreshTokens map[string]string // refresh_token -> access_token mapping
	mu          sync.RWMutex
}

// NewOAuth2Server creates a new OAuth2 server
func NewOAuth2Server() *OAuth2Server {
	return &OAuth2Server{
		clients:       make(map[string]*Client),
		tokens:        make(map[string]*Token),
		codes:         make(map[string]*PKCEData),
		refreshTokens: make(map[string]string),
	}
}

// RegisterClient registers a new OAuth2 client
func (s *OAuth2Server) RegisterClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[client.ID] = client
}

// TokenEndpoint handles token requests
func (s *OAuth2Server) TokenEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	grantType := r.FormValue("grant_type")
	if grantType == "" {
		writeOAuthError(w, "invalid_request", "grant_type is required")
		return
	}

	switch grantType {
	case "client_credentials":
		s.handleClientCredentials(w, r)
	case "password":
		s.handlePassword(w, r)
	case "authorization_code":
		s.handleAuthorizationCode(w, r)
	default:
		writeOAuthError(w, "unsupported_grant_type", "unsupported grant type")
	}
}

func (s *OAuth2Server) handleClientCredentials(w http.ResponseWriter, r *http.Request) {
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")

	client, err := s.authenticateClient(clientID, clientSecret)
	if err != nil {
		writeOAuthError(w, "invalid_client", err.Error())
		return
	}

	if !client.HasGrantType("client_credentials") {
		writeOAuthError(w, "unauthorized_client", "client not authorized for this grant type")
		return
	}

	token := s.generateToken(clientID, 3600)
	s.mu.Lock()
	s.tokens[token.AccessToken] = token
	s.mu.Unlock()

	writeToken(w, token)
}

func (s *OAuth2Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == "" || password == "" {
		writeOAuthError(w, "invalid_request", "username and password required")
		return
	}

	client, err := s.authenticateClient(clientID, clientSecret)
	if err != nil {
		writeOAuthError(w, "invalid_client", err.Error())
		return
	}

	if !client.HasGrantType("password") {
		writeOAuthError(w, "unauthorized_client", "client not authorized for this grant type")
		return
	}

	// In production, verify username/password against user store
	if !verifyUserCredentials(username, password) {
		writeOAuthError(w, "invalid_grant", "invalid username or password")
		return
	}

	token := s.generateToken(clientID, 3600)
	token.RefreshToken = generateRandomString(32)
	s.mu.Lock()
	s.tokens[token.AccessToken] = token
	s.refreshTokens[token.RefreshToken] = token.AccessToken
	s.mu.Unlock()

	writeToken(w, token)
}

func (s *OAuth2Server) handleAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	codeVerifier := r.FormValue("code_verifier")

	client, err := s.authenticateClient(clientID, clientSecret)
	if err != nil {
		writeOAuthError(w, "invalid_client", err.Error())
		return
	}

	if !client.HasGrantType("authorization_code") {
		writeOAuthError(w, "unauthorized_client", "client not authorized for this grant type")
		return
	}

	// Validate authorization code
	s.mu.Lock()
	pkceData, exists := s.codes[code]
	s.mu.Unlock()
	if !exists {
		writeOAuthError(w, "invalid_grant", "invalid authorization code")
		return
	}

	if pkceData.Used {
		writeOAuthError(w, "invalid_grant", "authorization code already used")
		return
	}

	// PKCE verification
	if pkceData.Challenge != "" && codeVerifier != "" {
		if !pkceData.Verify(codeVerifier) {
			writeOAuthError(w, "invalid_grant", "invalid code_verifier")
			return
		}
	} else if pkceData.Challenge != "" && codeVerifier == "" {
		writeOAuthError(w, "invalid_request", "code_verifier required for PKCE")
		return
	}

	// Validate redirect URI
	if redirectURI != "" {
		valid := false
		for _, uri := range client.RedirectURIs {
			if uri == redirectURI {
				valid = true
				break
			}
		}
		if !valid {
			writeOAuthError(w, "invalid_grant", "invalid redirect_uri")
			return
		}
	}

	// Mark code as used
	s.mu.Lock()
	s.codes[code] = &PKCEData{Used: true, Challenge: pkceData.Challenge, ChallengeMethod: pkceData.ChallengeMethod}
	s.mu.Unlock()

	token := s.generateToken(clientID, 3600)
	token.RefreshToken = generateRandomString(32)
	s.mu.Lock()
	s.tokens[token.AccessToken] = token
	s.refreshTokens[token.RefreshToken] = token.AccessToken
	s.mu.Unlock()

	writeToken(w, token)
}

// AuthorizeEndpoint handles authorization requests (authorization code flow)
func (s *OAuth2Server) AuthorizeEndpoint(w http.ResponseWriter, r *http.Request) {
	responseType := r.URL.Query().Get("response_type")
	clientID := r.URL.Query().Get("client_id")
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	codeChallenge := r.URL.Query().Get("code_challenge")
	codeChallengeMethod := r.URL.Query().Get("code_challenge_method")
	_ = state

	if responseType != "code" {
		writeOAuthError(w, "unsupported_response_type", "only 'code' is supported")
		return
	}

	s.mu.RLock()
	client, exists := s.clients[clientID]
	s.mu.RUnlock()
	if !exists {
		writeOAuthError(w, "invalid_client", "client not found")
		return
	}

	// Validate redirect URI
	validRedirect := false
	for _, uri := range client.RedirectURIs {
		if uri == redirectURI {
			validRedirect = true
			break
		}
	}
	if !validRedirect {
		writeOAuthError(w, "invalid_request", "invalid redirect_uri")
		return
	}

	// Generate authorization code with PKCE data
	code := generateRandomString(32)
	pkceData := &PKCEData{
		Challenge:       codeChallenge,
		ChallengeMethod: codeChallengeMethod,
		Code:            code,
	}
	if pkceData.ChallengeMethod == "" {
		pkceData.ChallengeMethod = "plain"
	}

	s.mu.Lock()
	s.codes[code] = pkceData
	s.mu.Unlock()

	// Redirect with code
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

// IntrospectEndpoint handles token introspection (RFC 7662)
func (s *OAuth2Server) IntrospectEndpoint(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	if token == "" {
		writeJSON(w, map[string]interface{}{"active": false})
		return
	}

	s.mu.RLock()
	tok, exists := s.tokens[token]
	s.mu.RUnlock()

	if !exists || tok.IsExpired() {
		writeJSON(w, map[string]interface{}{"active": false})
		return
	}

	response := map[string]interface{}{
		"active":     true,
		"client_id":  tok.ClientID,
		"token_type": tok.TokenType,
		"exp":        tok.CreatedAt.Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
		"scope":      tok.Scope,
	}
	writeJSON(w, response)
}

// RevokeEndpoint handles token revocation (RFC 7009)
func (s *OAuth2Server) RevokeEndpoint(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	if token == "" {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Try to find and delete as access token
	if tok, exists := s.tokens[token]; exists {
		delete(s.tokens, token)
		delete(s.refreshTokens, tok.RefreshToken)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Try as refresh token
	if accessToken, exists := s.refreshTokens[token]; exists {
		delete(s.refreshTokens, token)
		delete(s.tokens, accessToken)
		w.WriteHeader(http.StatusOK)
		return
	}

	// RFC 7009: respond with 200 even if token didn't exist
	w.WriteHeader(http.StatusOK)
}

// authenticateClient validates client credentials
func (s *OAuth2Server) authenticateClient(clientID, clientSecret string) (*Client, error) {
	if clientID == "" || clientSecret == "" {
		return nil, errors.New("client_id and client_secret required")
	}

	s.mu.RLock()
	client, exists := s.clients[clientID]
	s.mu.RUnlock()

	if !exists {
		return nil, errors.New("client not found")
	}

	if client.Secret != clientSecret {
		return nil, errors.New("invalid client_secret")
	}

	if !client.Active {
		return nil, errors.New("client is inactive")
	}

	return client, nil
}

func (s *OAuth2Server) generateToken(clientID string, expiresIn int) *Token {
	return &Token{
		AccessToken: generateRandomString(32),
		TokenType:   "Bearer",
		ExpiresIn:   expiresIn,
		CreatedAt:   time.Now(),
		ClientID:    clientID,
	}
}

func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	// Use a deterministic seed for testing
	seed := time.Now().UnixNano()
	for i := range b {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		b[i] = charset[seed%int64(len(charset))]
	}
	return string(b)
}

func verifyUserCredentials(username, password string) bool {
	// Mock implementation for testing
	return username == "testuser" && password == "testpass"
}

func writeToken(w http.ResponseWriter, token *Token) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(token)
}

func writeOAuthError(w http.ResponseWriter, err, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{
		"error":             err,
		"error_description": desc,
	})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// ============================================================================
// TESTS
// ============================================================================

func TestOAuth2Server_ClientCredentials_GivenValidClient_WhenRequested_ThenReturnsToken(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:         "client-1",
		Secret:     "secret-1",
		Name:       "Test Client",
		GrantTypes: []string{"client_credentials"},
		Scopes:     []string{"read", "write"},
		Active:     true,
	})

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	req := httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	server.TokenEndpoint(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var token Token
	err := json.Unmarshal(w.Body.Bytes(), &token)
	require.NoError(t, err)
	assert.NotEmpty(t, token.AccessToken)
	assert.Equal(t, "Bearer", token.TokenType)
	assert.Equal(t, 3600, token.ExpiresIn)
	assert.False(t, token.IsExpired())
}

func TestOAuth2Server_ClientCredentials_GivenInvalidClient_WhenRequested_ThenReturnsError(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:         "client-1",
		Secret:     "secret-1",
		GrantTypes: []string{"client_credentials"},
		Active:     true,
	})

	tests := []struct {
		name       string
		clientID   string
		secret     string
		expectErr  string
	}{
		{"wrong secret", "client-1", "wrong-secret", "invalid_client"},
		{"unknown client", "unknown", "secret", "invalid_client"},
		{"empty client_id", "", "secret", "invalid_request"},
		{"empty secret", "client-1", "", "invalid_client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("grant_type", "client_credentials")
			form.Set("client_id", tt.clientID)
			form.Set("client_secret", tt.secret)

			req := httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			server.TokenEndpoint(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]string
			json.Unmarshal(w.Body.Bytes(), &resp)
			assert.Equal(t, tt.expectErr, resp["error"])
		})
	}
}

func TestOAuth2Server_ClientCredentials_GivenUnauthorizedGrant_WhenRequested_ThenReturnsError(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:         "client-1",
		Secret:     "secret-1",
		GrantTypes: []string{"authorization_code"}, // No client_credentials
		Active:     true,
	})

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	req := httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	server.TokenEndpoint(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "unauthorized_client", resp["error"])
}

func TestOAuth2Server_Password_GivenValidCredentials_WhenRequested_ThenReturnsToken(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:         "client-1",
		Secret:     "secret-1",
		GrantTypes: []string{"password"},
		Active:     true,
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("username", "testuser")
	form.Set("password", "testpass")

	req := httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	server.TokenEndpoint(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var token Token
	err := json.Unmarshal(w.Body.Bytes(), &token)
	require.NoError(t, err)
	assert.NotEmpty(t, token.AccessToken)
	assert.NotEmpty(t, token.RefreshToken)
}

func TestOAuth2Server_Password_GivenInvalidCredentials_WhenRequested_ThenReturnsError(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:         "client-1",
		Secret:     "secret-1",
		GrantTypes: []string{"password"},
		Active:     true,
	})

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"wrong password", "testuser", "wrongpass"},
		{"wrong username", "wronguser", "testpass"},
		{"both wrong", "wrong", "wrong"},
		{"empty username", "", "testpass"},
		{"empty password", "testuser", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("grant_type", "password")
			form.Set("client_id", "client-1")
			form.Set("client_secret", "secret-1")
			form.Set("username", tt.username)
			form.Set("password", tt.password)

			req := httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			server.TokenEndpoint(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestOAuth2Server_AuthorizationCode_GivenValidCode_WhenRequested_ThenReturnsToken(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:           "client-1",
		Secret:       "secret-1",
		GrantTypes:   []string{"authorization_code"},
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
	})

	// First get an authorization code
	req := httptest.NewRequest("GET", "/authorize?response_type=code&client_id=client-1&redirect_uri=http://localhost:8080/callback&state=xyz", nil)
	w := httptest.NewRecorder()
	server.AuthorizeEndpoint(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	location := w.Header().Get("Location")
	require.NotEmpty(t, location)

	parsedURL, err := url.Parse(location)
	require.NoError(t, err)
	code := parsedURL.Query().Get("code")
	require.NotEmpty(t, code)

	// Exchange code for token
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost:8080/callback")

	req = httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()

	server.TokenEndpoint(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var token Token
	json.Unmarshal(w.Body.Bytes(), &token)
	assert.NotEmpty(t, token.AccessToken)
}

func TestOAuth2Server_PKCE_GivenS256Challenge_WhenVerified_ThenReturnsToken(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:           "client-1",
		Secret:       "secret-1",
		GrantTypes:   []string{"authorization_code"},
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
	})

	// Generate PKCE
	verifier := "my_verifier_123456789"
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Get authorization code with PKCE
	authURL := fmt.Sprintf("/authorize?response_type=code&client_id=client-1&redirect_uri=http://localhost:8080/callback&code_challenge=%s&code_challenge_method=S256", challenge)
	req := httptest.NewRequest("GET", authURL, nil)
	w := httptest.NewRecorder()
	server.AuthorizeEndpoint(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	parsedURL, _ := url.Parse(w.Header().Get("Location"))
	code := parsedURL.Query().Get("code")
	require.NotEmpty(t, code)

	// Exchange with verifier
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost:8080/callback")
	form.Set("code_verifier", verifier)

	req = httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()

	server.TokenEndpoint(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var token Token
	json.Unmarshal(w.Body.Bytes(), &token)
	assert.NotEmpty(t, token.AccessToken)
}

func TestOAuth2Server_PKCE_GivenWrongVerifier_WhenVerified_ThenReturnsError(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:           "client-1",
		Secret:       "secret-1",
		GrantTypes:   []string{"authorization_code"},
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
	})

	// Generate PKCE
	verifier := "my_verifier_123456789"
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Get authorization code with PKCE
	authURL := fmt.Sprintf("/authorize?response_type=code&client_id=client-1&redirect_uri=http://localhost:8080/callback&code_challenge=%s&code_challenge_method=S256", challenge)
	req := httptest.NewRequest("GET", authURL, nil)
	w := httptest.NewRecorder()
	server.AuthorizeEndpoint(w, req)

	parsedURL, _ := url.Parse(w.Header().Get("Location"))
	code := parsedURL.Query().Get("code")

	// Exchange with WRONG verifier
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost:8080/callback")
	form.Set("code_verifier", "wrong_verifier")

	req = httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()

	server.TokenEndpoint(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_grant", resp["error"])
}

func TestOAuth2Server_Introspect_GivenValidToken_WhenIntrospected_ThenReturnsActive(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:         "client-1",
		Secret:     "secret-1",
		GrantTypes: []string{"client_credentials"},
		Active:     true,
	})

	// Create a token
	token := &Token{
		AccessToken: "test-token-123",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		CreatedAt:   time.Now(),
		ClientID:    "client-1",
		Scope:       "read write",
	}
	server.tokens["test-token-123"] = token

	form := url.Values{}
	form.Set("token", "test-token-123")

	req := httptest.NewRequest("POST", "/introspect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	server.IntrospectEndpoint(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, true, resp["active"])
	assert.Equal(t, "client-1", resp["client_id"])
	assert.Equal(t, "read write", resp["scope"])
}

func TestOAuth2Server_Introspect_GivenExpiredToken_WhenIntrospected_ThenReturnsInactive(t *testing.T) {
	server := NewOAuth2Server()

	// Create an expired token
	token := &Token{
		AccessToken: "expired-token",
		TokenType:   "Bearer",
		ExpiresIn:   -1, // Already expired
		CreatedAt:   time.Now(),
		ClientID:    "client-1",
	}
	server.tokens["expired-token"] = token

	form := url.Values{}
	form.Set("token", "expired-token")

	req := httptest.NewRequest("POST", "/introspect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	server.IntrospectEndpoint(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, false, resp["active"])
}

func TestOAuth2Server_Introspect_GivenUnknownToken_WhenIntrospected_ThenReturnsInactive(t *testing.T) {
	server := NewOAuth2Server()

	form := url.Values{}
	form.Set("token", "unknown-token")

	req := httptest.NewRequest("POST", "/introspect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	server.IntrospectEndpoint(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, false, resp["active"])
}

func TestOAuth2Server_Revoke_GivenValidToken_WhenRevoked_ThenRemoved(t *testing.T) {
	server := NewOAuth2Server()

	// Create token
	token := &Token{
		AccessToken:  "token-to-revoke",
		RefreshToken: "refresh-123",
		CreatedAt:    time.Now(),
		ExpiresIn:    3600,
	}
	server.tokens["token-to-revoke"] = token
	server.refreshTokens["refresh-123"] = "token-to-revoke"

	form := url.Values{}
	form.Set("token", "token-to-revoke")

	req := httptest.NewRequest("POST", "/revoke", strings.NewReader(form.Encode()))
	w := httptest.NewRecorder()

	server.RevokeEndpoint(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Token should be gone
	_, exists := server.tokens["token-to-revoke"]
	assert.False(t, exists)
	_, exists = server.refreshTokens["refresh-123"]
	assert.False(t, exists)
}

func TestOAuth2Server_Revoke_GivenRefreshToken_WhenRevoked_ThenAccessTokenAlsoRemoved(t *testing.T) {
	server := NewOAuth2Server()

	token := &Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		CreatedAt:    time.Now(),
		ExpiresIn:    3600,
	}
	server.tokens["access-123"] = token
	server.refreshTokens["refresh-456"] = "access-123"

	form := url.Values{}
	form.Set("token", "refresh-456")

	req := httptest.NewRequest("POST", "/revoke", strings.NewReader(form.Encode()))
	w := httptest.NewRecorder()

	server.RevokeEndpoint(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	_, exists := server.tokens["access-123"]
	assert.False(t, exists)
}

func TestOAuth2Server_Revoke_GivenUnknownToken_WhenRevoked_ThenReturns200(t *testing.T) {
	server := NewOAuth2Server()

	form := url.Values{}
	form.Set("token", "nonexistent")

	req := httptest.NewRequest("POST", "/revoke", strings.NewReader(form.Encode()))
	w := httptest.NewRecorder()

	server.RevokeEndpoint(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOAuth2Server_InactiveClient_WhenRequested_ThenReturnsError(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:         "client-1",
		Secret:     "secret-1",
		GrantTypes: []string{"client_credentials"},
		Active:     false, // Inactive!
	})

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "client-1")
	form.Set("client_secret", "secret-1")

	req := httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	server.TokenEndpoint(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_client", resp["error"])
}

func TestOAuth2Server_Authorize_GivenInvalidClient_WhenRequested_ThenReturnsError(t *testing.T) {
	server := NewOAuth2Server()

	req := httptest.NewRequest("GET", "/authorize?response_type=code&client_id=unknown&redirect_uri=http://localhost/cb", nil)
	w := httptest.NewRecorder()

	server.AuthorizeEndpoint(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOAuth2Server_Authorize_GivenInvalidRedirectURI_WhenRequested_ThenReturnsError(t *testing.T) {
	server := NewOAuth2Server()
	server.RegisterClient(&Client{
		ID:           "client-1",
		Secret:       "secret-1",
		GrantTypes:   []string{"authorization_code"},
		RedirectURIs: []string{"http://allowed.com/cb"},
		Active:       true,
	})

	req := httptest.NewRequest("GET", "/authorize?response_type=code&client_id=client-1&redirect_uri=http://evil.com/cb", nil)
	w := httptest.NewRecorder()

	server.AuthorizeEndpoint(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPKCEData_Vify_GivenVariousMethods_WhenVerified_ThenCorrectResult(t *testing.T) {
	t.Run("S256 valid verifier", func(t *testing.T) {
		verifier := "my_verifier"
		h := sha256.Sum256([]byte(verifier))
		challenge := base64.RawURLEncoding.EncodeToString(h[:])

		pkce := &PKCEData{Challenge: challenge, ChallengeMethod: "S256"}
		assert.True(t, pkce.Verify(verifier))
	})

	t.Run("S256 invalid verifier", func(t *testing.T) {
		verifier := "my_verifier"
		h := sha256.Sum256([]byte(verifier))
		challenge := base64.RawURLEncoding.EncodeToString(h[:])

		pkce := &PKCEData{Challenge: challenge, ChallengeMethod: "S256"}
		assert.False(t, pkce.Verify("wrong_verifier"))
	})

	t.Run("plain valid", func(t *testing.T) {
		pkce := &PKCEData{Challenge: "my_challenge", ChallengeMethod: "plain"}
		assert.True(t, pkce.Verify("my_challenge"))
	})

	t.Run("plain invalid", func(t *testing.T) {
		pkce := &PKCEData{Challenge: "my_challenge", ChallengeMethod: "plain"}
		assert.False(t, pkce.Verify("wrong_challenge"))
	})

	t.Run("unknown method", func(t *testing.T) {
		pkce := &PKCEData{Challenge: "challenge", ChallengeMethod: "unknown"}
		assert.False(t, pkce.Verify("anything"))
	})
}

func TestToken_IsExpired_GivenVariousTokens_WhenChecked_ThenCorrectResult(t *testing.T) {
	tests := []struct {
		name     string
		created  time.Time
		expiresIn int
		expected bool
	}{
		{"not expired", time.Now(), 3600, false},
		{"expired", time.Now().Add(-2 * time.Hour), 3600, true},
		{"just expired", time.Now().Add(-3601 * time.Second), 3600, true},
		{"just created", time.Now(), 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &Token{CreatedAt: tt.created, ExpiresIn: tt.expiresIn}
			assert.Equal(t, tt.expected, token.IsExpired())
		})
	}
}

func TestClient_HasGrantType_GivenVariousTypes_WhenChecked_ThenCorrectResult(t *testing.T) {
	client := &Client{GrantTypes: []string{"client_credentials", "password"}}

	assert.True(t, client.HasGrantType("client_credentials"))
	assert.True(t, client.HasGrantType("password"))
	assert.False(t, client.HasGrantType("authorization_code"))
	assert.False(t, client.HasGrantType(""))
}

func TestClient_HasScope_GivenVariousScopes_WhenChecked_ThenCorrectResult(t *testing.T) {
	client := &Client{Scopes: []string{"read", "write"}}

	assert.True(t, client.HasScope("read"))
	assert.True(t, client.HasScope("write"))
	assert.False(t, client.HasScope("admin"))
	assert.False(t, client.HasScope(""))
}
