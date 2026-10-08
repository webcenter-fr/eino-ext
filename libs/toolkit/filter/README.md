# filter — Regex and JSON-selector filtering utilities

`filter` provides filter compilation and matching helpers for filtering tool
output. Used by list tools across ArgoCD and Kubernetes packages to filter
results by name, labels, or other fields.

## Functions

```go
func Compile(pattern string) (*regexp.Regexp, error)
func Match(data json.RawMessage, filter *regexp.Regexp) bool

func CompileMatcher(pattern string) (Matcher, error)

type Matcher interface {
    MatchJSON(data []byte) bool
    MatchObject(obj map[string]any) bool
}
```

- `Compile` / `Match` — the original regex-only API, kept for line-oriented
  filters (shell, pod logs, pod exec). `Compile` returns `nil` for empty
  strings; `Match` returns `true` when the filter is `nil`.
- `CompileMatcher` — compiles either a JSON-object selector or a Go RE2 regex.
  An empty (or whitespace-only) pattern matches everything.

## Filter forms

`CompileMatcher` auto-detects the form from the trimmed pattern:

- A pattern starting with `{` is parsed as a **JSON-object selector**.
- Any other non-empty pattern is a **Go RE2 regex**.
- An empty pattern is a match-all matcher.

### JSON-object selector

A selector is a JSON object whose entries are conditions. All conditions must
match (logical AND); `{}` matches everything.

```json
{"status.conditions[].reason": "NotSupported"}
```

Path syntax (split on `.`):

| Segment | Meaning |
|---|---|
| `name` | descend into object field `name` |
| `name[]` | `name` must be an array; match if any element satisfies the rest |
| `[]` | the current value must be an array; match if any element satisfies the rest |
| `name[0]` | unsupported — returns a compile error |

A key with no `.` and no `[` is a **bare key**: it matches any field with that
name at any depth (e.g. `{"reason":"NotSupported"}` matches
`status.conditions[].reason`).

Matching is **type-coerced and case-insensitive**: both sides are normalized to
a canonical lowercased string, so `500` matches `"500"`, `true` matches
`"true"`, and `"NotSupported"` matches `"notsupported"`. A value that is a JSON
array means IN (the field matches any listed value). A missing path never
matches; an explicit JSON `null` normalizes to `"null"` and is matchable.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/filter"

m, err := filter.CompileMatcher(params.Filter)
if err != nil {
    return err
}
for _, item := range items {
    if m.MatchObject(item.Object) {
        // include item
    }
}
```

The original regex API remains available for line-oriented filters:

```go
f := filter.Compile(params.Filter)
for _, line := range lines {
    if filter.Match([]byte(line), f) {
        // include line
    }
}
```

A `nil` filter (from an empty pattern) matches everything, providing a safe
default for optional filtering.
