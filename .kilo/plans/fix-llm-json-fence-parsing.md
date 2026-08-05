# Fix: LLM JSON fence parsing in `callbacks/activity`

## Problem

`ComplexityAnalyzer.Analyze` (`callbacks/activity/costsaver.go:220`) calls
`json.Unmarshal` directly on `resp.Content`. When an LLM wraps its JSON in a
markdown code fence (```` ```json ... ``` ````) — with or without surrounding
prose — unmarshal fails with:

```
activity: failed to parse LLM complexity analysis JSON: invalid character '`' looking for beginning of value
```

## Root cause

No preprocessing of the LLM response before `json.Unmarshal`. The shared helper
`strutil.ExtractJSONBlock` already exists for exactly this and is already the
established pattern in `components/agent/memory/extractor.go` (`parseExtractionResponse`).

## Design decisions (resolved, no further choices needed)

1. **Preprocess with `strutil.ExtractJSONBlock` before unmarshal.** Reuse the
   existing helper; do NOT duplicate logic (AGENTS.md rule).
2. **When `ExtractJSONBlock` returns `""`**: fall back to unmarshaling the
   **original** `resp.Content` (NOT the empty string). Rationale:
   - Preserves the existing `TestComplexityAnalyzer_InvalidJSON` behavior
     exactly (plain `"invalid json"` → `ExtractJSONBlock` returns `""` →
     unmarshal original → `invalid character 'i' ...`).
   - Keeps the most informative error (`invalid character '<X>' ...`) instead
     of the generic `unexpected end of JSON input`.
   - The error is still wrapped with `"activity: failed to parse LLM
     complexity analysis JSON"`, so the existing
     `assert.Contains(t, err.Error(), "failed to parse")` keeps passing.
   - No panic path: empty/whitespace-only content yields a wrapped error, never
     a panic.
3. **Prompt hardening**: optional but recommended one-line tweaks to both the
   inline system prompt and the embedded prompt file, telling the LLM not to
   use fences. The code fix alone is sufficient (it tolerates fences
   defensively); the prompt tweak reduces how often the fallback path runs.
4. **README**: unchanged. `callbacks/activity/README.md` does not document
   `ComplexityAnalyzer` at all (it covers the event bus/handler/catalog/agent
   attribution/config). Adding an isolated note about JSON-fence tolerance
   would be a dangling reference with no feature context, so no diff.

## Files to change

### 1. `callbacks/activity/costsaver.go` — add import (REQUIRED)

Add the local-module import group after the third-party group, matching the
project convention used in `components/agent/memory/extractor.go` (local
`github.com/webcenter-fr/eino-ext/...` imports live in their own group).

**oldString** (lines 3–17):
```go
import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sirupsen/logrus"
)
```

**newString**:
```go
import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/sirupsen/logrus"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/strutil"
)
```

### 2. `callbacks/activity/costsaver.go` — preprocess content in `Analyze` (REQUIRED)

**oldString** (lines 217–222):
```go
	content := resp.Content

	var analysis ComplexityAnalysis
	if err := json.Unmarshal([]byte(content), &analysis); err != nil {
		return nil, errors.Wrap(err, "activity: failed to parse LLM complexity analysis JSON")
	}
```

**newString**:
```go
	content := resp.Content
	// LLMs sometimes wrap JSON in ```json``` fences or surround it with prose
	// despite the prompt; extract the first balanced JSON block before parsing.
	// When no block is found, fall back to the original content so the unmarshal
	// error stays informative (e.g. "invalid character 'X' ...") and the wrapped
	// "failed to parse" message is preserved.
	if extracted := strutil.ExtractJSONBlock(content); extracted != "" {
		content = extracted
	}

	var analysis ComplexityAnalysis
	if err := json.Unmarshal([]byte(content), &analysis); err != nil {
		return nil, errors.Wrap(err, "activity: failed to parse LLM complexity analysis JSON")
	}
```

The inline comment adds real value: it explains *why* a preprocessing step that
looks redundant (given the prompt says "JSON only") is necessary, and documents
the empty-result fallback contract. This matches the repo's "occasional
explanatory comments" style (see `FallbackComplexityAnalyzer.Analyze`).

### 3. `callbacks/activity/costsaver.go` — inline system prompt (OPTIONAL, recommended)

Defensive hardening so the LLM is less likely to emit fences in the first place.

**oldString** (line 202, inside the `Generate` message):
```go
			Content: "You are a helpful assistant that analyzes AI agent sessions and estimates cost savings. Respond with valid JSON only.",
```

**newString**:
```go
			Content: "You are a helpful assistant that analyzes AI agent sessions and estimates cost savings. Respond with valid JSON only, without markdown code fences.",
```

### 4. `callbacks/activity/prompts/complexity_analysis.md` — embedded prompt (OPTIONAL, recommended)

**oldString** (line 60):
```
- Respond with valid JSON only, no prose, with exactly these keys:
```

**newString**:
```
- Respond with valid JSON only, no prose and no markdown code fences, with exactly these keys:
```

## Tests — `callbacks/activity/costsaver_test.go`

Add two table-driven test functions. Both use a minimal
`&SessionSummary{}` (the mock model ignores the prompt, and `Analyze` only
reads `summary` to build the prompt), mirroring the existing
`TestComplexityAnalyzer_TrivialZerosPassValidation` style. No new imports are
needed — `mockModel`, `schema`, `time`, `assert`, `require`, `context` are all
already imported.

Append at the end of the file (after `TestComplexityAnalyzerConfig_Defaults`):

```go
func TestComplexityAnalyzer_FencedAndProseJSON(t *testing.T) {
	summary := &SessionSummary{}

	tests := []struct {
		name      string
		content   string
		wantRatio float64
		wantTime  float64
		wantMoney float64
	}{
		{
			name:      "fenced JSON with language tag",
			content:   "```json\n{\"complexity_ratio\": 0.7, \"human_time_saved_seconds\": 300.0, \"money_saved_usd\": 5.0}\n```",
			wantRatio: 0.7,
			wantTime:  300.0,
			wantMoney: 5.0,
		},
		{
			name:      "fenced JSON without language tag",
			content:   "```\n{\"complexity_ratio\": 0.6, \"human_time_saved_seconds\": 200.0, \"money_saved_usd\": 4.0}\n```",
			wantRatio: 0.6,
			wantTime:  200.0,
			wantMoney: 4.0,
		},
		{
			name:      "JSON with leading prose",
			content:   "Sure! Here is the complexity analysis:\n{\"complexity_ratio\": 0.5, \"human_time_saved_seconds\": 100.0, \"money_saved_usd\": 2.0}",
			wantRatio: 0.5,
			wantTime:  100.0,
			wantMoney: 2.0,
		},
		{
			name:      "JSON with prose before and after",
			content:   "Here is the result:\n{\"complexity_ratio\": 0.4, \"human_time_saved_seconds\": 50.0, \"money_saved_usd\": 1.0}\nDone.",
			wantRatio: 0.4,
			wantTime:  50.0,
			wantMoney: 1.0,
		},
		{
			name:      "fenced JSON with prose around the fence",
			content:   "Analysis:\n```json\n{\"complexity_ratio\": 0.3, \"human_time_saved_seconds\": 25.0, \"money_saved_usd\": 0.5}\n```\nDone.",
			wantRatio: 0.3,
			wantTime:  25.0,
			wantMoney: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
				Model:           &mockModel{response: &schema.Message{Content: tt.content}},
				HumanHourlyRate: 50.0,
				BaseTaskTime:    5 * time.Minute,
				Timeout:         30 * time.Second,
			})
			analysis, err := analyzer.Analyze(context.Background(), summary)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRatio, analysis.ComplexityRatio)
			assert.Equal(t, tt.wantTime, analysis.HumanTimeSavedSeconds)
			assert.Equal(t, tt.wantMoney, analysis.MoneySavedUSD)
		})
	}
}

func TestComplexityAnalyzer_UnparseableResponses(t *testing.T) {
	summary := &SessionSummary{}

	tests := []struct {
		name    string
		content string
	}{
		{name: "fenced invalid inner text without JSON brackets", content: "```json\nnot valid json at all\n```"},
		{name: "backticks only no JSON", content: "```\n```"},
		{name: "pure prose without any JSON", content: "I cannot analyze this session."},
		{name: "empty response", content: ""},
		{name: "whitespace-only response", content: "   \n\t  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := NewComplexityAnalyzer(ComplexityAnalyzerConfig{
				Model:           &mockModel{response: &schema.Message{Content: tt.content}},
				HumanHourlyRate: 50.0,
				BaseTaskTime:    5 * time.Minute,
				Timeout:         30 * time.Second,
			})
			_, err := analyzer.Analyze(context.Background(), summary)
			require.Error(t, err, "expected error for %q", tt.name)
			assert.Contains(t, err.Error(), "failed to parse")
		})
	}
}
```

### Expected behavior traced for each new case

`strutil.ExtractJSONBlock` = `TrimSpace(StripMarkdownFences(content))` then
first `[`/last `]` or first `{`/last `}`.

Success cases — `ExtractJSONBlock` returns the inner object, unmarshal succeeds:

| Case | Trace | Result |
|------|-------|--------|
| fenced + lang tag | `StripMarkdownFences` strips ` ```json ` / ` ``` ` → object | parse OK |
| fenced no lang | `StripMarkdownFences` strips ` ``` ` / ` ``` ` → object | parse OK |
| leading prose | no fence; first `{`/last `}` = object | parse OK |
| prose before+after | no fence; first `{`/last `}` = object (no `}` in "Done.") | parse OK |
| prose around fence | fence not at start/end so not stripped; first `{`/last `}` = object | parse OK |

Failure cases — `ExtractJSONBlock` returns `""`, fall back to original, unmarshal
fails, wrapped with `"failed to parse"` (all contain "failed to parse", none
panic; reaching the assertion proves no panic):

| Case | Trace | Error contains |
|------|-------|----------------|
| fenced invalid (no brackets) | fence stripped → "not valid json at all", no brackets → `""`; unmarshal original ` ```json... ` | `invalid character '`'` + "failed to parse" |
| backticks only | `StripMarkdownFences("```\n```")` → `""`; → `""`; unmarshal original | `invalid character '`'` + "failed to parse" |
| pure prose | no fence, no brackets → `""`; unmarshal "I cannot..." | `invalid character 'I'` + "failed to parse" |
| empty | `ExtractJSONBlock("")` → `""`; unmarshal `""` | `unexpected end of JSON input` + "failed to parse" |
| whitespace | `StripMarkdownFences` trims to `""` → `""`; unmarshal whitespace | `unexpected end of JSON input` + "failed to parse" |

## No-regression verification (existing tests)

All existing tests in `costsaver_test.go` keep passing because the change only
adds an *optional* preprocessing step that is a no-op for already-valid plain
JSON:

- `TestComplexityAnalyzer_Analyze` — content `{"complexity_ratio": 0.7, ...}`
  → `ExtractJSONBlock` returns the same object (first `{`/last `}` = whole
  string) → unmarshal OK. Values unchanged.
- `TestComplexityAnalyzer_InvalidJSON` — content `"invalid json"` → no
  brackets → `ExtractJSONBlock` returns `""` → fall back to original →
  unmarshal fails → wrapped `"failed to parse"`. **`assert.Contains(...,
  "failed to parse")` still passes.** This is the explicit no-regression
  assertion the task calls out.
- `TestComplexityAnalyzer_InvalidValues` — valid JSON object extracted →
  unmarshal OK → `validateAnalysis` fails → `"invalid LLM complexity
  analysis"` (contains `"invalid"`). Passes.
- `TestComplexityAnalyzer_TrivialZerosPassValidation` — valid object
  extracted → unmarshal OK → validation passes (zeros). Passes.
- `TestCompositeComplexityAnalyzer_LLM` / `_Fallback` — composite delegates to
  `ComplexityAnalyzer.Analyze`; same preprocessing applies; LLM path parses
  OK, nil-model path uses fallback. Pass.
- `TestComplexityAnalyzer_ModelNil` — returns before the parsing block
  (`requires a model`). Unaffected.
- `TestFallbackComplexityAnalyzer_*`, `TestSessionSummarizer_*`,
  `TestComplexityAnalyzerConfig_Defaults` — do not touch `Analyze` JSON
  parsing. Unaffected.

## Documentation updates

`callbacks/activity/README.md`: **no change**. The README documents the event
bus, handler, event catalog, agent attribution, bus semantics, producer
invariants, and configuration. It does not mention `ComplexityAnalyzer`,
complexity analysis, or the LLM JSON contract at all. Adding a one-line note
about JSON-fence tolerance would reference an undocumented feature and read as
a dangling sentence. Skip it. (If a `ComplexityAnalyzer` section is added to the
README later, that section should note the fence/prose tolerance as a property
of `Analyze`.)

## Validation steps

Run from the repository root (`/projects/eino-ext`):

```bash
go build ./...
go vet ./...
go test ./callbacks/activity/...
```

All three must succeed. In particular:

- `go build ./...` confirms the new `strutil` import compiles.
- `go vet ./...` confirms no shadowing/unused issues.
- `go test ./callbacks/activity/...` runs the existing suite plus the two new
  table-driven tests; all must pass, including the preserved
  `TestComplexityAnalyzer_InvalidJSON`.

Optional, to confirm no broader breakage from the prompt edits:

```bash
go test ./...
```

## Implementation checklist

- [ ] `costsaver.go`: add `github.com/webcenter-fr/eino-ext/libs/toolkit/strutil` import (own group).
- [ ] `costsaver.go` `Analyze`: insert `strutil.ExtractJSONBlock` preprocessing with empty-result fallback to original content.
- [ ] (optional) `costsaver.go`: strengthen inline system prompt to "without markdown code fences".
- [ ] (optional) `prompts/complexity_analysis.md`: add "no markdown code fences" to the "Respond with valid JSON only" line.
- [ ] `costsaver_test.go`: add `TestComplexityAnalyzer_FencedAndProseJSON` (5 success cases).
- [ ] `costsaver_test.go`: add `TestComplexityAnalyzer_UnparseableResponses` (5 failure cases asserting "failed to parse", no panic).
- [ ] No license banners added; no source comments except the one explanatory block above.
- [ ] `go build ./...`, `go vet ./...`, `go test ./callbacks/activity/...` all pass.
