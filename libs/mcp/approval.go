package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	emperrors "emperror.dev/errors"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// maxApprovalArgsLen truncates the arguments shown in the approval message.
const maxApprovalArgsLen = 4000

// ErrApprovalDenied is returned when the human declines or cancels the approval.
var ErrApprovalDenied = emperrors.New("mcp: write execution was not approved by the user")

// ElicitationAuthorizer implements safety.ExecutionAuthorizer by asking the
// human (via the MCP client) to approve the write execution. It is the MCP
// equivalent of the host-app approval store: the decision comes from the
// elicitation response, never from the tool arguments.
//
// Fail closed: a nil session, a client without elicitation support, an
// elicitation error, a timeout, or a decline/cancel all deny execution.
type ElicitationAuthorizer struct {
	session  *mcpsdk.ServerSession
	identity *Identity
	instance string
	timeout  time.Duration
}

// AuthorizeExecute implements safety.ExecutionAuthorizer.
func (a *ElicitationAuthorizer) AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error {
	if a.session == nil {
		return emperrors.Wrap(safety.ErrExecutionNotAuthorized, "no MCP session available for elicitation")
	}
	if iparams := a.session.InitializeParams(); iparams == nil || iparams.Capabilities == nil ||
		iparams.Capabilities.Elicitation == nil {
		return emperrors.Wrap(safety.ErrExecutionNotAuthorized,
			"client does not support elicitation; write tools are limited to dry-run")
	}
	if a.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.timeout)
		defer cancel()
	}
	res, err := a.session.Elicit(ctx, &mcpsdk.ElicitParams{
		Message: buildApprovalMessage(a.identity, toolName, a.instance, args),
	})
	if err != nil {
		return emperrors.Wrap(safety.ErrExecutionNotAuthorized,
			"elicitation failed (client may not support elicitation); write tools are limited to dry-run")
	}
	if res.Action != "accept" {
		return emperrors.Wrapf(ErrApprovalDenied, "user action: %s", res.Action)
	}
	return nil
}

// buildApprovalMessage builds the human-readable approval prompt. It shows the
// ACTUAL arguments of the execution call (truncated), never a stale preview.
func buildApprovalMessage(id *Identity, toolName, instance string, args json.RawMessage) string {
	user := "anonymous"
	if id != nil && id.User != "" {
		user = id.User
	}
	argsStr := string(args)
	if len(argsStr) > maxApprovalArgsLen {
		argsStr = argsStr[:maxApprovalArgsLen] + "… (truncated)"
	}
	msg := fmt.Sprintf("Approve write operation?\n\nTool: %s\nUser: %s", toolName, user)
	if instance != "" {
		msg += fmt.Sprintf("\nTarget: %s", instance)
	}
	msg += fmt.Sprintf("\nArguments: %s", argsStr)
	return msg
}
