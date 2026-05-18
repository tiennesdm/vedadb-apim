// Package keymanager implements JWT signing, validation, JWKS generation and
// key rotation for the VedaDB API Manager.
package keymanager

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/vedadb/vapim/pkg/models"
)

// KeyPair holds either an RSA or ECDSA signing key together with metadata.
type KeyPair struct {
	KID        string
	Algorithm  string // RS256, ES256, HS256
	Use        string // sig, enc
	RSAPrivate *rsa.PrivateKey
	RSAPublic  *rsa.PublicKey
	ECPrivate  *ecdsa.PrivateKey
	ECPublic   *ecdsa.PublicKey
	Symmetric  []byte // for HS256
	CreatedAt  time.Time
	RevokedAt  *time.Time
}

// JWTManager is responsible for generating and validating JWTs and exposing JWKS.
type JWTManager struct {
	mu       sync.RWMutex
	issuer   string
	keys     map[string]*KeyPair // kid -> KeyPair
	activeKid string
}

// NewJWTManager creates a JWTManager and generates an initial RSA key pair.
func NewJWTManager(issuer string) (*JWTManager, error) {
	jm := &JWTManager{
		issuer: issuer,
		keys:   make(map[string]*KeyPair),
	}
	if err := jm.GenerateRSAKeyPair("default", 2048); err != nil {
		return nil, fmt.Errorf("generate default RSA key: %w", err)
	}
	return jm, nil
}

// GenerateRSAKeyPair creates a new RSA key pair and adds it to the manager.
func (jm *JWTManager) GenerateRSAKeyPair(kid string, bits int) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return fmt.Errorf("rsa generate: %w", err)
	}

	pair := &KeyPair{
		KID:        kid,
		Algorithm:  "RS256",
		Use:        "sig",
		RSAPrivate: privateKey,
		RSAPublic:  &privateKey.PublicKey,
		CreatedAt:  time.Now(),
	}

	jm.mu.Lock()
	defer jm.mu.Unlock()
	jm.keys[kid] = pair
	jm.activeKid = kid
	return nil
}

// GenerateECDSAKeyPair creates a new ECDSA P-256 key pair.
func (jm *JWTManager) GenerateECDSAKeyPair(kid string) error {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("ecdsa generate: %w", err)
	}

	pair := &KeyPair{
		KID:        kid,
		Algorithm:  "ES256",
		Use:        "sig",
		ECPrivate:  privateKey,
		ECPublic:   &privateKey.PublicKey,
		CreatedAt:  time.Now(),
	}

	jm.mu.Lock()
	defer jm.mu.Unlock()
	jm.keys[kid] = pair
	return nil
}

// GenerateSymmetricKey creates a new HS256 symmetric key.
func (jm *JWTManager) GenerateSymmetricKey(kid string, keySize int) error {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("random key: %w", err)
	}

	pair := &KeyPair{
		KID:       kid,
		Algorithm: "HS256",
		Use:       "sig",
		Symmetric: key,
		CreatedAt: time.Now(),
	}

	jm.mu.Lock()
	defer jm.mu.Unlock()
	jm.keys[kid] = pair
	return nil
}

// SetActiveKey sets the key used for signing new tokens.
func (jm *JWTManager) SetActiveKey(kid string) error {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if _, ok := jm.keys[kid]; !ok {
		return fmt.Errorf("key %s not found", kid)
	}
	jm.activeKid = kid
	return nil
}

// RevokeKey marks a key as revoked so it is no longer included in JWKS.
func (jm *JWTManager) RevokeKey(kid string) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if kp, ok := jm.keys[kid]; ok {
		t := time.Now()
		kp.RevokedAt = &t
	}
}

// GenerateToken creates a signed JWT string using the active signing key.
// Supported algorithms: RS256, ES256, HS256.
func (jm *JWTManager) GenerateToken(claims *models.JWTClaims, algorithm string) (string, error) {
	jm.mu.RLock()
	kid := jm.activeKid
	pair := jm.keys[kid]
	jm.mu.RUnlock()

	if pair == nil {
		return "", fmt.Errorf("no active signing key")
	}

	if algorithm == "" {
		algorithm = pair.Algorithm
	}

	jwtClaims := jwt.MapClaims{
		"sub": claims.Sub,
		"iss": claims.Iss,
		"exp": claims.Exp,
		"iat": claims.Iat,
		"jti": claims.Jti,
	}

	if len(claims.Aud) > 0 {
		jwtClaims["aud"] = claims.Aud
	}
	if claims.Scope != "" {
		jwtClaims["scope"] = claims.Scope
	}
	if claims.APIContext != "" {
		jwtClaims["api_context"] = claims.APIContext
	}
	if claims.ClientID != "" {
		jwtClaims["client_id"] = claims.ClientID
	}
	if claims.TenantID != "" {
		jwtClaims["tenant_id"] = claims.TenantID
	}

	token := jwt.NewWithClaims(jwt.GetSigningMethod(algorithm), jwtClaims)
	token.Header["kid"] = kid

	var signedString string
	var err error

	switch algorithm {
	case "RS256":
		signedString, err = token.SignedString(pair.RSAPrivate)
	case "ES256":
		signedString, err = token.SignedString(pair.ECPrivate)
	case "HS256":
		signedString, err = token.SignedString(pair.Symmetric)
	default:
		return "", fmt.Errorf("unsupported signing algorithm: %s", algorithm)
	}

	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signedString, nil
}

// ValidateToken parses and validates a JWT string using the JWKS.
func (jm *JWTManager) ValidateToken(tokenString string) (*models.JWTClaims, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		kidVal, ok := t.Header["kid"]
		if !ok {
			return nil, fmt.Errorf("token missing kid header")
		}
		kid, ok := kidVal.(string)
		if !ok {
			return nil, fmt.Errorf("invalid kid header type")
		}

		jm.mu.RLock()
		pair, exists := jm.keys[kid]
		jm.mu.RUnlock()
		if !exists {
			return nil, fmt.Errorf("key %s not found", kid)
		}

		switch t.Method.Alg() {
		case "RS256":
			return pair.RSAPublic, nil
		case "ES256":
			return pair.ECPublic, nil
		case "HS256":
			return pair.Symmetric, nil
		default:
			return nil, fmt.Errorf("unsupported algorithm: %s", t.Method.Alg())
		}
	}, jwt.WithIssuer(jm.issuer))

	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	claims := &models.JWTClaims{
		Jti: jm.stringClaim(mc, "jti"),
		Iss: jm.stringClaim(mc, "iss"),
		Sub: jm.stringClaim(mc, "sub"),
	}
	if exp, ok := mc["exp"].(float64); ok {
		claims.Exp = int64(exp)
	}
	if iat, ok := mc["iat"].(float64); ok {
		claims.Iat = int64(iat)
	}
	claims.Scope = jm.stringClaim(mc, "scope")
	claims.APIContext = jm.stringClaim(mc, "api_context")
	claims.ClientID = jm.stringClaim(mc, "client_id")
	claims.TenantID = jm.stringClaim(mc, "tenant_id")

	if aud, ok := mc["aud"]; ok {
		switch v := aud.(type) {
		case []interface{}:
			for _, a := range v {
				claims.Aud = append(claims.Aud, fmt.Sprintf("%v", a))
			}
		case string:
			claims.Aud = []string{v}
		}
	}

	return claims, nil
}

// stringClaim safely extracts a string claim from MapClaims.
func (jm *JWTManager) stringClaim(mc jwt.MapClaims, key string) string {
	val, ok := mc[key]
	if !ok {
		return ""
	}
	s, ok := val.(string)
	if !ok {
		return fmt.Sprintf("%v", val)
	}
	return s
}

// GetJWKS returns the JSON Web Key Set containing all active (non-revoked) keys.
func (jm *JWTManager) GetJWKS() models.JWKS {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	var jwks models.JWKS
	for _, pair := range jm.keys {
		if pair.RevokedAt != nil {
			continue
		}

		jwk := models.JWK{
			Kty: jm.keyType(pair.Algorithm),
			Kid: pair.KID,
			Use: pair.Use,
			Alg: pair.Algorithm,
		}

		switch pair.Algorithm {
		case "RS256":
			nBytes := pair.RSAPublic.N.Bytes()
			jwk.N = base64.RawURLEncoding.EncodeToString(nBytes)
			jwk.E = base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pair.RSAPublic.E)).Bytes())
		case "ES256":
			jwk.X = base64.RawURLEncoding.EncodeToString(pair.ECPublic.X.Bytes())
			jwk.Y = base64.RawURLEncoding.EncodeToString(pair.ECPublic.Y.Bytes())
			jwk.Crv = "P-256"
		case "HS256":
			jwk.K = base64.RawURLEncoding.EncodeToString(pair.Symmetric)
		}

		jwks.Keys = append(jwks.Keys, jwk)
	}
	return jwks
}

// JWKSHandler is an HTTP handler that returns the JWKS as JSON.
func (jm *JWTManager) JWKSHandler(c interface{ JSON(int, interface{}) }) {
	c.JSON(200, jm.GetJWKS())
}

// GetActivePublicKeyPEM returns the PEM-encoded public key of the active signing key.
func (jm *JWTManager) GetActivePublicKeyPEM() (string, error) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	pair := jm.keys[jm.activeKid]
	if pair == nil {
		return "", fmt.Errorf("no active key")
	}

	var pubKey interface{}
	switch pair.Algorithm {
	case "RS256":
		pubKey = pair.RSAPublic
	case "ES256":
		pubKey = pair.ECPublic
	default:
		return "", fmt.Errorf("cannot export PEM for algorithm %s", pair.Algorithm)
	}

	der, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}

	block := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: der,
	}
	return string(pem.EncodeToMemory(block)), nil
}

// keyType maps algorithm to JWK kty value.
func (jm *JWTManager) keyType(alg string) string {
	switch alg {
	case "RS256":
		return "RSA"
	case "ES256":
		return "EC"
	case "HS256":
		return "oct"
	default:
		return "RSA"
	}
}

// GenerateAPIKeyToken creates a self-signed JWT to be used as an API key.
// The token is signed with the active key and contains a unique jti.
func (jm *JWTManager) GenerateAPIKeyToken(apiID, appID, tenantID string, scopes []string, validHours int) (string, error) {
	now := time.Now()
	jti := uuid.New().String()

	claims := &models.JWTClaims{
		Sub:        appID,
		Iss:        jm.issuer,
		Aud:        []string{"vedadb-apim"},
		Exp:        now.Add(time.Duration(validHours) * time.Hour).Unix(),
		Iat:        now.Unix(),
		Jti:        jti,
		Scope:      strings.Join(scopes, " "),
		APIContext: apiID,
		ClientID:   appID,
		TenantID:   tenantID,
	}

	return jm.GenerateToken(claims, "")
}

// MakeToken generates an access token (JWT) for the given OAuth2 parameters.
func (jm *JWTManager) MakeToken(clientID, userID, tenantID, scope string, lifetimeSecs int) (string, error) {
	now := time.Now()
	claims := &models.JWTClaims{
		Sub:      userID,
		Iss:      jm.issuer,
		Aud:      []string{"vedadb-apim"},
		Exp:      now.Add(time.Duration(lifetimeSecs) * time.Second).Unix(),
		Iat:      now.Unix(),
		Jti:      uuid.New().String(),
		Scope:    scope,
		ClientID: clientID,
		TenantID: tenantID,
	}
	return jm.GenerateToken(claims, "")
}

// TokenMetadata returns the decoded claims without verifying the signature.
// Useful for introspection endpoints that need to read token metadata.
func (jm *JWTManager) TokenMetadata(tokenString string) (*models.JWTClaims, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("parse unverified token: %w", err)
	}
	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	claims := &models.JWTClaims{
		Jti: jm.stringClaim(mc, "jti"),
		Iss: jm.stringClaim(mc, "iss"),
		Sub: jm.stringClaim(mc, "sub"),
		Scope:      jm.stringClaim(mc, "scope"),
		APIContext: jm.stringClaim(mc, "api_context"),
		ClientID:   jm.stringClaim(mc, "client_id"),
		TenantID:   jm.stringClaim(mc, "tenant_id"),
	}
	if exp, ok := mc["exp"].(float64); ok {
		claims.Exp = int64(exp)
	}
	if iat, ok := mc["iat"].(float64); ok {
		claims.Iat = int64(iat)
	}
	return claims, nil
}

// SerializeJWKS serializes the JWKS to compact JSON.
func SerializeJWKS(jwks models.JWKS) ([]byte, error) {
	return json.MarshalIndent(jwks, "", "  ")
}

// ParseJWKS parses a JWKS from JSON bytes.
func ParseJWKS(data []byte) (models.JWKS, error) {
	var jwks models.JWKS
	if err := json.Unmarshal(data, &jwks); err != nil {
		return models.JWKS{}, fmt.Errorf("unmarshal jwks: %w", err)
	}
	return jwks, nil
}

// CountActiveKeys returns the number of active (non-revoked) signing keys.
func (jm *JWTManager) CountActiveKeys() int {
	jm.mu.RLock()
	defer jm.mu.RUnlock()
	count := 0
	for _, pair := range jm.keys {
		if pair.RevokedAt == nil {
			count++
		}
	}
	return count
}
