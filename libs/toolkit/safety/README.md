# safety — Shared safety primitives for tool invocations

`safety` provides the foundational types and building blocks for constructing a
safety control layer around LLM tool invocations: audit trails, CEL-based policy
evaluation, gate logic, and controller ownership detection.

## Audit

```go
type AuditEvent struct {
    Timestamp  time.Time
    ToolName   string
    CallID     string
    Phase      Phase
    Operation  OperationType
    Arguments  string
    Result     string
    Error      string
    PolicyPass bool
    Metadata   map[string]string
}

type AuditSink interface {
    Write(ctx context.Context, event AuditEvent) error
}
```

Built-in sinks:
- `LogSink` — logs via logrus.
- `ChannelSink` — non-blocking channel-based sink with configurable buffer.

## Policy

```go
type Policy interface {
    Evaluate(ctx context.Context, toolName string, params string) error
}
```

CEL-based policy evaluation:

```go
rules := []CELRule{
    {Name: "no-prod-delete", Expression: `params.namespace != 'production'`},
}
p := NewCELPolicy(rules)
err := p.Evaluate(ctx, "kubernetes_resource_delete", paramsJSON)
```

- `CELRule.Name` — identifier for error messages.
- `CELRule.Expression` — CEL expression evaluated against `params` (JSON string
  → `map[string]interface{}`).
- `CELRule.ToolNames` — optional filter; `nil` applies to all tools.
- `PolicyChain` — evaluates multiple policies in order; first failure stops.

## Gate

```go
func ExtractGateParams(rawJSON string) (GateParams, error)
func ShouldGateWithAuthorization(ctx context.Context, toolName string, writeTools map[string]bool, gp GateParams, args json.RawMessage, auth ExecutionAuthorizer) error
```

The gate pattern requires write tools to go through a two-step confirmation:
1. First call with `dryRun=true` — tool returns what would be done.
2. Second call with `confirmed=true` — tool executes, but only if the host
   application authorized it (see [Authorization](#authorization)).

Read tools and non-write operations skip the gate. The `ErrGateRequired`
sentinel error signals the LLM to retry with the proper gate parameters.

> `ShouldGate` is deprecated: it trusts the model-supplied `Confirmed` field
> and MUST NOT be used as an authorization boundary.

## Authorization

Real execution of a write tool requires host-app authorization carried in the
`context.Context` — never the model-supplied `confirmed:true` argument, which
prompt injection can fabricate.

```go
type ExecutionAuthorizer interface {
    AuthorizeExecute(ctx context.Context, toolName string, args json.RawMessage) error
}
```

- `ErrExecutionNotAuthorized` — fail-closed sentinel: with no
  `ExecutionAuthorizer` configured, write tools may only dry-run.
- `WithExecutionAuthorized(ctx, toolName)` — returns a copy of ctx marking
  toolName as authorized to execute. The safety middleware sets this
  automatically; programmatic callers who own the authorization decision
  (tests, non-ADK hosts) can set it directly.
- `ExecutionAuthorizedFor(ctx, toolName)` — reports whether toolName was
  marked executable. Fails closed: nil ctx, empty toolName, or no grant all
  return false.

`ShouldGateWithAuthorization` rules, in order:

1. Read-only tool (not in `writeTools`) → allow.
2. `DryRun` → allow (previews are safe).
3. Not `Confirmed` → `ErrGateRequired`.
4. `Confirmed` and `auth == nil` → `ErrExecutionNotAuthorized`.
5. `Confirmed` and the authorizer denies → the authorizer's error, wrapped
   with the tool name (`errors.Is`/`errors.As` preserved through the chain).
6. Otherwise → allow.

## Ownership

```go
func CheckOwnership(obj runtime.Object) OwnershipInfo
```

Detects managed-by annotations (ArgoCD, Helm, Flux, kubectl) and controller
owner references. Returns `IsManaged`, `ManagedBy`, `ControllerName`, and any
`Warnings`.

## Operation Types

| Operation | Description |
|---|---|
| `OpCreate` | Resource creation |
| `OpUpdate` | Resource update |
| `OpDelete` | Resource deletion |
| `OpSync` | Sync operation |
| `OpExec` | Command execution |

## Phases

| Phase | Description |
|---|---|
| `PhaseRead` | Read-only tool call |
| `PhaseDryRun` | Write tool dry-run preview |
| `PhaseExecute` | Write tool confirmed execution |
| `PhaseRejected` | Call rejected by policy or gate |
