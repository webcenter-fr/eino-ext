package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	emperrors "emperror.dev/errors"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpapproval "github.com/webcenter-fr/eino-ext/libs/mcp/approval"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// PreviewFunc runs the tool's own dry-run preview. It is best-effort: a
// failure is surfaced to the approver as a warning, never as an approval.
type PreviewFunc func(ctx context.Context, args json.RawMessage) (string, error)

// ApprovalURLAuthorizer implements safety.ExecutionAuthorizer by delegating
// the decision to the standalone approval service and a human browser session.
// It fails closed on every error: an unreachable service, a bad receipt, a
// hash mismatch, a decline, or a timeout all deny execution. It never
// downgrades to elicitation.
type ApprovalURLAuthorizer struct {
	client      *mcpapproval.Client
	serverName  string
	session     *mcpsdk.ServerSession
	identity    *Identity
	instance    string
	approvalID  string
	timeout     time.Duration
	ttl         time.Duration
	preview     PreviewFunc
	lastReceipt *mcpapproval.ReceiptClaims
	// onRequested is invoked (when non-nil) after a new approval is registered,
	// so the caller can emit a PhaseApprovalRequested audit event.
	onRequested func(approvalID string)
	// expired is set when the decision wait ends in expiry or timeout, so the
	// caller can emit PhaseApprovalExpired instead of PhaseApprovalDenied.
	expired bool
}

// LastReceipt returns the verified receipt of the last successful
// authorization (nil when none).
func (a *ApprovalURLAuthorizer) LastReceipt() *mcpapproval.ReceiptClaims {
	return a.lastReceipt
}

// AuthorizeExecute implements safety.ExecutionAuthorizer.
func (a *ApprovalURLAuthorizer) AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error {
	if a.client == nil {
		return emperrors.Wrap(safety.ErrExecutionNotAuthorized, "approval service is not configured")
	}
	hash, err := mcpapproval.ArgsHash(args)
	if err != nil {
		return emperrors.Wrap(safety.ErrExecutionNotAuthorized, "cannot hash tool arguments")
	}
	if a.approvalID != "" {
		decision, err := a.client.WaitDecision(ctx, a.approvalID, a.timeout)
		if err != nil {
			a.markExpired(err)
			return emperrors.Wrapf(safety.ErrExecutionNotAuthorized, "approval %s was not granted: %v", a.approvalID, err)
		}
		return a.verify(decision, toolName, hash)
	}

	preview, warning := "", ""
	if a.preview != nil {
		preview, err = a.preview(ctx, args)
		if err != nil {
			preview = ""
			warning = err.Error()
		}
	}
	requester := mcpapproval.Requester{Sub: "anonymous"}
	if a.identity != nil {
		// The subject is the stable OIDC sub (Identity.Sub), not the display
		// name (Identity.User), so the approval service's anti-phishing
		// self-approve check compares like-for-like.
		requester.Sub = a.identity.Subject()
		requester.User = a.identity.User
		requester.Groups = append([]string(nil), a.identity.Groups...)
	}
	ttl := ""
	if a.ttl > 0 {
		ttl = a.ttl.String()
	}
	registered, err := a.client.Register(ctx, mcpapproval.RegisterRequest{
		ServerName:     a.serverName,
		ToolName:       toolName,
		Instance:       a.instance,
		Args:           args,
		ArgsHash:       hash,
		Preview:        preview,
		PreviewWarning: warning,
		Requester:      requester,
		TTL:            ttl,
	})
	if err != nil {
		return emperrors.Wrapf(safety.ErrExecutionNotAuthorized, "failed to register approval: %v", err)
	}
	if a.onRequested != nil {
		a.onRequested(registered.ApprovalID)
	}

	if a.session != nil && supportsURLMode(a.session) {
		res, err := a.session.Elicit(ctx, &mcpsdk.ElicitParams{
			Mode:          "url",
			Message:       fmt.Sprintf("Approve %s on %s in the browser", toolName, a.instance),
			URL:           registered.URL,
			ElicitationID: registered.ApprovalID,
		})
		if err != nil {
			return emperrors.Wrapf(safety.ErrExecutionNotAuthorized, "url elicitation failed: %v", err)
		}
		if res == nil || res.Action != "accept" {
			return emperrors.Wrapf(ErrApprovalDenied, "user action: %s", resAction(res))
		}
	} else {
		return emperrors.Wrapf(safety.ErrExecutionNotAuthorized,
			"approval required: open %s (approvalId %s) and re-call this tool with approvalId",
			registered.URL, registered.ApprovalID)
	}

	decision, err := a.client.WaitDecision(ctx, registered.ApprovalID, a.timeout)
	if err != nil {
		a.markExpired(err)
		return emperrors.Wrapf(safety.ErrExecutionNotAuthorized, "approval %s was not granted: %v", registered.ApprovalID, err)
	}
	return a.verify(decision, toolName, hash)
}

// markExpired records that the decision wait ended in expiry or timeout, so the
// caller can emit PhaseApprovalExpired rather than PhaseApprovalDenied.
func (a *ApprovalURLAuthorizer) markExpired(err error) {
	if emperrors.Is(err, mcpapproval.ErrExpired) || emperrors.Is(err, mcpapproval.ErrTimeout) {
		a.expired = true
	}
}

func (a *ApprovalURLAuthorizer) verify(decision *mcpapproval.Decision, toolName, hash string) error {
	if decision == nil || decision.State != "approved" {
		state := "unknown"
		if decision != nil {
			state = decision.State
		}
		return emperrors.Wrapf(safety.ErrExecutionNotAuthorized, "approval state is %q", state)
	}
	claims, err := a.client.VerifyReceipt(decision.Receipt, mcpapproval.Expected{
		ServerName: a.serverName,
		ToolName:   toolName,
		Instance:   a.instance,
		ArgsHash:   hash,
	})
	if err != nil {
		return emperrors.Wrapf(safety.ErrExecutionNotAuthorized, "approval receipt rejected: %v", err)
	}
	a.lastReceipt = claims
	return nil
}

func supportsURLMode(session *mcpsdk.ServerSession) bool {
	if session == nil {
		return false
	}
	// TODO: 2026-07-28 clients declare capabilities per-request in
	// _meta.io.modelcontextprotocol/clientCapabilities. go-sdk v1.6.0 exposes
	// only the initialize-time capabilities; revisit when per-request
	// capabilities are available.
	params := session.InitializeParams()
	return params != nil && params.Capabilities != nil &&
		params.Capabilities.Elicitation != nil && params.Capabilities.Elicitation.URL != nil
}

func newApprovalClient(cfg *ApprovalServiceConfig) (*mcpapproval.Client, error) {
	pollInterval, err := parseOptionalDuration(cfg.PollInterval, 0)
	if err != nil {
		return nil, emperrors.Wrap(err, "invalid approval service poll interval")
	}
	requestTimeout, err := parseOptionalDuration(cfg.RequestTimeout, 0)
	if err != nil {
		return nil, emperrors.Wrap(err, "invalid approval service request timeout")
	}
	return mcpapproval.NewClient(mcpapproval.Config{
		BaseURL:        cfg.URL,
		ServerName:     cfg.ServerName,
		ServerToken:    cfg.ServerToken,
		PollInterval:   pollInterval,
		RequestTimeout: requestTimeout,
		Receipt: mcpapproval.ReceiptConfig{
			Secret:        []byte(cfg.ReceiptSecret),
			PrivateKeyPEM: []byte(cfg.ReceiptPrivateKeyPEM),
			KeyID:         cfg.ReceiptKeyID,
			Algorithm:     cfg.ReceiptAlgorithm,
		},
	})
}

func parseOptionalDuration(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, emperrors.Errorf("duration %q must be positive", value)
	}
	return d, nil
}
