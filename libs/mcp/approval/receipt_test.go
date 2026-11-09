package approval

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClaims() ReceiptClaims {
	return ReceiptClaims{
		ApprovalID:   "approval-1",
		ServerName:   "grafana",
		ToolName:     "grafana_dashboard_write",
		Instance:     "prod",
		RequesterSub: "alice",
		ApproverSub:  "bob",
		ArgsHash:     "abc123",
		Decision:     "approve",
		Policy:       "four-eyes",
	}
}

func TestReceiptHS256(t *testing.T) {
	signer, err := NewReceiptSigner(ReceiptConfig{Secret: []byte("secret")})
	require.NoError(t, err)
	token, issued, err := signer.Issue(testClaims())
	require.NoError(t, err)
	require.NotEmpty(t, token)
	assert.NotEmpty(t, issued.ID)

	claims, err := signer.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "approval-1", claims.ApprovalID)
	assert.Equal(t, "approve", claims.Decision)

	// Reuse within exp is allowed.
	_, err = signer.Verify(token)
	assert.NoError(t, err)
}

func TestReceiptRS256(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	signer, err := NewReceiptSigner(ReceiptConfig{PrivateKeyPEM: pemBytes, Algorithm: AlgorithmRS256})
	require.NoError(t, err)
	token, _, err := signer.Issue(testClaims())
	require.NoError(t, err)
	_, err = signer.Verify(token)
	assert.NoError(t, err)
}

func TestReceiptEdDSA(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	signer, err := NewReceiptSigner(ReceiptConfig{PrivateKeyPEM: pemBytes, Algorithm: AlgorithmEdDSA})
	require.NoError(t, err)
	token, _, err := signer.Issue(testClaims())
	require.NoError(t, err)
	_, err = signer.Verify(token)
	assert.NoError(t, err)
}

func TestReceiptRejectsBadSignature(t *testing.T) {
	signer, err := NewReceiptSigner(ReceiptConfig{Secret: []byte("secret")})
	require.NoError(t, err)
	token, _, err := signer.Issue(testClaims())
	require.NoError(t, err)

	other, err := NewReceiptSigner(ReceiptConfig{Secret: []byte("other")})
	require.NoError(t, err)
	_, err = other.Verify(token)
	assert.Error(t, err)
}

func TestReceiptRejectsAlgNone(t *testing.T) {
	signer, err := NewReceiptSigner(ReceiptConfig{Secret: []byte("secret")})
	require.NoError(t, err)
	claims := testClaims()
	claims.IssuedAt = jwt.NewNumericDate(time.Now())
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Minute))
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	unsigned, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	_, err = signer.Verify(unsigned)
	assert.Error(t, err)
}

func TestReceiptRejectsExpired(t *testing.T) {
	signer, err := NewReceiptSigner(ReceiptConfig{Secret: []byte("secret")})
	require.NoError(t, err)
	claims := testClaims()
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	token, _, err := signer.Issue(claims)
	require.NoError(t, err)
	_, err = signer.Verify(token)
	assert.Error(t, err)
}

func TestVerifyReceiptBinding(t *testing.T) {
	signer, err := NewReceiptSigner(ReceiptConfig{Secret: []byte("secret")})
	require.NoError(t, err)
	token, _, err := signer.Issue(testClaims())
	require.NoError(t, err)

	client := &Client{signer: signer}
	expected := Expected{ServerName: "grafana", ToolName: "grafana_dashboard_write", Instance: "prod", ArgsHash: "abc123"}
	_, err = client.VerifyReceipt(token, expected)
	assert.NoError(t, err)

	expected.ArgsHash = "different"
	_, err = client.VerifyReceipt(token, expected)
	assert.ErrorIs(t, err, ErrHashMismatch)

	expected = Expected{ServerName: "kubernetes", ToolName: "grafana_dashboard_write", Instance: "prod", ArgsHash: "abc123"}
	_, err = client.VerifyReceipt(token, expected)
	assert.ErrorIs(t, err, ErrReceiptInvalid)
}

func TestVerifyReceiptRejectsNonApprove(t *testing.T) {
	signer, err := NewReceiptSigner(ReceiptConfig{Secret: []byte("secret")})
	require.NoError(t, err)
	claims := testClaims()
	claims.Decision = "reject"
	token, _, err := signer.Issue(claims)
	require.NoError(t, err)
	client := &Client{signer: signer}
	_, err = client.VerifyReceipt(token, Expected{ServerName: "grafana", ToolName: "grafana_dashboard_write", Instance: "prod", ArgsHash: "abc123"})
	assert.ErrorIs(t, err, ErrReceiptInvalid)
}
