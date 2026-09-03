// Package safety provides an adk.ChatModelAgentMiddleware that enforces a safety
// control layer around LLM tool invocations. It combines audit trails, CEL-based
// policy evaluation, gate logic (dry-run/confirmed), and optional ownership
// detection for Kubernetes resources.
//
// The middleware applies to all tools (read + write):
//   - Audit: ALL tool calls are recorded.
//   - Policy: ALL tool calls are evaluated against CEL expressions.
//
// The gate (dry-run/confirmed) applies only to write tools:
//   - Write tools must first be called with dryRun=true to preview the result.
//   - Then they must be re-called with confirmed=true after user approval.
//   - Calling a write tool without dryRun or confirmed is rejected.
//   - Real execution additionally requires host-app authorization (an
//     ExecutionAuthorizer); with no authorizer configured, write tools may
//     only dry-run (fail closed).
package safety

import (
	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// Config configures the safety middleware.
type Config struct {
	// WriteToolNames lists tool names that perform write/mutative operations.
	// Read-only tools not in this list skip the gate (dry-run/confirmed) but are
	// still audited and subject to policy evaluation.
	WriteToolNames []string `json:"writeToolNames" jsonschema:"description=Tool names that perform write/mutative operations"`

	// AuditSink receives audit events for EVERY tool call (read + write). Required.
	// If nil, defaults to LogSink (structured logging).
	AuditSink safety.AuditSink `json:"-"`

	// Policy is evaluated before execution for ALL tool calls (read + write). Optional.
	// If nil, all tool calls are allowed (policy pass-through).
	Policy safety.Policy `json:"-"`

	// ExecutionAuthorizer gates real execution of write tools. When nil, write
	// tools may only dry-run: a confirmed=true call is rejected with
	// safety.ErrExecutionNotAuthorized. Implementations must derive the decision
	// from server-side state and must not trust model-supplied arguments.
	ExecutionAuthorizer safety.ExecutionAuthorizer `json:"-"`

	// AllowModelConfirmation restores the pre-hardening behavior where a
	// model-supplied confirmed=true is sufficient to execute a write tool.
	// INSECURE: only for tests and non-production sandboxes.
	AllowModelConfirmation bool `json:"allowModelConfirmation" jsonschema:"description=Insecure escape hatch that trusts model-supplied confirmed=true for write tools (tests/sandboxes only)"`

	// CheckOwnership enables controller/operator ownership detection for K8s tools.
	// Only applies to write operations on existing resources (update/delete).
	// Note: ownership detection runs in the tool itself, not in middleware.
	// This flag is reserved for future middleware-level ownership checks.
	CheckOwnership bool `json:"checkOwnership" jsonschema:"description=Enable controller/operator ownership detection for K8s resources"`
}
