# Safety Control Layer — Mutative K8s/ArgoCD Tools

## Goal

Allow an LLM agent to create, modify, and delete Kubernetes and ArgoCD resources **without losing control**, via:
1. **Audit trail** with streaming support — applies to ALL tool calls (read + write)
2. **Human-in-the-loop** approval (dry-run + confirmation gate) — applies to write tools only
3. **CEL policy engine** for validating operations — applies to ALL tool calls (read + write)
4. **Controller/operator ownership detection** before modification — applies to write tools on existing resources

## Architecture Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Architecture layer | ADK Middleware | Non-invasive, wraps all tool calls transparently |
| Approval pattern | Gate middleware + native tool params (DryRun + Confirmed) | LLM naturally orchestrates the 2-step flow |
| Policy engine | CEL (Common Expression Language) | Standard K8s ecosystem, safe, no external service |
| Audit sink | Interface `AuditSink` + `ChannelSink` impl | Generic + streaming support |
| Controller detection | Standard heuristics (ownerReferences, annotations, labels) | Covers 90% of cases, no extra API calls |
| Scope | Generic, injectable by consumer | Library for other developers |

## CONTRIBUTING.md Compliance

**Package placement:**
- `libs/toolkit/safety/` — shared primitives (not tied to a specific eino abstraction) ✓
- `components/middleware/safety/` — ADK `ChatModelAgentMiddleware` implementation ✓

**Component standards:**
- `Config` struct with `validate` and `jsonschema` tags ✓
- `New...` constructor with validation ✓
- `emperror.dev/errors` for error wrapping ✓
- `github.com/go-playground/validator/v10` for validation ✓
- Tests alongside implementation ✓
- README.md per component ✓

**Audit scope:**
- ALL tool calls are audited (read + write) for complete traceability ✓
- Policies apply to ALL tool calls (can restrict reads too, e.g., block secret access) ✓
- Gate (dry-run/confirmed) applies only to write tools ✓

## Package Structure

Per CONTRIBUTING.md rules:
- `components/middleware/` — strictly for `adk.ChatModelAgentMiddleware` implementations
- `libs/toolkit/` — shared, non-component support libraries

```
libs/toolkit/safety/
├── audit.go          # AuditSink interface, AuditEvent, ChannelSink, LogSink
├── policy.go         # Policy interface, CELPolicy implementation
├── ownership.go      # Ownership detection heuristics
├── gate.go           # Gate logic (dry-run/confirmed enforcement, write tools only)
└── types.go          # Common types (OperationType, Phase, MutabilityLevel)

components/middleware/safety/
├── middleware.go      # ADK ChatModelAgentMiddleware implementation
├── config.go          # Middleware Config struct
└── middleware_test.go # Tests
```

## Detailed Design

### 1. `libs/toolkit/safety/types.go` — Common Types

```go
type OperationType string
const (
    OpCreate OperationType = "create"
    OpUpdate OperationType = "update"
    OpDelete OperationType = "delete"
    OpSync   OperationType = "sync"
    OpExec   OperationType = "exec"
)

type Phase string
const (
    PhaseRead     Phase = "read"       // Read-only tool call (list, describe, log)
    PhaseDryRun   Phase = "dry-run"    // Write tool dry-run simulation
    PhaseExecute  Phase = "execute"    // Write tool actual execution
    PhaseRejected Phase = "rejected"   // Denied by gate or policy
)

type MutabilityLevel string
const (
    MutabilityReadOnly MutabilityLevel = "readonly"
    MutabilityWrite    MutabilityLevel = "write"
)
```

### 2. `libs/toolkit/safety/audit.go` — Audit Trail

```go
// AuditEvent represents a single tool invocation audit record.
type AuditEvent struct {
    Timestamp  time.Time         `json:"timestamp"`
    ToolName   string            `json:"toolName"`
    CallID     string            `json:"callID"`
    Phase      Phase             `json:"phase"`
    Operation  OperationType     `json:"operation,omitempty"`
    Arguments  json.RawMessage   `json:"arguments"`
    Result     string            `json:"result,omitempty"`
    Error      string            `json:"error,omitempty"`
    PolicyPass bool              `json:"policyPass"`
    Metadata   map[string]string `json:"metadata,omitempty"`
}

// AuditSink is the interface consumers implement to receive audit events.
type AuditSink interface {
    Write(ctx context.Context, event AuditEvent) error
}

// AuditSinkFunc is a function adapter for AuditSink.
type AuditSinkFunc func(ctx context.Context, event AuditEvent) error
func (f AuditSinkFunc) Write(ctx context.Context, event AuditEvent) error { return f(ctx, event) }

// ChannelSink sends audit events to a buffered channel for streaming consumers.
type ChannelSink struct {
    ch chan AuditEvent
}
func NewChannelSink(bufferSize int) *ChannelSink { ... }
func (s *ChannelSink) Write(ctx context.Context, event AuditEvent) error { ... }
func (s *ChannelSink) Events() <-chan AuditEvent { return s.ch }
func (s *ChannelSink) Close() { close(s.ch) }

// LogSink writes audit events as structured logs (default implementation).
type LogSink struct { ... }
```

### 3. `libs/toolkit/safety/policy.go` — CEL Policy Engine

```go
// Policy evaluates whether a tool invocation is allowed.
type Policy interface {
    // Evaluate returns nil if allowed, or an error describing why it was denied.
    Evaluate(ctx context.Context, toolName string, params map[string]any) error
}

// CELPolicy evaluates CEL expressions against tool params.
// Each rule has a name, an expression (must return bool), and optional tool name filter.
type CELRule struct {
    Name       string   // Human-readable rule name
    Expression string   // CEL expression, receives `params` and `toolName` variables
    ToolNames  []string // If empty, applies to all tools
}

type CELPolicy struct { ... }
func NewCELPolicy(rules []CELRule) (*CELPolicy, error) { ... }
func (p *CELPolicy) Evaluate(ctx context.Context, toolName string, params map[string]any) error { ... }

// PolicyChain evaluates multiple policies in order. First failure stops.
type PolicyChain []Policy
func (c PolicyChain) Evaluate(ctx context.Context, toolName string, params map[string]any) error { ... }
```

**Example CEL rules (applies to both read and write tools):**
```go
rules := []CELRule{
    {
        Name:       "no-kube-system",
        Expression: `!(toolName.startsWith("kubernetes_") && params.namespace == "kube-system")`,
        // Applies to ALL kubernetes tools (read + write)
    },
    {
        Name:       "no-secret-read",
        Expression: `!(toolName == "kubernetes_secret_describe" || toolName == "kubernetes_secret_list")`,
        // Block reading secrets entirely
    },
    {
        Name:       "no-cascade-delete",
        Expression: `!(toolName == "argocd_application_delete" && params.cascade == true)`,
        // Write-only rule
    },
    {
        Name:       "require-project",
        Expression: `toolName != "argocd_application_create" || params.project != ""`,
        ToolNames:  []string{"argocd_application_create"},
        // Scoped to specific tool
    },
}
```

**Dependency**: `github.com/google/cel-go` (standard CEL implementation, used by Kubernetes itself).

### 4. `libs/toolkit/safety/ownership.go` — Controller/Operator Detection

```go
// OwnershipInfo describes what manages a Kubernetes resource.
type OwnershipInfo struct {
    IsManaged       bool     `json:"isManaged"`
    ManagedBy       string   `json:"managedBy,omitempty"`       // e.g., "argocd", "helm", "flux"
    ControllerName  string   `json:"controllerName,omitempty"`  // e.g., "deployment-controller"
    OwnerReferences []string `json:"ownerReferences,omitempty"` // Owner reference names
    Warnings        []string `json:"warnings,omitempty"`        // Human-readable warnings
}

// CheckOwnership inspects a Kubernetes object for controller/operator management.
// Checks: ownerReferences, managed-by annotations, ArgoCD/Flux/Helm annotations.
func CheckOwnership(obj metav1.Object) OwnershipInfo { ... }
```

**Heuristics checked:**
- `metadata.ownerReferences[]` — any owner reference present
- `metadata.annotations["argocd.argoproj.io/instance"]` — managed by ArgoCD
- `metadata.annotations["meta.helm.sh/release-name"]` — managed by Helm
- `metadata.labels["app.kubernetes.io/managed-by"]` — generic managed-by label
- `metadata.annotations["kustomize.toolkit.fluxcd.io/name"]` — managed by Flux
- `metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]` — kubectl apply (less strict warning)

### 5. `libs/toolkit/safety/gate.go` — Gate Logic (Write Tools Only)

```go
// GateParams are the fields the middleware expects in write tool params.
type GateParams struct {
    DryRun    bool `json:"dryRun"`
    Confirmed bool `json:"confirmed"`
}

// ExtractGateParams extracts DryRun and Confirmed from a raw JSON params object.
func ExtractGateParams(rawJSON string) (GateParams, error) { ... }

// ShouldGate checks if a write tool call must be gated (not dry-run, not confirmed).
// Returns nil for read-only tools (not in writeTools map).
// Returns error if write tool lacks proper confirmation.
func ShouldGate(toolName string, writeTools map[string]bool, gp GateParams) error {
    if !writeTools[toolName] {
        return nil // read-only tool, no gate required
    }
    if gp.DryRun {
        return nil // dry-run is always allowed for write tools
    }
    if !gp.Confirmed {
        return errors.New(
            "SAFETY GATE: This is a write operation. You must first call this tool with dryRun=true, " +
            "show the result to the user, and then re-call with confirmed=true after user approval.",
        )
    }
    return nil
}
```

**Note:** Gate logic applies only to write tools. Read-only tools always pass the gate but are still audited and subject to policy evaluation.

### 6. `components/middleware/safety/middleware.go` — ADK Middleware

```go
// Config configures the safety middleware.
type Config struct {
    // WriteToolNames lists tool names that perform write/mutative operations.
    // Read-only tools not in this list skip the gate (dry-run/confirmed) but are
    // still audited and subject to policy evaluation.
    WriteToolNames []string `json:"writeToolNames"`

    // AuditSink receives audit events for EVERY tool call (read + write). Required.
    // If nil, defaults to LogSink (structured logging).
    AuditSink safety.AuditSink `json:"-"`

    // Policy is evaluated before execution for ALL tool calls (read + write). Optional.
    // If nil, all tool calls are allowed (policy pass-through).
    Policy safety.Policy `json:"-"`

    // CheckOwnership enables controller/operator ownership detection for K8s tools.
    // Only applies to write operations on existing resources (update/delete).
    CheckOwnership bool `json:"checkOwnership"`
}

type Middleware struct {
    *adk.BaseChatModelAgentMiddleware
    cfg        Config
    writeTools map[string]bool
}

func New(cfg *Config) (*Middleware, error) { ... }
```

**WrapInvokableToolCall flow:**

```
1. Receive tool call (toolName from ToolContext, argumentsInJSON)
2. Parse argumentsInJSON into map[string]any
3. Policy evaluation: if policy denies → reject + audit(PhaseRejected) + return error
4. Determine if write tool: check writeTools[toolName]
5. If write tool:
   a. Extract GateParams (DryRun, Confirmed) from argumentsInJSON
   b. Gate check: if !DryRun + !Confirmed → reject with guidance message + audit(PhaseRejected)
   c. Call endpoint (actual tool execution)
   d. Audit: emit AuditEvent{Phase: DryRun or Execute, ...}
   e. If dry-run result → append guidance to response:
      "DRY-RUN RESULT: Show this to the user and ask for confirmation before re-calling with confirmed=true."
   f. Return result
6. If read-only tool:
   a. Call endpoint (actual tool execution)
   b. Audit: emit AuditEvent{Phase: Read, ...}
   c. Return result
```

**Key point:** Audit and policy apply to ALL tool calls (read + write). Gate (dry-run/confirmed) applies only to write tools.

**WrapStreamableToolCall** — same logic but wraps the stream reader to audit on completion.

### 7. Tool Modifications — DryRun + Confirmed Fields

Each existing write tool needs two new fields in its params struct:

```go
// Added to every write tool's params struct:
DryRun    bool `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the operation without making changes. Show the result to the user and ask for confirmation."`
Confirmed bool `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the write operation. Set this after the user has approved the dry-run result."`
```

**ArgoCD tools to modify:**
- `application_create.go` — DryRun: build the app object and return what would be created without calling Create API. Confirmed: gate actual Create call.
- `application_delete.go` — DryRun: fetch the app and return what would be deleted. Confirmed: gate actual Delete call.
- `application_sync.go` — Already has `DryRun` field! Just add `Confirmed` field.

**Kubernetes tools to modify:**
- `pod_exec.go` — Already has safety (blocklist). Add `Confirmed` field. DryRun doesn't apply to exec (already blocked by description).

**New Kubernetes write tools to create (future scope, not in this iteration):**
- `deployment_scale.go` — Scale deployment replicas
- `configmap_apply.go` — Create/update ConfigMap
- `namespace_create.go` / `namespace_delete.go`
- `resource_apply.go` — Generic apply (create/update) using dynamic client
- `resource_delete.go` — Generic delete using dynamic client

### 8. Registry Integration

The registries already separate `readOnlyConstructors` and `writeConstructors`. The middleware `Config.WriteToolNames` can be populated from the tool names of write constructors. Provide a helper:

```go
// In registry or helper:
func WriteToolNames(tools []tool.InvokableTool) ([]string, error) {
    // Extract tool names from write tools
}
```

Or better: provide a factory that wires the middleware automatically:

```go
func NewAllToolsWithSafety(ctx context.Context, configs Configs, safetyCfg *safety.Config) (
    []tool.InvokableTool, *safety.Middleware, error,
) {
    tools, _ := NewAllTools(ctx, configs)
    // Auto-populate writeToolNames from writeConstructors
    safetyCfg.WriteToolNames = extractWriteToolNames()
    mw, _ := safety.New(safetyCfg)
    return tools, mw, nil
}
```

## Execution Flow — End-to-End Example

### Scenario: LLM wants to create an ArgoCD application

```
Step 1: LLM calls argocd_application_create with {dryRun: true, ...params}
  → Middleware: gate check passes (DryRun=true)
  → Middleware: CEL policy evaluated on params → passes
  → Tool: builds app spec, returns JSON of what would be created
  → Middleware: appends "DRY-RUN: Show this to the user and ask for confirmation."
  → Middleware: emits AuditEvent{Phase: "dry-run", ToolName: "argocd_application_create"}

Step 2: LLM shows the dry-run result to user: "This will create application 'my-app' in project 'default'..."

Step 3: User says "Yes, go ahead"

Step 4: LLM calls argocd_application_create with {confirmed: true, dryRun: false, ...params}
  → Middleware: gate check passes (Confirmed=true)
  → Middleware: CEL policy evaluated → passes
  → Tool: actually creates the application via ArgoCD API
  → Middleware: emits AuditEvent{Phase: "execute", ToolName: "argocd_application_create"}
  → Result returned to LLM → LLM tells user "Application created successfully"
```

### Scenario: Policy denies the operation

```
Step 1: LLM calls kubernetes_deployment_create with {dryRun: true, namespace: "kube-system", ...}
  → Middleware: gate check passes (DryRun=true)
  → Middleware: CEL policy "no-kube-system" → DENIED
  → Middleware: emits AuditEvent{Phase: "rejected", PolicyPass: false}
  → Returns error: "Policy denied: namespace 'kube-system' is protected"
```

### Scenario: Ownership detection warns

```
Step 1: LLM calls kubernetes_deployment_update with {dryRun: true, name: "my-app", ...}
  → Middleware: gate passes, policy passes
  → Tool: fetches current deployment, CheckOwnership() finds ArgoCD annotation
  → Tool: returns dry-run result WITH warning: "⚠ This resource is managed by ArgoCD (instance: prod). Modifying it directly may cause drift."
  → LLM shows warning to user, user decides whether to proceed
```

### Scenario: Read-only tool with policy restriction

```
Step 1: LLM calls kubernetes_secret_describe with {namespace: "kube-system", name: "admin-token"}
  → Middleware: not a write tool, skip gate check
  → Middleware: CEL policy "no-kube-system" → DENIED
  → Middleware: emits AuditEvent{Phase: "rejected", PolicyPass: false}
  → Returns error: "Policy denied: reading secrets in namespace 'kube-system' is not allowed"
```

### Scenario: Read-only tool passes all checks

```
Step 1: LLM calls kubernetes_pod_list with {namespace: "production"}
  → Middleware: not a write tool, skip gate check
  → Middleware: CEL policy evaluated → passes
  → Tool: returns list of pods
  → Middleware: emits AuditEvent{Phase: "read", ToolName: "kubernetes_pod_list", PolicyPass: true}
  → Result returned to LLM
```

## Implementation Order

### Phase 1: Safety Primitives (`libs/toolkit/safety/`)
1. `types.go` — Common types
2. `audit.go` — AuditSink interface, ChannelSink, LogSink
3. `gate.go` — Gate logic
4. `policy.go` — CEL policy engine (add `github.com/google/cel-go` dependency)
5. `ownership.go` — Ownership detection

### Phase 2: ADK Middleware (`components/middleware/safety/`)
6. `config.go` — Config struct
7. `middleware.go` — Full middleware implementation
8. `middleware_test.go` — Unit tests

### Phase 3: Tool Modifications
9. `argocd/application_create.go` — Add DryRun + Confirmed
10. `argocd/application_delete.go` — Add DryRun + Confirmed
11. `argocd/application_sync.go` — Add Confirmed (DryRun exists)
12. `kubernetes/pod_exec.go` — Add Confirmed

### Phase 4: Registry & Integration
13. Update registries to expose write tool names
14. Add `NewAllToolsWithSafety` factory helpers
15. Integration tests

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| LLM ignores DryRun/Confirmed guidance | Middleware hard-gates: write without Confirmed=true is rejected, not just warned |
| CEL expression injection | CEL is sandboxed by design, no side effects. Compile expressions at config time, not per-call |
| Ownership detection misses custom operators | Document the heuristics, allow consumers to add custom patterns via config |
| ChannelSink goroutine leak if consumer doesn't read | Use buffered channel + context cancellation + Close() method |
| ArgoCD API doesn't support native dry-run for all operations | Implement client-side dry-run (build the object, return JSON, don't call API) |
| Breaking change for existing consumers (new params fields) | New fields are optional (omitempty), existing calls without them get gate-rejected by middleware only if middleware is enabled |
| Audit overhead for high-volume read operations | AuditSink.Write is non-blocking (fire-and-forget). ChannelSink uses buffered channel. Consumer controls backpressure. |
| Policies too restrictive for read tools | Policies are optional and configurable. Consumers can scope rules to specific tools via ToolNames field. |

## Dependencies to Add

- `github.com/google/cel-go` — CEL expression evaluation (standard, used by K8s)

## Open Questions

1. **Should the middleware auto-populate WriteToolNames from registry patterns?** Or require explicit configuration? (Recommendation: auto-populate with override option)
2. **Should ownership detection be a middleware concern or a tool concern?** (Recommendation: tool concern — the tool has access to the K8s client needed to fetch the resource. Middleware can't easily do this without duplicating client logic.)
3. **Should there be a "force" override for ownership warnings?** (Recommendation: yes, via a `forceOverride` param field, but the warning is always shown in dry-run)
