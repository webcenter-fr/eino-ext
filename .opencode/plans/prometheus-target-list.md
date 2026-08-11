# Plan: `prometheus_target_list` tool

Add a read-only tool that lists Prometheus scrape targets and their health, so users can verify metrics are being scraped correctly (no network policy issues, etc.).

All changes are confined to `components/tool/prometheus/`.

---

## 1. New file: `components/tool/prometheus/target_list.go`

Follow the `alert_list.go` / `metric_query.go` pattern exactly.

### Package & imports

```go
package prometheus

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	promapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)
```

### Description constant

```go
const targetListDescription = `
** General Purpose **
It lists all active scrape targets and their health status from a Prometheus instance.
This is useful to verify that metrics are being scraped correctly (e.g. no network policy issues,
misconfigured scrape jobs, or unreachable exporters).

** Output **
It returns a JSON array of objects, where each object represents a single active target with the following fields:
- labels: the target labels (e.g. instance, job).
- scrapePool: the scrape pool name (typically "<job_name>/<instance>").
- scrapeUrl: the URL being scraped.
- health: the target health: 'up', 'down', or 'unknown'.
- lastError: the last scrape error message (empty when healthy).
- lastScrape: the timestamp of the last scrape in RFC3339 format.
- lastScrapeDuration: the duration of the last scrape as a Go duration string (e.g. '12.3ms').
`
```

### Params struct

```go
// TargetListParams defines the parameters for listing Prometheus scrape targets.
type TargetListParams struct {
	Instance   string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Filter     string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each target JSON. Keep only targets that match. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Invalid regex returns an error."`
	Health     string `json:"health,omitempty" validate:"omitempty,oneof=up down unknown" jsonschema:"(optional) Filter by target health: 'up', 'down', or 'unknown'."`
	ScrapePool string `json:"scrapePool,omitempty" jsonschema:"(optional) Filter by scrape pool name. Must match exactly (e.g. 'node-exporter/10.0.0.1:9100')."`
}
```

### Output struct

```go
// TargetListOutput is the structured output for a Prometheus target list.
type TargetListOutput struct {
	Labels             model.LabelSet `json:"labels"`
	ScrapePool         string         `json:"scrapePool"`
	ScrapeUrl          string         `json:"scrapeUrl"`
	Health             string         `json:"health"`
	LastError          string         `json:"lastError"`
	LastScrape         string         `json:"lastScrape"`
	LastScrapeDuration string         `json:"lastScrapeDuration"`
}
```

### Tool struct

```go
// TargetListTool is an eino tool for listing Prometheus scrape targets.
type TargetListTool struct {
	*baseTool
	tool.InvokableTool
}
```

### Invoke method

```go
// Invoke returns matching active scrape targets as JSON.
func (t *TargetListTool) Invoke(ctx context.Context, params *TargetListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	targetsResult, err := c.Targets(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list targets")
	}

	active := targetsResult.Active
	outputs := make([]json.RawMessage, 0, len(active))
	for _, tgt := range active {
		// Filter by health if specified
		if params.Health != "" && string(tgt.Health) != params.Health {
			continue
		}
		// Filter by scrapePool if specified (exact match)
		if params.ScrapePool != "" && tgt.ScrapePool != params.ScrapePool {
			continue
		}

		output := TargetListOutput{
			Labels:             tgt.Labels,
			ScrapePool:         tgt.ScrapePool,
			ScrapeUrl:          tgt.ScrapeUrl,
			Health:             string(tgt.Health),
			LastError:          tgt.LastError,
			LastScrape:         tgt.LastScrape.Format("2006-01-02T15:04:05Z"),
			LastScrapeDuration: time.Duration(tgt.LastScrapeDuration * float64(time.Second)).String(),
		}

		outputJSON := json.RawMessage(marshal.MustMarshal(output))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	return marshalOutputs(outputs)
}
```

Notes:
- Only `Active` targets are returned (not `Dropped`). Dropped targets are typically noisy and not useful for the "are metrics being scraped?" use case.
- `Health` is converted via `string(tgt.Health)` because `promapi.Health` is a `string` type.
- `LastScrapeDuration` is a `float64` of seconds; convert to `time.Duration` and use `.String()` for a human-readable form (e.g. `12.3ms`, `1.5s`).
- `LastScrape` uses the same format string as `alert_list.go` (`"2006-01-02T15:04:05Z"`).
- A zero `time.Time` formats as `"0001-01-01T00:00:00Z"` — acceptable and consistent with the alert_list pattern.

### Constructor

```go
// NewTargetListTool creates a new TargetListTool.
func NewTargetListTool(ctx context.Context, configs Configs) (*TargetListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &TargetListTool{baseTool: base}
	t, err := utils.InferTool("prometheus_target_list", fmt.Sprintf("%s\n%s", targetListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
```

---

## 2. Modify: `components/tool/prometheus/registry.go`

Add one entry to the `readOnlyConstructors` slice (after `NewAlertDescribeTool`):

```go
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewTargetListTool(ctx, c) },
```

Resulting slice (6 entries):

```go
var readOnlyConstructors = []toolConstructor{
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewInstanceListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricQueryTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewMetricRangeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertListTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewAlertDescribeTool(ctx, c) },
	func(ctx context.Context, c Configs) (tool.InvokableTool, error) { return NewTargetListTool(ctx, c) },
}
```

No other changes to `registry.go` — `NewAllTools`, `NewReadOnlyTools`, `NewAllToolsWithSafety` all derive from this slice automatically.

---

## 3. Modify: `components/tool/prometheus/check.go`

### 3a. `clientErrorResults` — add a 6th result

Append this line to the returned `checkup.Results` slice (after the `prometheus_metric_range` entry):

```go
		{Component: "prometheus_target_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
```

### 3b. `probeInstance` — add targetList probe

Append this call before the final `return results`:

```go
	results = append(results, probeTargetList(ctx, client, instance))
```

### 3c. New `probeTargetList` function

Add at the end of the file (after `probeMetricRange`):

```go
func probeTargetList(ctx context.Context, client promapi.API, instance string) checkup.Result {
	targetsResult, err := client.Targets(ctx)
	if err != nil {
		return checkup.Result{
			Component: "prometheus_target_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list targets").Error(),
		}
	}
	msg := fmt.Sprintf("%d active targets, %d dropped targets, RBAC ok", len(targetsResult.Active), len(targetsResult.Dropped))
	return checkup.Result{
		Component: "prometheus_target_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}
}
```

---

## 4. Modify: `components/tool/prometheus/check_test.go`

Two assertions count from 5 → 6:

### 4a. `TestCheckInvalidInstance` (line ~51)

Change:
```go
	if len(results) != 5 {
		t.Errorf("expected 5 results (one per tool), got %d", len(results))
	}
```
to:
```go
	if len(results) != 6 {
		t.Errorf("expected 6 results (one per tool), got %d", len(results))
	}
```

### 4b. `TestCheckClientErrorResults` (line ~72)

Change:
```go
	if len(r) != 5 {
		t.Fatalf("expected 5 results, got %d", len(r))
	}
```
to:
```go
	if len(r) != 6 {
		t.Fatalf("expected 6 results, got %d", len(r))
	}
```

---

## 5. New file: `components/tool/prometheus/target_list_test.go`

The existing tests do not mock `promapi.API` (they rely on invalid configs failing at client creation). For `target_list_test.go` we need a mock client so we can exercise `Invoke` logic. Use the standard Go "embed the interface, override only `Targets`" pattern.

### Mock client

```go
package prometheus

import (
	"context"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
)

// mockTargetAPI embeds promapi.API so only Targets needs to be implemented.
// Calling any other method panics (nil pointer), which is fine for these tests.
type mockTargetAPI struct {
	v1.API
	targetsResult v1.TargetsResult
	targetsErr    error
}

func (m *mockTargetAPI) Targets(ctx context.Context) (v1.TargetsResult, error) {
	return m.targetsResult, m.targetsErr
}

func newTargetListToolWithMock(mock v1.API) *TargetListTool {
	return &TargetListTool{
		baseTool: &baseTool{
			clients: map[string]v1.API{"prod": mock},
			knownInstances: []string{"prod"},
		},
	}
}
```

### Test cases (table-driven)

```go
func TestTargetListTool(t *testing.T) {
	now := time.Now().UTC()
	upTarget := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "node", "instance": "10.0.0.1:9100"},
		ScrapePool:          "node/10.0.0.1:9100",
		ScrapeUrl:           "http://10.0.0.1:9100/metrics",
		Health:              v1.HealthUp,
		LastError:           "",
		LastScrape:          now,
		LastScrapeDuration:  0.0123,
	}
	downTarget := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "kubelet", "instance": "10.0.0.2:10250"},
		ScrapePool:          "kubelet/10.0.0.2:10250",
		ScrapeUrl:           "https://10.0.0.2:10250/metrics",
		Health:              v1.HealthDown,
		LastError:           "connection refused",
		LastScrape:          time.Time{},
		LastScrapeDuration:  0,
	}

	tests := []struct {
		name        string
		mock        *mockTargetAPI
		params      *TargetListParams
		wantCount   int
		wantErr     bool
		errContains string
	}{
		{
			name:      "happy path returns all active targets",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod"},
			wantCount: 2,
		},
		{
			name:      "health filter up returns only up targets",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", Health: "up"},
			wantCount: 1,
		},
		{
			name:      "health filter down returns only down targets",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", Health: "down"},
			wantCount: 1,
		},
		{
			name:      "scrapePool filter exact match",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", ScrapePool: "kubelet/10.0.0.2:10250"},
			wantCount: 1,
		},
		{
			name:      "scrapePool filter no match returns empty",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", ScrapePool: "nonexistent"},
			wantCount: 0,
		},
		{
			name:      "regex filter on scrapeUrl",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget, downTarget}}},
			params:    &TargetListParams{Instance: "prod", Filter: "10\\.0\\.0\\.1"},
			wantCount: 1,
		},
		{
			name:      "empty results returns empty array",
			mock:      &mockTargetAPI{targetsResult: v1.TargetsResult{Active: nil}},
			params:    &TargetListParams{Instance: "prod"},
			wantCount: 0,
		},
		{
			name:        "missing instance returns error",
			mock:        &mockTargetAPI{targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{upTarget}}},
			params:      &TargetListParams{Instance: "nonexistent"},
			wantErr:     true,
			errContains: "nonexistent",
		},
		{
			name:        "invalid health value fails validation",
			mock:        &mockTargetAPI{},
			params:      &TargetListParams{Instance: "prod", Health: "broken"},
			wantErr:     true,
			errContains: "invalid parameters",
		},
		{
			name:        "api error propagates",
			mock:        &mockTargetAPI{targetsErr: context.DeadlineExceeded},
			params:      &TargetListParams{Instance: "prod"},
			wantErr:     true,
			errContains: "failed to list targets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := newTargetListToolWithMock(tt.mock)
			result, err := tool.Invoke(context.Background(), tt.params)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			assert.NoError(t, err)

			var outputs []TargetListOutput
			err = json.Unmarshal([]byte(result), &outputs)
			assert.NoError(t, err)
			assert.Len(t, outputs, tt.wantCount)
		})
	}
}
```

### Additional focused tests

```go
func TestTargetListToolOutputFields(t *testing.T) {
	// Verify the formatted string fields render as expected.
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	tgt := v1.ActiveTarget{
		Labels:             model.LabelSet{"job": "node"},
		ScrapePool:          "node/x",
		ScrapeUrl:           "http://x/metrics",
		Health:              v1.HealthUp,
		LastError:           "",
		LastScrape:          ts,
		LastScrapeDuration:  1.5,
	}
	tool := newTargetListToolWithMock(&mockTargetAPI{
		targetsResult: v1.TargetsResult{Active: []v1.ActiveTarget{tgt}},
	})
	result, err := tool.Invoke(context.Background(), &TargetListParams{Instance: "prod"})
	assert.NoError(t, err)

	var outputs []TargetListOutput
	err = json.Unmarshal([]byte(result), &outputs)
	assert.NoError(t, err)
	assert.Len(t, outputs, 1)
	assert.Equal(t, "up", outputs[0].Health)
	assert.Equal(t, "node/x", outputs[0].ScrapePool)
	assert.Equal(t, "http://x/metrics", outputs[0].ScrapeUrl)
	assert.Equal(t, "2024-01-02T03:04:05Z", outputs[0].LastScrape)
	assert.Equal(t, "1.5s", outputs[0].LastScrapeDuration)
}

func TestTargetListToolConstructor(t *testing.T) {
	// Constructor with empty configs should succeed (no API calls at construction time).
	tool, err := NewTargetListTool(context.Background(), Configs{})
	assert.NoError(t, err)
	assert.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "prometheus_target_list", info.Name)
}

func TestTargetListToolNilConfigs(t *testing.T) {
	tool, err := NewTargetListTool(context.Background(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, tool)
}
```

---

## 6. Modify: `components/tool/prometheus/README.md`

### 6a. Available Tools table (after the `prometheus_alert_describe` row)

Add:
```
| `prometheus_target_list` | List active scrape targets and their health status |
```

### 6b. Tool Details section (after the `### prometheus_alert_describe` block)

Add a new subsection:

```markdown
### prometheus_target_list

List active scrape targets and their health status. Useful for verifying that
metrics are being scraped correctly (e.g. no network policy issues, unreachable
exporters, or misconfigured scrape jobs).

| Parameter | Required | Description |
|---|---|---|
| `instance` | Yes | Prometheus instance name |
| `health` | No | Filter by health: `up`, `down`, or `unknown` |
| `scrapePool` | No | Filter by exact scrape pool name (e.g. `node/10.0.0.1:9100`) |
| `filter` | No | Go RE2 regex on target JSON |

Each result contains: `labels`, `scrapePool`, `scrapeUrl`, `health`,
`lastError`, `lastScrape` (RFC3339), and `lastScrapeDuration` (Go duration
string). Only active targets are returned; dropped targets are excluded.
```

---

## Validation

After implementation, run from the repo root:

```sh
go build ./components/tool/prometheus/...
go vet ./components/tool/prometheus/...
go test ./components/tool/prometheus/... -count=1
```

Expected:
- Build clean, no vet warnings.
- All existing tests pass with the updated count assertions (6 instead of 5).
- New `target_list_test.go` tests all pass.
- `TestCheckInvalidInstance` and `TestCheckClientErrorResults` now expect 6 results.

---

## Decisions & Rationale

- **Active targets only**: Dropped targets are noisy (often thousands) and not useful for the "are metrics being scraped?" use case. The probe still reports dropped count in its message for observability.
- **`ScrapePool` is exact match, not regex**: It's a discrete identifier; exact match is predictable and avoids RE2 confusion. Users who need fuzzy matching can use the `filter` regex on the full JSON.
- **`Health` uses `oneof=up down unknown`**: Matches the `promapi.Health` string values exactly and is consistent with the `state` filter pattern in `alert_list.go`.
- **No pagination**: Target lists are typically small (tens to low hundreds). Pagination adds complexity without clear value here. If a user has thousands of active targets, the `filter`/`health`/`scrapePool` parameters narrow results sufficiently.
- **Mock via interface embedding**: The codebase has no existing mock for `promapi.API`. Embedding `v1.API` and overriding only `Targets` is the minimal, idiomatic Go approach and keeps the test file self-contained.

## Out of scope

- Listing dropped targets (could be added later as a `includeDropped` param if needed).
- Pagination.
- Any write tools (Prometheus tools remain read-only).
