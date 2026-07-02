# Replace `encoding/json` with `github.com/goccy/go-json`

## Goal

Replace the remaining 6 files that use `encoding/json` with `github.com/goccy/go-json`, which is already a dependency in `go.mod` (`v0.10.6`) and used by 38 files in the `argocd/` and `kubernetes/` tool components.

## Files to Change

| File | Replacement |
|---|---|
| `callbacks/activity/event.go:37` | `"encoding/json"` → `"github.com/goccy/go-json"` |
| `callbacks/activity/event_test.go:20` | `"encoding/json"` → `"github.com/goccy/go-json"` |
| `libs/contentcomp/jsoncrush/jsoncrush.go:18` | `"encoding/json"` → `"github.com/goccy/go-json"` |
| `libs/contentcomp/jsoncrush/jsoncrush_test.go:5` | `"encoding/json"` → `"github.com/goccy/go-json"` |
| `components/memory/file/file.go:21` | `"encoding/json"` → `"github.com/goccy/go-json"` |
| `components/model/cachestab/cachestab_test.go:5` | `"encoding/json"` → `"github.com/goccy/go-json"` |

## Implementation Steps

1. For each of the 6 files, replace `"encoding/json"` with `"github.com/goccy/go-json"` in the import block. If `"encoding/json"` is the only standard library import left, remove it; otherwise remove just that line.
2. Run `go build ./...` and `go test ./...` to confirm no regressions.

## Risk

None. `goccy/go-json` is a drop-in replacement with the same API surface (`Marshal`, `Unmarshal`, `NewDecoder`, `RawMessage`, `Number`, `UseNumber`). Struct tags (`json:"..."`) are fully compatible.