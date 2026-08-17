# Plan: Make the Alertmanager component read-only and redirect writes to Kubernetes CRDs

## Goal

The Alertmanager component (`components/tool/alertmanager/`) must become
**read-only against the Alertmanager HTTP API**: only `GET /api/v2/alerts`
remains. The `alertmanager_alert_write` tool must stop POSTing to Alertmanager
and instead return a **structured guidance object** that tells the LLM to
manage the alert lifecycle declaratively via the existing Kubernetes tools
(`kubernetes_resource_apply` / `kubernetes_resource_create` /
`kubernetes_resource_patch` / `kubernetes_resource_delete`) using the
`monitoring.coreos.com` CRDs (`PrometheusRule`, `Silence`, `AlertmanagerConfig`).

The tool **name** `alertmanager_alert_write` is preserved (LLM tool contract
stability); only its description and behavior change.

## Verified current state (from reading the code)

- `alert.go` — read tool `alertmanager_alert` (`AlertTool`, `AlertParams`,
  `AlertOutput`), lists alerts via `alertmanagerClient.ListAlerts`. **Unchanged.**
- `alert_write.go` — write tool `alertmanager_alert_write` (`AlertWriteTool`,
  `AlertWriteParams` with `operation` ∈ create/update/delete + `Labels`/
  `Annotations`/`StartsAt`/`EndsAt`/`GeneratorURL`/`DryRun`/`Confirmed`,
  `AlertWriteOutput`). POSTs via `alertmanagerClient.PostAlerts`. **Transformed.**
- `client.go` — `alertmanagerClient` with `ListAlerts` + `PostAlerts`,
  `redactingTransport` redacting `*alert.GetAlertsBadRequest`,
  `*alert.GetAlertsInternalServerError`, `*alert.PostAlertsBadRequest`,
  `*alert.PostAlertsInternalServerError`, plus `*runtime.APIError` fallback.
  Helpers: `buildRedactSecrets`, `redactSecret`, `truncateRedacted`, `boolPtr`,
  `listAlertsParams`.
- `base.go` — `baseTool`, tool-name consts `instanceListToolName`,
  `alertToolName`, `alertWriteToolName`.
- `helper.go` — `marshalOutputs`, `marshalString`, `instanceNotFoundError`,
  `validateParams`, `parseRFC3339`, `AlertPaginate`, `alertPaginateToken`,
  `paginateWindow`, `nextPageToken`, `receiverNames`, `ptrString`,
  `ptrDateTime`, `ptrDateTimeFormat`, `alertStatusState`, `alertStatusSilencedBy`.
- `check.go` — `Check`, `clientErrorResults`, `allComponentNames` (3 names),
  `probeInstance` (write tool reported as `StatusLimited` "write tool, not
  probed to avoid side effects"), `probeAlert`.
- `registry.go` — `readOnlyConstructors` = [InstanceList, Alert],
  `writeConstructors` = [AlertWrite], `WriteToolNames()` = `[alertWriteToolName]`,
  `NewAllToolsWithSafety`, compile-time assertions for all three tools.
- `config.go`, `instance_list.go`, `README.md`, tests `alert_test.go`,
  `alert_write_test.go`, `client_test.go`, `instance_list_test.go`,
  `check_test.go`.
- Kubernetes component (`components/tool/kubernetes/`) confirmed:
  - Tool names (exact): `kubernetes_resource_apply`,
    `kubernetes_resource_create`, `kubernetes_resource_patch`,
    `kubernetes_resource_delete`.
  - `kubernetes_resource_apply` / `_create` / `_patch` take a `manifest` JSON
    string (must include `apiVersion`, `kind`, `metadata`).
  - `kubernetes_resource_delete` takes `kind` + `name` + `namespace` (no
    manifest).
  - `blocklistedKinds` / `blocklistedResources` do **not** include
    `PrometheusRule`, `Silence`, or `AlertmanagerConfig` — all three CRDs are
    manageable today.
- CRD apiVersions (kube-prometheus-stack / prometheus-operator):
  - `PrometheusRule` → `apiVersion: monitoring.coreos.com/v1`, `kind: PrometheusRule`, namespaced.
  - `Silence` → `apiVersion: monitoring.coreos.com/v1alpha1`, `kind: Silence`, namespaced.
  - `AlertmanagerConfig` → `apiVersion: monitoring.coreos.com/v1alpha1`, `kind: AlertmanagerConfig`, namespaced.

## Scope / out of scope

- In scope: `alert_write.go`, `client.go`, `registry.go`, `check.go`,
  `alert_write_test.go`, `client_test.go`, `check_test.go`, `README.md`.
- Out of scope: `alert.go`, `alert_test.go`, `instance_list.go`,
  `instance_list_test.go`, `config.go`, `base.go`, `helper.go`, the
  `prompts/list_output_guidance.md` embed, the Kubernetes component, `go.mod`
  (no dependency change — `prometheus/alertmanager` is still used by the read
  client).

---

## 1. `components/tool/alertmanager/client.go`

### 1.1 Remove the `PostAlerts` method

Delete the `PostAlerts` method (lines ~274–281):

```go
// PostAlerts calls POST /api/v2/alerts with the given alerts.
func (c *alertmanagerClient) PostAlerts(ctx context.Context, alerts models.PostableAlerts) error {
    ...
}
```

### 1.2 Trim `redactAPIErrorPayload` to the read path only

In `redactAPIErrorPayload`, **remove** the two POST cases:

```go
case *alert.PostAlertsBadRequest:
    e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
case *alert.PostAlertsInternalServerError:
    e.Payload = truncateRedacted(redactSecret(e.Payload, secrets))
```

**Keep** `*alert.GetAlertsBadRequest`, `*alert.GetAlertsInternalServerError`,
and the `*runtime.APIError` fallback (a read-only GET can still return a
403/500 body that echoes the `Authorization` header). Update the doc comment
on `redactAPIErrorPayload` to say "the generated GET 400/500 response structs"
instead of "400/500".

### 1.3 Imports

After the changes:
- `models` is still imported (used by `ListAlerts` return type
  `[]*models.GettableAlert` and `listAlertsParams` does not use models, but
  `ListAlerts` does). **Keep** `github.com/prometheus/alertmanager/api/v2/models`.
- `alert` is still imported (`alert.NewGetAlertsParamsWithContext`,
  `alert.GetAlertsBadRequest`, etc.). **Keep.**
- No other imports become unused. `bytes`/`io` are not imported today.
  Verify with `goimports`/`gofmt` after edit.

### 1.4 No other changes

`NewClient`, `BuildClients`, `redactingTransport.Submit`, `buildRedactSecrets`,
`redactSecret`, `truncateRedacted`, `boolPtr`, `listAlertsParams`,
`ListAlerts`, `amMaxErrorBodyLen` — all unchanged.

---

## 2. `components/tool/alertmanager/alert_write.go` (full rewrite)

Package: `alertmanager`. Keep the file path. Replace the entire content.

### 2.1 New description constant

```go
const alertWriteToolDescription = `
** General Purpose **
This tool does NOT mutate Alertmanager. Alertmanager alert lifecycle
(create / update / delete / silence) must be managed declaratively via
Kubernetes CRDs from the prometheus-operator (monitoring.coreos.com), applied
through the kubernetes_resource_* tools. This tool returns structured
guidance telling you which CRD and which kubernetes tool to use, plus a
ready-to-adapt manifest skeleton built from the labels/annotations you
provide.

The required 'operation' param selects the intent:
- create: define a new alerting rule (PrometheusRule).
- update: modify an existing alerting rule (PrometheusRule).
- delete: stop an alert — either remove/silence the generating PrometheusRule
  or apply a temporary Silence CRD. Both options are returned.

** Read-only against Alertmanager **
This tool never calls the Alertmanager HTTP API. It only validates params and
returns guidance. Use the kubernetes_resource_* tools to perform the actual
write.

** Required labels **
All operations require a 'labels' map that includes the 'alertname' label.
`
```

### 2.2 New params struct (breaking change — param removals)

```go
// AlertWriteParams defines the parameters for the alertmanager_alert_write
// guidance tool. The tool does NOT mutate Alertmanager; it returns guidance
// pointing the LLM to the Kubernetes CRD tools.
type AlertWriteParams struct {
    Instance    string            `json:"instance" validate:"required" jsonschema:"(required) The Alertmanager instance name (context only; no live call is made)."`
    Operation   string            `json:"operation" validate:"required,oneof=create update delete" jsonschema:"(required) Intent: 'create', 'update', or 'delete'."`
    Labels      map[string]string `json:"labels" validate:"required,min=1,max=64,dive,keys,required,endkeys,required" jsonschema:"(required) Alert labels; must include 'alertname'. Used to pre-fill the example manifest (PrometheusRule rule labels, Silence matchers)."`
    Annotations map[string]string `json:"annotations,omitempty" validate:"omitempty,max=64,dive,keys,required,endkeys,required" jsonschema:"(optional) Alert annotations. Used to pre-fill the example PrometheusRule rule annotations."`
}
```

**Removed fields (breaking change — document in README "Breaking changes"):**
`StartsAt`, `EndsAt`, `GeneratorURL`, `DryRun`, `Confirmed`. Rationale: the
CRD model has its own fields (`for`, `expr`, Silence `startsAt`/`endsAt`,
etc.) that the LLM must fill in the manifest; the Alertmanager
`PostableAlert` time/URL fields do not map 1:1 and would mislead. No
confirmation gate is needed because the tool no longer mutates anything.

### 2.3 New output struct

```go
// RecommendedCRD names a Kubernetes CRD the LLM should use.
type RecommendedCRD struct {
    Name       string `json:"name"`       // e.g. "PrometheusRule"
    APIVersion string `json:"apiVersion"` // e.g. "monitoring.coreos.com/v1"
    Kind       string `json:"kind"`       // e.g. "PrometheusRule"
    Namespaced bool   `json:"namespaced"` // true for all three CRDs
    Purpose    string `json:"purpose"`    // short description of when to use it
}

// ManifestExample is one ready-to-adapt manifest skeleton plus the
// kubernetes tool the LLM should drive with it.
type ManifestExample struct {
    CRD      string          `json:"crd"`      // "PrometheusRule" | "Silence"
    Tool     string          `json:"tool"`     // e.g. "kubernetes_resource_apply"
    Action   string          `json:"action"`   // "apply" | "create" | "delete"
    Manifest json.RawMessage `json:"manifest"` // JSON skeleton (YAML not used; kubernetes tools take JSON)
    Comment  string          `json:"comment,omitempty"`
}

// AlertGuidanceOutput is the structured guidance returned by
// alertmanager_alert_write. It never mutates Alertmanager.
type AlertGuidanceOutput struct {
    Action           string            `json:"action"`           // requested operation: "create" | "update" | "delete"
    Message          string            `json:"message"`          // human/LLM-readable explanation
    RecommendedTools []string          `json:"recommendedTools"` // kubernetes tool names, e.g. ["kubernetes_resource_apply"]
    RecommendedCRDs  []RecommendedCRD  `json:"recommendedCRDs"`  // CRDs the LLM should target
    Examples         []ManifestExample `json:"examples"`         // one or more manifest skeletons
    Notes            []string          `json:"notes,omitempty"`  // extra guidance (e.g. delete dual options, routing hint)
}
```

### 2.4 Tool struct + constructor

```go
// AlertWriteTool is an eino tool that returns CRD guidance instead of
// mutating Alertmanager. It implements tool.InvokableTool.
type AlertWriteTool struct {
    *baseTool
    tool.InvokableTool
}

// NewAlertWriteTool creates a new AlertWriteTool. It still builds a baseTool
// (so the Instance param can be validated against known instances), but
// Invoke never calls the Alertmanager API.
func NewAlertWriteTool(ctx context.Context, configs Configs) (*AlertWriteTool, error) {
    base, err := newBaseTool(ctx, configs)
    if err != nil {
        return nil, err
    }
    writeTool := &AlertWriteTool{baseTool: base}
    t, err := utils.InferTool(alertWriteToolName, alertWriteToolDescription, writeTool.Invoke)
    if err != nil {
        return nil, err
    }
    writeTool.InvokableTool = t
    return writeTool, nil
}
```

> Note: `newBaseTool` still builds live clients for `Instance` validation.
> This keeps the existing `instanceNotFoundError` behavior. If the user
> prefers to avoid building clients for a non-mutating tool, that is a
> follow-up; for now we keep the baseTool so `Instance` is validated
> consistently with the read tool.

### 2.5 Invoke flow

```go
func (t *AlertWriteTool) Invoke(ctx context.Context, params *AlertWriteParams) (string, error) {
    if err := validateParams(params); err != nil {
        return "", err
    }
    // Validate instance is known (consistent with the read tool). Does NOT
    // make an HTTP call.
    if _, err := t.client(params.Instance); err != nil {
        return "", err
    }
    if params.Labels["alertname"] == "" {
        return "", errors.Errorf("labels must include 'alertname'")
    }

    switch params.Operation {
    case "create", "update":
        return marshalString(buildRuleGuidance(params.Operation, params.Labels, params.Annotations)), nil
    case "delete":
        return marshalString(buildDeleteGuidance(params.Labels)), nil
    default:
        return "", errors.Errorf("unsupported operation %q", params.Operation)
    }
}
```

- `validateParams` (existing helper) runs `validate.Struct` → enforces
  `operation` oneof, `labels` required + size limits, `annotations` size
  limits. The `unsupported operation` default branch is unreachable due to
  the `oneof` tag but kept for defense-in-depth (matches the prior code
  pattern).
- The `alertname` check is preserved verbatim (same error message as before).
- No `confirm.RequireConfirmation` call (removed with `DryRun`/`Confirmed`).
- No `models.PostableAlert`, no `strfmt`, no `time`, no `net/url`, no
  `regexp`, no `sort`, no `strings` usage in the new file (see imports below).

### 2.6 Guidance builders

#### 2.6.1 `buildRuleGuidance` (create / update)

```go
func buildRuleGuidance(operation string, labels, annotations map[string]string) AlertGuidanceOutput {
    alertname := labels["alertname"]
    ruleLabels := withoutAlertname(labels)
    ruleAnnotations := annotations // may be nil; the manifest will omit it if empty

    prometheusRuleManifest := map[string]any{
        "apiVersion": "monitoring.coreos.com/v1",
        "kind":       "PrometheusRule",
        "metadata": map[string]any{
            "name":      sanitizeRuleName(alertname),
            "namespace": "REPLACE_WITH_TARGET_NAMESPACE",
            "labels": map[string]any{
                "prometheus": "REPLACE_WITH_PROMETHEUS_CR_SELECTOR",
            },
        },
        "spec": map[string]any{
            "groups": []any{
                map[string]any{
                    "name": alertname,
                    "rules": []any{
                        map[string]any{
                            "alert":   alertname,
                            "expr":    "REPLACE_WITH_PROMQL_EXPRESSION",
                            "for":     "5m",
                            "labels":  ruleLabels,
                            "annotations": ruleAnnotations,
                        },
                    },
                },
            },
        },
    }
    // If ruleLabels is empty, omit the "labels" key on the rule (cleaner skeleton).
    // If ruleAnnotations is empty/nil, omit the "annotations" key on the rule.
    // (Implement with helper omitEmptyMap as needed; see 2.8.)

    return AlertGuidanceOutput{
        Action:  operation,
        Message: fmt.Sprintf("Alertmanager is read-only. To %s the alert %q, declare it as a PrometheusRule CRD and apply it via the kubernetes_resource_apply tool. The example manifest below is pre-filled with your labels and annotations; fill in expr, for, namespace, and the Prometheus CR selector label.", operation, alertname),
        RecommendedTools: []string{"kubernetes_resource_apply", "kubernetes_resource_create", "kubernetes_resource_patch"},
        RecommendedCRDs: []RecommendedCRD{
            {
                Name:       "PrometheusRule",
                APIVersion: "monitoring.coreos.com/v1",
                Kind:       "PrometheusRule",
                Namespaced:  true,
                Purpose:     "Defines alerting rules (alert + expr + for + labels + annotations) evaluated by Prometheus.",
            },
        },
        Examples: []ManifestExample{
            {
                CRD:    "PrometheusRule",
                Tool:   "kubernetes_resource_apply",
                Action: "apply",
                Manifest: json.RawMessage(marshal.MustMarshal(prometheusRuleManifest)),
                Comment: "For update, apply the modified PrometheusRule (server-side apply will reconcile the existing rule). For create, the same apply creates it if absent.",
            },
        },
        Notes: []string{
            "If your intent is to change alert ROUTING or receivers (not the rule itself), use the AlertmanagerConfig CRD (apiVersion monitoring.coreos.com/v1alpha1, kind AlertmanagerConfig) via kubernetes_resource_apply instead.",
        },
    }
}
```

#### 2.6.2 `buildDeleteGuidance` (delete — dual recommendation)

```go
func buildDeleteGuidance(labels map[string]string) AlertGuidanceOutput {
    alertname := labels["alertname"]
    matchers := toSilenceMatchers(labels)
    now := time.Now().UTC()
    endsAt := now.Add(2 * time.Hour)

    // Option A: delete the generating PrometheusRule.
    prometheusRuleDeleteManifest := map[string]any{
        "apiVersion": "monitoring.coreos.com/v1",
        "kind":       "PrometheusRule",
        "metadata": map[string]any{
            "name":      sanitizeRuleName(alertname),
            "namespace": "REPLACE_WITH_TARGET_NAMESPACE",
        },
    }

    // Option B: apply a temporary Silence CRD.
    silenceManifest := map[string]any{
        "apiVersion": "monitoring.coreos.com/v1alpha1",
        "kind":       "Silence",
        "metadata": map[string]any{
            "name":      fmt.Sprintf("silence-%s-%d", sanitizeRuleName(alertname), now.Unix()),
            "namespace": "REPLACE_WITH_TARGET_NAMESPACE",
        },
        "spec": map[string]any{
            "matchers":  matchers,
            "startsAt":  now.Format(time.RFC3339),
            "endsAt":    endsAt.Format(time.RFC3339),
            "createdBy": "eino-agent",
            "comment":   "Silence created by alertmanager_alert_write guidance tool",
        },
    }

    return AlertGuidanceOutput{
        Action: "delete",
        Message: fmt.Sprintf("Alertmanager is read-only. To stop the alert %q, choose one of two declarative options: (A) permanently remove or update the generating PrometheusRule CRD, or (B) temporarily suppress it with a Silence CRD. Both example manifests are provided below.", alertname),
        RecommendedTools: []string{"kubernetes_resource_delete", "kubernetes_resource_apply"},
        RecommendedCRDs: []RecommendedCRD{
            {
                Name:       "PrometheusRule",
                APIVersion: "monitoring.coreos.com/v1",
                Kind:       "PrometheusRule",
                Namespaced:  true,
                Purpose:     "Source of the alert. Delete or update this CRD to permanently stop the alert firing.",
            },
            {
                Name:       "Silence",
                APIVersion: "monitoring.coreos.com/v1alpha1",
                Kind:       "Silence",
                Namespaced:  true,
                Purpose:     "Temporary suppression of matching alerts without touching the rule. Expires at endsAt.",
            },
        },
        Examples: []ManifestExample{
            {
                CRD:    "PrometheusRule",
                Tool:   "kubernetes_resource_delete",
                Action: "delete",
                Manifest: json.RawMessage(marshal.MustMarshal(prometheusRuleDeleteManifest)),
                Comment: "Pass kind=\"PrometheusRule\", name=<metadata.name>, namespace=<metadata.namespace> to kubernetes_resource_delete. This permanently stops the alert (rule removal).",
            },
            {
                CRD:    "Silence",
                Tool:   "kubernetes_resource_apply",
                Action: "apply",
                Manifest: json.RawMessage(marshal.MustMarshal(silenceManifest)),
                Comment: "Temporary suppression. Adjust endsAt to the desired silence window. The silence auto-expires; the rule keeps firing afterwards.",
            },
        },
        Notes: []string{
            "Prefer option A (delete/update PrometheusRule) when the alert should no longer exist. Prefer option B (Silence) for temporary maintenance windows.",
            "If the alert is produced by a rule you do not own, use the Silence CRD (option B) — do not delete someone else's PrometheusRule.",
            "If your intent is to change alert routing rather than stop the alert, use the AlertmanagerConfig CRD (apiVersion monitoring.coreos.com/v1alpha1, kind AlertmanagerConfig) via kubernetes_resource_apply.",
        },
    }
}
```

### 2.7 Helpers (new, local to this file)

```go
// withoutAlertname returns a copy of labels with the alertname key removed.
// Used to build PrometheusRule rule.labels (which must not contain alertname;
// alertname is the rule's `alert` field).
func withoutAlertname(labels map[string]string) map[string]string {
    out := make(map[string]string, len(labels))
    for k, v := range labels {
        if k == "alertname" {
            continue
        }
        out[k] = v
    }
    return out
}

// sanitizeRuleName derives a Kubernetes-safe metadata.name for the
// PrometheusRule from the alertname. It lowercases and replaces any
// character outside [a-z0-9-] with '-'. The result is RFC1123-compliant
// (lowercase, alphanumeric and hyphens). If the result is empty or starts
// with a non-alphanumeric character, it is prefixed with "rule-".
func sanitizeRuleName(alertname string) string {
    s := strings.ToLower(alertname)
    var b strings.Builder
    for _, r := range s {
        switch {
        case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
            b.WriteRune(r)
        default:
            b.WriteByte('-')
        }
    }
    out := strings.Trim(b.String(), "-")
    if out == "" {
        return "rule"
    }
    if out[0] < 'a' || out[0] > 'z' { // starts with digit
        out = "rule-" + out
    }
    return out
}

// toSilenceMatchers converts the alert labels into Silence CRD matcher
// objects: {name, value, isRegex:false}. alertname is included as the first
// matcher. Deterministic order: sorted by key, with alertname first.
func toSilenceMatchers(labels map[string]string) []map[string]any {
    keys := make([]string, 0, len(labels))
    for k := range labels {
        if k != "alertname" {
            keys = append(keys, k)
        }
    }
    sort.Strings(keys)
    matchers := make([]map[string]any, 0, len(keys)+1)
    matchers = append(matchers, map[string]any{
        "name":    "alertname",
        "value":   labels["alertname"],
        "isRegex": false,
    })
    for _, k := range keys {
        matchers = append(matchers, map[string]any{
            "name":    k,
            "value":   labels[k],
            "isRegex": false,
        })
    }
    return matchers
}
```

### 2.8 Imports for `alert_write.go`

```go
import (
    "context"
    "fmt"
    "sort"
    "strings"
    "time"

    "emperror.dev/errors"
    "github.com/cloudwego/eino/components/tool"
    "github.com/cloudwego/eino/components/tool/utils"
    "github.com/goccy/go-json"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)
```

- `errors` — for `errors.Errorf` (alertname check, unsupported operation).
- `tool`, `utils` — for `InvokableTool` + `InferTool`.
- `json` — for `json.RawMessage`.
- `marshal` — for `marshal.MustMarshal` (used to build `json.RawMessage`).
- `time` — for Silence `startsAt`/`endsAt`.
- `sort`, `strings` — for `toSilenceMatchers` and `sanitizeRuleName`.
- `fmt` — for `Sprintf` in messages.
- `context` — for `Invoke` signature.
- **Removed** vs the old file: `net/url`, `regexp`, `github.com/go-openapi/strfmt`,
  `github.com/prometheus/alertmanager/api/v2/models`,
  `github.com/webcenter-fr/eino-ext/libs/toolkit/confirm`.

### 2.9 Removed symbols (old → new / removed)

| Old symbol | Status |
|---|---|
| `labelNameRE` | **Removed** (no more matcher building) |
| `buildMatcherFilter` | **Removed** |
| `validateMatcherLabelKeys` | **Removed** |
| `coalesceTime` | **Removed** |
| `validateGeneratorURL` | **Removed** |
| `AlertWriteTool.postAlert` | **Removed** |
| `AlertWriteParams.StartsAt/EndsAt/GeneratorURL/DryRun/Confirmed` | **Removed** |
| `AlertWriteOutput` (Status/Action/Fingerprint/EndsAt) | **Replaced** by `AlertGuidanceOutput` |
| `alertWriteToolDescription` (old) | **Replaced** (new content) |
| — | **New**: `RecommendedCRD`, `ManifestExample`, `AlertGuidanceOutput`, `buildRuleGuidance`, `buildDeleteGuidance`, `withoutAlertname`, `sanitizeRuleName`, `toSilenceMatchers` |

---

## 3. `components/tool/alertmanager/registry.go`

### 3.1 Move `NewAlertWriteTool` to `readOnlyConstructors`

```go
var readOnlyConstructors = []toolConstructor{
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertTool(ctx, c) },
    func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertWriteTool(ctx, c) },
}

var writeConstructors = []toolConstructor{}
```

### 3.2 `WriteToolNames` returns empty (non-nil)

```go
func WriteToolNames() []string {
    return []string{}
}
```

### 3.3 `ExtractWriteToolNames`

Keep the function (still compiles — iterates an empty slice, returns an
empty `names`). No behavior change required; it now returns `[]string{}`
with no error. Optionally simplify the doc comment to note that there are
no write tools anymore.

### 3.4 `NewAllTools` / `NewReadOnlyTools` / `NewAllToolsWithSafety`

- `NewAllTools` now builds only `readOnlyConstructors` (since
  `writeConstructors` is empty, `append(readOnlyConstructors, writeConstructors...)`
  == `readOnlyConstructors`). No code change needed; behavior: returns all 3
  tools.
- `NewReadOnlyTools` now returns the same 3 tools. No code change.
- `NewAllToolsWithSafety`: `safetyCfg.WriteToolNames` defaults to
  `WriteToolNames()` which is now `[]string{}`. The safety middleware will
  treat no tool as a write tool. No code change needed; behavior: the
  guidance tool is no longer gated by the safety middleware's confirmation
  flow (correct — it does not mutate).

### 3.5 Compile-time assertions

Keep unchanged:

```go
var (
    _ tool.InvokableTool = (*InstanceListTool)(nil)
    _ tool.InvokableTool = (*AlertTool)(nil)
    _ tool.InvokableTool = (*AlertWriteTool)(nil)
)
```

---

## 4. `components/tool/alertmanager/check.go`

### 4.1 `probeInstance` write-tool result

Change the `alertWriteToolName` result from:

```go
{
    Component: alertWriteToolName,
    Instance:  instance,
    Status:    checkup.StatusLimited,
    Message:   "write tool, not probed to avoid side effects",
},
```

to:

```go
{
    Component: alertWriteToolName,
    Instance:  instance,
    Status:    checkup.StatusOK,
    Message:   "guidance tool, no external call required",
},
```

Rationale: the tool no longer makes any external call, so there is nothing
to probe and no side effect to avoid; `StatusOK` reflects that the tool is
always available. `allComponentNames()` still returns the 3 names
(unchanged). `probeAlert` unchanged. `clientErrorResults` unchanged (still
emits 3 results, all `StatusError`, when client creation fails — the
guidance tool's instance resolution would also fail in that case, so
`StatusError` is correct).

---

## 5. `components/tool/alertmanager/alert_write_test.go` (full rewrite)

Package: `alertmanager`. Replace the entire file. No `httptest` server is
needed for the guidance behavior (the tool makes no HTTP call). A minimal
`httptest` server is still used only for the `Instance` validation path
(`newBaseTool` builds a client; `t.client(params.Instance)` must succeed).

### 5.1 Test helpers

```go
func newWriteTool(t *testing.T) *AlertWriteTool {
    t.Helper()
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // The guidance tool must NOT call Alertmanager. If it does, fail.
        t.Errorf("guidance tool must not call Alertmanager; got %s %s", r.Method, r.URL.Path)
        w.WriteHeader(http.StatusInternalServerError)
    }))
    t.Cleanup(server.Close)
    tool, err := NewAlertWriteTool(context.Background(), Configs{"t": {Address: server.URL}})
    require.NoError(t, err)
    return tool
}

func validParams(op string, overrides func(*AlertWriteParams)) *AlertWriteParams {
    p := &AlertWriteParams{
        Instance:    "t",
        Operation:   op,
        Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
        Annotations: map[string]string{"summary": "high cpu"},
    }
    if overrides != nil {
        overrides(p)
    }
    return p
}
```

### 5.2 Required tests

1. `TestAlertWriteCreateGuidance` — invoke with `operation=create`. Assert:
   - No HTTP call to Alertmanager (the httptest handler `t.Errorf`s if hit).
   - Output unmarshals to `AlertGuidanceOutput`.
   - `Action == "create"`.
   - `RecommendedTools` contains `kubernetes_resource_apply`.
   - `RecommendedCRDs` has exactly one entry: `PrometheusRule`,
     `monitoring.coreos.com/v1`, namespaced.
   - `Examples` has one entry with `CRD == "PrometheusRule"`,
     `Tool == "kubernetes_resource_apply"`, `Action == "apply"`.
   - The example manifest JSON contains `apiVersion: monitoring.coreos.com/v1`,
     `kind: PrometheusRule`, `spec.groups[0].rules[0].alert == "HighCPU"`,
     `spec.groups[0].rules[0].labels.severity == "critical"` (alertname NOT
     in rule labels), `spec.groups[0].rules[0].annotations.summary == "high cpu"`,
     and `expr == "REPLACE_WITH_PROMQL_EXPRESSION"` placeholder.
2. `TestAlertWriteUpdateGuidance` — same as create but `operation=update`,
   `Action == "update"`, message contains "update".
3. `TestAlertWriteDeleteGuidance` — `operation=delete`. Assert:
   - `Action == "delete"`.
   - `RecommendedTools` contains both `kubernetes_resource_delete` and
     `kubernetes_resource_apply`.
   - `RecommendedCRDs` has two entries: `PrometheusRule` and `Silence`
     (with `monitoring.coreos.com/v1alpha1`).
   - `Examples` has two entries: one `PrometheusRule` + `kubernetes_resource_delete`
     + `Action: "delete"`, one `Silence` + `kubernetes_resource_apply` +
     `Action: "apply"`.
   - The Silence manifest `spec.matchers` contains `{name:"alertname",
     value:"HighCPU", isRegex:false}` first, then `{name:"severity",
     value:"critical", isRegex:false}` (sorted after alertname).
   - The Silence manifest `spec.startsAt` and `spec.endsAt` are valid
     RFC3339 and `endsAt > startsAt`.
4. `TestAlertWriteRequiresAlertname` — for each op, labels without
   `alertname` → error contains `labels must include 'alertname'`. No HTTP
   call.
5. `TestAlertWriteInvalidOperation` — `operation="foo"` → error contains
   `invalid parameters` (from `validate.Struct` oneof). No HTTP call.
6. `TestAlertWriteMissingInstance` — `Instance="nope"` → error contains
   `nope` (instanceNotFoundError). No HTTP call to the Alertmanager
   handler.
7. `TestAlertWriteInputSizeLimits` — too many labels (>64) or too many
   annotations (>64) → error contains `invalid parameters`. No HTTP call.
8. `TestAlertWriteNoExternalCall` — for each op (create/update/delete),
   invoke with valid params and assert the httptest handler is never hit
   (the handler `t.Errorf`s on any request). This is the security-relevant
   regression test that the tool is genuinely read-only.
9. `TestSanitizeRuleName` — table-driven:
   - `"HighCPU"` → `"highcpu"`
   - `"High/CPU"` → `"high-cpu"`
   - `"1Alert"` → `"rule-1alert"`
   - `"---"` → `"rule"`
   - `""` → `"rule"`
10. `TestWithoutAlertname` — input `{"alertname":"X","severity":"y"}` →
    output map has `severity:"y"` and no `alertname` key.
11. `TestToSilenceMatchers` — input `{"alertname":"X","b":"2","a":"1"}`
    → matchers[0] is `{name:"alertname",value:"X"}`, then
    `{name:"a",value:"1"}`, then `{name:"b",value:"2"}` (sorted after
    alertname), all `isRegex:false`.

### 5.3 Removed tests (do not port)

All of: `TestAlertWriteCreate`, `TestAlertWriteUpdate`,
`TestAlertWriteUpdateFilterParams`,
`TestAlertWriteUpdateKeepsExistingAnnotationsWhenOmitted`,
`TestAlertWriteDelete`, `TestAlertWriteDryRun`,
`TestAlertWriteRequiresConfirmation`, `TestAlertWriteInvalidTimes`,
`TestAlertWriteUpdateNoMatch`, `TestAlertWriteUpdateMultipleMatchesUsesFirst`,
`TestAlertWriteGeneratorURLScheme`, `TestAlertWriteDeleteIgnoresGeneratorURL`,
`TestAlertWriteAPIError`, `TestAlertWriteAuthHeaders`,
`TestAlertWriteSecretRedaction`, `TestBuildMatcherFilterEscapesSpecialChars`,
`TestBuildMatcherFilterOneMatcherPerLabel`,
`TestValidateMatcherLabelKeys`,
`TestAlertWriteUpdateRejectsInvalidLabelKeys`.

### 5.4 Imports for the test file

```go
import (
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/goccy/go-json"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)
```

(`models` no longer imported; `io`, `net/url` no longer imported.)

---

## 6. `components/tool/alertmanager/client_test.go`

### 6.1 Remove

- `TestPostAlerts` (entire function).
- `TestPostAlertsError` (entire function).
- In `TestSecretRedactionInErrors`: remove the second block that calls
  `c.PostAlerts(...)` (lines ~293–299). Keep the GET-path block
  (`c.ListAlerts`).
- In `TestSecretRedactionFromEchoedHeader`: remove the subtest
  `"redaction applies to POST errors too"` (lines ~375–395). Keep the two
  GET-path subtests.

### 6.2 Keep unchanged

`TestNewClient`, `TestBuildClients`, `TestListAlerts`, `TestListAlertsError`,
`TestAuthHeaders`, `TestNewClientRejectsNonHTTPSchemes`,
`TestRedactionTruncation`, `TestBuildRedactSecrets`,
`TestSecretRedactionDefaultCaseAPIError`, `TestNetworkErrorDoesNotLeakSecrets`,
`TestRedirectStripsAuthorization`, and the GET-only portions of
`TestSecretRedactionInErrors` / `TestSecretRedactionFromEchoedHeader`.

### 6.3 Imports

After removal, check whether `models` is still used (it is — in
`TestListAlerts`? No — `TestListAlerts` uses `*models.GettableAlert`
indirectly via `alerts[0].Labels` which is `models.LabelSet`. Actually
`TestListAlerts` does not reference `models` directly; it uses
`alerts[0].Labels["alertname"]` which works via map indexing on the
`models.LabelSet` type — but the type itself is referenced through the
return type, not by name. Verify by running `go build` after edits; if
`models` becomes unused, remove the import. `alert` is still used
(`*alert.GetAlertsInternalServerError`, `*alert.GetAlertsBadRequest` in
`TestListAlertsError` / `TestRedactionTruncation`). Keep `alert`.

---

## 7. `components/tool/alertmanager/check_test.go`

### 7.1 Update `TestCheckResultStatuses`

The set of acceptable statuses already includes `checkup.StatusOK`,
`checkup.StatusError`, `checkup.StatusLimited`. The write-tool result is
now `StatusOK` (was `StatusLimited`). The test already accepts `StatusOK`,
so no change is strictly required. **No change.**

### 7.2 No other changes

`TestCheckEmptyConfigs`, `TestCheckNilConfigs`, `TestCheckInvalidInstance`
(still expects 3 results, all `StatusError` — unchanged because
`clientErrorResults` still emits 3 names), `TestCheckClientErrorResults`
(still 3 results), `TestAllComponentNames` (still 3 names) — all unchanged.

> Optional: add a focused test `TestProbeInstanceWriteToolStatus` that
> builds a real client against an httptest server returning `[]` for
> `/api/v2/alerts` and asserts the write-tool result is `StatusOK` with
> message `"guidance tool, no external call required"`. This is
> recommended (low cost, locks the new behavior) but not strictly required
> since `TestCheckResultStatuses` already covers the status enum.

---

## 8. `components/tool/alertmanager/README.md`

### 8.1 Update the "Design" section

Replace the "Read + write split" bullet:

> - **Read + write split** — one read tool (`alertmanager_alert`) and one
>   write tool (`alertmanager_alert_write`), the latter gated by
>   `dryRun`/`confirmed`.

with:

> - **Read-only against Alertmanager** — the component only performs
>   `GET /api/v2/alerts` against the Alertmanager HTTP API. Alert
>   lifecycle (create / update / delete / silence) is managed declaratively
>   via Kubernetes CRDs (`PrometheusRule`, `Silence`, `AlertmanagerConfig`
>   from `monitoring.coreos.com`) through the `kubernetes_resource_*`
>   tools. The `alertmanager_alert_write` tool returns structured guidance
>   pointing the LLM to the right CRD and kubernetes tool, with a
>   pre-filled manifest skeleton; it never calls Alertmanager.

### 8.2 Update the tools table

| Tool Name | Type | Description |
|---|---|---|
| `alertmanager_instance_list` | Read | List configured Alertmanager instances |
| `alertmanager_alert` | Read | Read alerts (list or get-single via `fingerprint`) |
| `alertmanager_alert_write` | Guidance | Returns CRD guidance for create/update/delete intent; does NOT call Alertmanager |

### 8.3 Update the "Factory Functions" / `WriteToolNames` note

Replace:

> `alertmanager.WriteToolNames()` returns `["alertmanager_alert_write"]`.
> The write tool is gated by `dryRun`/`confirmed` (use `dryRun=true` to
> preview, then `confirmed=true` to execute).

with:

> `alertmanager.WriteToolNames()` returns `[]string{}` (the component has
> no mutating tools; `alertmanager_alert_write` is a non-mutating guidance
> tool). The safety middleware's `WriteToolNames` can be left empty.

### 8.4 Replace the `### alertmanager_alert_write` section

Replace the entire section with:

> ### alertmanager_alert_write
>
> Returns structured guidance for managing an alert via Kubernetes CRDs.
> **Does not call Alertmanager.** The required `operation` param selects
> the intent: `create`, `update`, or `delete`.
>
> | Parameter | Required | Description |
> |---|---|---|
> | `instance` | Yes | Alertmanager instance name (context only; no live call is made) |
> | `operation` | Yes | `create`, `update`, or `delete` |
> | `labels` | Yes | Alert labels; must include `alertname`. Pre-fills the example manifest |
> | `annotations` | No | Alert annotations. Pre-fills the example PrometheusRule rule annotations |
>
> Intent → CRD mapping:
>
> - `create` / `update` → `PrometheusRule`
>   (`apiVersion: monitoring.coreos.com/v1`) via `kubernetes_resource_apply`
>   (preferred), `kubernetes_resource_create`, or `kubernetes_resource_patch`.
> - `delete` → two options, both returned:
>   - (A) delete/update the generating `PrometheusRule` via
>     `kubernetes_resource_delete` (permanent);
>   - (B) apply a temporary `Silence` CRD
>     (`apiVersion: monitoring.coreos.com/v1alpha1`) via
>     `kubernetes_resource_apply` (expires at `endsAt`).
> - For alert routing/receivers (not alert lifecycle), use
>   `AlertmanagerConfig` (`apiVersion: monitoring.coreos.com/v1alpha1`)
>   via `kubernetes_resource_apply`.
>
> The output is a JSON object `AlertGuidanceOutput` with `action`,
> `message`, `recommendedTools`, `recommendedCRDs`, `examples` (one or more
> manifest skeletons with the recommended kubernetes tool), and `notes`.

### 8.5 Add a "Breaking changes" subsection at the end

> ### Breaking changes (this release)
>
> - `alertmanager_alert_write` no longer POSTs to the Alertmanager API.
>   It returns structured CRD guidance instead. The tool name is
>   preserved.
> - `AlertWriteParams` removed fields: `startsAt`, `endsAt`,
>   `generatorURL`, `dryRun`, `confirmed`. The confirmation gate is gone
>   because the tool no longer mutates anything.
> - `AlertWriteOutput` (Status/Action/Fingerprint/EndsAt) is replaced by
>   `AlertGuidanceOutput`.
> - `alertmanagerClient.PostAlerts` is removed.
> - `WriteToolNames()` now returns `[]string{}`.
> - `check.go` reports the write tool as `StatusOK` ("guidance tool, no
>   external call required") instead of `StatusLimited`.

---

## 9. Edge cases & validation summary

- **Operation validation**: enforced by `validate:"required,oneof=create update delete"`
  on `AlertWriteParams.Operation` via `validateParams` → `validate.Struct`.
  Error message contains `invalid parameters` (from the validate helper
  wrapper). The `default` switch branch in `Invoke` is unreachable but kept
  for defense-in-depth.
- **Missing `alertname`**: explicit check after `validateParams` returns
  `labels must include 'alertname'` (same message as before).
- **Empty labels**: rejected by `validate:"required,min=1"` on `Labels`.
- **Empty annotations**: allowed (`omitempty`); the example PrometheusRule
  rule omits the `annotations` key when annotations is empty/nil.
- **Mapping correctness**:
  - `create`/`update` → exactly one `PrometheusRule` example, tool
    `kubernetes_resource_apply`, action `apply`.
  - `delete` → two examples: `PrometheusRule` + `kubernetes_resource_delete`
    + action `delete`, and `Silence` + `kubernetes_resource_apply` + action
    `apply`.
  - `alertname` is never placed inside `PrometheusRule.spec.groups[].rules[].labels`
    (it is the `alert` field); `withoutAlertname` enforces this.
  - `alertname` is the first `Silence.spec.matchers` entry; remaining
    matchers are sorted by key for deterministic output.
- **Instance validation**: `t.client(params.Instance)` is called to keep
  the `instanceNotFoundError` behavior consistent with the read tool. This
  does NOT make an HTTP call (it only looks up the pre-built client in the
  map). If the user wants to avoid building clients entirely for the
  guidance tool, that is a follow-up out of scope here.
- **No external call**: the guidance tool must never hit Alertmanager. The
  `TestAlertWriteNoExternalCall` test enforces this regression invariant.

---

## 10. Symbol map (old → new / removed)

| Symbol | File | Status |
|---|---|---|
| `alertmanagerClient.PostAlerts` | `client.go` | **Removed** |
| `*alert.PostAlertsBadRequest` case in `redactAPIErrorPayload` | `client.go` | **Removed** |
| `*alert.PostAlertsInternalServerError` case in `redactAPIErrorPayload` | `client.go` | **Removed** |
| `labelNameRE` | `alert_write.go` | **Removed** |
| `buildMatcherFilter` | `alert_write.go` | **Removed** |
| `validateMatcherLabelKeys` | `alert_write.go` | **Removed** |
| `coalesceTime` | `alert_write.go` | **Removed** |
| `validateGeneratorURL` | `alert_write.go` | **Removed** |
| `AlertWriteTool.postAlert` | `alert_write.go` | **Removed** |
| `AlertWriteParams.StartsAt` | `alert_write.go` | **Removed** |
| `AlertWriteParams.EndsAt` | `alert_write.go` | **Removed** |
| `AlertWriteParams.GeneratorURL` | `alert_write.go` | **Removed** |
| `AlertWriteParams.DryRun` | `alert_write.go` | **Removed** |
| `AlertWriteParams.Confirmed` | `alert_write.go` | **Removed** |
| `AlertWriteOutput` | `alert_write.go` | **Replaced** by `AlertGuidanceOutput` |
| `alertWriteToolDescription` | `alert_write.go` | **Replaced** (new content) |
| `AlertWriteTool.Invoke` | `alert_write.go` | **Replaced** (guidance flow) |
| `NewAlertWriteTool` | `alert_write.go` | **Kept** (signature unchanged) |
| `RecommendedCRD` | `alert_write.go` | **New** |
| `ManifestExample` | `alert_write.go` | **New** |
| `AlertGuidanceOutput` | `alert_write.go` | **New** |
| `buildRuleGuidance` | `alert_write.go` | **New** |
| `buildDeleteGuidance` | `alert_write.go` | **New** |
| `withoutAlertname` | `alert_write.go` | **New** |
| `sanitizeRuleName` | `alert_write.go` | **New** |
| `toSilenceMatchers` | `alert_write.go` | **New** |
| `writeConstructors` | `registry.go` | **Emptied** (`[]toolConstructor{}`) |
| `readOnlyConstructors` | `registry.go` | **Appended** `NewAlertWriteTool` |
| `WriteToolNames()` | `registry.go` | **Changed** to return `[]string{}` |
| `probeInstance` write-tool result | `check.go` | **Changed** to `StatusOK` + "guidance tool, no external call required" |
| `allComponentNames` | `check.go` | **Unchanged** (3 names) |
| `TestPostAlerts` | `client_test.go` | **Removed** |
| `TestPostAlertsError` | `client_test.go` | **Removed** |
| POST subtests of `TestSecretRedactionInErrors` / `TestSecretRedactionFromEchoedHeader` | `client_test.go` | **Removed** |
| All old `TestAlertWrite*` | `alert_write_test.go` | **Replaced** by new guidance tests |

---

## 11. Verification checklist

Run from the repo root:

```bash
# Build everything (catches import / unused-symbol issues)
go build ./...

# Vet
go vet ./...

# Alertmanager tests (the focus of this change)
go test ./components/tool/alertmanager/...

# Kubernetes tests (must still pass — we did not change them, but the
# guidance references their tool names; the envtest-based tests have a
# pre-existing limitation: they require a running envtest control plane
# and may be skipped if KUBEBUILDER_ASSETS is unset. That is pre-existing
# and not introduced by this change.)
go test ./components/tool/kubernetes/...
```

CONTRIBUTING.md checklist (apply to the changed files):

- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass.
- [ ] No new `Config` introduced; existing `Config` validation unchanged.
- [ ] `NewAlertWriteTool` still accepts `ctx context.Context` first and
      threads it to `newBaseTool` (unchanged signature).
- [ ] No license banner added.
- [ ] Errors wrapped with `emperror.dev/errors` (the `errors.Errorf` calls
      for `alertname` / `unsupported operation` are preserved; no new
      error paths that need wrapping — the guidance path returns `nil`
      error).
- [ ] Naming: `AlertGuidanceOutput`, `RecommendedCRD`, `ManifestExample`,
      `buildRuleGuidance`, `buildDeleteGuidance` follow Go conventions.
- [ ] README updated; tools table, factory notes, and a Breaking changes
      subsection added.
- [ ] `check.go` + `check_test.go` reflect the new guidance-tool status.
- [ ] No duplication of `libs/toolkit/` helpers (`marshal.MustMarshal`,
      `validate.Struct` via `validateParams`, `marshalString` reused).
- [ ] No new dependency added; `prometheus/alertmanager` kept (read
      client still uses `models.GettableAlert`, `alert.GetAlerts*`).
- [ ] `go.mod` / `go.sum` unchanged.

### Specific regression assertions the tests must cover

- The Alertmanager HTTP API is never called by `alertmanager_alert_write`
  for any operation (`TestAlertWriteNoExternalCall`).
- Read-path redaction still works: `TestSecretRedactionInErrors` (GET
  only), `TestSecretRedactionFromEchoedHeader` (GET only),
  `TestRedactionTruncation`, `TestSecretRedactionDefaultCaseAPIError`,
  `TestNetworkErrorDoesNotLeakSecrets`, `TestRedirectStripsAuthorization`
  all pass unchanged.
- `alertmanager_alert` (`alert.go`, `alert_test.go`) is untouched and its
  tests pass unchanged.
- `WriteToolNames()` returns a non-nil empty slice.
- `NewAllTools` returns 3 tools; `NewReadOnlyTools` returns 3 tools.
- `check.go` emits `StatusOK` for `alertmanager_alert_write`.
