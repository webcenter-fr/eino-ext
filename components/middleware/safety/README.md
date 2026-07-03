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
| `CheckOwnership` | Reserved for future controller ownership checks. |

## Audit event structure

Each `AuditEvent` records: `Timestamp`, `ToolName`, `CallID`, `Phase`
(read/dry-run/execute/rejected), `Operation`, `Arguments`, `Result`, `Error`,
`PolicyPass`, and custom `Metadata`.
