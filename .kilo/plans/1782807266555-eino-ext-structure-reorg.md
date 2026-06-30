# Plan — Align repo with official eino-ext structure + relocate `cachestab`

## Goal

Reorganize the repository so its layout follows the **official eino-ext project
structure**, move `cachestab` out of `components/middleware/` (it is a model
decorator, not an eino middleware), and document the structure convention in
`CONTRIBUTING.md`.

Module path is unchanged: `github.com/webcenter-fr/eino-ext`. All moves are
directory relocations within the module; **package names stay the same** (only
import paths change).

## Context / rationale

Official eino-ext (`github.com/cloudwego/eino-ext`) organizes code as:

- `components/<abstraction>/<impl>` where `<abstraction>` is an eino component
  type: `document, embedding, indexer, model, prompt, retriever, tool`.
- top-level `callbacks/` for `callbacks.Handler` implementations.
- top-level `libs/` for shared, non-component support code.

Why `cachestab` is misplaced: it only implements `model.ToolCallingChatModel`
(a model decorator) and ships **no** `adk.ChatModelAgentMiddleware`, so it is not
a "middleware" in the eino sense → it belongs under `components/model/`.
`contextopt` *does* ship a real `adk.ChatModelAgentMiddleware`
(`components/middleware/contextopt/middleware.go:18`), which justifies keeping a
project-specific `components/middleware/` category for adk middlewares.

## Target layout

```
components/
  model/cachestab/            ← moved from components/middleware/cachestab
  middleware/contextopt/      ← unchanged (genuine adk.ChatModelAgentMiddleware)
  memory/ (file, session)     ← unchanged (project extension; no eino equivalent)
  prompt/                     ← unchanged (already matches official)
  tool/ (convertor, kubernetes, opensearch) ← unchanged (already matches)
callbacks/
  activity/ (sse)             ← moved from components/observability/activity
libs/
  contentcomp/ (jsoncrush, shellout) ← moved from components/contentcomp
```

Decisions already confirmed with the user:
- Scope = full reorganization now.
- Keep `components/middleware/` strictly for `adk.ChatModelAgentMiddleware`
  implementations; `contextopt` stays there.
- `contentcomp` moves to top-level `libs/`.

## Tasks (ordered)

### 1. Move `cachestab` → `components/model/cachestab`
- `git mv components/middleware/cachestab components/model/cachestab`
  (moves `cachestab.go`, `cachestab_test.go`, `example_test.go`, `README.md`).
- Update import in `components/model/cachestab/example_test.go` (was line 9):
  `.../components/middleware/cachestab` → `.../components/model/cachestab`.
- Package name stays `cachestab` (no other code edits).

### 2. Move `observability/activity` → top-level `callbacks/activity`
- `git mv components/observability/activity callbacks/activity` (carries the
  `sse/` subpackage, all `.go`, and READMEs).
- Update imports `.../components/observability/activity` → `.../callbacks/activity`:
  - `callbacks/activity/sse/sse.go` (was line 32)
  - `callbacks/activity/sse/sse_test.go` (was line 31)
- Remove the now-empty `components/observability/` directory.
- Package names stay `activity` / `sse`.

### 3. Move `contentcomp` → `libs/contentcomp`
- `git mv components/contentcomp libs/contentcomp` (carries `jsoncrush/`,
  `shellout/`, READMEs).
- Update every import `.../components/contentcomp...` → `.../libs/contentcomp...`:
  - Internal: `libs/contentcomp/jsoncrush/jsoncrush.go`,
    `libs/contentcomp/jsoncrush/compressor.go`,
    `libs/contentcomp/jsoncrush/jsoncrush_test.go`,
    `libs/contentcomp/shellout/shellout.go`,
    `libs/contentcomp/shellout/compressor.go`,
    `libs/contentcomp/shellout/shellout_test.go`
  - External consumers (in `components/middleware/contextopt`):
    - `optimizer.go` (was line 24): `.../components/contentcomp`
    - `contentcomp_test.go` (was lines 10–11): `.../components/contentcomp` and
      `.../components/contentcomp/jsoncrush`
- Remove the now-empty `components/contentcomp/` directory.
- Package names stay `contentcomp` / `jsoncrush` / `shellout`.

### 4. Document the structure convention in `CONTRIBUTING.md`
Add a new **"Project structure"** section (place it before the existing
"Components" section). It must state:
- The repo follows the official eino-ext layout.
- `components/<abstraction>/<impl>`; `<abstraction>` ∈
  `document, embedding, indexer, model, prompt, retriever, tool`.
- A model decorator/wrapper (implements `model.BaseChatModel` /
  `model.ToolCallingChatModel`) is a **model** component → `components/model/...`,
  not a "middleware". Cite `cachestab` as the canonical example.
- `callbacks.Handler` implementations → top-level `callbacks/`.
- Shared, non-component support libraries → top-level `libs/`.
- Documented **project-specific extensions** (deviations from official eino-ext):
  - `components/middleware/` — reserved strictly for eino adk middlewares
    (`adk.ChatModelAgentMiddleware`), e.g. `contextopt`.
  - `components/memory/` — conversation-history persistence (no eino-ext
    equivalent).
- Keep the existing per-component rules ("Components" section: `Config` +
  validate/jsonschema tags, `New...` constructor, `emperror.dev/errors`,
  `validator/v10`, tests + `README.md` per component).

## Validation

```bash
go build ./...
go vet ./...
go test ./...
```

All three must pass. `Makefile` uses `./...` and needs no path edits.

## Notes / non-goals

- Markdown READMEs travel with their packages via `git mv`.
- Relative link in `components/memory/session/README.md` → `../../middleware/contextopt`
  stays valid (contextopt does not move). No README link fixes required by the
  moves.
- Historical `.kilo/plans/*.md` files reference old paths; leave them as-is
  (historical record).
- No `go.mod` / module-path change.
- Implementation requires source edits and `git mv`; run under an
  implementation-capable agent.
