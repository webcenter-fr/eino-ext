# Plan: Prometheus `instance_list` Tool

Add a read-only `InvokableTool` to the existing Prometheus component that
returns the sorted list of configured Prometheus instance names as a JSON
string array. This mirrors `argocd_instance_list` and `kubernetes_cluster_list`:
the agent can discover which instances are available before calling tools that
require an `instance` parameter.

## Files to create

### 1. `components/tool/prometheus/instance_list.go` (NEW)

Mirror `components/tool/argocd/instance_list.go` exactly, adapted to the
prometheus package. No `baseTool` (instance_list needs no client); it only
reads `configs.GetInstanceNames()`.

```go
package prometheus

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

const instanceListDescription = `
** General Purpose **
It lists all the Prometheus instances where it can connect.

** Output **
It returns a JSON array of objects, where each object represents an instance with the following fields:
- name: the name of the Prometheus instance.
`

// InstanceListTool lists all configured Prometheus instances. It implements tool.InvokableTool.
type InstanceListTool struct {
	knownInstances []string
	tool.InvokableTool
}

// InstanceListParams holds the parameters for InstanceListTool (none required).
type InstanceListParams struct{}

// Invoke returns the configured instance names as a JSON string array.
func (t *InstanceListTool) Invoke(ctx context.Context, params *InstanceListParams) (string, error) {
	b, err := json.Marshal(t.knownInstances)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal known instances")
	}
	return string(b), nil
}

// NewInstanceListTool creates a new InstanceListTool for the given configs.
func NewInstanceListTool(ctx context.Context, configs Configs) (*InstanceListTool, error) {
	instanceListTool := &InstanceListTool{
		knownInstances: configs.GetInstanceNames(),
	}

	invokable, err := utils.InferTool("prometheus_instance_list", instanceListDescription, instanceListTool.Invoke,
		utils.WithUnmarshalArguments(toolutil.EmptyJSONUnmarshaler[*InstanceListParams]()))
	if err != nil {
		return nil, err
	}
	instanceListTool.InvokableTool = invokable

	return instanceListTool, nil
}
```

### 2. `components/tool/prometheus/instance_list_test.go` (NEW)

Mirror `components/tool/kubernetes/cluster_list_test.go`. Uses testify/assert
and `goccy/go-json` (the json package already imported by other prometheus tests).

```go
package prometheus

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

func TestInstanceListTool(t *testing.T) {
	ctx := context.Background()

	configs := Configs{
		"prod":    {Address: "http://prod:9090"},
		"staging": {Address: "http://staging:9090"},
	}

	tool, err := NewInstanceListTool(ctx, configs)
	assert.NoError(t, err)

	info, err := tool.Info(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "prometheus_instance_list", info.Name)

	// Empty string arguments — must not fail (EmptyJSONUnmarshaler handles "").
	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 2)
	assert.ElementsMatch(t, []string{"prod", "staging"}, outputs)

	// Empty JSON object arguments — also valid for a no-param tool.
	result, err = tool.InvokableRun(ctx, "{}")
	assert.NoError(t, err)

	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 2)
	assert.ElementsMatch(t, []string{"prod", "staging"}, outputs)
}

func TestInstanceListToolEmptyConfigs(t *testing.T) {
	ctx := context.Background()

	tool, err := NewInstanceListTool(ctx, Configs{})
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Empty(t, outputs)
}

func TestInstanceListToolNilConfigs(t *testing.T) {
	ctx := context.Background()

	tool, err := NewInstanceListTool(ctx, nil)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "{}")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Empty(t, outputs)
}

func TestInstanceListToolSorted(t *testing.T) {
	ctx := context.Background()

	configs := Configs{
		"zeta":  {Address: "http://zeta:9090"},
		"alpha": {Address: "http://alpha:9090"},
		"mid":   {Address: "http://mid:9090"},
	}

	tool, err := NewInstanceListTool(ctx, configs)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(ctx, "")
	assert.NoError(t, err)

	var outputs []string
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, outputs)
}
```

## Files to modify

### 3. `components/tool/prometheus/registry.go`

Edit 1 — add InstanceListTool as the FIRST entry of `readOnlyConstructors`:

OLD:
```go
var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricQueryTool(ctx, c) },
```
NEW:
```go
var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricQueryTool(ctx, c) },
```

Edit 2 — add a compile-time interface check block at the end of the file
(after the `NewAllToolsWithSafety` func). There currently is no such block in
prometheus/registry.go; add one for the new tool only (do not touch others):

```go
var (
	_ tool.InvokableTool = (*InstanceListTool)(nil)
)
```

### 4. `components/tool/prometheus/check.go`

Edit 1 — `clientErrorResults`: add `prometheus_instance_list` as the FIRST
entry so the count becomes 5:

OLD:
```go
	return checkup.Results{
		{Component: "prometheus_alert_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
```
NEW:
```go
	return checkup.Results{
		{Component: "prometheus_instance_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "prometheus_alert_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
```

Edit 2 — add `probeInstanceList` helper. Mirror argocd's `probeInstanceList`
(local op, always OK if the instance was reached). Add near the other probe
helpers (e.g. right after `probeInstance`):

```go
func probeInstanceList(instance string) checkup.Result {
	return checkup.Result{
		Component: "prometheus_instance_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
	}
}
```

Edit 3 — call `probeInstanceList` FIRST inside `probeInstance`:

OLD:
```go
func probeInstance(ctx context.Context, client promapi.API, instance string) checkup.Results {
	var results checkup.Results

	ar, alerts, err := probeAlertList(ctx, client, instance)
```
NEW:
```go
func probeInstance(ctx context.Context, client promapi.API, instance string) checkup.Results {
	var results checkup.Results

	results = append(results, probeInstanceList(instance))

	ar, alerts, err := probeAlertList(ctx, client, instance)
```

### 5. `components/tool/prometheus/check_test.go`

Update the count assertions now that there are 5 probed components.

Edit 1 — `TestCheckInvalidInstance`:
OLD: `	if len(results) != 4 {`
NEW: `	if len(results) != 5 {`
and the message: `t.Errorf("expected 4 results (one per tool), got %d", len(results))`
→ `t.Errorf("expected 5 results (one per tool), got %d", len(results))`

Edit 2 — `TestCheckClientErrorResults`:
OLD: `	if len(r) != 4 {`
NEW: `	if len(r) != 5 {`
OLD: `		t.Fatalf("expected 4 results, got %d", len(r))`
NEW: `		t.Fatalf("expected 5 results, got %d", len(r))`

Search the whole package for any other `4` count references related to results
and update to 5. (`TestCheckEmptyConfigs`/`TestCheckNilConfigs` assert length 1,
do not change.)

### 6. `components/tool/prometheus/README.md`

Add a new first row to the "Available Tools" table:

OLD:
```markdown
| Tool Name | Description |
|---|---|
| `prometheus_metric_query` | Execute an instant PromQL query |
```
NEW:
```markdown
| Tool Name | Description |
|---|---|
| `prometheus_instance_list` | List all configured Prometheus instances |
| `prometheus_metric_query` | Execute an instant PromQL query |
```

## Edge cases / error handling

- **Empty configs**: `GetInstanceNames()` returns `nil`; `json.Marshal(nil)`
  produces `null`. Test `TestInstanceListToolEmptyConfigs` asserts an empty
  slice after unmarshal (a JSON `null` unmarshals into a nil slice →
  `assert.Empty` passes). This matches kubernetes behavior.
- **Nil configs**: same as empty; `nil` map → `GetInstanceNames()` returns nil.
- **Marshal error**: only possible on a non-string slice, which cannot happen
  here; still wrapped with `errors.Wrap` per conventions.
- **Empty-string args**: `toolutil.EmptyJSONUnmarshaler` handles `""` and `"{}`
  returning a zero-value `*InstanceListParams`; verified by tests (this was a
  past bug in kubernetes_cluster_list).
- **No validation needed**: there is no `Config` to validate for instance_list
  (it reads names only), so no `validate.Struct` call — matches argocd
  `NewInstanceListTool`.

## Constraints honored

- No license banner.
- `emperror.dev/errors` for wrapping.
- `fmt.Sprintf` where formatting (none needed here).
- Reuses existing `Configs.GetInstanceNames()` and `toolutil.EmptyJSONUnmarshaler`.
- Naming: `InstanceListTool`, `InstanceListParams`, `NewInstanceListTool`.
- Constructor first param `ctx context.Context`.
- Compile-time check `var _ tool.InvokableTool = (*InstanceListTool)(nil)`.
- Does not modify AGENTS.md / CONTRIBUTING.md.

## Verification

```bash
go build ./components/tool/prometheus/...
go vet ./components/tool/prometheus/...
go test ./components/tool/prometheus/...
```
