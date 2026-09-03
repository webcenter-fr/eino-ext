package safety

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
)

// GateParams represents the DryRun and Confirmed fields that every write tool
// params struct must include. The middleware extracts these from the raw JSON
// arguments before passing them to the tool.
type GateParams struct {
	DryRun    bool `json:"dryRun"`
	Confirmed bool `json:"confirmed"`
}

// ExtractGateParams extracts DryRun and Confirmed from a raw JSON params string.
// Unknown fields are ignored; missing boolean fields default to false.
func ExtractGateParams(rawJSON string) (GateParams, error) {
	var gp GateParams
	if err := json.Unmarshal([]byte(rawJSON), &gp); err != nil {
		return GateParams{}, errors.Wrap(err, "failed to parse gate params")
	}
	return gp, nil
}

// ShouldGate checks whether a tool call must be gated (require dry-run/confirmed flow).
//
// Deprecated: ShouldGate trusts the model-supplied Confirmed field and MUST NOT
// be used as an authorization boundary. Use ShouldGateWithAuthorization instead,
// which requires host-app authorization from context before real execution.
//
// Rules:
//   - Read-only tools (not in writeTools) always pass — no gate required.
//   - Write tools with DryRun=true always pass — dry-run is always allowed.
//   - Write tools with Confirmed=true pass — trusted unconditionally (INSECURE).
//   - Write tools with neither DryRun nor Confirmed are rejected.
//
// Returns nil if the call is allowed, or an error explaining the required flow.
func ShouldGate(toolName string, writeTools map[string]bool, gp GateParams) error {
	if !writeTools[toolName] {
		return nil // read-only tool, no gate required
	}
	if gp.DryRun {
		return nil // dry-run is always allowed for write tools
	}
	if !gp.Confirmed {
		return ErrGateRequired
	}
	return nil
}

// ShouldGateWithAuthorization checks whether a write tool call may proceed,
// authorizing real execution from the context instead of trusting the
// model-supplied Confirmed field.
//
// Rules, evaluated in order:
//  1. Read-only tool (not in writeTools) -> allow.
//  2. DryRun -> allow (previews are safe; see the WriteToolNames contract).
//  3. Not Confirmed -> ErrGateRequired (the model must dry-run first).
//  4. Confirmed and auth == nil -> ErrExecutionNotAuthorized (fail closed).
//  5. Confirmed and auth.AuthorizeExecute(...) returns non-nil -> that error,
//     wrapped with the tool name for context.
//  6. Otherwise -> allow.
//
// ctx and args are passed through to the authorizer untouched and are only
// dereferenced by the authorizer. writeTools is the set of write-tool names
// (the same map built by the middleware from Config.WriteToolNames).
func ShouldGateWithAuthorization(
	ctx context.Context, toolName string, writeTools map[string]bool,
	gp GateParams, args json.RawMessage, auth ExecutionAuthorizer,
) error {
	if !writeTools[toolName] {
		return nil // read-only tool, no gate required
	}
	if gp.DryRun {
		return nil // dry-run is always allowed
	}
	if !gp.Confirmed {
		return ErrGateRequired
	}
	// Real execution of a write tool: require host authorization.
	if auth == nil {
		return ErrExecutionNotAuthorized
	}
	if err := auth.AuthorizeExecute(ctx, toolName, args); err != nil {
		return errors.Wrapf(err, "execution of write tool %q was not authorized by the host authorizer", toolName)
	}
	return nil
}

// ErrGateRequired is returned when a write tool is called without dry-run or confirmation.
var ErrGateRequired = errors.New(
	"SAFETY GATE: This is a write operation. You must first call this tool with dryRun=true, show the result to the user, and then re-call with confirmed=true after user approval.",
)
