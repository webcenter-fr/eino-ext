package approval

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

	"emperror.dev/errors"
)

// Client errors. Callers must fail closed on every one of them.
var (
	ErrNotFound           = errors.New("approval not found")
	ErrUnauthorized       = errors.New("approval service rejected the server token")
	ErrForbidden          = errors.New("approval service forbade the request")
	ErrBadRequest         = errors.New("approval service rejected the request")
	ErrHashMismatch       = errors.New("approval args hash mismatch")
	ErrDenied             = errors.New("approval was rejected")
	ErrExpired            = errors.New("approval expired")
	ErrTimeout            = errors.New("approval decision timed out")
	ErrServiceUnavailable = errors.New("approval service unavailable")
	ErrReceiptInvalid     = errors.New("approval receipt invalid")
)

// DefaultPollInterval is the delay between long-poll slices.
const DefaultPollInterval = 250 * time.Millisecond

// MaxPollSlice is the maximum server-side long-poll hold.
const MaxPollSlice = 30 * time.Second

// Config configures the approval REST client.
type Config struct {
	BaseURL        string
	ServerName     string
	ServerToken    string
	HTTPClient     *http.Client
	PollInterval   time.Duration
	RequestTimeout time.Duration
	Receipt        ReceiptConfig
}

// Client is the MCP-server-side approval client.
type Client struct {
	baseURL        string
	serverName     string
	serverToken    string
	httpClient     *http.Client
	pollInterval   time.Duration
	requestTimeout time.Duration
	signer         *ReceiptSigner
}

// NewClient builds a client. The receipt signer is optional for registration
// but required for VerifyReceipt.
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("approval client: base URL is required")
	}
	if cfg.ServerName == "" {
		return nil, errors.New("approval client: server name is required")
	}
	if cfg.ServerToken == "" {
		return nil, errors.New("approval client: server token is required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 35 * time.Second}
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 35 * time.Second
	}
	c := &Client{
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		serverName:     cfg.ServerName,
		serverToken:    cfg.ServerToken,
		httpClient:     httpClient,
		pollInterval:   pollInterval,
		requestTimeout: requestTimeout,
	}
	if len(cfg.Receipt.Secret) > 0 || len(cfg.Receipt.PrivateKeyPEM) > 0 {
		signer, err := NewReceiptSigner(cfg.Receipt)
		if err != nil {
			return nil, err
		}
		c.signer = signer
	}
	return c, nil
}

// RegisterRequest is the POST /api/v1/approvals body.
type RegisterRequest struct {
	ServerName     string          `json:"serverName"`
	ToolName       string          `json:"toolName"`
	Instance       string          `json:"instance,omitempty"`
	Operation      string          `json:"operation,omitempty"`
	Args           json.RawMessage `json:"args"`
	ArgsHash       string          `json:"argsHash"`
	Preview        string          `json:"preview,omitempty"`
	PreviewWarning string          `json:"previewWarning,omitempty"`
	Requester      Requester       `json:"requester"`
	TTL            string          `json:"ttl,omitempty"`
}

// Requester identifies the caller.
type Requester struct {
	Sub    string   `json:"sub"`
	User   string   `json:"user,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// RegisterResponse is the approval registration result.
type RegisterResponse struct {
	ApprovalID string    `json:"approvalId"`
	URL        string    `json:"url"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// Decision is the long-poll result.
type Decision struct {
	State        string    `json:"state"`
	Receipt      string    `json:"receipt,omitempty"`
	ApprovalID   string    `json:"approvalId,omitempty"`
	ServerName   string    `json:"serverName,omitempty"`
	ToolName     string    `json:"toolName,omitempty"`
	Instance     string    `json:"instance,omitempty"`
	ArgsHash     string    `json:"argsHash,omitempty"`
	RequesterSub string    `json:"requesterSub,omitempty"`
	ApproverSub  string    `json:"approverSub,omitempty"`
	Decision     string    `json:"decision,omitempty"`
	Policy       string    `json:"policy,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

// Register creates an approval request.
func (c *Client) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	if req.ServerName == "" {
		req.ServerName = c.serverName
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to encode approval registration")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/approvals", bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "failed to build approval registration request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.serverToken)
	var out RegisterResponse
	if err := c.do(httpReq, &out); err != nil {
		return nil, err
	}
	if out.ApprovalID == "" {
		return nil, errors.New("approval service returned an empty approval id")
	}
	return &out, nil
}

// Decision performs one long-poll slice.
func (c *Client) Decision(ctx context.Context, id string, wait time.Duration) (*Decision, error) {
	if wait > MaxPollSlice {
		wait = MaxPollSlice
	}
	endpoint := fmt.Sprintf("%s/api/v1/approvals/%s/decision", c.baseURL, url.PathEscape(id))
	if wait > 0 {
		endpoint += "?wait=" + url.QueryEscape(wait.String())
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build approval decision request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.serverToken)
	var out Decision
	if err := c.do(httpReq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WaitDecision long-polls in slices until the decision is final, the timeout
// elapses, or ctx is cancelled. A timeout returns ErrTimeout while the record
// may still be pending server-side (a later approvalId re-call can succeed).
func (c *Client) WaitDecision(ctx context.Context, id string, timeout time.Duration) (*Decision, error) {
	if timeout <= 0 {
		return c.Decision(ctx, id, 0)
	}
	deadline := time.Now().Add(timeout)
	backoff := c.pollInterval
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrTimeout
		}
		slice := remaining
		if slice > MaxPollSlice {
			slice = MaxPollSlice
		}
		d, err := c.Decision(ctx, id, slice)
		if err != nil {
			if errors.Is(err, ErrServiceUnavailable) {
				if err := sleepCtx(ctx, backoff); err != nil {
					return nil, err
				}
				backoff *= 2
				if backoff > 5*time.Second {
					backoff = 5 * time.Second
				}
				continue
			}
			return nil, err
		}
		switch d.State {
		case "approved":
			return d, nil
		case "rejected":
			return d, ErrDenied
		case "expired":
			return d, ErrExpired
		case "pending":
			backoff = c.pollInterval
			continue
		default:
			return nil, errors.Errorf("approval service returned unknown state %q", d.State)
		}
	}
}

// Expected binds a receipt to the call about to execute.
type Expected struct {
	ServerName string
	ToolName   string
	Instance   string
	ArgsHash   string
}

// VerifyReceipt verifies the signature, expiry, decision and call binding.
func (c *Client) VerifyReceipt(token string, expected Expected) (*ReceiptClaims, error) {
	if c.signer == nil {
		return nil, errors.Wrap(ErrReceiptInvalid, "no receipt key configured")
	}
	claims, err := c.signer.Verify(token)
	if err != nil {
		return nil, errors.Wrap(ErrReceiptInvalid, err.Error())
	}
	if claims.Decision != "approve" {
		return nil, errors.Wrapf(ErrReceiptInvalid, "receipt decision is %q", claims.Decision)
	}
	if claims.ServerName != expected.ServerName {
		return nil, errors.Wrapf(ErrReceiptInvalid, "receipt server %q does not match %q", claims.ServerName, expected.ServerName)
	}
	if claims.ToolName != expected.ToolName {
		return nil, errors.Wrapf(ErrReceiptInvalid, "receipt tool %q does not match %q", claims.ToolName, expected.ToolName)
	}
	if claims.Instance != expected.Instance {
		return nil, errors.Wrapf(ErrReceiptInvalid, "receipt instance %q does not match %q", claims.Instance, expected.Instance)
	}
	if claims.ArgsHash != expected.ArgsHash {
		return nil, errors.Wrap(ErrHashMismatch, "receipt args hash does not match the args about to execute")
	}
	return claims, nil
}

func (c *Client) do(req *http.Request, out any) error {
	ctx := req.Context()
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(ErrServiceUnavailable, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return errors.Wrap(ErrServiceUnavailable, "failed to read approval service response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.Wrap(err, "failed to decode approval service response")
	}
	return nil
}

func statusError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	switch status {
	case http.StatusNotFound:
		return errors.Wrap(ErrNotFound, message)
	case http.StatusUnauthorized:
		return errors.Wrap(ErrUnauthorized, message)
	case http.StatusForbidden:
		return errors.Wrap(ErrForbidden, message)
	case http.StatusBadRequest:
		return errors.Wrap(ErrBadRequest, message)
	case http.StatusConflict:
		return errors.Wrap(ErrBadRequest, message)
	default:
		if status >= 500 {
			return errors.Wrapf(ErrServiceUnavailable, "status %d: %s", status, message)
		}
		return errors.Errorf("approval service returned status %d: %s", status, message)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
