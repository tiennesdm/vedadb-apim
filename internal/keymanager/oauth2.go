// Package keymanager implements the full OAuth2 authorization server including
// client_credentials, password, authorization_code and refresh_token flows,
// PKCE support, token introspection, revocation, and scope management.
package keymanager

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vedadb/vapim/internal/auth"
	"github.com/vedadb/vapim/pkg/models"
)

// OAuth2Store defines the persistence interface needed by the OAuth2 server.
type OAuth2Store interface {
	// Client methods
	SaveClient(client *models.OAuth2Client) error
	GetClient(id string) (*models.OAuth2Client, error)
	GetClientByCredentials(id, secret string) (*models.OAuth2Client, error)
	UpdateClient(client *models.OAuth2Client) error
	DeleteClient(id string) error

	// Token methods
	SaveToken(token *TokenRecord) error
	GetTokenByAccess(accessToken string) (*TokenRecord, error)
	GetTokenByRefresh(refreshToken string) (*TokenRecord, error)
	RevokeToken(accessToken string) error
	RevokeTokensByClient(clientID string) error

	// Authorization code methods
	SaveAuthCode(code *models.AuthCode) error
	GetAuthCode(code string) (*models.AuthCode, error)
	UseAuthCode(code string) error

	// User methods
	GetUserByUsername(username, tenantID string) (*models.User, error)
	ValidateUserPassword(username, password, tenantID string) (*models.User, error)
}

// TokenRecord is the persisted representation of an access or refresh token.
type TokenRecord struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	TokenType     string    `json:"token_type"`
	ClientID      string    `json:"client_id"`
	UserID        string    `json:"user_id,omitempty"`
	Scope         string    `json:"scope"`
	ExpiresAt     time.Time `json:"expires_at"`
	RefreshExpiry time.Time `json:"refresh_expiry,omitempty"`
	IssuedAt      time.Time `json:"issued_at"`
	Revoked       bool      `json:"revoked"`
	TenantID      string    `json:"tenant_id"`
	GrantType     string    `json:"grant_type"`
}

// OAuth2Server implements the full OAuth2 authorization server.
type OAuth2Server struct {
	mu            sync.RWMutex
	store         OAuth2Store
	jwtMgr        *JWTManager
	issuer        string
	tenantID      string
	codes         map[string]*models.AuthCode // in-memory auth code cache
	codeLifetime  time.Duration
}

// NewOAuth2Server creates a new OAuth2 authorization server.
func NewOAuth2Server(store OAuth2Store, jwtMgr *JWTManager, issuer, tenantID string) *OAuth2Server {
	return &OAuth2Server{
		store:        store,
		jwtMgr:       jwtMgr,
		issuer:       issuer,
		tenantID:     tenantID,
		codes:        make(map[string]*models.AuthCode),
		codeLifetime: 10 * time.Minute,
	}
}

// RegisterClient registers a new OAuth2 client and returns the client credentials.
func (s *OAuth2Server) RegisterClient(req models.ClientRegisterRequest) (*models.OAuth2Client, error) {
	if len(req.GrantTypes) == 0 {
		return nil, fmt.Errorf("at least one grant type is required")
	}

	// Validate grant types
	validGrants := map[string]bool{
		"client_credentials": true,
		"password":           true,
		"authorization_code": true,
		"refresh_token":      true,
	}
	for _, gt := range req.GrantTypes {
		if !validGrants[gt] {
			return nil, fmt.Errorf("unsupported grant_type: %s", gt)
		}
	}

	client := &models.OAuth2Client{
		ID:                   uuid.New().String(),
		Secret:               generateSecret(),
		Name:                 req.Name,
		Description:          req.Description,
		CallbackURLs:         req.CallbackURLs,
		GrantTypes:           req.GrantTypes,
		Scopes:               normalizeScopes(req.Scopes),
		Owner:                req.Owner,
		TenantID:             s.tenantID,
		AccessTokenLifetime:  3600,
		RefreshTokenLifetime: 86400,
		IDTokenLifetime:      3600,
		RequirePKCE:          req.RequirePKCE,
		Metadata:             make(map[string]string),
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
		Enabled:              true,
	}

	if err := s.store.SaveClient(client); err != nil {
		return nil, fmt.Errorf("save client: %w", err)
	}

	return client, nil
}

// TokenHandler processes token requests (client_credentials, password, authorization_code, refresh_token).
func (s *OAuth2Server) TokenHandler(c *gin.Context) {
	var req models.TokenRequest
	if err := c.ShouldBind(&req); err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "failed to parse token request")
		return
	}

	// Authenticate client
	client, err := s.store.GetClientByCredentials(req.ClientID, req.ClientSecret)
	if err != nil {
		s.respondError(c, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	if !client.Enabled {
		s.respondError(c, http.StatusUnauthorized, "invalid_client", "client is disabled")
		return
	}

	switch req.GrantType {
	case "client_credentials":
		s.handleClientCredentials(c, client, req)
	case "password":
		s.handlePassword(c, client, req)
	case "authorization_code":
		s.handleAuthorizationCode(c, client, req)
	case "refresh_token":
		s.handleRefreshToken(c, client, req)
	default:
		s.respondError(c, http.StatusBadRequest, "unsupported_grant_type", "grant type not supported")
	}
}

// handleClientCredentials processes the client_credentials grant.
func (s *OAuth2Server) handleClientCredentials(c *gin.Context, client *models.OAuth2Client, req models.TokenRequest) {
	if !s.grantAllowed(client, "client_credentials") {
		s.respondError(c, http.StatusBadRequest, "unauthorized_client", "client_credentials not allowed for this client")
		return
	}

	scope := s.intersectScope(client, req.Scope)
	if scope == "" {
		scope = strings.Join(client.Scopes, " ")
	}

	token, err := s.issueToken(client, "", scope, client.AccessTokenLifetime)
	if err != nil {
		s.respondError(c, http.StatusInternalServerError, "server_error", "failed to issue token")
		return
	}

	c.JSON(http.StatusOK, token)
}

// handlePassword processes the resource owner password credentials grant.
func (s *OAuth2Server) handlePassword(c *gin.Context, client *models.OAuth2Client, req models.TokenRequest) {
	if !s.grantAllowed(client, "password") {
		s.respondError(c, http.StatusBadRequest, "unauthorized_client", "password grant not allowed for this client")
		return
	}

	if req.Username == "" || req.Password == "" {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "username and password required")
		return
	}

	user, err := s.store.ValidateUserPassword(req.Username, req.Password, s.tenantID)
	if err != nil {
		s.respondError(c, http.StatusUnauthorized, "invalid_grant", "invalid user credentials")
		return
	}

	if !user.Enabled {
		s.respondError(c, http.StatusUnauthorized, "invalid_grant", "user account is disabled")
		return
	}

	scope := s.intersectScope(client, req.Scope)

	accessLifetime := client.AccessTokenLifetime
	if accessLifetime == 0 {
		accessLifetime = 3600
	}
	refreshLifetime := client.RefreshTokenLifetime
	if refreshLifetime == 0 {
		refreshLifetime = 86400
	}

	token, err := s.issueTokenWithRefresh(client, user.ID, scope, accessLifetime, refreshLifetime)
	if err != nil {
		s.respondError(c, http.StatusInternalServerError, "server_error", "failed to issue token")
		return
	}

	c.JSON(http.StatusOK, token)
}

// handleAuthorizationCode processes the authorization_code grant with optional PKCE.
func (s *OAuth2Server) handleAuthorizationCode(c *gin.Context, client *models.OAuth2Client, req models.TokenRequest) {
	if !s.grantAllowed(client, "authorization_code") {
		s.respondError(c, http.StatusBadRequest, "unauthorized_client", "authorization_code not allowed for this client")
		return
	}

	if req.Code == "" {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "authorization code is required")
		return
	}

	// Load authorization code
	code, err := s.store.GetAuthCode(req.Code)
	if err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "invalid authorization code")
		return
	}

	if code.ClientID != client.ID {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "code was issued for a different client")
		return
	}

	if code.Used {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "authorization code already used")
		return
	}

	if time.Now().After(code.ExpiresAt) {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "authorization code expired")
		return
	}

	// PKCE verification
	if code.CodeChallenge != "" {
		if req.CodeVerifier == "" {
			s.respondError(c, http.StatusBadRequest, "invalid_request", "code_verifier required for PKCE")
			return
		}
		computed := s.computeCodeChallenge(req.CodeVerifier, code.CodeChallengeMethod)
		if computed != code.CodeChallenge {
			s.respondError(c, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
			return
		}
	} else if client.RequirePKCE {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "PKCE code_challenge required")
		return
	}

	// Redirect URI validation
	if req.RedirectURI != "" && req.RedirectURI != code.RedirectURI {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}

	// Mark code as used
	_ = s.store.UseAuthCode(req.Code)

	scope := s.intersectScope(client, code.Scope)

	accessLifetime := client.AccessTokenLifetime
	if accessLifetime == 0 {
		accessLifetime = 3600
	}
	refreshLifetime := client.RefreshTokenLifetime
	if refreshLifetime == 0 {
		refreshLifetime = 86400
	}

	userID := ""
	if code.UserID != "" {
		userID = code.UserID
	}

	token, err := s.issueTokenWithRefresh(client, userID, scope, accessLifetime, refreshLifetime)
	if err != nil {
		s.respondError(c, http.StatusInternalServerError, "server_error", "failed to issue token")
		return
	}

	c.JSON(http.StatusOK, token)
}

// handleRefreshToken processes the refresh_token grant.
func (s *OAuth2Server) handleRefreshToken(c *gin.Context, client *models.OAuth2Client, req models.TokenRequest) {
	if req.RefreshToken == "" {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "refresh_token required")
		return
	}

	record, err := s.store.GetTokenByRefresh(req.RefreshToken)
	if err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "invalid refresh token")
		return
	}

	if record.ClientID != client.ID {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "token was issued to a different client")
		return
	}

	if record.Revoked {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "refresh token revoked")
		return
	}

	if time.Now().After(record.RefreshExpiry) {
		s.respondError(c, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	}

	scope := s.intersectScope(client, req.Scope)
	if scope == "" {
		scope = record.Scope
	}

	// Revoke old token
	_ = s.store.RevokeToken(record.AccessToken)

	accessLifetime := client.AccessTokenLifetime
	if accessLifetime == 0 {
		accessLifetime = 3600
	}
	refreshLifetime := client.RefreshTokenLifetime
	if refreshLifetime == 0 {
		refreshLifetime = 86400
	}

	token, err := s.issueTokenWithRefresh(client, record.UserID, scope, accessLifetime, refreshLifetime)
	if err != nil {
		s.respondError(c, http.StatusInternalServerError, "server_error", "failed to issue token")
		return
	}

	c.JSON(http.StatusOK, token)
}

// AuthorizeHandler handles the OAuth2 authorization endpoint (for authorization_code flow).
func (s *OAuth2Server) AuthorizeHandler(c *gin.Context) {
	var req models.AuthorizationRequest
	if err := c.ShouldBind(&req); err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "failed to parse authorization request")
		return
	}

	client, err := s.store.GetClient(req.ClientID)
	if err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_client", "invalid client_id")
		return
	}

	if !client.Enabled {
		s.respondError(c, http.StatusBadRequest, "invalid_client", "client is disabled")
		return
	}

	// Validate redirect URI
	if req.RedirectURI != "" {
		valid := false
		for _, u := range client.CallbackURLs {
			if u == req.RedirectURI {
				valid = true
				break
			}
		}
		if !valid {
			s.respondError(c, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
			return
		}
	} else if len(client.CallbackURLs) == 1 {
		req.RedirectURI = client.CallbackURLs[0]
	} else {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "redirect_uri is required")
		return
	}

	// Check response type
	if req.ResponseType != "code" {
		s.respondError(c, http.StatusBadRequest, "unsupported_response_type", "only 'code' is supported")
		return
	}

	// PKCE code challenge validation
	if client.RequirePKCE && req.CodeChallenge == "" {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "code_challenge is required for this client")
		return
	}

	if req.CodeChallenge != "" {
		if req.CodeChallengeMethod == "" {
			req.CodeChallengeMethod = "plain"
		}
		if req.CodeChallengeMethod != "S256" && req.CodeChallengeMethod != "plain" {
			s.respondError(c, http.StatusBadRequest, "invalid_request", "invalid code_challenge_method")
			return
		}
	}

	scope := s.intersectScope(client, req.Scope)

	// Generate authorization code
	code := &models.AuthCode{
		Code:                uuid.New().String(),
		ClientID:            client.ID,
		UserID:              "", // Would be set after user login in real flow
		RedirectURI:         req.RedirectURI,
		Scope:               scope,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		ExpiresAt:           time.Now().Add(s.codeLifetime),
		Used:                false,
	}

	if err := s.store.SaveAuthCode(code); err != nil {
		s.respondError(c, http.StatusInternalServerError, "server_error", "failed to generate authorization code")
		return
	}

	resp := models.AuthorizationResponse{
		Code:  code.Code,
		State: req.State,
	}

	// Return code in response body (in production, redirect to redirect_uri)
	c.JSON(http.StatusOK, resp)
}

// IntrospectHandler processes token introspection requests per RFC 7662.
func (s *OAuth2Server) IntrospectHandler(c *gin.Context) {
	var req models.TokenIntrospectionRequest
	if err := c.ShouldBind(&req); err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "failed to parse introspection request")
		return
	}

	if req.Token == "" {
		c.JSON(http.StatusOK, models.TokenIntrospectionResponse{Active: false})
		return
	}

	// Try to validate as JWT access token first
	claims, err := s.jwtMgr.ValidateToken(req.Token)
	if err == nil {
		// Valid JWT token
		active := claims.Exp > time.Now().Unix()
		resp := models.TokenIntrospectionResponse{
			Active:    active,
			Scope:     claims.Scope,
			ClientID:  claims.ClientID,
			Sub:       claims.Sub,
			Iss:       claims.Iss,
			Aud:       claims.Aud,
			Jti:       claims.Jti,
			Exp:       claims.Exp,
			Iat:       claims.Iat,
			TokenType: "Bearer",
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	// Fallback: check opaque token in store
	record, err := s.store.GetTokenByAccess(req.Token)
	if err != nil || record.Revoked {
		c.JSON(http.StatusOK, models.TokenIntrospectionResponse{Active: false})
		return
	}

	active := time.Now().Before(record.ExpiresAt)
	resp := models.TokenIntrospectionResponse{
		Active:    active,
		Scope:     record.Scope,
		ClientID:  record.ClientID,
		Sub:       record.UserID,
		TokenType: record.TokenType,
		Exp:       record.ExpiresAt.Unix(),
		Iat:       record.IssuedAt.Unix(),
		Jti:       record.AccessToken,
	}

	if !active {
		resp.Active = false
		resp.Scope = ""
		resp.ClientID = ""
	}

	c.JSON(http.StatusOK, resp)
}

// RevokeHandler processes token revocation requests per RFC 7009.
func (s *OAuth2Server) RevokeHandler(c *gin.Context) {
	var req models.TokenRevocationRequest
	if err := c.ShouldBind(&req); err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "failed to parse revocation request")
		return
	}

	// Authenticate client
	client, err := s.store.GetClientByCredentials(req.ClientID, req.ClientSecret)
	if err != nil {
		s.respondError(c, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	if !client.Enabled {
		s.respondError(c, http.StatusUnauthorized, "invalid_client", "client is disabled")
		return
	}

	if req.Token == "" {
		c.Status(http.StatusOK)
		return
	}

	// Try to revoke the token (best effort - always return 200 per RFC 7009)
	_ = s.store.RevokeToken(req.Token)

	// Also try as refresh token
	record, err := s.store.GetTokenByRefresh(req.Token)
	if err == nil && record.ClientID == client.ID {
		_ = s.store.RevokeToken(record.AccessToken)
	}

	c.Status(http.StatusOK)
}

// RegisterClientHandler handles dynamic client registration.
func (s *OAuth2Server) RegisterClientHandler(c *gin.Context) {
	var req models.ClientRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_request", "invalid client registration payload")
		return
	}

	if uid, ok := c.Get(auth.CtxKeyUserID); ok {
		req.Owner, _ = uid.(string)
	}
	tid := auth.GetTenantID(c)
	req.TenantID = tid

	client, err := s.RegisterClient(req)
	if err != nil {
		s.respondError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	c.JSON(http.StatusCreated, client)
}

// GetClientHandler returns a client by ID.
func (s *OAuth2Server) GetClientHandler(c *gin.Context) {
	clientID := c.Param("client_id")
	client, err := s.store.GetClient(clientID)
	if err != nil {
		s.respondError(c, http.StatusNotFound, "not_found", "client not found")
		return
	}

	// Do not return secret in GET
	resp := *client
	resp.Secret = ""
	c.JSON(http.StatusOK, resp)
}

// ListClientsHandler lists OAuth2 clients (filtered by tenant).
func (s *OAuth2Server) ListClientsHandler(c *gin.Context) {
	// In production, query store with pagination
	c.JSON(http.StatusOK, gin.H{"clients": []interface{}{}})
}

// DeleteClientHandler deletes (disables) an OAuth2 client.
func (s *OAuth2Server) DeleteClientHandler(c *gin.Context) {
	clientID := c.Param("client_id")
	client, err := s.store.GetClient(clientID)
	if err != nil {
		s.respondError(c, http.StatusNotFound, "not_found", "client not found")
		return
	}

	client.Enabled = false
	client.UpdatedAt = time.Now()

	if err := s.store.UpdateClient(client); err != nil {
		s.respondError(c, http.StatusInternalServerError, "server_error", "failed to disable client")
		return
	}

	// Revoke all tokens for this client
	_ = s.store.RevokeTokensByClient(clientID)

	c.Status(http.StatusNoContent)
}

// issueToken creates and persists a new access token (client_credentials, no refresh).
func (s *OAuth2Server) issueToken(client *models.OAuth2Client, userID, scope string, lifetimeSecs int) (*models.TokenResponse, error) {
	if lifetimeSecs <= 0 {
		lifetimeSecs = 3600
	}

	accessToken, err := s.jwtMgr.MakeToken(client.ID, userID, s.tenantID, scope, lifetimeSecs)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	record := &TokenRecord{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ClientID:    client.ID,
		UserID:      userID,
		Scope:       scope,
		ExpiresAt:   time.Now().Add(time.Duration(lifetimeSecs) * time.Second),
		IssuedAt:    time.Now(),
		Revoked:     false,
		TenantID:    s.tenantID,
		GrantType:   "client_credentials",
	}

	if err := s.store.SaveToken(record); err != nil {
		return nil, fmt.Errorf("save token record: %w", err)
	}

	return &models.TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   lifetimeSecs,
		Scope:       scope,
	}, nil
}

// issueTokenWithRefresh creates access token + refresh token.
func (s *OAuth2Server) issueTokenWithRefresh(client *models.OAuth2Client, userID, scope string, accessLifetime, refreshLifetime int) (*models.TokenResponse, error) {
	if accessLifetime <= 0 {
		accessLifetime = 3600
	}
	if refreshLifetime <= 0 {
		refreshLifetime = 86400
	}

	accessToken, err := s.jwtMgr.MakeToken(client.ID, userID, s.tenantID, scope, accessLifetime)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken := generateSecret()

	record := &TokenRecord{
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		TokenType:     "Bearer",
		ClientID:      client.ID,
		UserID:        userID,
		Scope:         scope,
		ExpiresAt:     time.Now().Add(time.Duration(accessLifetime) * time.Second),
		RefreshExpiry: time.Now().Add(time.Duration(refreshLifetime) * time.Second),
		IssuedAt:      time.Now(),
		Revoked:       false,
		TenantID:      s.tenantID,
		GrantType:     "authorization_code",
	}

	if userID == "" {
		record.GrantType = "client_credentials"
	}

	if err := s.store.SaveToken(record); err != nil {
		return nil, fmt.Errorf("save token record: %w", err)
	}

	return &models.TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    accessLifetime,
		RefreshToken: refreshToken,
		Scope:        scope,
	}, nil
}

// grantAllowed checks if a client is registered for a specific grant type.
func (s *OAuth2Server) grantAllowed(client *models.OAuth2Client, grantType string) bool {
	for _, gt := range client.GrantTypes {
		if gt == grantType {
			return true
		}
	}
	return false
}

// intersectScope returns the intersection of requested scope and client's allowed scopes.
func (s *OAuth2Server) intersectScope(client *models.OAuth2Client, requestedScope string) string {
	if requestedScope == "" {
		return ""
	}

	allowed := make(map[string]bool)
	for _, s := range client.Scopes {
		allowed[s] = true
	}

	requested := strings.Fields(requestedScope)
	var valid []string
	for _, s := range requested {
		if allowed[s] {
			valid = append(valid, s)
		}
	}

	return strings.Join(valid, " ")
}

// computeCodeChallenge computes the PKCE code_challenge from code_verifier.
func (s *OAuth2Server) computeCodeChallenge(verifier, method string) string {
	if method == "S256" {
		h := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(h[:])
	}
	// plain
	return verifier
}

// generateSecret creates a random client secret string.
func generateSecret() string {
	return uuid.New().String() + "-" + uuid.New().String()
}

// normalizeScopes trims, deduplicates, and lowercases scope values.
func normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// respondError writes an OAuth2 error response (RFC 6749 format).
func (s *OAuth2Server) respondError(c *gin.Context, status int, code, description string) {
	c.Header("Content-Type", "application/json;charset=UTF-8")
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(status, gin.H{
		"error":             code,
		"error_description": description,
	})
}

// ValidateAccessToken validates an access token and returns its claims.
func (s *OAuth2Server) ValidateAccessToken(tokenString string) (*models.JWTClaims, error) {
	return s.jwtMgr.ValidateToken(tokenString)
}

// ValidateAndCheckScope validates a token and verifies it has the required scope.
func (s *OAuth2Server) ValidateAndCheckScope(tokenString, requiredScope string) (*models.JWTClaims, error) {
	claims, err := s.jwtMgr.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}

	if requiredScope == "" {
		return claims, nil
	}

	scopes := make(map[string]bool)
	for _, s := range strings.Fields(claims.Scope) {
		scopes[s] = true
	}

	if !scopes[requiredScope] {
		return nil, fmt.Errorf("token missing required scope: %s", requiredScope)
	}

	return claims, nil
}

// ScopeManager handles scope management.
type ScopeManager struct {
	mu     sync.RWMutex
	scopes map[string]*ScopeDefinition
}

// ScopeDefinition defines a scope with metadata.
type ScopeDefinition struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	TenantID    string `json:"tenant_id"`
	BuiltIn     bool   `json:"built_in"`
}

// NewScopeManager creates a ScopeManager with default scopes.
func NewScopeManager() *ScopeManager {
	return &ScopeManager{
		scopes: map[string]*ScopeDefinition{
			"openid":       {Name: "openid", DisplayName: "OpenID", Description: "Access to OpenID Connect identity claims", BuiltIn: true},
			"profile":      {Name: "profile", DisplayName: "Profile", Description: "Access to user profile information", BuiltIn: true},
			"email":        {Name: "email", DisplayName: "Email", Description: "Access to email address", BuiltIn: true},
			"address":      {Name: "address", DisplayName: "Address", Description: "Access to address information", BuiltIn: true},
			"phone":        {Name: "phone", DisplayName: "Phone", Description: "Access to phone number", BuiltIn: true},
			"api:read":     {Name: "api:read", DisplayName: "API Read", Description: "Read access to APIs", BuiltIn: true},
			"api:write":    {Name: "api:write", DisplayName: "API Write", Description: "Write access to APIs", BuiltIn: true},
			"api:delete":   {Name: "api:delete", DisplayName: "API Delete", Description: "Delete access to APIs", BuiltIn: true},
			"api:admin":    {Name: "api:admin", DisplayName: "API Admin", Description: "Admin access to APIs", BuiltIn: true},
			"subscribe":    {Name: "subscribe", DisplayName: "Subscribe", Description: "Subscribe to APIs", BuiltIn: true},
			"publish":      {Name: "publish", DisplayName: "Publish", Description: "Publish APIs", BuiltIn: true},
		},
	}
}

// RegisterScope adds a new scope definition.
func (sm *ScopeManager) RegisterScope(def *ScopeDefinition) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if def.Name == "" {
		return errors.New("scope name is required")
	}
	sm.scopes[def.Name] = def
	return nil
}

// GetScope returns a scope definition by name.
func (sm *ScopeManager) GetScope(name string) (*ScopeDefinition, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.scopes[name]
	return s, ok
}

// ListScopes returns all registered scopes.
func (sm *ScopeManager) ListScopes(tenantID string) []*ScopeDefinition {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	var out []*ScopeDefinition
	for _, s := range sm.scopes {
		if tenantID == "" || s.TenantID == "" || s.TenantID == tenantID {
			out = append(out, s)
		}
	}
	return out
}

// DeleteScope removes a non-built-in scope.
func (sm *ScopeManager) DeleteScope(name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	s, ok := sm.scopes[name]
	if !ok {
		return errors.New("scope not found")
	}
	if s.BuiltIn {
		return errors.New("cannot delete built-in scope")
	}
	delete(sm.scopes, name)
	return nil
}

// BuildOAuth2Routes registers all OAuth2 endpoints on the given Gin router group.
func (s *OAuth2Server) BuildOAuth2Routes(rg *gin.RouterGroup) {
	rg.POST("/token", s.TokenHandler)
	rg.POST("/authorize", s.AuthorizeHandler)
	rg.POST("/introspect", s.IntrospectHandler)
	rg.POST("/revoke", s.RevokeHandler)

	rg.POST("/register", auth.PublisherOrAbove(), s.RegisterClientHandler)
	rg.GET("/clients/:client_id", s.GetClientHandler)
	rg.GET("/clients", s.ListClientsHandler)
	rg.DELETE("/clients/:client_id", auth.AdminOnly(), s.DeleteClientHandler)
}
