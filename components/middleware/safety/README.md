# safety

An [eino adk](https://github.com/cloudwego/eino) `ChatModelAgentMiddleware` that
enforces a safety control layer around LLM tool invocations: audit trails, CEL-based
policy evaluation, and gate logic (dry-run/confirmed).

## How it works

Every tool call passes through three layers:

1. **Audit** — all tool calls are recorded via an `AuditSink` (default
   `LogSink`).
2. **Policy** — all tool calls are evaluated against CEL expressions; first
   failure rejects the call.
3. **Gate** — write tools (those listed in `WriteToolNames`) must first be called
   with `dryRun=true`, then re-called with `confirmed=true`. Read-only tools skip
   the gate.
4. **Authorization** — real execution of a write tool requires the host
   application to authorize it via an `ExecutionAuthorizer`. **The default is
   fail-closed**: with no authorizer configured, write tools may only dry-run,
   and a `confirmed:true` call is rejected with
   `safety.ErrExecutionNotAuthorized`.

The middleware wraps all four tool call hook types (`WrapInvokableToolCall`,
`WrapStreamableToolCall`, `WrapEnhancedInvokableToolCall`,
`WrapEnhancedStreamableToolCall`) so safety controls apply uniformly. Model calls
pass through unchanged.

## Usage

```go
import (
    "github.com/cloudwego/eino/adk"

    "github.com/webcenter-fr/eino-ext/components/middleware/safety"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

policy := safety.NewCELPolicy([]safety.CELRule{
    {Name: "no-prod-delete", Expression: `params.namespace != 'production'`,
     ToolNames: []string{"kubernetes_resource_delete"}},
})

mw, err := safety.New(&safety.Config{
    WriteToolNames: []string{"kubernetes_resource_delete", "kubernetes_resource_create"},
    Policy:         policy,
})

agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Name:     "operator",
    Model:    m,
    Handlers: []adk.ChatModelAgentMiddleware{mw},
})
```

## Configuration

| Field | Description |
|---|---|
| `WriteToolNames` | Tool names that require the dry-run/confirmed gate. |
| `AuditSink` | Where to write audit events (default: log via logrus). |
| `Policy` | CEL-based or custom policy evaluator. |
| `ExecutionAuthorizer` | Gates real execution of write tools. When nil, write tools may only dry-run. |
| `AllowModelConfirmation` | INSECURE escape hatch that trusts model-supplied `confirmed=true` (tests/sandboxes only). |
| `CheckOwnership` | Reserved for future controller ownership checks. |

> **Fail-closed by default.** The model cannot write to `context.Context`, so
> prompt injection cannot fabricate authorization.
> `AllowModelConfirmation: true` restores the pre-hardening (insecure)
> behavior and must not be used in production.

Implementing a host `ExecutionAuthorizer`:

```go
type approvalStoreAuthorizer struct{ approvals *store.Approvals }

func (a *approvalStoreAuthorizer) AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error {
    if a.approvals.HasPendingApproval(ctx, toolName, args) {
        return nil
    }
    return fmt.Errorf("no approval recorded for %q", toolName)
}

mw, err := safety.New(&safety.Config{
    WriteToolNames:      kubernetes.WriteToolNames(),
    ExecutionAuthorizer: &approvalStoreAuthorizer{approvals: store.Approvals},
})
```

## Audit event structure

Each `AuditEvent` records: `Timestamp`, `ToolName`, `CallID`, `Phase`
(read/dry-run/execute/rejected), `Operation`, `Arguments`, `Result`, `Error`,
`PolicyPass`, and custom `Metadata`.
