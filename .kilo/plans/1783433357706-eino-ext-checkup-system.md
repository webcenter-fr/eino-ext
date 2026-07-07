# Checkup System Plan

## Goal

A one-shot diagnostic that consumers run after configuring their project to verify
every component's connectivity and RBAC permissions work before going to production.

Returns a JSON structure with component name, status (ok/error/limited), instance,
and error message.

## Decisions Made

| # | Decision |
|---|---|
| D1 | Each tool probed individually (catches per-endpoint RBAC gaps) |
| D2 | Shared `Result` type lives in `libs/toolkit/checkup/` |
| D3 | Write tools (create/delete/exec) skipped for now |
| D4 | Describe tools probed via list-first-then-describe chain |
| D5 | If list returns empty → describe gets status `"limited"` |
| D6 | All components in one iteration (tools + memory + agent + session) |
| D7 | WebSearch probed with real HTTP call; Convertor skipped (no external deps) |
| D8 | OpenSearch tool skipped (needs pod-specific params for invoke), client connectivity verified |

## Architecture

### 1. Core layer: `libs/toolkit/checkup/`

New package with no external dependencies beyond stdlib.

```go
package checkup

type Result struct {
    Component string `json:"component"`           // e.g. "argocd_application_list"
    Instance  string `json:"instance,omitempty"`  // e.g. "prod"
    Status    string `json:"status"`              // "ok", "error", "limited"
    Error     string `json:"error,omitempty"`
    Message   string `json:"message,omitempty"`   // e.g. "3 applications found, RBAC ok"
}

type Results []Result

func (r Results) OK() bool // true if no "error" statuses
func (r Results) JSON(indent string) string
func Merge(all ...Results) Results
```

Status values:
- `"ok"` — probe succeeded
- `"error"` — probe failed (connectivity, auth, RBAC, timeout)
- `"limited"` — probe partially succeeded (e.g., list worked but no resources to describe)

### 2. Per-component `Check()` functions

Each component package gets a `Check()` function returning `checkup.Results`.

### 3. Probing strategies by component

#### ArgoCD (`components/tool/argocd/check.go`)

```go
func Check(ctx context.Context, configs Configs) checkup.Results
```

Per instance, per read-only tool:
- **List tools** (application_list, cluster_list, project_list, repository_list, certificate_list, instance_list):
  Call with `pageSize=1`, no filters. Status `"ok"` if no error.
- **Describe tools** (application_describe, cluster_describe, project_describe, repository_describe):
  First call corresponding list → pick first result → call describe with that name.
  If list returns empty → status `"limited"`, message "no resources to test describe".

Skip flow:
- If list tool fails, skip its paired describe tool → status `"error"` (dependency failed)
- If client creation fails → all tools for that instance get `"error"` (no client)

#### Kubernetes (`components/tool/kubernetes/check.go`)

```go
func Check(ctx context.Context, configs Configs) checkup.Results
```

Per cluster, per read-only tool (all ~61 from `readOnlyConstructors` including
kafka/OLM/openshift/spark/generic):

- **List tools**: Call with `pageSize=1`, no namespace (cluster-scoped).
  For namespaced resources missing namespace → k8s lists across all namespaces, tests full RBAC.
- **Describe tools**: List first → pick first item → parse namespace+name → describe.
  If list empty → `"limited"`.
  **Namespace handling**: For namespaced resources (pods, deployments, etc.), parse
  the namespace from the list output JSON. For cluster-scoped resources (nodes,
  namespaces, storageclasses, CRDs), the `DescribeParams` has `validate:"required"`
  on `namespace`, so describe probes are marked `"limited"` with a message
  "cluster-scoped resource, describe requires a dummy namespace workaround".
  Alternative: use the resource name as namespace for namespace resources
  being described (a namespace IS its own namespace in k8s API).
- **pod_log**: Skipped (needs pod name + container). Marked `"limited"`.
- **Generic tools** (resource_list, resource_describe): Skipped (needs GVR). Marked `"limited"`.

#### Prometheus (`components/tool/prometheus/check.go`)

```go
func Check(ctx context.Context, configs Configs) checkup.Results
```

Per instance:
- `alert_list`: Call directly (no extra required params).
- `alert_describe`: First call alert_list → pick first alert → use alertname as filter regex for describe.
- `metric_query`: Call with query `"up"` (standard Prometheus metric).
- `metric_range`: Call with query `"up[1m]"` + small time range.
- If any list returns empty → paired describe gets `"limited"`.

#### GitHub (`components/tool/github/check.go`)

```go
func Check(ctx context.Context, configs Configs) checkup.Results
```

Per instance:
1. **repo_search**: Call with `query="stars:>=0"`, `perPage=1`.
2. Parse `fullName` from result → extract owner and repo name.
3. **issue_list**: Call with discovered owner+repo, `perPage=1`.
4. **issue_get**: Pick first issue from step 3, call get with its number.
5. **pr_list**: Call with discovered owner+repo, `perPage=1`.
6. **pr_get**: Pick first PR from step 5, call get with its number.
7. **org_repo_list**: Call with owner from step 2 as org name.

Skip flows:
- If repo_search returns empty → all downstream tools get `"limited"` ("no repos to discover")
- If issue_list returns empty → `issue_get` gets `"limited"`
- If pr_list returns empty → `pr_get` gets `"limited"`
- If client creation fails → all tools get `"error"`

#### WebSearch (`components/tool/websearch/check.go`)

```go
func Check(ctx context.Context, cfg *Config) checkup.Results
```

- `web_search`: Call with a trivial query (e.g., `"health check"`), verify response isn't an error.
- `web_fetch`: Call with a known stable URL (e.g., `"https://httpbin.org/get"`), verify response.

#### OpenSearch tool (`components/tool/opensearch/check.go`)

```go
func Check(ctx context.Context, cfg *config.Config) checkup.Results
```

- Client connectivity only: create client → call `Cluster.Health()` or `Ping()`.
- Log tool itself marked `"limited"` (needs pod-specific params for Invoke).

#### Convertor: skipped entirely (no `Check()` function, no external deps)

### 4. Memory / Storage / Agent checkups

#### FileMemory (conversation history) — `components/memory/file/check.go`

```go
func Check(ctx context.Context, cfg FileMemoryConfig) checkup.Results
```

This component implements `memory.Memory` using local files. Its `Check()`
uses `NewFileMemory(cfg)` and exercises the same `memory.Memory` interface
operations as the OpenSearch variant, making them interchangeable.

| Check name | What it verifies |
|---|---|
| `connect` | `NewFileMemory(cfg)` succeeds (directory accessible, writable) |
| `get_conversation` | `GetConversation("__checkup", "test", true)` — creates file, reads back |
| `append_message` | Append a message → `Messages` grow by 1 |
| `list_conversations` | `ListConversations("__checkup")` — lists all test conversations |
| `delete_conversation` | `DeleteConversation("__checkup", "test")` — file deleted |

Cleanup: on completion, best-effort delete all conversations under
`"__checkup"` user so no test artifacts remain.

#### OpenSearch Memory (conversation history) — `components/memory/opensearch/check.go`

```go
func Check(ctx context.Context, cfg *Config) checkup.Results
```

This component implements `memory.Memory` for conversation history in OpenSearch.
Its `Check()` uses `NewOpenSearchMemory(cfg)` to build the component and exercises
the `memory.Memory` interface. Two-phase: read probes on configured index (no
side effects), then write probes on a test index `eino_memory_checkup_<uuid>`.

| Check name | What it verifies |
|---|---|
| `connect` | `NewOpenSearchMemory(cfg)` succeeds (client auth + index ensured) |
| `index_exists` | `Indices().Exists` on configured index |
| `get_conversation` | `GetConversation("__checkup", "test", true)` — tests Document.Get + Create |
| `append_message` | Append → Save via Document.Index (upsert) |
| `list_conversations` | `ListConversations("__checkup")` — tests Search().Search |
| `delete_conversation` | `DeleteConversation("__checkup", "test")` — tests Document.Delete |

#### Agent Memory OpenSearch Store — `components/agent/memory/opensearch/check.go`

```go
func Check(ctx context.Context, cfg *Config, embedder embedding.Embedder) checkup.Results
```

This component implements `MemoryStore` (Indexer + Retriever) for agent memory.
Its `Check()` uses `NewStore(ctx, cfg)` to build the component. Two-phase:
read probes on configured index, write probes on `eino_agent_memory_checkup_<uuid>`.

| Check name | What it verifies |
|---|---|
| `connect` | `NewStore()` succeeds (client + indexer + retriever created) |
| `count` | `Store.Count(ctx)` — tests Search().Count |
| `list` | `Store.List(ctx, 0, 1)` — tests Search().Search |
| `store` | `Store.Store(ctx, testDoc)` — tests eino indexer (bulk API) |
| `retrieve` | `Store.Retrieve(ctx, "test query")` — tests eino retriever (search, BM25 or kNN) |
| `delete` | `Store.Delete(ctx, docID)` — tests Document().Delete |
| `delete_by_filter` | `Store.DeleteByFilter(ctx, filter)` — tests Document().DeleteByQuery |

If `embedder` is nil, kNN is skipped and `retrieve` uses BM25 only, with the
Result message noting "kNN not configured, BM25 only".

#### SessionManager (`components/memory/session/check.go`)

```go
func Check(ctx context.Context, sm *SessionManager) checkup.Results
```

- `BeginTurn("__checkup_user", "__checkup_session", testMsg)` then immediately `Discard()`.
- If begin fails → error. If discard works → ok.
- This relies on the underlying memory store already being verified
  by the storage-level checkups above. If the session check fails,
  the root cause is in the storage checkup results, not here.

#### MemoryAgent (`components/agent/memory/check.go`)

```go
func Check(ctx context.Context, store MemoryStore, model model.BaseChatModel) checkup.Results
```

- Store: `Count(ctx)` or `List(ctx, 0, 1)` — verifies store connectivity.
  Produces Result `"memoryagent_store"`.
- Model: trivial chat completion call (e.g. `"Say hello"` with max_tokens=1) —
  verifies model connectivity. Produces Result `"memoryagent_model"`.
- These are high-level, not a replacement for the detailed storage checks above.

### 5. Result file structure

Each component `Check()` lives in a new file `check.go` in the component's package directory.

```
libs/toolkit/checkup/checkup.go                    # Result, Results, Merge, OK, JSON
libs/toolkit/checkup/checkup_test.go               # table-driven tests
components/tool/argocd/check.go                    # 12 read tools per instance
components/tool/argocd/check_test.go
components/tool/kubernetes/check.go                # ~61 read tools per cluster
components/tool/kubernetes/check_test.go
components/tool/prometheus/check.go                # 4 tools per instance
components/tool/prometheus/check_test.go
components/tool/github/check.go                    # 6 read tools, discovery chain
components/tool/github/check_test.go
components/tool/websearch/check.go                 # 2 tools, HTTP probes
components/tool/websearch/check_test.go
components/tool/opensearch/check.go                # client connectivity
components/tool/opensearch/check_test.go
components/memory/file/check.go                    # directory writability + CRUD
components/memory/file/check_test.go
components/memory/opensearch/check.go              # ~15 permission checks (see strategy)
components/memory/opensearch/check_test.go
components/agent/memory/opensearch/check.go        # ~17 permission checks (see strategy)
components/agent/memory/opensearch/check_test.go
components/memory/session/check.go                 # BeginTurn + Discard
components/memory/session/check_test.go
components/agent/memory/check.go                   # store + model connectivity
components/agent/memory/check_test.go
```

### 6. Consumer usage example

```go
results := checkup.Merge(
    argocd.Check(ctx, argocdConfigs),
    kubernetes.Check(ctx, kubeConfigs),
    prometheus.Check(ctx, promConfigs),
    github.Check(ctx, githubConfigs),
    websearch.Check(ctx, wsConfig),
    opensearchTool.Check(ctx, osToolConfig),
    filememory.Check(ctx, fmConfig),
    opensearchMemory.Check(ctx, osmConfig),
    agentMemoryOpensearch.Check(ctx, amosConfig, embedder),
    session.Check(ctx, sm),
    memoryagent.Check(ctx, store, model),
)

if !results.OK() {
    log.Fatalf("Checkup failed:\n%s", results.JSON("  "))
}
fmt.Println("All components healthy.\n%s", results.JSON("  "))
```

Note: `session.Check(ctx, sm)` accepts an existing `*SessionManager`
instance; it does not create one from config. Same for `memoryagent.Check`.
Consumers create the components as they normally would, then pass them
to the checkup.

### 7. Common probing patterns (shared helpers in `checkup` package)

Two helper functions to keep probe code consistent across components:

```go
// ProbeList calls a list function and returns the first item's name for pairing
// with a describe probe. Returns (name, json of first item, error).
func ProbeList[T any](ctx context.Context, name string, listFn func(context.Context) ([]T, error)) (firstName string, items []T, err error)

// ProbeDescribe calls a describe function with a specific name.
func ProbeDescribe(ctx context.Context, name string, describeFn func(context.Context, string) error) error
```

These are optional — not all probe chains fit the generic pattern (GitHub has variable params).

### 8. Edge cases and error handling

- **Timeout**: Each probe call uses a reasonable timeout (e.g., 10s). Use `context.WithTimeout`.
- **No resources in cluster/instance**: List returns empty → `"ok"` for list (RBAC works, just empty), `"limited"` for paired describe.
- **Clients nil after creation**: Should not happen (constructor validates), but check defensively.
- **Multi-instance failures**: One instance failing does not stop probing other instances.
- **Panic recovery**: Each probe call in a separate `func` so one panic doesn't crash the whole checkup.

## Implementation Order

1. `libs/toolkit/checkup/checkup.go` — Result, Results, Merge, OK, JSON
2. `libs/toolkit/checkup/checkup_test.go` — table-driven tests for JSON/Merge/OK
3. ArgoCD checkup (simplest multi-instance tool, good template for list→describe chain)
4. Prometheus checkup (simple, few tools)
5. Kubernetes checkup (large scale, follows argocd pattern)
6. GitHub checkup (unique discovery chain)
7. WebSearch checkup (simple HTTP probes)
8. OpenSearch tool checkup (client connectivity only)
9. FileMemory checkup (filesystem probes)
10. **Memory OpenSearch checkup** — implement OpenSearch strategy: cluster health → index exists → search/count/get on actual index → create test index → create/upsert/search/delete doc on test index → delete-by-query → delete index
11. **Agent Memory OpenSearch Store checkup** — same strategy, plus bulk index + retrieve checks
12. SessionManager checkup (BeginTurn → Discard)
13. MemoryAgent checkup (store Count/List + model connectivity)

## Validation

For each component `Check()`:
- `go build ./...`, `go vet ./...`, `go test ./...` pass
- Existing tests unchanged
- New test suite (if component already has test suite) verifies:
  - All tools return either `"ok"`, `"error"`, or `"limited"`
  - Instance name is populated for multi-instance components
  - No panics on nil configs or empty configs
- README updated with `Check()` usage example
