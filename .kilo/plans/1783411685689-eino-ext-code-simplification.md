# eino-ext Code Simplification Plan

Status: **PLAN ONLY — not implemented.**

This plan is the result of looping over all non-test Go files in the repository
(220 `.go` files) and identifying simplification opportunities: duplicated code,
dead code, convention violations, and overly convoluted logic.

Every dead-code and convention claim below was verified by `grep` before being
written down.

Findings are ordered by value/risk. Prefer starting with the "Quick wins"
(trivial, low-risk deletions) before the larger refactors.

---

## Guiding constraints (from AGENTS.md / CONTRIBUTING.md)

- Shared helpers live under `libs/toolkit/`; do NOT reimplement `CompileFilter`,
  `MustMarshal`, etc.
- Every `New...` constructor MUST call `libs/toolkit/validate.Struct(cfg)` AFTER
  defaults; do NOT hand-roll `if len(cfg.URLs) == 0`.
- Acronyms/brands use official casing: `URL`, `ID`, `JSON`, `OpenSearch`.
- Errors wrapped with `emperror.dev/errors` including operation context.
- Each component keeps its test + README + package comment + interface check.
- Validate with `go build ./... && go vet ./... && go test ./...` before PR.

---

## Group A — Quick wins (delete dead code / trivial dedupe)

These are low-risk. Each was confirmed to have **zero non-test importers**.

### A1. Delete `libs/pricer` (dead + duplicate)
- **Files:** `libs/pricer/pricer.go`.
- **Evidence:** zero non-test importers. `Tokens`, `CacheTokens`, `Pricer` are
  byte-for-byte duplicates of `callbacks/activity` types. The live pricing path
  uses `activity.*` + `modelsdev.CatalogPricer` (`libs/modelsdev/pricer.go:28`
  takes `activity.Tokens`).
- **Action:** remove the package (and its test).

### A2. Delete `libs/toolkit/guidance` (dead package)
- **Files:** `libs/toolkit/guidance/guidance.go` (+ test).
- **Evidence:** zero importers. Components use embedded Markdown prompts instead
  (`kubernetes/helper.go`, `argocd/helper.go`, `prometheus/helper.go`), which is
  the CONTRIBUTING-preferred approach.
- **Action:** remove the package. (If you would rather keep it, `ListField.Name`
  is a dead field never read by `List` — but deletion is the recommended call.)

### A3. Remove unused `libs/toolkit` helpers
- `validate.StructName` — `libs/toolkit/validate/validate.go:28-33`; no callers.
- `filter.MatchString` — `libs/toolkit/filter/filter.go:28-33`; no callers (the
  `re.MatchString` hits in k8s/shellout are stdlib `*regexp.Regexp`, unrelated).
- `marshal.MustUnmarshal` — `libs/toolkit/marshal/marshal.go:17-21`; no callers,
  and its panic message is inconsistent with `MustMarshal`.
- **Action:** delete all three; update their READMEs. Keep `filter.Match`,
  `marshal.MustMarshal` (both actively used).

### A4. Delete duplicate `argocd.MustMarshal`; use `libs/toolkit/marshal`
- **Files:** `components/tool/argocd/helper.go:23-31`; callers in
  `application_list.go:86`, `certificate_list.go:70`, `cluster_list.go:70`,
  `project_list.go:66`, `repository_list.go:73`.
- **Action:** delete the local func, import `libs/toolkit/marshal`, switch the 5
  call sites. Direct AGENTS.md pitfall ("import it instead of reimplementing").

### A5. Delete k8s `IsMatch` duplicates; call `filter.Match`
- **Files:** `components/tool/kubernetes/generic_list.go:57-62` and
  `resource_list.go:61-66` re-implement `filter.Match`.
- **Action:** delete both methods, call `filter.Match(output, re)` (matches what
  argocd/prometheus already do).

### A6. Replace `stringPtr`/`intPtr` with `k8s.io/utils/ptr.To`
- **Files:** `components/retriever/opensearch/retriever.go:290-291` (defs),
  `:143-144` (uses). `ptr` is already a repo dependency (`chatmodel.go:31`).
- **Action:** `ptr.To("")`, `ptr.To(defaultTopK)`; delete both helpers.

### A7. Remove `rest` import hack in k8s pod_exec
- **Files:** `components/tool/kubernetes/pod_exec.go:63`
  (`var _ *rest.Config // ensure import is not removed`) and the unused `rest`
  import at `:21`. Config actually comes from `t.base.restConfig`.
- **Action:** remove the sentinel line and the unused import.

### A8. Register or remove k8s `ClusterListTool`
- **Files:** `components/tool/kubernetes/cluster_list.go`; registry compile-check
  at `registry.go:181` but it is NOT in `readOnlyConstructors` (`:17`), so
  `NewAllTools`/`NewReadOnlyTools` never build it (verified).
- **Action (decide intent):** either add `NewClusterListTool` to
  `readOnlyConstructors` (likely the intent — argocd registers its equivalent),
  or delete the file if intentionally dropped.

### A9. Remove unreachable nil-check in opensearch tool
- **Files:** `components/tool/opensearch/opensearch_log_kubernetes.go:85-87` —
  `buildQuery` (`:182`) always returns a query, so this nil branch is dead.
- **Action:** delete `:85-87`; keep the single validate check at `:80-82`.

### A10. `gofmt -w` pass
- `libs/toolkit/safety/policy.go:55` (comment over-indent),
  `ownership.go:19-25` (const misalignment), `contextopt/diagnostics.go:3-7` and
  `memory/file/file.go:4-15` (import ordering).
- **Action:** run `gofmt -w` / fix imports; also fix capitalized error strings in
  `memory/file/file.go:363,367,370` ("Failed ..." -> "failed ...").

---

## Group B — Convention fixes (validation / API correctness)

### B1. `sizecap` must call `validate.Struct`
- **Files:** `components/document/transformer/splitter/sizecap/sizecap.go:34-52`.
  Has `validate` tags but `NewSplitter` hand-rolls the checks.
- **Action:** call `validate.Struct(config)` after defaults; drop manual checks.
  Change `ChunkSize` tag from `required,gte=1` to `omitempty,gte=1` (it is
  defaulted), per CONTRIBUTING.

### B2. `loader/opensearch` must use `validate.Struct`
- **Files:** `components/document/loader/opensearch/opensearch.go:46-52` — manual
  `config == nil` / `len(URLs) == 0` + bare `errors.New`.
- **Action:** add `validate:"required,min=1"` to `URLs`, call `validate.Struct`
  (matches the other three OpenSearch configs).

### B3. opensearch tool: add `validate.Struct` + drop manual URL check
- **Files:** `components/tool/opensearch/opensearch.go:15-17`
  (`NewClient` manual `len(cfg.URLs) == 0`) and
  `opensearch_log_kubernetes.go:219-245` (`New...` never validates).
- **Action:** validate config via the shared helper after defaults.

### B4. `NewFileMemory` should return an error
- **Files:** `components/memory/file/file.go:60-69` — returns `nil` on
  `validate.Struct` failure instead of `(memory.Memory, error)`. Latent
  nil-panic and inconsistent with every other constructor.
- **Action:** change signature to return an error and propagate it. (Breaking;
  update callers + README.)

### B5. Standardize `validateParams` wrappers
- **Files:** `argocd/base.go:42-44` and `prometheus/helper.go:36-37` both wrap
  `validate.Struct`; kubernetes calls it directly.
- **Action:** pick one convention (recommend calling `validate.Struct` directly)
  and apply consistently.

### B6. Rename argocd `Url` -> `URL`
- **Files:** `argocd/config.go:14`, `client.go:18-26`, `repository_list.go:36`.
- **Action:** rename for Go-convention compliance. Breaking for callers; note in
  changelog/README.

---

## Group C — Extract shared helpers into `libs/toolkit/`

### C1. Shared confirmation gate (7 duplicated sites)
- **Sites:** k8s `resource_create.go:55-57`, `resource_delete.go:78-80`,
  `resource_patch.go:77-79`, `resource_apply.go:63-65`; argocd
  `application_create.go:92-94`, `application_delete.go:67-69`,
  `application_sync.go:47-49` (wording drifts across all 7).
- **Action:** add `RequireConfirmation(dryRun, confirmed bool) error` (in
  `libs/toolkit/safety` or a small `confirm` helper) with one canonical message;
  replace all 7. Review `application_sync.go` which validates `confirmed` even
  though it forwards `DryRun` to the API — align its semantics with create/delete.

### C2. Shared `marshalOutputs` / `NotFoundError` / `SortedKeys`
- `marshalOutputs([]json.RawMessage)` — good helper in `prometheus/helper.go:24`;
  duplicated shape in `argocd/application_list.go:93` and
  `kubernetes/generic_list.go:130`. Promote to `libs/toolkit/marshal`.
- `NotFoundError(kind, name, known)` — unify `argocd/helper.go:34`,
  `prometheus/helper.go:32`, `kubernetes/base.go:49` (same "must be one of" text).
- `SortedKeys[V any](map[string]V) []string` — unify the identical accessors in
  `kubernetes/config.go:18`, `argocd/config.go:24`, `prometheus/config.go:28`.

### C3. LLM-JSON + string helpers into `libs/toolkit`
- `extractJSONBlock` / `stripMarkdownFences` — `agent/memory/extractor.go:117-155`
  -> `libs/toolkit/marshal` (or new `libs/toolkit/llmjson`).
- `Truncate(s, n, marker)` — unify `agent/memory/extractor.go:157-161`,
  `contextopt/optimizer.go:281-287`, `contextopt/summarizer.go:92-94`
  -> `libs/toolkit/strutil`.
- Boolean `Extra` marker read/write — `HasBoolMarker(msg,key)` /
  `NewMarkedMessage(...)`: unify `memory.IsSummary`/`IsIncomplete`/`IsEphemeral`
  (`memory/markers.go`, `conversation.go`), `agent/memory.IsMemoryContext`
  (`types.go:101-122`), `contextopt.isPruned`/`isCompressed`
  (`optimizer.go:306-322`). Best placed in `components/memory`.
- `collectStream(sr) ([]*schema.Message, error)` — unify the drain-and-concat
  loops in `agent/memory/agent.go:277-299`, `memory/runner/runner.go:155-207`.

---

## Group D — Larger refactors (highest line savings, more care)

### D1. Shared OpenSearch client + hit projection + scroll lib
- **5 identical client builders:** `indexer/opensearch/indexer.go:109-121`,
  `retriever/opensearch/retriever.go:100-111`,
  `document/loader/opensearch/opensearch.go:54-65`,
  `memory/opensearch/opensearch.go:110-121`,
  `tool/opensearch/opensearch.go:19-28` (differ only in `Timeout`).
- **Hit->map projection dup:** `retriever/opensearch/retriever.go:258-270`,
  `document/parser/opensearch/opensearch.go:189-215`, plus 3 decode loops in
  `indexer/opensearch/reconcile.go:80-88,122-133,204-214`. The `_id/_index/...`
  key constants also diverge (`parser` `:17-20` vs `retriever` `:263-268`).
- **Scroll dup:** `reconcile.go` scroll/ClearScroll/decode skeleton repeated in
  `BulkLookupSourceHashes` + `Reconcile`.
- **Timeout:** promote `ensureContextTimeout` (`indexer.go:404-408`) to the shared
  lib; memory/loader hardcode `context.WithTimeout(ctx, 30s)` in many spots,
  ignoring caller deadlines.
- **Action:** create `libs/toolkit/opensearch` (or `libs/osclient`) with an
  embeddable `ClientConfig` (shared `validate`+`jsonschema` tags), `NewClient`,
  `hitToSource`, `metaFromHit`, `scrollAll(ctx, client, req, fn)`,
  `ensureContextTimeout`. Have all packages embed/consume it. Unify the metadata
  key constants. (README note per repo rules: this is for the eino-component
  OpenSearch client; keep the `disaster37/opensearch/v4` distinction intact.)

### D2. Shared `Conversation` base for memory backends (~150 dup lines)
- **Files:** `memory/file/file.go` vs `memory/opensearch/opensearch.go` implement
  identical `GetFullMessages/GetMessages/AppendSummary/LastSummaryIndex/GetWindow/
  CountTokens/Append`. The `GetWindow` binary-search (~90 lines) is verbatim
  duplicated (`file.go:236-325` vs `opensearch.go:378-456`) and already drifting
  in comments.
- **Action:** add a `BaseConversation` in `components/memory` holding
  `Messages/maxWindowSize/tokenCounter/maxWindowTokens/mutex` and implement the
  shared behavior once. Backends embed it and override only `Append/Load/Save`.
  While consolidating, simplify `GetWindow`'s 5 special-case branches
  (`n==1`, `!hasSummary`, `n==2`, `minWindow`, scratch buffer) into one binary
  search that pins index 0 when it is a summary.

### D3. Collapse `middleware/safety` four Wrap methods (~600 -> ~300 lines)
- **Files:** `components/middleware/safety/middleware.go` — `WrapInvokableToolCall`
  (`:59`), `WrapStreamableToolCall` (`:185`), `WrapEnhancedInvokableToolCall`
  (`:284`), `WrapEnhancedStreamableToolCall` (`:397`) repeat identical
  policy->gate->audit flow; the `AuditEvent{...}` literal is built ~18 times.
- **Action:** extract `preflight(...) (phase, gateParams, err)` and
  `auditResult(...)`; generify the two stream-audit goroutines
  (`:518-564` and `:568-608`) with `schema.StreamReader[T]` + an `onDone` callback.

### D4. Generic factory for argocd list/describe tools
- **List (5 files):** `application_list.go`, `certificate_list.go`,
  `cluster_list.go`, `project_list.go`, `repository_list.go` share
  validate->compile->client->List->map->filter->marshal.
- **Describe (4 files):** `application_describe.go`, `cluster_describe.go`,
  `project_describe.go`, `repository_describe.go` share the same skeleton +
  identical exclude-field `switch`.
- **Action:** mirror `kubernetes/generic_list.go`. Add
  `runListTool[TItem,TOut](ctx, base, instance, filter, fetch, toOutput)` +
  `newListTool(name, desc, invoke)`, and a `newDescribeTool` + generic
  `applyExcludes`. Each concrete tool reduces to `fetch`+`toOutput` closures.
  Also fix `application_delete.go:49-52` to call the existing `projectFilter`
  (`base.go:49-53`) instead of inlining it.

### D5. Dedupe k8s resource tools
- `newBaseToolWithDynamic(configs) (*baseToolWithDynamic, error)` in `base.go` to
  replace the ~20-line two-step build in `resource_{list,describe,create,patch,
  apply}.go` constructors.
- `toGVR(group, version, resource)` for the repeated `schema.GroupVersionResource`
  literal (5 sites).
- Merge identical `DescribeOutput` (`generic_describe.go:29-35`) and
  `ResourceDescribeOutput` (`resource_describe.go:37-43`); share the exclude loop
  (`generic_describe.go:84-97` == `resource_describe.go:103-116`).
- `parseManifest` + `marshalUnstructured` (strip `managedFields`) helpers for the
  create/apply/patch dup.
- Extract shared PodLog/PodExec plumbing: `newClientsetBaseTool`, an embeddable
  `invokableStreamable` with the 3 delegation methods, and
  `streamFilteredLines(scanner, re, sw)`.

### D6. Consolidate `contextopt` clone/summary/truncate helpers
- `lastSummaryText` triplicated: `optimizer.go:491-498`,
  `session/session.go:306-313`, agent/memory lookups -> use the shared
  `memory.LastSummaryIndex/Text` (see C3/D2).
- `cloneWithExtra(msg, kv)` for the repeated clone+Extra-copy+marker pattern in
  `optimizer.go:383-403,458-466` and `diagnostics.go:106-116`.

---

## Group E — Smaller / optional

- **E1.** `contentcomp/store.go:35-37` — redundant lazy map init in `Put`
  (`NewMemoryStore` already allocates). Drop it or document zero-value support.
- **E2.** `jsoncrush.go` marker keys duplicated as consts (`:27-33`) and struct
  tags (`:250-255`) — decode via `map[string]json.RawMessage` + consts (as
  `IsCrushed` already does) or add a guard test.
- **E3.** `safety/policy.go`: replace `mapToProtoStruct` (`:120-130`) with
  `structpb.NewStruct`; drop unused `CELPolicy.env` field (`:35-38,68`); use
  `slices.Contains` in `matchesTool` (`:106-118`).
- **E4.** `sizecap.mergeOverlap` (`:134-161`) — review correctness: prepending
  overlap can exceed `chunkSize`, partially defeating the cap; simplify or fix.
- **E5.** `agent/memory` `MaintainerConfig.MaxAge` is reachable code but never
  wired through `NewMemoryAgent` (`agent.go:67-71`) — expose it in the agent
  `Config` or document.
- **E6.** websearch: extract shared `doGET(ctx,url,ua,maxSize,client,retryable...)`
  + retry wrapper for `search.go:116-143`/`webfetch.go:162-191` and the two retry
  loops; optional `rejectPrivateIPs(ips, host)` shared by
  `config.go:80-97`/`webfetch.go:213-236`.
- **E7.** prometheus: `(t *baseTool) prepare(instance, filter)` returning
  client+compiled regex to remove the repeated Invoke prologue
  (`alert_list.go:63`, `alert_describe.go:58`, `metric_query.go:46`,
  `metric_range.go:57`).
- **E8.** opensearch tool: extract `extractLogLines(searchRes)` shared by Invoke
  and stream paths (`opensearch_log_kubernetes.go:115-136` vs `:153-174`).
- **E9.** `libs/modelsdev/catalog.go:63-76` — `Provider.ID/Name`, `Model.Name`
  decoded but never consumed; trim if strict minimalism desired (else keep for
  diagnostics).

---

## Suggested execution order

1. **Group A** (delete dead code / trivial dedupe) — safest, immediate.
2. **Group B** (validation/API convention fixes) — small, correctness-improving.
3. **Group C** (extract shared `libs/toolkit` helpers) — enables later dedupe.
4. **Group D** (larger refactors) — biggest savings; do one directory at a time,
   run `go build ./... && go vet ./... && go test ./...` after each.
5. **Group E** (optional cleanups) as capacity allows.

After each group: `go build ./... && go vet ./... && go test ./...`.
Note that B4/B6 (and D4's argocd `Url` rename) are **breaking API changes** and
should be called out in the PR / changelog and reflected in READMEs.
