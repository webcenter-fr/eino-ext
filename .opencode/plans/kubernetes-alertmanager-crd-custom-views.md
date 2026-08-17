# Plan: Custom views (curated formatters) for prometheus-operator alerting CRDs in the Kubernetes component

## Goal

In `components/tool/kubernetes/` (package `kubernetes`), add curated, type-specific
output formatters for BOTH the `list` tool and the `describe` (single-resource
get) tool for the four prometheus-operator "alerting" custom resources from
`monitoring.coreos.com`:

| Kind               | Group                    | Version  | Scope      |
|--------------------|--------------------------|----------|------------|
| `Alertmanager`     | `monitoring.coreos.com`  | `v1`     | namespaced |
| `AlertmanagerConfig` | `monitoring.coreos.com` | `v1alpha1` | namespaced |
| `PrometheusRule`   | `monitoring.coreos.com`  | `v1`     | namespaced |
| `Silence`          | `monitoring.coreos.com`  | `v1alpha1` | namespaced |

All four are namespaced (confirmed against the prometheus-operator CRDs and the
existing `components/tool/alertmanager/alert_write.go` guidance, which marks all
three it references as `Namespaced: true`; `Silence` is also namespaced per the
same operator CRD family).

When a user `list`s or `describe`s (gets) one of these kinds, they receive a
curated, human/LLM-friendly summary instead of the generic
`name/namespace/status` fallback (list) or the raw `metadata/spec/status/data`
dump (describe). The curated view surfaces ALERT-relevant information.

## Scope / out of scope

**In scope** (this plan):
- `components/tool/kubernetes/formatters.go` (extend registry + dispatch)
- `components/tool/kubernetes/describe.go` (custom-view dispatch + metadata helper)
- `components/tool/kubernetes/formatters_monitoring.go` (NEW — 4 list + 4 describe formatters + helpers + registration)
- `components/tool/kubernetes/formatters_monitoring_test.go` (NEW — pure unit tests)
- `components/tool/kubernetes/README.md` (count 24 → 28; document the custom view)
- `components/tool/kubernetes/check.go` (informational message at line ~96)

**Out of scope**:
- `components/tool/alertmanager/` — do NOT touch. That component already
  references these CRDs in its guidance tool (`alert_write.go`); this plan only
  adds curated *output* views to the Kubernetes component.
- No new tools. No new tool params. No new `Configs`/`ClusterConfig` fields.
- No `go.mod` / `go.sum` changes (unstructured approach — see decision #1).
- No envtest/CRD-installation changes in `suite_test.go`.

---

## Key design decisions

### Decision #1 — Typed vs unstructured formatters: **UNSTRUCTURED for all 4 kinds.**

Rationale:
1. **The `Silence` CRD has no Go API type** in
   `github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1`.
   The v1alpha1 directory contains only `alertmanager_config_types.go`,
   `prometheusagent_types.go`, `scrapeconfig_types.go`, `register.go`,
   `doc.go`, `validation*.go`, `zz_generated.deepcopy.go` — there is no
   `silence_types.go`. (Verified via the GitHub directory listing for
   `pkg/apis/monitoring/v1alpha1`.) A typed approach would therefore force a
   **mixed** typed/unstructured implementation (typed for 3 kinds, unstructured
   for `Silence`), which is inconsistent and harder to maintain.
2. **Dependency weight.** `github.com/prometheus-operator/prometheus-operator`
   is NOT present in `go.mod`/`go.sum` today (verified). Adding it pulls the
   full operator module into the module graph. The `pkg/apis` packages are
   relatively self-contained (depend on `k8s.io/apimachinery`/`k8s.io/api`,
   already present), but the module download and `go mod tidy` churn is
   disproportionate for four read-only field-projecting formatters.
3. **The unstructured pattern already exists** in this package:
   `defaultListFormatter(u *unstructured.Unstructured)` reads
   `u.Object["status"].(map[string]any)["conditions"]` directly. The new
   formatters follow the same nil-safe map-access idiom.
4. **Formatters are read-only field projection.** No type-safety benefit that
   outweighs the dependency cost; field names are verified against the
   prometheus-operator API docs (Context7) and the existing
   `alert_write.go` manifest skeletons.

**Result:** `go.mod` and `go.sum` are unchanged. No new imports beyond
`k8s.io/apimachinery/pkg/apis/meta/v1/unstructured` (already imported in
`formatters.go`) and `metav1` (already imported in `describe.go`).

### Decision #2 — Describe custom-view mechanism: **extend `formatterEntry` with an optional `describe` field; dispatch in `DescribeTool.Invoke` before the raw path.**

The existing `formatterEntry` is:
```go
type formatterEntry struct {
    newObj func() runtime.Object
    format listFormatter
}
```

Extend it to:
```go
type formatterEntry struct {
    newObj   func() runtime.Object      // nil for unstructured-only kinds
    format   listFormatter               // list view (required)
    describe describeFormatter           // optional curated describe view
}
```

New type:
```go
// describeFormatter produces a curated describeOutput for a single resource.
// When nil, DescribeTool falls back to the raw metadata/spec/status/data dump.
type describeFormatter func(*unstructured.Unstructured) describeOutput
```

`DescribeTool.Invoke` dispatch logic (inserted after the Get succeeds and after
the existing secret-redaction special case, before building the raw
`describeOutput`):

```go
if entry, ok := formatterRegistry[resolved.GVK]; ok && entry.describe != nil {
    output := entry.describe(o)
    if err := output.applyFieldExclusions(params.ExcludeFieldsOutput); err != nil {
        return "", err
    }
    data, err := json.Marshal(output)
    if err != nil {
        return "", errors.Wrap(err, "failed to marshal output")
    }
    return string(data), nil
}
// ... existing raw path unchanged ...
```

This is backward-compatible: every existing registry entry has
`describe == nil`, so all other kinds keep the raw `metadata/spec/status/data`
behavior. The 4 new monitoring entries set `describe`.

### Decision #2b — Reconciling `excludeFieldsOutput` with the custom view

The curated describe output is shaped to **mirror the raw `describeOutput`
sections** (`metadata`, `spec`, `status`) so the existing
`applyFieldExclusions` (which sets `metadata|spec|status|data` to nil) keeps
working unchanged. Concretely, each curated describe formatter returns a
`describeOutput` whose `Metadata`, `Spec`, and `Status` fields are populated
with **curated** sub-structs (not the raw maps), but the field names match the
exclusion contract.

Behavior:
- `excludeFieldsOutput: ["metadata"]` on a curated describe → the curated
  metadata block is dropped (the `metadata` JSON key is omitted via `omitempty`
  after `applyFieldExclusions` sets it to nil). Same for `spec` / `status`.
- `excludeFieldsOutput: ["data"]` on a curated describe → no-op (curated views
  never populate `Data`); still accepted (no error) for contract stability.
- The prompt guidance in `prompts/describe_output_guidance.md` remains accurate
  as-is (it already says you can drop `metadata|spec|status|data`).

To make this work, `describeOutput.Metadata` must change from
`*metav1.ObjectMeta` to `any` so curated views can assign their own metadata
shape. This is a package-private type; JSON output for the raw path is
unchanged (marshaling `any` holding `*metav1.ObjectMeta` produces identical
JSON to marshaling a `*metav1.ObjectMeta` field). `omitempty` on `any` omits
only a nil interface, which matches the existing behavior (a non-nil pointer
is always included).

### Decision #3 — Curated field schemas (list + describe) for each kind

Field names below are JSON tags. All accessors are nil-safe (missing spec /
status / nil pointers / empty slices produce zero values, never panics).
Numbers from unstructured JSON arrive as `float64` and are converted to `int32`
via a helper.

#### Shared metadata helper (used by all 4 describe formatters + the raw path)

Extract from the current `DescribeTool.Invoke` into a reusable helper in
`describe.go`:

```go
// unstructuredMetadata builds a *metav1.ObjectMeta from an unstructured
// resource. Shared by the raw describe path and the curated describe
// formatters so the metadata block is consistent across all kinds.
func unstructuredMetadata(o *unstructured.Unstructured) *metav1.ObjectMeta {
    return &metav1.ObjectMeta{
        Name:              o.GetName(),
        Namespace:         o.GetNamespace(),
        Labels:            o.GetLabels(),
        Annotations:       o.GetAnnotations(),
        OwnerReferences:   o.GetOwnerReferences(),
        ResourceVersion:   o.GetResourceVersion(),
        CreationTimestamp: o.GetCreationTimestamp(),
        DeletionTimestamp: o.GetDeletionTimestamp(),
    }
}
```

The raw path in `describe.go` is refactored to call `unstructuredMetadata(o)`
instead of inlining the struct literal (no behavior change).

#### 3.1 `Alertmanager` (monitoring.coreos.com/v1)

Spec fields (verified): `replicas` (*int32 → 0 if nil), `version` (string),
`image` (string), `paused` (bool), `logLevel` (string), `resources`
(corev1.ResourceRequirements — surfaced as the raw map for describe, omitted
from list), `storage` (StorageSpec — raw map for describe, omitted from list).
Status fields: `paused` (bool), `replicas` (int32), `updatedReplicas` (int32),
`availableReplicas` (int32), `unavailableReplicas` (int32), `conditions`
([]Condition: type, status, reason, message, lastTransitionTime).

**List view** (`formatListItem`):
```go
struct {
    Name              string `json:"name"`
    Namespace         string `json:"namespace"`
    Replicas          int32  `json:"replicas"`           // spec.replicas (0 if nil)
    Version           string `json:"version,omitempty"`  // spec.version
    Paused            bool   `json:"paused"`             // spec.paused
    AvailableReplicas int32  `json:"availableReplicas"`  // status.availableReplicas
    Status            string `json:"status,omitempty"`   // derived
}
```
Derived `status`: `"Paused"` if `spec.paused`; else `"Available"` if
`status.availableReplicas >= spec.replicas && spec.replicas > 0`; else
`"Degraded"` if `status.unavailableReplicas > 0`; else `""`.

**Describe view** (`describeFormatter`):
```go
describeOutput{
    TypeMeta: {Kind: "Alertmanager", APIVersion: "monitoring.coreos.com/v1"},
    Metadata: unstructuredMetadata(o),                       // *metav1.ObjectMeta
    Spec: alertmanagerSpecView{                               // curated struct
        Replicas          int32                `json:"replicas"`
        Version           string               `json:"version,omitempty"`
        Image             string               `json:"image,omitempty"`
        Paused            bool                 `json:"paused"`
        LogLevel          string               `json:"logLevel,omitempty"`
        Resources         map[string]any       `json:"resources,omitempty"`  // raw
        Storage           map[string]any       `json:"storage,omitempty"`    // raw
    },
    Status: alertmanagerStatusView{
        Paused            bool                 `json:"paused"`
        Replicas          int32                `json:"replicas"`
        UpdatedReplicas   int32                `json:"updatedReplicas"`
        AvailableReplicas int32                `json:"availableReplicas"`
        UnavailableReplicas int32              `json:"unavailableReplicas"`
        Conditions        []conditionView      `json:"conditions,omitempty"`
    },
}
```
`conditionView{Type,Status,Reason,Message,LastTransitionTime string}`.

#### 3.2 `AlertmanagerConfig` (monitoring.coreos.com/v1alpha1)

Spec fields: `route` (Route: `receiver`, `groupBy` ([]string), `groupWait`,
`groupInterval`, `repeatInterval`, `routes` ([]child Route)), `receivers`
([]Receiver: `name` + one or more typed config slices:
`emailConfigs`, `webhookConfigs`, `slackConfigs`, `pagerDutyConfigs`,
`opsgenieConfigs`, `pushoverConfigs`, `victoropsConfigs`, `wechatConfigs`,
`telegramConfigs`, `snsConfigs`, `webexConfigs`, `discordConfigs`,
`msteamsConfigs`, `rocketchatConfigs`, `threemaConfigs`,
`alertmanagerConfigs`), `inhibitRules` ([]InhibitRule).
Status (newer subresource, optional): `bindings[].conditions[]` with
`type`/`status`/`reason`/`message`.

**Redaction note:** AlertmanagerConfig receivers reference Kubernetes Secrets
**by name** (e.g. `slackConfigs[].apiURL.name/key`, `emailConfigs[].authPassword.name/key`)
but never contain secret *data*. The curated view surfaces only receiver names
and config-type tags, never secret payloads. **No redaction is required.**
(State this explicitly in code comments and README.)

**List view**:
```go
struct {
    Name      string   `json:"name"`
    Namespace string   `json:"namespace"`
    Receiver  string   `json:"receiver,omitempty"`     // spec.route.receiver
    Receivers []string `json:"receivers,omitempty"`    // names of spec.receivers
    Routes    int      `json:"routes"`                 // len(spec.route.routes)
}
```

**Describe view**:
```go
describeOutput{
    TypeMeta: {Kind: "AlertmanagerConfig", APIVersion: "monitoring.coreos.com/v1alpha1"},
    Metadata: unstructuredMetadata(o),
    Spec: alertmanagerConfigSpecView{
        Route         *routeView           `json:"route,omitempty"`
        Receivers     []receiverView       `json:"receivers,omitempty"`
        InhibitRules  []map[string]any     `json:"inhibitRules,omitempty"`  // raw
    },
    Status: alertmanagerConfigStatusView{
        Conditions []conditionView `json:"conditions,omitempty"`  // flattened from status.bindings[].conditions
    },
}
```
- `routeView{Receiver string; GroupBy []string; GroupWait, GroupInterval, RepeatInterval string; Routes []routeView}` (recursive, but cap depth at 2 to avoid cycles — see edge cases).
- `receiverView{Name string; Types []string}` where `Types` is the set of
  non-empty config slice keys (e.g. `["webhook","slack"]`), sorted
  alphabetically for deterministic output.

#### 3.3 `PrometheusRule` (monitoring.coreos.com/v1)

Spec fields: `groups` ([]RuleGroup: `name`, `interval` (string), `rules`
([]Rule: `alert` (string, optional — recording rules use `record` instead),
`record` (string, optional), `expr` (string, required), `for` (string),
`labels` (map[string]string), `annotations` (map[string]string))).
Status (newer subresource, optional): `bindings[].conditions[]`.

**List view**:
```go
struct {
    Name       string   `json:"name"`
    Namespace  string   `json:"namespace"`
    Groups     int      `json:"groups"`               // len(spec.groups)
    Rules      int      `json:"rules"`                // total rule count
    Alerts     []string `json:"alerts,omitempty"`     // rule.alert where alert != ""
    Severities []string `json:"severities,omitempty"`// unique labels.severity, sorted
}
```

**Describe view**:
```go
describeOutput{
    TypeMeta: {Kind: "PrometheusRule", APIVersion: "monitoring.coreos.com/v1"},
    Metadata: unstructuredMetadata(o),
    Spec: prometheusRuleSpecView{
        Groups []ruleGroupView `json:"groups,omitempty"`
    },
    Status: prometheusRuleStatusView{
        Conditions []conditionView `json:"conditions,omitempty"`
    },
}
```
- `ruleGroupView{Name string; Interval string; Rules []ruleView}`.
- `ruleView{Alert string; Record string; Expr string; For string; Severity string; Labels map[string]string; Annotations map[string]string}` — `Severity` is hoisted from `labels.severity` for quick scanning; `Labels`/`Annotations` are the full maps (kept because describe is the detailed path).

#### 3.4 `Silence` (monitoring.coreos.com/v1alpha1)

Spec fields (verified against `alert_write.go` manifest skeleton):
`matchers` ([]Matcher: `name`, `value`, `isRegex` (bool)), `startsAt` (string,
RFC3339), `endsAt` (string, RFC3339), `createdBy` (string), `comment` (string).
Status: `state` (string: `active`/`pending`/`expired`), optionally `conditions`.

**List view**:
```go
struct {
    Name      string        `json:"name"`
    Namespace string        `json:"namespace"`
    State     string        `json:"state,omitempty"`      // status.state
    Matchers  []matcherView `json:"matchers,omitempty"`  // spec.matchers
    StartsAt  string        `json:"startsAt,omitempty"`
    EndsAt    string        `json:"endsAt,omitempty"`
    CreatedBy string        `json:"createdBy,omitempty"`
}
```
- `matcherView{Name string; Value string; IsRegex bool}`.

**Describe view**:
```go
describeOutput{
    TypeMeta: {Kind: "Silence", APIVersion: "monitoring.coreos.com/v1alpha1"},
    Metadata: unstructuredMetadata(o),
    Spec: silenceSpecView{
        Matchers  []matcherView `json:"matchers,omitempty"`
        StartsAt  string        `json:"startsAt,omitempty"`
        EndsAt    string        `json:"endsAt,omitempty"`
        CreatedBy string        `json:"createdBy,omitempty"`
        Comment   string        `json:"comment,omitempty"`
    },
    Status: silenceStatusView{
        State      string         `json:"state,omitempty"`
        Conditions []conditionView `json:"conditions,omitempty"`
    },
}
```

---

## File-by-file changes

### 1. `components/tool/kubernetes/formatters.go` (modify)

**1.1 Extend `formatterEntry` and add `describeFormatter` type** (near line 24–29):

```go
type listFormatter func(runtime.Object) json.RawMessage

// describeFormatter produces a curated describeOutput for a single resource.
// When nil, DescribeTool falls back to the raw metadata/spec/status/data dump.
type describeFormatter func(*unstructured.Unstructured) describeOutput

type formatterEntry struct {
    newObj   func() runtime.Object // nil for unstructured-only kinds
    format   listFormatter         // list view (required)
    describe describeFormatter     // optional curated describe view
}
```

**1.2 `formatListItem` — handle `newObj == nil` (unstructured path).** Replace
the block at lines 686–691:

```go
dst := entry.newObj()
if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, dst); err != nil {
    return defaultListFormatter(u)
}
return entry.format(dst)
```

with:

```go
if entry.newObj == nil {
    // Unstructured-only formatter (e.g. monitoring CRDs without typed deps).
    return entry.format(u)
}
dst := entry.newObj()
if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, dst); err != nil {
    return defaultListFormatter(u)
}
return entry.format(dst)
```

The 4 new monitoring list formatters type-assert `o.(*unstructured.Unstructured)`
inside `format` (they are registered with `newObj: nil`).

**1.3 Register the 4 monitoring entries.** At the end of
`initFormatterRegistry`, just before `return reg`:

```go
registerMonitoringFormatters(reg)
return reg
```

No other changes to `formatters.go`.

### 2. `components/tool/kubernetes/describe.go` (modify)

**2.1 Change `describeOutput.Metadata` type from `*metav1.ObjectMeta` to `any`:**

```go
type describeOutput struct {
    metav1.TypeMeta `json:",inline"`
    Metadata        any `json:"metadata,omitempty"`
    Spec            any `json:"spec,omitempty"`
    Status          any `json:"status,omitempty"`
    Data            any `json:"data,omitempty"`
}
```

`applyFieldExclusions` is unchanged (it already sets the fields to nil; works
for `any`).

**2.2 Extract `unstructuredMetadata` helper** (see Decision #3 shared helper).
Add it in `describe.go`. Refactor the raw path to use it:

```go
output := describeOutput{
    TypeMeta: metav1.TypeMeta{
        Kind:       o.GetKind(),
        APIVersion: o.GetAPIVersion(),
    },
    Metadata: unstructuredMetadata(o),
    Spec:     o.Object["spec"],
    Status:   o.Object["status"],
    Data:     o.Object["data"],
}
```

**2.3 Add custom-view dispatch.** After the secret-redaction block (line ~101)
and before building the raw `describeOutput`, insert:

```go
if entry, ok := formatterRegistry[resolved.GVK]; ok && entry.describe != nil {
    output := entry.describe(o)
    if err := output.applyFieldExclusions(params.ExcludeFieldsOutput); err != nil {
        return "", err
    }
    data, err := json.Marshal(output)
    if err != nil {
        return "", errors.Wrap(err, "failed to marshal output")
    }
    return string(data), nil
}
```

No other changes to `describe.go`. The `metav1` import remains used (TypeMeta,
ObjectMeta). No new imports.

### 3. `components/tool/kubernetes/formatters_monitoring.go` (NEW)

Package `kubernetes`. Contains:

**3.1 Unstructured access helpers** (nil-safe). These are package-private and
collocated with the monitoring formatters. If a future non-monitoring
unstructured formatter needs them, promote to a shared `formatters_helpers.go`
(follow-up, out of scope here).

```go
func uSpec(u *unstructured.Unstructured) map[string]any   // u.Object["spec"].(map[string]any), nil-safe
func uStatus(u *unstructured.Unstructured) map[string]any // u.Object["status"].(map[string]any), nil-safe
func uString(m map[string]any, key string) string
func uBool(m map[string]any, key string) bool
func uInt32(m map[string]any, key string) int32          // handles float64/int64/int
func uMap(m map[string]any, key string) map[string]any
func uSlice(m map[string]any, key string) []any
func uStringSlice(m map[string]any, key string) []string // each element asserted string; non-strings skipped
```

**3.2 Shared view sub-structs** (used across the 4 describe formatters):

```go
type conditionView struct {
    Type              string `json:"type"`
    Status            string `json:"status"`
    Reason            string `json:"reason,omitempty"`
    Message           string `json:"message,omitempty"`
    LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}
type matcherView struct {
    Name    string `json:"name"`
    Value   string `json:"value"`
    IsRegex bool   `json:"isRegex"`
}
```

Plus the per-kind view structs listed in Decision #3
(`alertmanagerSpecView`, `alertmanagerStatusView`, `routeView`,
`receiverView`, `alertmanagerConfigSpecView`, `alertmanagerConfigStatusView`,
`ruleGroupView`, `ruleView`, `prometheusRuleSpecView`,
`prometheusRuleStatusView`, `silenceSpecView`, `silenceStatusView`).

**3.3 `extractConditions(status map[string]any) []conditionView`** — handles
two layouts: (a) `status.conditions[]` (Alertmanager), (b)
`status.bindings[].conditions[]` (AlertmanagerConfig / PrometheusRule newer
status subresource). Flattens into a single `[]conditionView`. Nil-safe.

**3.4 The 4 list formatters** — each is a `listFormatter` (signature
`func(runtime.Object) json.RawMessage`) that type-asserts
`*unstructured.Unstructured` and builds the curated struct via the helpers,
then `marshal.MustMarshal`. On a failed type assertion (should never happen via
`formatListItem`), fall back to `defaultListFormatter(u)`.

**3.5 The 4 describe formatters** — each is a `describeFormatter` (signature
`func(*unstructured.Unstructured) describeOutput`) building the curated
`describeOutput` per Decision #3.

**3.6 `registerMonitoringFormatters(reg map[schema.GroupVersionKind]formatterEntry)`**
— registers the 4 GVKs with `newObj: nil`, the list `format`, and the
`describe` formatter:

```go
func registerMonitoringFormatters(reg map[schema.GroupVersionKind]formatterEntry) {
    reg[schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "Alertmanager"}] = formatterEntry{
        newObj: nil, format: formatAlertmanagerList, describe: describeAlertmanager,
    }
    reg[schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}] = formatterEntry{
        newObj: nil, format: formatAlertmanagerConfigList, describe: describeAlertmanagerConfig,
    }
    reg[schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}] = formatterEntry{
        newObj: nil, format: formatPrometheusRuleList, describe: describePrometheusRule,
    }
    reg[schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "Silence"}] = formatterEntry{
        newObj: nil, format: formatSilenceList, describe: describeSilence,
    }
}
```

**3.7 Imports** for the new file:
```go
import (
    "sort"

    "github.com/goccy/go-json"
    "github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/schema"
)
```
(`json` is used by `listFormatter`'s `json.RawMessage` return type;
`runtime` for the `listFormatter` signature; `schema` only if needed —
`registerMonitoringFormatters` takes the map so `schema` is used here.)

### 4. `components/tool/kubernetes/check.go` (modify, informational)

Line ~96 currently:
```go
Message: "CRD-only kinds (kafka, olm, ocp, spark) tested with dedicated env",
```
Update to:
```go
Message: "CRD-only kinds (kafka, olm, ocp, spark, monitoring.coreos.com) tested with dedicated env",
```
No behavioral change (still `StatusLimited`). No test impact (`check_test.go`
does not assert this message text).

### 5. `components/tool/kubernetes/README.md` (modify)

- Line 14: change `24 resource types` → `28 resource types`.
- Add a bullet to the "Design" section:
  > - **Curated describe views** — the `describe` tool emits a curated,
  >   ALERT-relevant summary for the prometheus-operator CRDs (`Alertmanager`,
  >   `AlertmanagerConfig`, `PrometheusRule`, `Silence` from
  >   `monitoring.coreos.com`). Other kinds fall back to the raw
  >   `metadata/spec/status/data` dump. `excludeFieldsOutput` still applies to
  >   the curated sections (`metadata`/`spec`/`status`).
- In the "Available Tools" table, append to the `kubernetes_describe` row:
  `; curated views for monitoring.coreos.com alerting CRDs`.
- Add a short subsection under "Design" listing the 4 kinds and the alert
  fields surfaced (one line each), noting that `AlertmanagerConfig` receivers
  reference Secrets by name only (no secret data in the CRD → no redaction
  needed).

### 6. `components/tool/kubernetes/formatters_monitoring_test.go` (NEW)

Pure unit tests (no envtest, no live cluster) — matches the style of
`validate_manifest_test.go` and `check_test.go` (table-driven, `testify/assert`).
Build `*unstructured.Unstructured` from `map[string]any` and call
`formatListItem` / the registered `describe` formatter directly.

**Required test cases:**

1. `TestFormatAlertmanagerList` — golden: replicas=3, version set, paused=false,
   availableReplicas=3 → asserts `replicas`, `version`, `paused`,
   `availableReplicas`, `status=="Available"`.
2. `TestFormatAlertmanagerList_Paused` — `spec.paused=true` → `status=="Paused"`,
   `paused==true`.
3. `TestFormatAlertmanagerList_NilReplicas` — `spec.replicas` absent →
   `replicas==0`, `status==""` (no crash).
4. `TestFormatAlertmanagerList_Degraded` — `unavailableReplicas=1` →
   `status=="Degraded"`.
5. `TestDescribeAlertmanager` — asserts TypeMeta, metadata name/namespace,
   spec fields, status conditions parsed into `conditionView`.
6. `TestDescribeAlertmanager_ExcludeSpec` — `excludeFieldsOutput=["spec"]` →
   output JSON has no `spec` key.
7. `TestDescribeAlertmanager_ExcludeMetadata` — `excludeFieldsOutput=["metadata"]`
   → no `metadata` key.
8. `TestFormatAlertmanagerConfigList` — `route.receiver`, `receivers` names,
   `routes` count.
9. `TestDescribeAlertmanagerConfig` — `route` view, `receivers` with `Types`
   (e.g. `["webhook","slack"]` sorted), `inhibitRules` raw, status conditions
   flattened from `status.bindings[].conditions[]`.
10. `TestDescribeAlertmanagerConfig_NoRedaction` — receiver with
    `slackConfigs[].apiURL` as a `secretKeyRef` → output contains the secret
    **name** but no secret payload (assert the `apiURL` field, if surfaced at
    all, is the raw map without redaction; the test documents that no
    redaction is needed because the CRD never holds secret data).
11. `TestFormatPrometheusRuleList` — `groups` count, `rules` total, `alerts`
    names, `severities` sorted unique.
12. `TestFormatPrometheusRuleList_RecordingRules` — rules with `record` and no
    `alert` → `alerts` omits them, `rules` count includes them.
13. `TestDescribePrometheusRule` — `ruleView` per rule with `alert`, `expr`,
    `for`, `severity` hoisted, `labels`, `annotations`.
14. `TestFormatSilenceList` — `state` from `status.state`, `matchers`,
    `startsAt`, `endsAt`, `createdBy`.
15. `TestFormatSilenceList_NoStatus` — `status` absent → `state==""`, no crash.
16. `TestDescribeSilence` — full curated spec + status.
17. `TestDescribeSilence_ExcludeStatus` — `excludeFieldsOutput=["status"]` →
    no `status` key.
18. `TestRegistryMonitoringEntries` — assert the 4 GVKs are present in
    `formatterRegistry` with `format != nil` and `describe != nil` and
    `newObj == nil`.
19. `TestFormatListItem_FallbackUnknownGVK` — an unstructured with an
    unregistered kind → `defaultListFormatter` output (name/namespace/status).
20. `TestFormatListItem_FallbackMissingAPIVersion` — `apiVersion==""` →
    `defaultListFormatter`.
21. `TestDescribeRawPathUnchangedForConfigMap` — regression: a ConfigMap
    unstructured (no `describe` entry) → raw `describeOutput` with
    `metadata`/`spec`/`status`/`data` (ensures the dispatch did not break the
    raw path). Build the `describeOutput` via the existing raw-path code by
    calling `DescribeTool.Invoke` is not feasible without envtest; instead
    assert that `formatterRegistry[ConfigMapGVK].describe == nil` (so the raw
    path is taken) and that `describeOutput` with `Metadata any` still
    marshals a ConfigMap correctly.
22. `TestUnstructuredHelpers` — table-driven: `uInt32` on `float64(3)` → 3,
    `uInt32` on nil map → 0, `uStringSlice` on mixed types skips non-strings,
    `uMap` on absent key → nil.

**Test framework:** `testify/assert` (matches `check_test.go`,
`cluster_list_test.go`, `validate_manifest_test.go`). No Ginkgo/Gomega in this
package (the suite uses `testify/suite` in `suite_test.go` for envtest tests;
the new tests are plain `func TestX(t *testing.T)` and do not need the suite).

**No envtest tests added.** Installing the 4 CRDs into envtest would require
CRD manifests in `CRDDirectoryPaths` and is disproportionate for testing
pure field-projection logic. The existing `TestConsolidatedListAndDescribe`
(ConfigMap) continues to exercise the raw describe path end-to-end.

---

## Edge cases & nil handling (all covered by the helpers)

- **Nil spec / status:** `uSpec`/`uStatus` return nil maps; all `uXxx` helpers
  return zero values on a nil map. No panics.
- **Nil pointer fields** (e.g. `Alertmanager.spec.replicas` absent): `uInt32`
  returns `0`.
- **Empty slices:** surfaced as `nil` in the curated struct → omitted via
  `omitempty` (e.g. `AlertmanagerConfig` with no receivers → `receivers`
  omitted).
- **Numbers from JSON:** arrive as `float64`; `uInt32` converts via
  `int32(v)` after type-switching on `float64`/`int64`/`int`.
- **Cluster-scoped vs namespaced:** all 4 are namespaced (confirmed). The
  `describe`/`list` tools already handle scope via `resolved.Scoped`; no
  per-kind scope logic needed.
- **Unknown GVK fallback:** `formatListItem` falls back to
  `defaultListFormatter`; `DescribeTool.Invoke` falls back to the raw path
  when `entry.describe == nil` or the GVK is not registered.
- **Unstructured conversion failure:** N/A for the 4 new entries (no
  `newObj`, no `FromUnstructured` call). For existing typed entries, the
  existing fallback to `defaultListFormatter` is preserved.
- **AlertmanagerConfig route recursion:** `routeView.Routes` is recursive. Cap
  nesting at depth 2 (flatten deeper child routes into the parent's `Routes`
  list with a `receiver`-only summary) to bound output size and prevent cycles
  in malformed objects. Implement with a depth parameter on the route builder.
- **Secret redaction:** the 4 CRDs do not contain secret data. The existing
  `secret` kind redaction in `describe.go` is untouched and still runs before
  the custom-view dispatch (it is gated on `strings.ToLower(resolved.GVK.Kind)
  == "secret"`, which is false for the 4 new kinds). **No redaction needed**
  for `AlertmanagerConfig` (references secrets by name only).
- **`excludeFieldsOutput: ["data"]` on a curated describe:** no-op (curated
  views never set `Data`); accepted without error for contract stability.

## Error handling & validation

- **No new params.** `DescribeParams` and `ListParams` are unchanged.
- **`validate.Struct`** is already called in `DescribeTool.Invoke` and
  `ListTool.Invoke`; the `excludeFieldsOutput` `oneof=metadata spec status data`
  tag still validates the exclusions. No change.
- **Error wrapping:** the new dispatch in `describe.go` wraps the marshal
  error with `emperror.dev/errors` (matching the existing raw-path line). The
  `applyFieldExclusions` error path is unchanged. The formatters themselves
  use `marshal.MustMarshal` (which never returns an error to the caller — it
  encodes marshal errors into the JSON string), consistent with all existing
  formatters.
- **No new `Config` struct** → no `validate.Struct(cfg)` call needed in a
  constructor (no new constructor).

## go.mod / go.sum

**No changes.** The unstructured approach uses only modules already in
`go.mod` (`k8s.io/apimachinery`, `k8s.io/api`, `github.com/goccy/go-json`,
`emperror.dev/errors`, `github.com/webcenter-fr/eino-ext/libs/toolkit/marshal`).
`github.com/prometheus-operator/prometheus-operator` is NOT added.

---

## Verification

Run from the repo root:

```bash
# Build everything (catches import / unused-symbol / type issues)
go build ./...

# Vet
go vet ./...

# New formatter unit tests (pure, no envtest)
go test ./components/tool/kubernetes/ -run 'TestFormatAlertmanager|TestDescribeAlertmanager|TestFormatAlertmanagerConfig|TestDescribeAlertmanagerConfig|TestFormatPrometheusRule|TestDescribePrometheusRule|TestFormatSilence|TestDescribeSilence|TestRegistryMonitoringEntries|TestFormatListItem_Fallback|TestDescribeRawPathUnchangedForConfigMap|TestUnstructuredHelpers'

# Full kubernetes package (envtest tests require KUBEBUILDER_ASSETS; the new
# tests do not)
go test ./components/tool/kubernetes/...

# Component completeness check (README + test file present)
bash scripts/check_components.sh
```

CONTRIBUTING.md checklist (apply to changed files):
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass.
- [ ] No new dependency; `go.mod`/`go.sum` unchanged.
- [ ] No new `Config`; existing validation unchanged.
- [ ] Errors wrapped with `emperror.dev/errors` (new dispatch marshal error).
- [ ] Naming follows Go conventions (`Alertmanager`, `PrometheusRule`,
      `AlertmanagerConfig`, `Silence`, `conditionView`, `matcherView`).
- [ ] `README.md` updated (count 28; curated describe views documented).
- [ ] New test file `formatters_monitoring_test.go` present.
- [ ] No duplication of `libs/toolkit/` helpers (`marshal.MustMarshal` reused;
      unstructured helpers are k8s-specific, kept in-package).
- [ ] No security regression: secret redaction for `kind: Secret` unchanged;
      no secret data in the 4 monitoring CRDs.

---

## Open questions

None — all three required design decisions are resolved above (unstructured
formatters; extended `formatterEntry` with optional `describe`; field
exclusions reconciled by mirroring `metadata`/`spec`/`status` section names in
the curated `describeOutput`).
