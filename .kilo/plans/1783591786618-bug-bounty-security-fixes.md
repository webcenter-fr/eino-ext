# Security Fixes Plan — Bug Bounty Analysis

## Decisions Made
- **K8s create/apply**: Content validation (reject dangerous Pod specs), not blanket Pod/Job blocklist
- **Namespace restriction**: Wrapper `ClusterConfig` with `DisallowedNamespaces` (default `["kube-system"]`)
- **PodExec blocklist**: Extend defaults + add customizable `Blocklist` parameter
- **Ownership checks**: In tools during dryRun phase (not in middleware)
- **Audit secrets**: Redact `WebhookUpsertParams.Secret` from audit events

---

## Phase 1: Infrastructure (shared helpers + config changes)

### Task 1.1: Create `ClusterConfig` wrapper type
**File:** `components/tool/kubernetes/config.go`

Add `ClusterConfig` struct wrapping `*rest.Config` + `DisallowedNamespaces`:
```go
type ClusterConfig struct {
    *rest.Config
    DisallowedNamespaces []string `validate:"omitempty"`
}
```
Change `Configs` from `map[string]*rest.Config` to `map[string]*ClusterConfig`.
Update `GetConfig` to return `*ClusterConfig`.
Update `GetConfig` callers in `base.go:54` (access `.Config`) and `check.go:33,40` (access `.Config`).
Update `client.go:62` (`BuildClientsFromKubeconfig`) to wrap in `&ClusterConfig{Config: ...}`.
Default: `DisallowedNamespaces` = `["kube-system"]` applied in `newBaseTool`.

### Task 1.2: Add namespace restriction enforcement
**Files:** `components/tool/kubernetes/base.go`, all list/describe/exec/log/write tools

Add `disallowedNamespaces` map to `baseTool` (populated from `ClusterConfig.DisallowedNamespaces`):
```go
type baseTool struct {
    ...
    disallowedNamespaces map[string]map[string]bool // cluster -> namespace -> true
}
```

Add helper `checkNamespace(cluster, namespace string) error` — returns error if namespace is disallowed for that cluster.

Call `checkNamespace` early in every tool's `Invoke`/`InvokeAsStream` that has a namespace parameter.

### Task 1.3: Add timeout support to K8s operations
**File:** `components/tool/kubernetes/client.go` — add `timeout` parameter to `NewClient`, `NewClientSet`, `NewClientDynamic` as a `*rest.Config.Timeout` override or wrap context.

**Alternative (simpler):** Wrap `ctx` with a per-operation timeout in each tool before calling K8s API. Default: 60s for exec, 30s for all others. Add to `Config` as `DefaultTimeout time.Duration`.

### Task 1.4: Add customizable blocklist to PodExecTool
**Files:** `components/tool/kubernetes/pod_exec.go`

Add `Blocklist []string` to `PodExecTool` struct. In `NewPodExecTool`, merge `defaultBlocklist` + `customBlocklist`. Export `DefaultBlocklist` as a variable for users to copy/extend.

---

## Phase 2: HIGH Severity Fixes

### Task 2.1: Fix GitHub Webhook SSRF — add DNS resolution
**File:** `components/tool/github/webhook_upsert.go` (`validateWebhookURL`)

After the literal IP check, if `host` is a hostname (not a literal IP), do `net.LookupIP(host)` and check each resolved IP with `isPrivateIP`-equivalent logic. Block metadata endpoint `169.254.169.254`.

Model after `checkSSRF` in `components/tool/websearch/webfetch.go:213-236`.

### Task 2.2: Add Pod manifest content validation
**File:** `components/tool/kubernetes/resource_create.go` (also `resource_apply.go` and `resource_patch.go`)

After parsing the manifest and validating `apiVersion`/`kind`/`name`, if `kind` is `Pod`, `Job`, or `CronJob`, call new `validatePodSpec(obj)`:

Reject manifest if the pod spec contains:
- `spec.hostNetwork == true`
- `spec.hostPID == true`
- `spec.hostIPC == true`
- Any container has `securityContext.privileged == true`
- Any container has `securityContext.capabilities.add` including `SYS_ADMIN`
- Any `hostPath` volume
- `spec.containers[*].volumeMounts[*]` pointing to host paths (`/proc`, `/sys`, `/etc/kubernetes`, `/var/run/docker.sock`)

For `Job`/`CronJob`, extract the pod template spec:
- `Job`: `spec.template.spec` (same validation)
- `CronJob`: `spec.jobTemplate.spec.template.spec` (same validation)

Create shared helper `validateManifestSecurity(unstructuredObj)` in a new file `components/tool/kubernetes/validate_manifest.go`.

### Task 2.3: Extend PodExec blocklist
**File:** `components/tool/kubernetes/pod_exec.go` (`defaultBlocklist`)

Add these patterns:
| Pattern | Rationale |
|---|---|
| `\beval\b` | Direct shell eval of arbitrary strings |
| `\bsource\b` | Shell sourcing of arbitrary scripts |
| `^\s*\.\s+` | Dot-command (equivalent to source) |
| `\bg?awk\b` | GNU awk with system() calls |
| `\bnawk\b` | Alternative awk with system() |
| `\btar\s+.*--to-command` | tar with arbitrary command execution |
| `\bxargs\b` | xargs can pass input to arbitrary commands |
| `\binstall\b` | Can be used to place binaries (`install /dev/stdin /usr/bin/evil`) |
| `\bcpio\b` | Archive extraction with arbitrary paths |
| `\bscreen\b` | Terminal multiplexer with command execution |
| `\btmux\b` | Terminal multiplexer with command execution |
| `\bscript\b` | Records terminal, can proxy commands |
| `\bexpect\b` | Scriptable command automation |
| `\btee\b` | Write to arbitrary files |
| `\b(?:/usr/bin/|/bin/)?execlineb\b` | execline shell |
| `\bopenssl\s+enc\b` | Can decrypt embedded payloads |

---

## Phase 3: MEDIUM Severity Fixes

### Task 3.1: Add CheckOwnership to resource_delete and resource_apply dryRun
**Files:** `components/tool/kubernetes/resource_delete.go`, `components/tool/kubernetes/resource_apply.go`, `components/tool/kubernetes/resource_patch.go`

During the dryRun phase:
1. Fetch the existing resource (already done in `resource_delete.go:123-127`; add for `resource_apply.go` and `resource_patch.go`)
2. Call `safety.CheckOwnership(existingResource)` 
3. Include `ownership` info in dryRun output JSON
4. Output includes warnings like "This resource is managed by ArgoCD. Modifying it directly may cause drift."

**resource_apply.go** currently does `Patch` directly for dryRun (server-side dry-run). Change to: for dryRun, fetch existing first, then return ownership + would-apply preview.

### Task 3.2: Validate cloneDir in GitHub tools
**File:** `components/tool/github/config.go`, `components/tool/github/helper.go`

Add path validation in `Config.CloneDir` validation:
- Must be an absolute path (starts with `/`)
- Must not be `/` (root)
- Must not be a system directory (`/etc`, `/bin`, `/usr`, `/var`, `/proc`, `/sys`, `/dev`, `/tmp`)
- Must exist or be creatable

Alternatively, validate at tool construction time in `newBaseTool` and return error if `cloneDir` is dangerous.

### Task 3.3: Verify timeout enforced on all K8s write operations
**File:** All K8s tool Invoke methods

Ensure every tool wraps its `ctx` with the configured timeout before making K8s API calls. Add after validation but before client calls:
```go
if t.base.timeout > 0 {
    var cancel context.CancelFunc
    ctx, cancel = context.WithTimeout(ctx, t.base.timeout)
    defer cancel()
}
```

---

## Phase 4: LOW Severity Fixes

### Task 4.1: Redact webhook secrets from audit events
**File:** `components/middleware/safety/middleware.go` or `components/tool/github/webhook_upsert.go`

Option A (preferred): In `webhook_upsert.go`, clear `params.Secret` after using it but before returning (it's already only set in the API call, not in the output — but it IS in the audit trail).

Option B: Add a `sensitiveFields` map to the audit event serialization in the middleware.

Option A is simpler: add `params.Secret = ""` after the API call at line 92/100.

### Task 4.2: Add Prometheus query complexity limit
**File:** `components/tool/prometheus/config.go`, `components/tool/prometheus/metric_query.go`, `components/tool/prometheus/metric_range.go`

Add `MaxSamples int` to `Config` (default 10000). Before executing query, check if the query contains subqueries `[<range>]` with range > 7d — reject. Also restrict `metric_range.go` `Step` to minimum 15s and `End-Start` to maximum 7d.

### Task 4.3: Document WebFetch proxy SSRF caveat
**File:** `components/tool/websearch/config.go` — add doc comment on `SkipSSRFCheck` and `HTTPClient` fields noting that proxy configuration bypasses transport-level SSRF checks.

---

## Files Changed (complete list)

### New files
- `components/tool/kubernetes/validate_manifest.go` — manifest security validation
- `components/tool/kubernetes/validate_manifest_test.go` — tests

### Modified files (by package)

**components/tool/kubernetes/**
- `config.go` — ClusterConfig wrapper, DisallowedNamespaces
- `base.go` — disallowedNamespaces map, checkNamespace, timeout store
- `client.go` — BuildClientsFromKubeconfig wrapper adaptation
- `check.go` — GetConfig adaptation
- `pod_exec.go` — extended blocklist, customizable blocklist, timeout
- `pod_log.go` — namespace check, timeout
- `resource_create.go` — manifest validation call, namespace check, timeout
- `resource_apply.go` — manifest validation call, dryRun ownership check, namespace check, timeout
- `resource_delete.go` — dryRun ownership check, namespace check, timeout
- `resource_patch.go` — manifest validation call, dryRun ownership check, namespace check, timeout
- `registry.go` — Configs type change propagates
- All `*_list.go` files — namespace check, timeout
- All `*_describe.go` files — namespace check, timeout
- `cluster_list.go` — timeout (no namespace)
- `generic_list.go` — namespace check, timeout
- `generic_describe.go` — namespace check, timeout
- `configmap_test.go`, `suite_test.go` — test adaptation for new Configs type

**components/tool/github/**
- `webhook_upsert.go` — DNS resolution in validateWebhookURL, secret redaction
- `config.go` — cloneDir validation
- `helper.go` — clonePath enhancement (no logic change)

**components/tool/prometheus/**
- `config.go` — add MaxSamples, MaxTimeRange, MinStep
- `metric_query.go` — query complexity validation, timeout
- `metric_range.go` — range/step validation, timeout

**components/middleware/safety/**
- No code changes (ownership is in tools, not middleware)

**components/tool/websearch/**
- `config.go` — doc comment on proxy caveat

---

## Validation

```bash
go build ./...
go vet ./...
go test ./components/tool/kubernetes/...
go test ./components/tool/github/...
go test ./components/tool/prometheus/...
go test ./libs/toolkit/safety/...
```

### Test coverage requirements
| Test | What it validates |
|---|---|
| `TestValidateWebhookURL_HostnameSSRF` | DNS-resolved internal hostname rejected |
| `TestValidateManifest_PrivilegedPod` | hostNetwork, privileged, hostPath rejected |
| `TestValidateManifest_LegitimatePod` | Normal pod passes |
| `TestPodExecBlocklist_BypassAttempts` | eval, source, gawk, tar, xargs blocked |
| `TestPodExecBlocklist_LegitimateCommands` | ls, cat, curl (read-only) pass |
| `TestNamespaceDisallow` | kube-system namespace rejected |
| `TestOwnershipCheckDryRun` | ArgoCD-managed resource shows warning |
| `TestGitHubCloneDirValidation` | `/`, `/etc`, `/bin` rejected |
| `TestPrometheusQueryLimit` | Query with 30d range rejected |
