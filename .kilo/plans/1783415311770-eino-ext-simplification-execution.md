# eino-ext Code Simplification — Execution Plan & Status

Status: **Plan document. No code is modified by this file.**

This plan records the simplification work applied to the repository (looping
over every non-test Go file), the concrete edits made per group, and the
remaining verification steps. It complements the earlier findings plan
`1783411685689-eino-ext-code-simplification.md` and reflects what is now in the
working tree (70 changed paths, 4 new `libs/toolkit` packages).

Every dead-code and convention claim was verified with `grep` before acting.

---

## Guiding constraints (AGENTS.md / CONTRIBUTING.md)

- Shared helpers live under `libs/toolkit/`; do not reimplement `MustMarshal`,
  `CompileFilter`, etc.
- Every `New...` calls `libs/toolkit/validate.Struct(cfg)` AFTER defaults; no
  hand-rolled `if len(cfg.URLs) == 0`.
- Acronyms/brands use official casing: `URL`, `ID`, `JSON`, `OpenSearch`.
- Errors wrapped with `emperror.dev/errors` including operation context.
- Each component keeps its test + README + package comment + interface check.
- Validate with `go build ./... && go vet ./... && go test ./...`.

---

## Group A — Dead code / trivial dedupe (verified zero non-test importers)

- **A1** Delete `libs/pricer` (duplicate of `callbacks/activity` token/pricer
  types; unused).
- **A2** Delete `libs/toolkit/guidance` (unused; components embed Markdown).
- **A3** Remove unused toolkit helpers: `validate.StructName`,
  `filter.MatchString`, `marshal.MustUnmarshal` (+ README updates).
- **A4** Delete `argocd.MustMarshal`; use `libs/toolkit/marshal.MustMarshal` at
  the 5 list call sites.
- **A5** Delete k8s `IsMatch` duplicates in `generic_list.go` /
  `resource_list.go`; delegate to `filter.Match`.
- **A6** Replace retriever `stringPtr`/`intPtr` with `k8s.io/utils/ptr.To`.
- **A7** Remove the `rest` import hack (`var _ *rest.Config`) in
  `kubernetes/pod_exec.go`.
- **A8** Register k8s `NewClusterListTool` in `readOnlyConstructors` (was
  defined + compile-checked but never built).
- **A9** Remove the unreachable `buildQuery == nil` branch in
  `tool/opensearch/opensearch_log_kubernetes.go`.
- **A10** `gofmt` pass + lowercase error strings in `memory/file/file.go`
  ("Failed ..." → "failed ...").

## Group B — Validation / API convention fixes

- **B1** `sizecap.NewSplitter` now calls `validate.Struct` after defaults;
  `ChunkSize` tag changed `required,gte=1` → `omitempty,gte=1`.
- **B2** `loader/opensearch.NewOpensearchLoader` uses `validate.Struct` instead
  of manual `config == nil` / `len(URLs) == 0`.
- **B3** `tool/opensearch.NewClient` adds a nil guard and drops the redundant
  `TLSSkipVerify: false`.
- **B4** `memory/file.NewFileMemory` and `GetDefaultMemory` now return an error
  instead of swallowing it and returning `nil` (breaking; callers + tests
  updated: example, `file_test.go`, `session_test.go`, `runner_test.go`).
- **B5** `validateParams` wrappers in argocd/prometheus left as-is (kept for
  in-package naming consistency; prometheus tests depend on them).
- **B6** Rename argocd `Config.Url` → `URL` and `RepositoryListOutput.Url` →
  `URL` (json tags preserved); updated client.go, tests, README (breaking).

## Group C — Shared `libs/toolkit` helpers

- **C1** New `libs/toolkit/confirm`: `RequireConfirmation` (k8s: create/delete/
  patch/apply) and `RequireConfirmationForAction` (argocd: create/delete/sync).
- **C2** `marshal.Outputs([]json.RawMessage)` added; new `libs/toolkit/toolutil`
  with `NotFoundError(kind,name,known)` and `SortedKeys[V]`. Wired into
  prometheus (`marshalOutputs`, `instanceNotFoundError`, `GetInstanceNames`),
  argocd (`instanceNotFoundError`, `GetInstanceNames`), and kubernetes
  (`clusterNotFoundError`, `GetClusterNames`).
- **C3** Marker helpers unified in `components/memory` (`HasBoolMarker`,
  `SetBoolMarker`); `IsSummary`/`IsIncomplete`/`IsEphemeral`/`MarkIncomplete`,
  `agent/memory.IsMemoryContext`/`NewMemoryContextMessage`, and
  `contextopt.isPruned`/`isCompressed` now delegate. New `libs/toolkit/strutil`
  (`Truncate`, `StripMarkdownFences`, `ExtractJSONBlock`) used by
  `agent/memory/extractor.go`, `contextopt/optimizer.go`, `summarizer.go`.
  Stream-collect unification was intentionally **skipped** (divergent
  filtering/error semantics; low value).

## Group D — Larger refactors

- **D1** New `libs/toolkit/osclient` (`Config`, `New(cfg, timeout)`) replaces the
  5 duplicated client builders in indexer/retriever/loader/memory/tool
  OpenSearch packages. Hit-projection/scroll dedup was **not** done (see
  Remaining).
- **D2** `components/memory` gains `LastSummaryIndex` and `SelectWindow`; the
  identical ~90-line `GetWindow` and `lastSummaryIndexLocked` in `memory/file`
  and `memory/opensearch` now delegate.
- **D3** `middleware/safety/middleware.go` collapsed via `preflight`,
  `auditReject`, `auditResult` helpers; the four Wrap methods shrank from
  ~600 → ~350 lines (stream-audit goroutines left as-is).
- **D4** argocd: new generic `filterMapMarshal[T,O]` used by all 5 list tools;
  new `applyExcludes(map[string]func())` used by all 4 describe tools; fixed
  `application_delete.go` to use `projectFilter`.
- **D5** kubernetes: `toGVR(group,version,resource)` and
  `newBaseToolWithDynamic(configs)` helpers; the 5 resource constructors and 6
  GVR sites simplified; dead `ResourceDescribeOutput` removed (reuses
  `DescribeOutput`); shared `applyFieldExclusions` on `DescribeOutput`.

## Group E — Optional cleanups

- **E3 (done)** `safety/policy.go`: `structpb.NewStruct` replaces
  `mapToProtoStruct`; `matchesTool` uses `slices.Contains`; unused
  `CELPolicy.env` field dropped.
- **E1 (skipped)** `contentcomp/store.go` lazy map init kept — removing it would
  break zero-value `MemoryStore{}` users since `Get` does not nil-guard.
- **E4 (skipped)** `sizecap.mergeOverlap` — potential over-`chunkSize` behavior
  left unchanged to avoid altering splitter semantics without new tests.
- Remaining E items (jsoncrush marker keys, websearch `doGET`, prometheus
  `prepare`, opensearch `extractLogLines`, `MaxAge` wiring) not applied — see
  Remaining.

---

## New packages added

- `libs/toolkit/confirm/` (+ README)
- `libs/toolkit/toolutil/` (+ README)
- `libs/toolkit/strutil/` (+ README)
- `libs/toolkit/osclient/` (+ README)

READMEs for the removed helpers were updated:
`libs/toolkit/{filter,validate,marshal}/README.md`.

---

## Verification

- `go build ./...` — passes.
- `go vet ./...` — passes.
- `go test ./...` — all packages pass EXCEPT
  `components/tool/kubernetes` `TestToolSuite`, which fails in `SetupSuite`
  because `envtest` needs `/usr/local/kubebuilder/bin/etcd` (kube-apiserver/etcd
  binaries). Confirmed pre-existing via `git stash` on a clean tree — unrelated
  to these changes. Requires installing `setup-envtest` binaries to run.

Suggested re-run before PR:

```bash
go build ./... && go vet ./... && go test ./...
# For the k8s suite:
#   go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
#   KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test ./components/tool/kubernetes/
```

---

## Breaking changes to call out in the PR

1. `memory/file.NewFileMemory` / `GetDefaultMemory` now return
   `(memory.Memory, error)`.
2. argocd `Config.Url` → `Config.URL` (and `RepositoryListOutput.Url` → `URL`;
   JSON wire tag `url` unchanged).
3. Deleted public packages `libs/pricer` and `libs/toolkit/guidance`, and
   removed exported `validate.StructName`, `filter.MatchString`,
   `marshal.MustUnmarshal`, `argocd.MustMarshal`.

---

## Remaining / not-yet-done (optional follow-ups)

- **D1 (rest)** Unify OpenSearch hit→map projection and scroll loops
  (`retriever.searchHitToMap`, `parser.buildMeta`, `reconcile.go` decode loops)
  and the `_id/_index/...` key constants into `osclient`.
- **E2** jsoncrush: reconcile const marker keys vs literal struct tags.
- **E6** websearch: shared `doGET` + retry wrapper; `rejectPrivateIPs`.
- **E7** prometheus: `prepare(instance,filter)` to remove the repeated Invoke
  prologue.
- **E8** tool/opensearch: `extractLogLines` shared by Invoke + stream paths.
- **E5** `agent/memory` `MaintainerConfig.MaxAge` is reachable code but not
  wired through `NewMemoryAgent` — expose in the agent `Config` or document.
- Repo is not globally `gofmt`-clean (pre-existing, likely custom import
  grouping); only new/edited files were formatted to avoid noisy diffs. Decide
  whether a repo-wide `gofmt`/`goimports` pass is in scope.
