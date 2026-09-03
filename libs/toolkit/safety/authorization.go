package safety

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
)

// ExecutionAuthorizer decides whether a write tool may execute for real
// (not dry-run). Implementations MUST derive their answer from server-side
// state — an approval record, a signed token, an operator policy — and MUST
// NOT trust anything carried in the tool arguments, which are model-controlled.
//
// It is invoked only for write tools whose GateParams already has
// Confirmed == true (the "execute" step of the dry-run/confirm flow).
type ExecutionAuthorizer interface {
	// AuthorizeExecute returns nil if the tool may execute, or an error
	// describing why it was denied. Implementations must be safe for
	// concurrent use and must not panic on a nil ctx or empty toolName.
	AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error
}

// ErrExecutionNotAuthorized is returned when a write tool attempts real
// execution without host-app authorization. It is the fail-closed sentinel:
// with no ExecutionAuthorizer configured, write tools may only dry-run.
var ErrExecutionNotAuthorized = errors.New(
	"SAFETY GATE: execution of this write tool was not authorized by the host application")

// executionAuthorizedKey is an unexported context key type to avoid collisions.
type executionAuthorizedKey struct{}

var executionAuthorizedCtxKey = executionAuthorizedKey{}

// WithExecutionAuthorized returns a copy of ctx marking toolName as authorized
// to execute for real. The safety middleware sets this automatically when a
// write tool is authorized; programmatic callers who own the authorization
// decision (tests, non-ADK hosts) can set it directly.
//
// A nil ctx or empty toolName is a no-op and returns ctx unchanged.
func WithExecutionAuthorized(ctx context.Context, toolName string) context.Context {
	if ctx == nil || toolName == "" {
		return ctx
	}
	authorized, _ := ctx.Value(executionAuthorizedCtxKey).(map[string]struct{})
	next := make(map[string]struct{}, len(authorized)+1)
	for k := range authorized {
		next[k] = struct{}{}
	}
	next[toolName] = struct{}{}
	return context.WithValue(ctx, executionAuthorizedCtxKey, next)
}

// ExecutionAuthorizedFor reports whether toolName was marked executable via
// WithExecutionAuthorized on ctx (or one of its parents). It fails closed:
// a nil ctx, an empty toolName, or the absence of a grant all return false.
func ExecutionAuthorizedFor(ctx context.Context, toolName string) bool {
	if ctx == nil || toolName == "" {
		return false
	}
	authorized, _ := ctx.Value(executionAuthorizedCtxKey).(map[string]struct{})
	_, ok := authorized[toolName]
	return ok
}
