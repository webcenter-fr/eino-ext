# Plan: Fix GitHub Copilot Responses API routing heuristic

## Problem

`useResponsesAPI()` in `copilot_chat.go:697-719` uses regex `^gpt-(\d+)` to
decide whether a model uses `/responses` or `/chat/completions`. This regex
cannot distinguish integer-version models (e.g., `gpt-5`) from dotted-version
models (e.g., `gpt-5.4-nano`). Both extract `"5"`, and since `5 >= 5`, both
route to `/responses` — but `gpt-5.4-nano` is not available on `/responses`.

Kilocode has the **exact same bug** (`^gpt-(\d+)` with `Number(match[1]) >= 5`
in two separate files). This plan fixes eino-ext only.

## Design

### Fix 1: Regex distinguishes dotted versions

Change the regex to capture the separator character after the major version:

```
Old: ^gpt-(\d+)
New: ^gpt-(\d+)([.\-]|$)
```

Group 1 = major version number, group 2 = separator (`.` for dotted, `-` for
suffixed, empty/end for bare `gpt-N`). Dotted versions always return `false`.

### Fix 2: `ForceChatCompletions` config escape hatch

Add `ForceChatCompletions bool` to `Config`. When `true`, `useResponsesAPI`
returns `false` unconditionally. This provides an override when the heuristic
is wrong or the API changes.

### Routing function signature

`useResponsesAPI` is currently a package-level function. Two options:

**Option A: Make it a method** — `func (m *CopilotModel) useResponsesAPI(modelID string) bool`.
Reads `m.cfg.ForceChatCompletions`. Both `Generate` and `Stream` already have
access to `m`. **Preferred** — no signature change at call sites.

**Option B: Add config parameter** — `func useResponsesAPI(modelID string, forceChat bool) bool`.
Requires updating both call sites but keeps the function testable in isolation.

Choose **Option A**: `useResponsesAPI` is already only called from methods on
`*CopilotModel`. Making it a method is the minimal change. The test just
constructs a `CopilotModel` with the desired `ForceChatCompletions` value.

## Task list

### 1. Fix the regex in `copilot_chat.go`

Replace:
```go
var gpt5ModelPattern = regexp.MustCompile(`^gpt-(\d+)`)
```

With:
```go
var gpt5ModelPattern = regexp.MustCompile(`^gpt-(\d+)([.\-]|$)`)
```

### 2. Convert `useResponsesAPI` from function to method

Change the signature and access `m.cfg.ForceChatCompletions`:

```go
func (m *CopilotModel) useResponsesAPI(modelID string) bool {
    if m.cfg.ForceChatCompletions {
        return false
    }
    match := gpt5ModelPattern.FindStringSubmatch(modelID)
    if match == nil {
        return false
    }
    if modelID == "gpt-5-mini" {
        return false
    }
    // Dotted versions (gpt-5.4-nano) use chat completions, not Responses.
    if match[2] == "." {
        return false
    }
    var n int
    if _, err := fmt.Sscanf(match[1], "%d", &n); err == nil && n >= 5 {
        return true
    }
    return false
}
```

Update the two call sites in `Generate` and `Stream` from
`useResponsesAPI(resolvedModel)` to `m.useResponsesAPI(resolvedModel)`.

### 3. Add `ForceChatCompletions` to `Config`

In `copilot.go`, add:
```go
ForceChatCompletions bool `validate:"omitempty" jsonschema:"description=Force chat/completions endpoint even for models that would use /responses"`
```

No validation required — a bool with zero value `false` preserves existing behavior.

### 4. Update tests

In `copilot_test.go` `TestUseResponsesAPI`:
- Add test cases for dotted versions: `gpt-5.4-nano → false`, `gpt-6.1 → false`
- Make the function a method on a `*CopilotModel` with `cfg` set appropriately
- Add a case testing `ForceChatCompletions: true` with `gpt-5` → `false`

### 5. Update `README.md`

- Document `ForceChatCompletions` in the Config section
- Update the GPT-5 routing section to mention dotted-version models stay on chat completions

## Test cases to add/update

| Model ID | ForceChatCompletions | Expected |
|---|---|---|
| `gpt-5` | `false` | `true` |
| `gpt-5-chat-latest` | `false` | `true` |
| `gpt-5-mini` | `false` | `false` |
| `gpt-5.4-nano` | `false` | `false` |
| `gpt-6.1` | `false` | `false` |
| `gpt-6` | `false` | `true` |
| `gpt-4o` | `false` | `false` |
| `gpt-5` | `true` | `false` |
| `gpt-5-chat-latest` | `true` | `false` |

## Validation

```bash
go build ./...
go vet ./...
go test ./components/model/copilot/...
```

All existing tests must continue passing; new test cases must pass.

## Files changed

- `components/model/copilot/copilot_chat.go` — regex, function → method
- `components/model/copilot/copilot.go` — `ForceChatCompletions` field in Config
- `components/model/copilot/copilot_test.go` — expanded test table
- `components/model/copilot/copilot_stream.go` — call site update (`useResponsesAPI` → `m.useResponsesAPI`)
- `components/model/copilot/README.md` — documentation
