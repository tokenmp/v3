// Package jwt implements Ed25519 (EdDSA) access token issuance and verification
// for the auth service.
//
// Tokens are signed with an Ed25519 private key and verified with the
// corresponding public key (github.com/golang-jwt/jwt/v5). Consumers that only
// need to validate tokens can be distributed the public key alone — they never
// need the private key. The trade-off is documented in ADR 0005: if the
// private key is compromised, an attacker can forge valid access tokens until
// the key is rotated.
//
// Registered claims: iss, aud, sub, jti, iat, nbf, exp. Custom claims carry
// the user role and token_version. token_version is compared against the
// users.token_version column on every authenticated request so a password
// change or logout-all (which bump token_version) immediately invalidates all
// outstanding access tokens.
//
// Keys are loaded from files on disk (AUTH_JWT_PRIVATE_KEY_FILE /
// AUTH_JWT_PUBLIC_KEY_FILE). The PEM content and file paths are never echoed
// in errors or logs.
package jwt

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// Sentinel errors. None echo the key, path or PEM content.
var (
	ErrPrivateKeyFileRequired = errors.New("jwt: AUTH_JWT_PRIVATE_KEY_FILE is required")
	ErrPublicKeyFileRequired  = errors.New("jwt: AUTH_JWT_PUBLIC_KEY_FILE is required")
	ErrPrivateKeyReadFailed   = errors.New("jwt: private key file could not be read")
	ErrPublicKeyReadFailed    = errors.New("jwt: public key file could not be read")
	ErrPrivateKeyParseFailed  = errors.New("jwt: private key is not a valid Ed25519 PEM (PKCS8)")
	ErrPublicKeyParseFailed   = errors.New("jwt: public key is not a valid Ed25519 PEM (PKIX)")
)

// KeyPair holds the parsed Ed25519 keys. The Verifier only needs the public
// key; the Issuer needs the private key.
type KeyPair struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// LoadKeyPair reads and parses the Ed25519 PEM files. The file paths and PEM
// contents are never returned in errors. Failures are fail-fast at startup.
func LoadKeyPair(privateKeyFile, publicKeyFile string) (*KeyPair, error) {
	if privateKeyFile == "" {
		return nil, ErrPrivateKeyFileRequired
	}
	if publicKeyFile == "" {
		return nil, ErrPublicKeyFileRequired
	}
	priv, err := loadPrivate(privateKeyFile)
	if err != nil {
		return nil, err
	}
	pub, err := loadPublic(publicKeyFile)
	if err != nil {
		return nil, err
	}
	// Guard against mismatched key pairs: derive the public key from the
	// private key and require it to match the loaded public key. This catches
	// a configuration mistake at startup rather than at first verification.
	pubDerived := priv.Public()
	pubBytes, ok := pubDerived.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("jwt: private key did not yield an Ed25519 public key")
	}
	if !bytes.Equal(pubBytes, pub) {
		return nil, errors.New("jwt: private and public keys do not form a matching Ed25519 pair")
	}
	return &KeyPair{Private: priv, Public: pub}, nil
}

func loadPrivate(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrPrivateKeyReadFailed
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, ErrPrivateKeyParseFailed
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrPrivateKeyParseFailed
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, ErrPrivateKeyParseFailed
	}
	return ed, nil
}

func loadPublic(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrPublicKeyReadFailed
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, ErrPublicKeyParseFailed
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, ErrPublicKeyParseFailed
	}
	ed, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, ErrPublicKeyParseFailed
	}
	return ed, nil
}

// Claims is the JWT claims payload. Registered claims are validated by the
// jwt/v5 parser; Verify additionally requires sub, jti, role, token_version >= 1
// and that all registered claims (iss, aud, exp, nbf, iat) are present with
// correct types. MapClaims are not used for extraction — a custom struct with
// typed fields prevents zero-value silent pass-through.
type Claims struct {
	jwtv5.RegisteredClaims
	Role         string `json:"role"`
	TokenVersion int    `json:"token_version"`
}

// Issuer signs access tokens with the Ed25519 private key.
type Issuer struct {
	private   ed25519.PrivateKey
	issuer    string
	audience  string
	accessTTL time.Duration
}

// NewIssuer builds an Issuer. accessTTL must be > 0.
func NewIssuer(kp *KeyPair, issuer, audience string, accessTTL time.Duration) (*Issuer, error) {
	if kp == nil || kp.Private == nil {
		return nil, errors.New("jwt: private key is required")
	}
	if issuer == "" {
		return nil, errors.New("jwt: issuer is required")
	}
	if audience == "" {
		return nil, errors.New("jwt: audience is required")
	}
	if accessTTL <= 0 {
		return nil, errors.New("jwt: access token TTL must be > 0")
	}
	return &Issuer{private: kp.Private, issuer: issuer, audience: audience, accessTTL: accessTTL}, nil
}

// IssueAccessToken builds and signs a new access token for the given user.
// now is injected for deterministic tests.
func (i *Issuer) IssueAccessToken(userID, role string, tokenVersion int, now time.Time) (string, time.Time, error) {
	exp := now.Add(i.accessTTL)
	claims := &Claims{
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    i.issuer,
			Subject:   userID,
			Audience:  jwtv5.ClaimStrings{i.audience},
			ExpiresAt: jwtv5.NewNumericDate(exp),
			NotBefore: jwtv5.NewNumericDate(now),
			IssuedAt:  jwtv5.NewNumericDate(now),
			ID:        newJTI(),
		},
		Role:         role,
		TokenVersion: tokenVersion,
	}
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(i.private)
	if err != nil {
		return "", time.Time{}, errors.New("jwt: sign failed")
	}
	return signed, exp, nil
}

// Verifier validates access tokens and returns the parsed claims. It checks
// the EdDSA signature, iss, aud, exp and nbf. It does NOT check the user's
// current status or token_version — the auth middleware does that against the
// DB on every request.
type Verifier struct {
	public   ed25519.PublicKey
	issuer   string
	audience string
}

// NewVerifier builds a Verifier from the public key alone. Consumers that only
// validate tokens never need the private key.
func NewVerifier(kp *KeyPair, issuer, audience string) (*Verifier, error) {
	if kp == nil || kp.Public == nil {
		return nil, errors.New("jwt: public key is required")
	}
	if issuer == "" {
		return nil, errors.New("jwt: issuer is required")
	}
	if audience == "" {
		return nil, errors.New("jwt: audience is required")
	}
	return &Verifier{public: kp.Public, issuer: issuer, audience: audience}, nil
}

// Verify parses and validates the token. A returned error is a stable
// sentinel; it never contains the token string or signing error details.
var (
	ErrInvalidToken = errors.New("jwt: invalid token")
	ErrExpired      = errors.New("jwt: token expired")
)

func (v *Verifier) Verify(raw string) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwtv5.ParseWithClaims(raw, claims, func(t *jwtv5.Token) (any, error) {
		if _, ok := t.Method.(*jwtv5.SigningMethodEd25519); !ok {
			return nil, ErrInvalidToken // alg confusion rejected
		}
		return v.public, nil
	}, jwtv5.WithIssuer(v.issuer), jwtv5.WithAudience(v.audience), jwtv5.WithValidMethods([]string{"EdDSA"}))
	if err != nil {
		if errors.Is(err, jwtv5.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, ErrInvalidToken
	}
	if !parsed.Valid {
		return nil, ErrInvalidToken
	}
	// Strict claim validation: sub, jti, role, token_version must be present
	// and token_version >= 1. MapClaims could silently zero these; the typed
	// struct makes missing claims obvious.
	if claims.RegisteredClaims.Subject == "" {
		return nil, ErrInvalidToken
	}
	if claims.RegisteredClaims.ID == "" {
		return nil, ErrInvalidToken
	}
	if claims.Role == "" {
		return nil, ErrInvalidToken
	}
	if claims.TokenVersion < 1 {
		return nil, ErrInvalidToken
	}
	// Registered claims existence checks (the parser validates iss/aud/exp/nbf
	// but we also confirm iat is present and typed correctly).
	if claims.RegisteredClaims.IssuedAt == nil {
		return nil, ErrInvalidToken
	}
	if claims.RegisteredClaims.ExpiresAt == nil {
		return nil, ErrInvalidToken
	}
	if claims.RegisteredClaims.NotBefore == nil {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
