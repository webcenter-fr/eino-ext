# toolutil — Shared tool helpers

`toolutil` provides small helpers shared across tool components: consistent
"not found" errors for named instances/clusters and sorted map-key extraction
for configuration maps.

## Functions

```go
func NotFoundError(kind, name string, known []string) error
func SortedKeys[V any](m map[string]V) []string
```

- `NotFoundError` — returns a consistent error indicating a named entity of the
  given kind was not found, listing the known values.
- `SortedKeys` — returns the keys of a string-keyed map in ascending order.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"

func (c Configs) GetInstanceNames() []string {
    return toolutil.SortedKeys(c)
}

return toolutil.NotFoundError("ArgoCD instance", instance, known)
```
