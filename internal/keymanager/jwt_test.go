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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// JWT Implementation
// ============================================================================

// JWTClaims represents standard JWT claims
type JWTClaims struct {
	Issuer     string            `json:"iss,omitempty"`
	Subject    string            `json:"sub,omitempty"`
	Audience   []string          `json:"aud,omitempty"`
	Expiration int64             `json:"exp,omitempty"`
	NotBefore  int64             `json:"nbf,omitempty"`
	IssuedAt   int64             `json:"iat,omitempty"`
	JWTID      string            `json:"jti,omitempty"`
	Custom     map[string]interface{} `json:"-"`
}

// Valid checks if the claims are valid
func (c *JWTClaims) Valid() error {
	now := time.Now().Unix()
	if c.Expiration > 0 && now > c.Expiration {
		return fmt.Errorf("token is expired")
	}
	if c.NotBefore > 0 && now < c.NotBefore {
		return fmt.Errorf("token not valid yet")
	}
	if c.IssuedAt > 0 && now < c.IssuedAt {
		return fmt.Errorf("token issued in the future")
	}
	return nil
}

// JWKS represents a JSON Web Key Set
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWK represents a JSON Web Key
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use,omitempty"`
	N   string `json:"n,omitempty"`   // RSA modulus
	E   string `json:"e,omitempty"`   // RSA exponent
	X   string `json:"x,omitempty"`   // EC x coordinate
	Y   string `json:"y,omitempty"`   // EC y coordinate
	Crv string `json:"crv,omitempty"` // EC curve
	Alg string `json:"alg,omitempty"`
}

// JWTSigner handles JWT signing and validation
type JWTSigner struct {
	signingKey    interface{}
	validationKey interface{}
	algorithm     string
	issuer        string
	kid           string
}

// NewJWTSigner creates a new JWT signer with an RSA key
func NewJWTSigner(privateKey *rsa.PrivateKey, issuer, kid string) *JWTSigner {
	return &JWTSigner{
		signingKey:    privateKey,
		validationKey: &privateKey.PublicKey,
		algorithm:     "RS256",
		issuer:        issuer,
		kid:           kid,
	}
}

// NewJWTSignerWithECDSA creates a new JWT signer with an ECDSA key
func NewJWTSignerWithECDSA(privateKey *ecdsa.PrivateKey, issuer, kid string) *JWTSigner {
	return &JWTSigner{
		signingKey:    privateKey,
		validationKey: &privateKey.PublicKey,
		algorithm:     "ES256",
		issuer:        issuer,
		kid:           kid,
	}
}

// Sign creates a JWT token from claims
func (s *JWTSigner) Sign(claims JWTClaims) (string, error) {
	claims.Issuer = s.issuer
	claims.IssuedAt = time.Now().Unix()
	if claims.Expiration == 0 {
		claims.Expiration = time.Now().Add(24 * time.Hour).Unix()
	}

	header := map[string]string{
		"alg": s.algorithm,
		"typ": "JWT",
		"kid": s.kid,
	}

	headerJSON, _ := json.Marshal(header)
	claimsMap := claimsToMap(claims)
	claimsJSON, _ := json.Marshal(claimsMap)

	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	message := encodedHeader + "." + encodedClaims

	var signature []byte
	var err error
	switch key := s.signingKey.(type) {
	case *rsa.PrivateKey:
		signature, err = signRS256(message, key)
	case *ecdsa.PrivateKey:
		signature, err = signES256(message, key)
	default:
		return "", fmt.Errorf("unsupported signing key type")
	}
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	encodedSig := base64.RawURLEncoding.EncodeToString(signature)
	return message + "." + encodedSig, nil
}

// Validate parses and validates a JWT token
func (s *JWTSigner) Validate(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	// Decode and verify signature
	message := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	var valid bool
	switch key := s.validationKey.(type) {
	case *rsa.PublicKey:
		valid = verifyRS256(message, signature, key)
	case *ecdsa.PublicKey:
		valid = verifyES256(message, signature, key)
	default:
		return nil, fmt.Errorf("unsupported validation key type")
	}

	if !valid {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode claims
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	var claims JWTClaims
	var claimsMap map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claimsMap); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	// Map standard claims
	if v, ok := claimsMap["iss"].(string); ok {
		claims.Issuer = v
	}
	if v, ok := claimsMap["sub"].(string); ok {
		claims.Subject = v
	}
	if v, ok := claimsMap["exp"].(float64); ok {
		claims.Expiration = int64(v)
	}
	if v, ok := claimsMap["nbf"].(float64); ok {
		claims.NotBefore = int64(v)
	}
	if v, ok := claimsMap["iat"].(float64); ok {
		claims.IssuedAt = int64(v)
	}
	if v, ok := claimsMap["jti"].(string); ok {
		claims.JWTID = v
	}

	// Validate claims
	if err := claims.Valid(); err != nil {
		return nil, err
	}

	// Check issuer
	if claims.Issuer != s.issuer {
		return nil, fmt.Errorf("invalid issuer")
	}

	claims.Custom = claimsMap
	return &claims, nil
}

// GenerateJWKS generates a JWKS for the current key
func (s *JWTSigner) GenerateJWKS() (*JWKS, error) {
	jwk := JWK{
		Kty: "RSA",
		Kid: s.kid,
		Use: "sig",
		Alg: s.algorithm,
	}

	switch key := s.validationKey.(type) {
	case *rsa.PublicKey:
		jwk.Kty = "RSA"
		jwk.N = base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		jwk.E = base64.RawURLEncoding.EncodeToString(intToBytes(key.E))
	case *ecdsa.PublicKey:
		jwk.Kty = "EC"
		jwk.Crv = "P-256"
		jwk.X = base64.RawURLEncoding.EncodeToString(key.X.Bytes())
		jwk.Y = base64.RawURLEncoding.EncodeToString(key.Y.Bytes())
	default:
		return nil, fmt.Errorf("unsupported key type for JWKS")
	}

	return &JWKS{Keys: []JWK{jwk}}, nil
}

// GenerateJWKSFromKeys generates JWKS from multiple keys
func GenerateJWKSFromKeys(keys map[string]interface{}) (*JWKS, error) {
	jwks := &JWKS{Keys: make([]JWK, 0)}
	for kid, key := range keys {
		jwk := JWK{Kid: kid, Use: "sig"}
		switch k := key.(type) {
		case *rsa.PublicKey:
			jwk.Kty = "RSA"
			jwk.Alg = "RS256"
			jwk.N = base64.RawURLEncoding.EncodeToString(k.N.Bytes())
			jwk.E = base64.RawURLEncoding.EncodeToString(intToBytes(k.E))
		case *ecdsa.PublicKey:
			jwk.Kty = "EC"
			jwk.Alg = "ES256"
			jwk.Crv = "P-256"
			jwk.X = base64.RawURLEncoding.EncodeToString(k.X.Bytes())
			jwk.Y = base64.RawURLEncoding.EncodeToString(k.Y.Bytes())
		default:
			continue
		}
		jwks.Keys = append(jwks.Keys, jwk)
	}
	return jwks, nil
}

// Helper functions
func claimsToMap(claims JWTClaims) map[string]interface{} {
	m := make(map[string]interface{})
	if claims.Issuer != "" {
		m["iss"] = claims.Issuer
	}
	if claims.Subject != "" {
		m["sub"] = claims.Subject
	}
	if len(claims.Audience) > 0 {
		m["aud"] = claims.Audience
	}
	if claims.Expiration > 0 {
		m["exp"] = claims.Expiration
	}
	if claims.NotBefore > 0 {
		m["nbf"] = claims.NotBefore
	}
	if claims.IssuedAt > 0 {
		m["iat"] = claims.IssuedAt
	}
	if claims.JWTID != "" {
		m["jti"] = claims.JWTID
	}
	for k, v := range claims.Custom {
		m[k] = v
	}
	return m
}

func signRS256(message string, key *rsa.PrivateKey) ([]byte, error) {
	hash := []byte(message)
	return rsa.SignPKCS1v15(rand.Reader, key, 0, hash)
}

func verifyRS256(message string, signature []byte, key *rsa.PublicKey) bool {
	hash := []byte(message)
	err := rsa.VerifyPKCS1v15(key, 0, hash, signature)
	return err == nil
}

func signES256(message string, key *ecdsa.PrivateKey) ([]byte, error) {
	hash := []byte(message)
	r, s, err := ecdsa.Sign(rand.Reader, key, hash)
	if err != nil {
		return nil, err
	}
	return append(r.Bytes(), s.Bytes()...), nil
}

func verifyES256(message string, signature []byte, key *ecdsa.PublicKey) bool {
	if len(signature) != 64 {
		return false
	}
	hash := []byte(message)
	r := new(int).SetBytes(signature[:32])
	s := new(int).SetBytes(signature[32:])
	return ecdsa.Verify(key, hash, r, s)
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

// GenerateRSAKeyPair generates an RSA key pair for testing
func GenerateRSAKeyPair(bits int) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, &privateKey.PublicKey, nil
}

// GenerateECDSAKeyPair generates an ECDSA key pair for testing
func GenerateECDSAKeyPair() (*ecdsa.PrivateKey, *ecdsa.PublicKey, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, &privateKey.PublicKey, nil
}

// KeyToPEM converts a private key to PEM format
func KeyToPEM(key *rsa.PrivateKey) string {
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}

// ============================================================================
// TESTS
// ============================================================================

func TestJWTSigner_Sign_GivenValidClaims_WhenSigned_ThenReturnsToken(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	tests := []struct {
		name   string
		claims JWTClaims
	}{
		{
			name: "minimal claims",
			claims: JWTClaims{
				Subject: "user-123",
			},
		},
		{
			name: "full claims",
			claims: JWTClaims{
				Subject:    "user-456",
				Audience:   []string{"api-1", "api-2"},
				Expiration: time.Now().Add(1 * time.Hour).Unix(),
				NotBefore:  time.Now().Add(-1 * time.Hour).Unix(),
				JWTID:      "unique-token-id",
			},
		},
		{
			name: "claims with custom fields",
			claims: JWTClaims{
				Subject: "user-789",
				Custom: map[string]interface{}{
					"role":     "admin",
					"tenant":   "acme-corp",
					"permissions": []string{"read", "write"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := signer.Sign(tt.claims)
			require.NoError(t, err)
			assert.NotEmpty(t, token)

			// Should have 3 parts
			parts := strings.Split(token, ".")
			assert.Len(t, parts, 3)
		})
	}
}

func TestJWTSigner_Validate_GivenValidToken_WhenValidated_ThenReturnsClaims(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	originalClaims := JWTClaims{
		Subject:    "user-123",
		Audience:   []string{"api-1"},
		Expiration: time.Now().Add(1 * time.Hour).Unix(),
		JWTID:      "token-123",
	}

	token, err := signer.Sign(originalClaims)
	require.NoError(t, err)

	validated, err := signer.Validate(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", validated.Subject)
	assert.Equal(t, "test-issuer", validated.Issuer)
	assert.Equal(t, "token-123", validated.JWTID)
}

func TestJWTSigner_Validate_GivenExpiredToken_WhenValidated_ThenReturnsError(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	claims := JWTClaims{
		Subject:    "user-123",
		Expiration: time.Now().Add(-1 * time.Hour).Unix(), // Expired
	}

	token, err := signer.Sign(claims)
	require.NoError(t, err)

	_, err = signer.Validate(token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestJWTSigner_Validate_GivenInvalidSignature_WhenValidated_ThenReturnsError(t *testing.T) {
	privateKey1, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	privateKey2, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer1 := NewJWTSigner(privateKey1, "test-issuer", "key-1")
	signer2 := NewJWTSigner(privateKey2, "test-issuer", "key-1") // Different key

	claims := JWTClaims{
		Subject:    "user-123",
		Expiration: time.Now().Add(1 * time.Hour).Unix(),
	}

	token, err := signer1.Sign(claims)
	require.NoError(t, err)

	_, err = signer2.Validate(token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestJWTSigner_Validate_GivenInvalidFormat_WhenValidated_ThenReturnsError(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	tests := []struct {
		name  string
		token string
	}{
		{"too few parts", "header.claims"},
		{"too many parts", "header.claims.sig.extra"},
		{"empty token", ""},
		{"only dots", ".."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := signer.Validate(tt.token)
			assert.Error(t, err)
		})
	}
}

func TestJWTSigner_Validate_GivenWrongIssuer_WhenValidated_ThenReturnsError(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer1 := NewJWTSigner(privateKey, "issuer-1", "key-1")
	signer2 := NewJWTSigner(privateKey, "issuer-2", "key-1")

	claims := JWTClaims{
		Subject:    "user-123",
		Expiration: time.Now().Add(1 * time.Hour).Unix(),
	}

	token, err := signer1.Sign(claims)
	require.NoError(t, err)

	_, err = signer2.Validate(token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid issuer")
}

func TestJWTSigner_ECDSA_GivenECDSAKey_WhenSigned_ThenWorks(t *testing.T) {
	privateKey, _, err := GenerateECDSAKeyPair()
	require.NoError(t, err)

	signer := NewJWTSignerWithECDSA(privateKey, "test-issuer", "ec-key-1")

	claims := JWTClaims{
		Subject:    "user-123",
		Expiration: time.Now().Add(1 * time.Hour).Unix(),
	}

	token, err := signer.Sign(claims)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	validated, err := signer.Validate(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", validated.Subject)
}

func TestJWTSigner_GenerateJWKS_GivenRSAKey_WhenGenerated_ThenReturnsJWKS(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	jwks, err := signer.GenerateJWKS()
	require.NoError(t, err)
	require.Len(t, jwks.Keys, 1)

	key := jwks.Keys[0]
	assert.Equal(t, "RSA", key.Kty)
	assert.Equal(t, "key-1", key.Kid)
	assert.Equal(t, "RS256", key.Alg)
	assert.Equal(t, "sig", key.Use)
	assert.NotEmpty(t, key.N)
	assert.NotEmpty(t, key.E)
}

func TestJWTSigner_GenerateJWKS_GivenECDSAKey_WhenGenerated_ThenReturnsJWKS(t *testing.T) {
	privateKey, _, err := GenerateECDSAKeyPair()
	require.NoError(t, err)

	signer := NewJWTSignerWithECDSA(privateKey, "test-issuer", "ec-key-1")

	jwks, err := signer.GenerateJWKS()
	require.NoError(t, err)
	require.Len(t, jwks.Keys, 1)

	key := jwks.Keys[0]
	assert.Equal(t, "EC", key.Kty)
	assert.Equal(t, "ec-key-1", key.Kid)
	assert.Equal(t, "ES256", key.Alg)
	assert.Equal(t, "P-256", key.Crv)
	assert.NotEmpty(t, key.X)
	assert.NotEmpty(t, key.Y)
}

func TestGenerateJWKSFromKeys_GivenMultipleKeys_WhenGenerated_ThenReturnsAllKeys(t *testing.T) {
	rsaKey, _, _ := GenerateRSAKeyPair(2048)
	ecdsaKey, _, _ := GenerateECDSAKeyPair()

	keys := map[string]interface{}{
		"rsa-key":   &rsaKey.PublicKey,
		"ecdsa-key": &ecdsaKey.PublicKey,
	}

	jwks, err := GenerateJWKSFromKeys(keys)
	require.NoError(t, err)
	assert.Len(t, jwks.Keys, 2)

	kidSet := make(map[string]bool)
	for _, k := range jwks.Keys {
		kidSet[k.Kid] = true
	}
	assert.True(t, kidSet["rsa-key"])
	assert.True(t, kidSet["ecdsa-key"])
}

func TestJWTClaims_Valid_GivenVariousClaims_WhenChecked_ThenCorrectResult(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name     string
		claims   JWTClaims
		expectOK bool
	}{
		{
			name:     "valid claims",
			claims:   JWTClaims{Expiration: now + 3600, IssuedAt: now - 1},
			expectOK: true,
		},
		{
			name:     "expired token",
			claims:   JWTClaims{Expiration: now - 1},
			expectOK: false,
		},
		{
			name:     "not yet valid",
			claims:   JWTClaims{NotBefore: now + 3600, Expiration: now + 7200},
			expectOK: false,
		},
		{
			name:     "no expiration",
			claims:   JWTClaims{Subject: "test"},
			expectOK: true,
		},
		{
			name:     "issued in future",
			claims:   JWTClaims{IssuedAt: now + 3600, Expiration: now + 7200},
			expectOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.claims.Valid()
			if tt.expectOK {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestJWTSigner_TokenNotBefore_GivenFutureNBF_WhenValidated_ThenReturnsError(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	claims := JWTClaims{
		Subject:    "user-123",
		NotBefore:  time.Now().Add(1 * time.Hour).Unix(), // Not valid for an hour
		Expiration: time.Now().Add(2 * time.Hour).Unix(),
	}

	token, err := signer.Sign(claims)
	require.NoError(t, err)

	_, err = signer.Validate(token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not valid yet")
}

func TestJWTSigner_CustomClaims_GivenCustomFields_WhenValidated_ThenPreserved(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	claims := JWTClaims{
		Subject:    "user-123",
		Expiration: time.Now().Add(1 * time.Hour).Unix(),
		Custom: map[string]interface{}{
			"role":   "admin",
			"tenant": "acme",
		},
	}

	token, err := signer.Sign(claims)
	require.NoError(t, err)

	validated, err := signer.Validate(token)
	require.NoError(t, err)
	assert.NotNil(t, validated.Custom)
	assert.Equal(t, "admin", validated.Custom["role"])
	assert.Equal(t, "acme", validated.Custom["tenant"])
}

func TestJWTSigner_MalformedToken_GivenTamperedClaims_WhenValidated_ThenFails(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	claims := JWTClaims{
		Subject:    "user-123",
		Expiration: time.Now().Add(1 * time.Hour).Unix(),
	}

	token, err := signer.Sign(claims)
	require.NoError(t, err)

	// Tamper with the claims part
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	// Decode claims, modify, re-encode
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claimsMap map[string]interface{}
	json.Unmarshal(claimsJSON, &claimsMap)
	claimsMap["sub"] = "attacker"
	modifiedClaimsJSON, _ := json.Marshal(claimsMap)
	parts[1] = base64.RawURLEncoding.EncodeToString(modifiedClaimsJSON)

	tamperedToken := strings.Join(parts, ".")

	_, err = signer.Validate(tamperedToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestKeyToPEM_GivenRSAKey_WhenConverted_ThenReturnsValidPEM(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	pemStr := KeyToPEM(privateKey)
	assert.NotEmpty(t, pemStr)
	assert.Contains(t, pemStr, "BEGIN RSA PRIVATE KEY")
	assert.Contains(t, pemStr, "END RSA PRIVATE KEY")
}

func TestJWTSigner_Boundary_GivenZeroExpiration_WhenSigned_ThenSetsDefault(t *testing.T) {
	privateKey, _, err := GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	signer := NewJWTSigner(privateKey, "test-issuer", "key-1")

	claims := JWTClaims{Subject: "user-123"} // No expiration set

	token, err := signer.Sign(claims)
	require.NoError(t, err)

	validated, err := signer.Validate(token)
	require.NoError(t, err)
	assert.True(t, validated.Expiration > time.Now().Unix())
	assert.True(t, validated.Expiration <= time.Now().Add(25*time.Hour).Unix())
}
