# Fix: `kubernetes_describe` returns full resource content

## Summary

**Issue:** webcenter-fr/eino-ext#5 — "fix: describe kubernetes resources".

The `kubernetes_describe` tool drops non-standard / extra top-level fields for many
Kubernetes resource kinds. Example query:

```json
{ "cluster": "ocp", "kind": "ValidatingWebhookConfiguration", "name": "trust-manager" }
```

Current output only contains kind/apiVersion/metadata:

```json
{"kind":"ValidatingWebhookConfiguration","apiVersion":"admissionregistration.k8s.io/v1","metadata":{"name":"trust-manager","resourceVersion":"1592156413","creationTimestamp":"2026-09-16T14:24:45Z","labels":{...},"annotations":{...}}}
```

Expected: the top-level `webhooks[]` array (containing `clientConfig.caBundle`, etc.)
must be surfaced, and more generally the describe tool must return the **full resource
content** (all top-level fields, including `spec` and nested arrays) for every kind.

## Decision (scope)

The describe tool must return the **full unstructured object** for **every** kind,
including the four `monitoring.coreos.com` alerting CRDs that previously had curated
describe views (`Alertmanager`, `AlertmanagerConfig`, `PrometheusRule`, `Silence`).

Consequences (confirmed with the requester):

- The curated **describe** views are removed entirely. The `kubernetes_describe` tool
  always emits the full resource JSON.
- The curated **list** views (28 typed kinds + 4 monitoring CRDs) are **kept
  unchanged** — the bug is scoped to `describe`, not `list`.
- Metadata is no longer reconstructed through a fixed `unstructuredMetadata` subset;
  the full `metadata` block (`uid`, `generation`, `finalizers`, `managedFields`, etc.)
  is returned as stored by the API server.

## Root cause

File: `components/tool/kubernetes/describe.go`.

The raw/fallback describe path rebuilds a lossy struct instead of re-serializing the
fetched unstructured object:

- `describeOutput` (lines 26–32) only carries `metav1.TypeMeta` (`apiVersion`/`kind`),
  `Metadata`, `Spec`, `Status`, `Data`.
- The raw path (lines 126–135) fills it from `o.Object["spec"]`, `o.Object["status"]`,
  `o.Object["data"]`, plus `unstructuredMetadata(o)` (lines 37–48) which copies only a
  fixed subset of metadata fields.

`ValidatingWebhookConfiguration` (admissionregistration.k8s.io/v1) stores its config in
a **top-level `webhooks` field** — it has no `spec` — so `Spec`/`Status`/`Data` are all
`nil` and `webhooks` is dropped. The same field loss affects any kind whose content
lives outside `spec`/`status`/`data`, e.g.:

- `MutatingWebhookConfiguration` → top-level `webhooks`
- `ClusterRole` / `Role` → top-level `rules`
- `RoleBinding` / `ClusterRoleBinding` → top-level `roleRef`, `subjects`
- `PriorityClass` → top-level `value`, `globalDefault`, `preemptionPolicy`, `description`
- `APIService` / CRDs with non-standard top-level fields

The dynamic client (`dynamic.Interface`, built in `base.go:171` / `client.go:110`) already
returns the complete object as an `*unstructured.Unstructured`; the data loss happens
only at serialization time in `describe.go`.

## Files to change

1. `components/tool/kubernetes/describe.go` — replace curated+raw dispatch with a single
   full-object marshal; remove `describeOutput`, `unstructuredMetadata`,
   `applyFieldExclusions`, `marshalDescribeOutput`; add `validateExcludeFields` +
   `marshalRawDescribeOutput`.
2. `components/tool/kubernetes/formatters.go` — remove `describeFormatter` type and the
   `describe` field of `formatterEntry`.
3. `components/tool/kubernetes/formatters_monitoring.go` — remove all describe-only
   formatters, view structs, and helpers; keep list formatters.
4. `components/tool/kubernetes/formatters_monitoring_test.go` — delete describe-only
   tests; update registry test; drop the raw-path struct test.
5. `components/tool/kubernetes/describe_test.go` (NEW) — unit tests for
   `marshalRawDescribeOutput` incl. the `ValidatingWebhookConfiguration` regression.
6. `components/tool/kubernetes/README.md` — update describe documentation.

---

## Implementation steps

### 1. `components/tool/kubernetes/describe.go`

#### 1a. Remove the lossy struct and helpers

Delete the `describeOutput` struct (current lines 26–32), `unstructuredMetadata`
(lines 34–48), `applyFieldExclusions` (lines 50–68), and `marshalDescribeOutput`
(lines 138–149).

#### 1b. Add exclusion validation + raw marshal

Add these functions (place where the old helpers were):

```go
// describeExcludableFields are the top-level fields a caller may ask the
// describe tool to omit from the output.
var describeExcludableFields = []string{"metadata", "spec", "status", "data"}

// validateExcludeFields validates every requested exclusion against the
// top-level fields the describe tool can omit.
func validateExcludeFields(excludeFields []string) error {
	for _, excludeField := range excludeFields {
		switch excludeField {
		case "metadata", "spec", "status", "data":
			continue
		default:
			return errors.Errorf("parameter 'excludeFieldsOutput' has invalid value %q; allowed values are: %s. Remove or fix it and retry",
				excludeField, strings.Join(describeExcludableFields, ", "))
		}
	}
	return nil
}

// marshalRawDescribeOutput marshals the full unstructured resource content as
// JSON, deleting only the top-level fields requested via excludeFields. Unlike
// a fixed metadata/spec/status/data struct, this preserves every field the API
// server returns — including non-standard top-level fields such as webhooks
// (ValidatingWebhookConfiguration / MutatingWebhookConfiguration), rules
// (ClusterRole / Role), roleRef and subjects (RoleBinding /
// ClusterRoleBinding), or value/globalDefault (PriorityClass).
func marshalRawDescribeOutput(o *unstructured.Unstructured, excludeFields []string) (string, error) {
	if err := validateExcludeFields(excludeFields); err != nil {
		return "", err
	}

	obj := o.DeepCopy().Object
	for _, field := range excludeFields {
		delete(obj, field)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(data), nil
}
```

#### 1c. Simplify `Invoke`

Current `Invoke` tail (lines 122–135):

```go
	if entry, ok := formatterRegistry[resolved.GVK]; ok && entry.describe != nil {
		return marshalDescribeOutput(entry.describe(o), params.ExcludeFieldsOutput)
	}

	return marshalDescribeOutput(describeOutput{
		TypeMeta: metav1.TypeMeta{
			Kind:       o.GetKind(),
			APIVersion: o.GetAPIVersion(),
		},
		Metadata: unstructuredMetadata(o),
		Spec:     o.Object["spec"],
		Status:   o.Object["status"],
		Data:     o.Object["data"],
	}, params.ExcludeFieldsOutput)
```

Replace with:

```go
	return marshalRawDescribeOutput(o, params.ExcludeFieldsOutput)
```

The secret-redaction block above it (lines 113–120) is unchanged and must stay
**before** the marshal call so `data`/`stringData` are redacted in-place.

#### 1d. Update the tool description

Replace `describeDescription` (current lines 70–76) with:

```go
const describeDescription = `
** General Purpose **
It describes any Kubernetes resource and returns its full JSON content as stored in the cluster. The 'kind' parameter accepts a PascalCase singular kind (e.g. 'Pod', 'Deployment', 'ConfigMap'), a kubectl shortname ('po', 'deploy'), or a 'resource.group' form ('deployments.apps'). Plural resource names ('pods') are also accepted. Supports core types and CRDs.

** Output **
Returns the full JSON object for the resource: apiVersion, kind, metadata, and every remaining top-level field (spec, status, data, webhooks, rules, roleRef, subjects, ...).
`
```

Imports: `metav1` is still used (`metav1.GetOptions{}`); `strings` is still used
(`strings.Join` in `validateExcludeFields` and `strings.ToLower`); `unstructured` is
still used (`*unstructured.Unstructured`). No import changes required beyond what is
already present.

### 2. `components/tool/kubernetes/formatters.go`

Remove the `describeFormatter` type (current lines 26–28) and the `describe` field
(current line 33):

```go
// Before (lines 24–34)
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

```go
// After
type listFormatter func(runtime.Object) json.RawMessage

type formatterEntry struct {
	newObj func() runtime.Object // nil for unstructured-only kinds
	format listFormatter         // list view (required)
}
```

`unstructured` is still imported in this file for other uses (`defaultListFormatter`,
`formatListItem`, `redactSecretData`); `runtime` and `schema` remain used. No other
change.

### 3. `components/tool/kubernetes/formatters_monitoring.go`

Keep list formatters; delete describe-only code.

**Keep:** `registerMonitoringFormatters` (modified), `unstructuredListFormatter`,
`matcherView`, `formatAlertmanagerList`, `formatAlertmanagerConfigList`,
`formatPrometheusRuleList`, `formatSilenceList`, `extractMatchers`, `maxListAlerts`.

**Delete:** `conditionView`, `alertmanagerSpecView`, `alertmanagerStatusView`,
`routeView`, `receiverView`, `alertmanagerConfigSpecView`, `alertmanagerConfigStatusView`,
`ruleGroupView`, `ruleView`, `prometheusRuleSpecView`, `prometheusRuleStatusView`,
`silenceSpecView`, `silenceStatusView`, `monitoringDescribe`, `extractConditions`,
`extractRoute` (+ `maxRouteDepth`), `extractReceivers`, `receiverTypes`,
`extractRuleGroups`, `extractRules`, `describeAlertmanager`,
`describeAlertmanagerConfig`, `describePrometheusRule`, `describeSilence`.

#### 3a. `registerMonitoringFormatters`

```go
// Before
func registerMonitoringFormatters(reg map[schema.GroupVersionKind]formatterEntry) {
	add := func(gvk schema.GroupVersionKind, list func(*unstructured.Unstructured) json.RawMessage, describe describeFormatter) {
		reg[gvk] = formatterEntry{
			newObj:   nil,
			format:   unstructuredListFormatter(list),
			describe: describe,
		}
	}

	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "Alertmanager"}, formatAlertmanagerList, describeAlertmanager)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}, formatAlertmanagerConfigList, describeAlertmanagerConfig)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}, formatPrometheusRuleList, describePrometheusRule)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "Silence"}, formatSilenceList, describeSilence)
}
```

```go
// After
func registerMonitoringFormatters(reg map[schema.GroupVersionKind]formatterEntry) {
	add := func(gvk schema.GroupVersionKind, list func(*unstructured.Unstructured) json.RawMessage) {
		reg[gvk] = formatterEntry{
			newObj: nil,
			format: unstructuredListFormatter(list),
		}
	}

	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "Alertmanager"}, formatAlertmanagerList)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "AlertmanagerConfig"}, formatAlertmanagerConfigList)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}, formatPrometheusRuleList)
	add(schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1alpha1", Kind: "Silence"}, formatSilenceList)
}
```

Update the package comment header at the top of the file (lines 14–17) which currently
says "registers curated list/describe formatters" → "registers curated list formatters".

#### 3b. Imports

`metav1` (line 8) was only used by `monitoringDescribe` → remove the import. `sort`
stays (used by `formatPrometheusRuleList`). All other imports (`json`, `marshal`,
`unstructured`, `runtime`, `schema`) stay.

### 4. `components/tool/kubernetes/formatters_monitoring_test.go`

- **Delete** these describe-only tests (they reference now-removed symbols):
  `TestDescribeAlertmanager` (98–157), `TestDescribeAlertmanager_ExcludeSpec` (159–177),
  `TestDescribeAlertmanager_ExcludeMetadata` (179–197), `TestDescribeAlertmanagerConfig`
  (224–293), `TestDescribeAlertmanagerConfig_NoRedaction` (295–336),
  `TestDescribePrometheusRule` (444–491), `TestDescribeSilence` (540–580),
  `TestDescribeSilence_ExcludeStatus` (582–600),
  `TestDescribeRawPathUnchangedForConfigMap` (649–683).
- **Update** `TestRegistryMonitoringEntries` (602–616): drop the
  `assert.NotNil(t, entry.describe, ...)` line; keep assertions that the 4 GVKs are
  registered, `newObj == nil`, and `format != nil`.
- **Remove** the now-unused `metav1` import (line 10).
- Keep all list-formatter tests (`TestFormatAlertmanagerList*`,
  `TestFormatAlertmanagerConfigList`, `TestFormatPrometheusRuleList*`,
  `TestFormatSilenceList*`, `TestFormatListItem_*`, `TestUnstructuredHelpers`).

### 5. `components/tool/kubernetes/describe_test.go` (NEW)

Pure unit tests (no envtest), matching the package's `testify/assert` style. Build
`*unstructured.Unstructured` from `map[string]any` and call `marshalRawDescribeOutput`
directly. Reuse the existing `mustUnmarshal` helper (defined in
`formatters_monitoring_test.go`, same package).

Helper:

```go
func rawDescribeJSON(t *testing.T, u *unstructured.Unstructured, exclude []string) map[string]any {
	t.Helper()
	s, err := marshalRawDescribeOutput(u, exclude)
	require.NoError(t, err)
	return mustUnmarshal(t, []byte(s))
}
```

Required cases:

1. `TestMarshalRawDescribeOutput_ValidatingWebhookConfiguration` (regression):
   ```go
   u := &unstructured.Unstructured{Object: map[string]any{
       "apiVersion": "admissionregistration.k8s.io/v1",
       "kind":       "ValidatingWebhookConfiguration",
       "metadata":   map[string]any{"name": "trust-manager"},
       "webhooks": []any{
           map[string]any{
               "name": "trust-manager.cert-manager.io",
               "clientConfig": map[string]any{
                   "service":  map[string]any{"name": "cert-manager", "namespace": "cert-manager", "path": "/validate"},
                   "caBundle": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t",
               },
               "rules": []any{map[string]any{"apiGroups": []any{"*"}, "apiVersions": []any{"*"}, "operations": []any{"CREATE", "UPDATE"}, "resources": []any{"certificates"}}},
           },
       },
   }}
   m := rawDescribeJSON(t, u, nil)
   ```
   Assert `m["kind"]=="ValidatingWebhookConfiguration"`,
   `m["apiVersion"]=="admissionregistration.k8s.io/v1"`,
   `m["webhooks"]` is a `[]any` of length 1, and that
   `m["webhooks"].([]any)[0].(map[string]any)["clientConfig"].(map[string]any)["caBundle"]`
   equals `"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t"`. Assert `_, ok := m["spec"]` is
   `false` (the kind genuinely has no spec).
2. `TestMarshalRawDescribeOutput_ExcludeFields` — object with `metadata`, `spec`,
   `status`, `data`, and a top-level `webhooks`; call with
   `[]string{"metadata","spec","status","data"}` → assert none of the four keys are
   present but `webhooks` is still present.
3. `TestMarshalRawDescribeOutput_InvalidExcludeField` — call with `[]string{"bogus"}` →
   assert error returned and its message contains `"bogus"` and the allowed list.
4. `TestMarshalRawDescribeOutput_ClusterScopedNoSpec` — a `PriorityClass`-like object
   (`apiVersion: scheduling.k8s.io/v1`, `kind: PriorityClass`, top-level `value` and
   `globalDefault`, no `spec`) → assert `value` and `globalDefault` are surfaced.
5. `TestMarshalRawDescribeOutput_PreservesUnknownCRDFields` — an arbitrary
   `example.com/v1 Kind=Widget` with a nested `spec` and a custom top-level `config`
   map → assert `spec` and `config` are both preserved verbatim.
6. `TestMarshalRawDescribeOutput_DoesNotMutateInput` — after calling with
   `[]string{"metadata"}`, assert the original `u.Object["metadata"]` still exists
   (deep copy honored).
7. `TestMarshalRawDescribeOutput_EmptyExclusions` — `nil`/empty exclusion slice returns
   the full object unchanged (sanity).
8. `TestRedactSecretData` — table-driven: `redactSecretData(map[string]any{"a":"x","b":"y"})`
   → all values `"REDACTED"`; non-map input is a no-op (no panic).

### 6. `components/tool/kubernetes/README.md`

- **Design bullet** (lines 16–21): replace the "Curated describe views" bullet with:
  > - **Full-content describe** — the `describe` tool returns the complete resource
  >   JSON (all top-level fields, not just metadata/spec/status/data).
  >   `excludeFieldsOutput` (`metadata`/`spec`/`status`/`data`) still applies.
- **"Curated views for monitoring.coreos.com alerting CRDs"** (lines 28–42): change the
  intro from "`list` and `describe` emit curated..." to "`list` emits curated,
  alert-relevant summaries for:" and remove the describe-specific framing. Keep the four
  kind bullets (they describe list views). Keep the `AlertmanagerConfig` secret-reference
  note (still accurate for list).
- **Tool table row** (line 65): update the `kubernetes_describe` description to
  "Describe any K8s resource by kind/shortname + name; returns the full resource JSON
  with `excludeFieldsOutput` support".

---

## Edge cases

- **Cluster-scoped vs namespaced:** unchanged. `Invoke` already only sets `namespace`
  when `resolved.Scoped` (describe.go lines 103–106). The full-object marshal is
  orthogonal to scope.
- **Resources with no `spec`:** handled by construction — `marshalRawDescribeOutput`
  serializes whatever top-level keys exist (`webhooks`, `value`, `rules`, ...) instead of
  a fixed spec field.
- **CRDs / unknown kinds:** `resolveKind` (resolver.go) resolves via RESTMapper;
  `marshalRawDescribeOutput` preserves every field for unknown/CRD kinds.
- **List vs get:** this is the `get`-by-name `describe` tool. `list.go` and its curated
  list views are untouched.
- **Missing name:** `Name` is `validate:"required"`; `validate.Struct(params)` at the top
  of `Invoke` rejects empty names before any API call.
- **Empty results / not found:** `Get` returns a not-found error → already wrapped via
  `errors.Wrapf` (describe.go lines 108–111).
- **Large payloads:** the raw path now returns the full object, so responses can be
  larger than before (notably `metadata.managedFields`). The existing
  `describe_output_guidance.md` prompt already instructs the model to use
  `excludeFieldsOutput` to trim large sections. No new size cap is introduced (per the
  issue's "full content" requirement); `excludeFieldsOutput: ["metadata"]` remains the
  escape hatch.
- **Sensitive data:** Secret `data`/`stringData` redaction is preserved (describe.go
  lines 113–120 run before marshal). `caBundle` in webhook configs is public cluster
  trust data and is **not** redacted (correct — it is not secret material). `metadata`
  fields surfaced are already visible via `kubectl get -o yaml` (same RBAC read scope).
- **No mutation of fetched object:** `o.DeepCopy()` prevents the exclusion deletions from
  mutating the cached/returned object.

## Error handling & validation

- No new `Config` struct → no new `validate.Struct(cfg)` constructor call.
- `DescribeParams.ExcludeFieldsOutput` keeps its existing
  `validate:"omitempty,dive,oneof=metadata spec status data"` tag; `validate.Struct`
  still runs first. `validateExcludeFields` additionally re-validates at marshal time and
  returns the same actionable error message for defense-in-depth.
- Marshal failure is wrapped with `emperror.dev/errors`: `errors.Wrap(err, "failed to
  marshal output")`.
- `Get` failures remain wrapped with `errors.Wrapf` (existing).

## Conventions compliance

- Reuses `github.com/goccy/go-json` (already imported) and does **not** duplicate any
  `libs/toolkit/` helper. No new `go.mod`/`go.sum` changes.
- No license banners added.
- Naming: no new exported identifiers with acronym/brand issues.
- `README.md` + test files updated so the component remains complete.

## Branch / PR workflow

- Create branch `fix/kubernetes-describe-full-content` from `main`.
- Implement, then run all local CI-equivalent checks (below), then open the PR.

## Verification checklist

Run from repo root:

```bash
# Format check (no diffs expected)
gofmt -l components/tool/kubernetes/

# Build everything
go build ./...

# Vet
go vet ./...

# Lint (if golangci-lint is available)
make lint

# New + existing pure unit tests (no envtest needed)
go test ./components/tool/kubernetes/ -run 'TestMarshalRawDescribeOutput|TestRedactSecretData|TestFormatAlertmanager|TestFormatAlertmanagerConfig|TestFormatPrometheusRule|TestFormatSilence|TestRegistryMonitoringEntries|TestFormatListItem|TestUnstructuredHelpers'

# Full kubernetes package (envtest tests require KUBEBUILDER_ASSETS; see Makefile `make test`)
go test ./components/tool/kubernetes/...

# Whole-repo test suite via Makefile (downloads envtest binaries)
make test

# Component completeness check (README + test file presence)
bash scripts/check_components.sh
```

The integration test `TestConsolidatedListAndDescribe` (ConfigMap) must still pass
unchanged: ConfigMap has `data`, so `marshalRawDescribeOutput` returns
kind/apiVersion/metadata/data, and the `excludeFieldsOutput` path still works.

## Out of scope

- `kubernetes_list` (and its curated list views) — unchanged.
- Any typed-scheme conversion in other tools — the describe tool already uses the
  dynamic client; only serialization changed.
