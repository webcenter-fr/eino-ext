# filter — Regex filtering utilities

`filter` provides regex compilation and matching helpers for filtering tool
output. Used by list tools across ArgoCD and Kubernetes packages to filter
results by name, labels, or other fields.

## Functions

```go
func Compile(pattern string) *regexp.Regexp
func Match(data json.RawMessage, filter *regexp.Regexp) bool
func MatchString(s string, filter *regexp.Regexp) bool
```

- `Compile` — compiles a Go RE2 regex pattern; returns `nil` for empty strings.
- `Match` — returns `true` if the filter is `nil` or the JSON data contains a
  match.
- `MatchString` — same for plain string input.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/filter"

f := filter.Compile(params.Filter)
for _, item := range items {
    itemJSON, _ := json.Marshal(item)
    if filter.Match(itemJSON, f) {
        // include item
    }
}
```

A `nil` filter (from an empty pattern) matches everything, providing a safe
default for optional filtering.
