package approval

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"time"

	"emperror.dev/errors"
	"github.com/golang-jwt/jwt/v5"
)

// Receipt algorithms.
const (
	AlgorithmHS256 = "HS256"
	AlgorithmRS256 = "RS256"
	AlgorithmEdDSA = "EdDSA"
)

// DefaultReceiptTTL is the short receipt validity window. Receipts are
// reusable within it; every use is audited with the jti.
const DefaultReceiptTTL = 2 * time.Minute

// ReceiptClaims is the signed approval receipt.
type ReceiptClaims struct {
	jwt.RegisteredClaims
	ApprovalID   string `json:"approvalId"`
	ServerName   string `json:"serverName"`
	ToolName     string `json:"toolName"`
	Instance     string `json:"instance,omitempty"`
	RequesterSub string `json:"requesterSub"`
	ApproverSub  string `json:"approverSub"`
	ArgsHash     string `json:"argsHash"`
	Decision     string `json:"decision"`
	Policy       string `json:"policy,omitempty"`
}

// ReceiptConfig configures receipt signing and verification.
type ReceiptConfig struct {
	// Secret is the HS256 shared secret.
	Secret []byte
	// PrivateKeyPEM is a PEM-encoded RSA or Ed25519 private key for RS256/EdDSA.
	PrivateKeyPEM []byte
	// KeyID is the JWT kid header.
	KeyID string
	// Algorithm is HS256 (default), RS256 or EdDSA.
	Algorithm string
	// TTL is the receipt validity. Default 2m.
	TTL time.Duration
}

// ReceiptSigner issues and verifies approval receipts.
type ReceiptSigner struct {
	algorithm string
	keyID     string
	ttl       time.Duration
	signKey   any
	verifyKey any
}

// NewReceiptSigner builds a signer from the config.
func NewReceiptSigner(cfg ReceiptConfig) (*ReceiptSigner, error) {
	algorithm := cfg.Algorithm
	if algorithm == "" {
		algorithm = AlgorithmHS256
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultReceiptTTL
	}
	s := &ReceiptSigner{algorithm: algorithm, keyID: cfg.KeyID, ttl: ttl}
	switch algorithm {
	case AlgorithmHS256:
		if len(cfg.Secret) == 0 {
			return nil, errors.New("approval receipt: HS256 requires a secret")
		}
		s.signKey = cfg.Secret
		s.verifyKey = cfg.Secret
	case AlgorithmRS256:
		key, err := parseRSAPrivateKey(cfg.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
		s.signKey = key
		s.verifyKey = &key.PublicKey
	case AlgorithmEdDSA:
		key, err := parseEd25519PrivateKey(cfg.PrivateKeyPEM)
		if err != nil {
			return nil, err
		}
		s.signKey = key
		s.verifyKey = key.Public()
	default:
		return nil, errors.Errorf("approval receipt: unsupported algorithm %q", algorithm)
	}
	return s, nil
}

// Issue signs the claims, filling iat, exp and jti when absent.
func (s *ReceiptSigner) Issue(claims ReceiptClaims) (string, *ReceiptClaims, error) {
	now := time.Now()
	if claims.IssuedAt == nil {
		claims.IssuedAt = jwt.NewNumericDate(now)
	}
	if claims.ExpiresAt == nil {
		claims.ExpiresAt = jwt.NewNumericDate(now.Add(s.ttl))
	}
	if claims.ID == "" {
		jti, err := randomID()
		if err != nil {
			return "", nil, err
		}
		claims.ID = jti
	}
	token := jwt.NewWithClaims(s.method(), claims)
	if s.keyID != "" {
		token.Header["kid"] = s.keyID
	}
	signed, err := token.SignedString(s.signKey)
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to sign approval receipt")
	}
	return signed, &claims, nil
}

// Verify checks the signature, algorithm, exp and iat, and returns the claims.
// It does not check the decision or the call binding; callers must do that.
func (s *ReceiptSigner) Verify(token string) (*ReceiptClaims, error) {
	claims := &ReceiptClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != s.algorithm {
			return nil, errors.Errorf("unexpected receipt algorithm %q", t.Method.Alg())
		}
		return s.verifyKey, nil
	}, jwt.WithValidMethods([]string{s.algorithm}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return nil, errors.Wrap(err, "invalid approval receipt")
	}
	if !parsed.Valid {
		return nil, errors.New("invalid approval receipt")
	}
	return claims, nil
}

func (s *ReceiptSigner) method() jwt.SigningMethod {
	switch s.algorithm {
	case AlgorithmRS256:
		return jwt.SigningMethodRS256
	case AlgorithmEdDSA:
		return jwt.SigningMethodEdDSA
	default:
		return jwt.SigningMethodHS256
	}
}

func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("approval receipt: invalid RSA private key PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "approval receipt: failed to parse RSA private key")
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("approval receipt: PEM does not contain an RSA private key")
	}
	return rsaKey, nil
}

func parseEd25519PrivateKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("approval receipt: invalid Ed25519 private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "approval receipt: failed to parse Ed25519 private key")
	}
	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("approval receipt: PEM does not contain an Ed25519 private key")
	}
	return edKey, nil
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "failed to generate receipt id")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
