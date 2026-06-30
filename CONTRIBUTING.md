# Contributing to eino-ext

Thanks for contributing! This document captures repository conventions that are
not obvious from the code alone.

## Prompts

Prefer keeping LLM prompt text in dedicated Markdown files and embedding them
into the Go package with `//go:embed`, rather than inlining large multi-line
string literals in `.go` source.

This keeps prompts readable, diff-friendly, and editable without touching Go
code, while still compiling them into the binary (no runtime file I/O).

### Conventions

- Store prompt files under a `prompts/` directory inside the component package
  (e.g. `components/middleware/contextopt/prompts/summary_template.md`).
- Use the `.md` extension and one prompt per file.
- Embed each prompt into an exported (or package) `string` variable using
  `//go:embed`, and import the blank `embed` package:

  ```go
  import _ "embed"

  // DefaultSummaryTemplate is the anchored Markdown summary template.
  //
  //go:embed prompts/summary_template.md
  var DefaultSummaryTemplate string
  ```

- Keep the variable doc comment describing what the prompt is for; the file
  itself holds the prompt content.
- If a prompt must be parameterized, embed the static template and assemble the
  dynamic parts in Go (do not build prompts by string-concatenating many small
  embedded fragments).

### Rationale

- Prompts are content, not code: editing them should not require Go expertise.
- `//go:embed` keeps everything in a single binary with no external file
  dependencies at runtime.
- Markdown renders nicely in reviews and editors.

## String formatting

Prefer `fmt.Sprintf` over concatenating strings with `+`. It is more readable,
easier to maintain, and keeps format and arguments clearly separated.

```go
// Preferred
msg := fmt.Sprintf("user %s has %d items", name, count)

// Avoid
msg := "user " + name + " has " + strconv.Itoa(count) + " items"
```

## Project structure

The repository follows the official
[eino-ext](https://github.com/cloudwego/eino-ext) layout. Place new code
according to what it implements:

- `components/<abstraction>/<impl>` where `<abstraction>` is an eino component
  type: one of `document`, `embedding`, `indexer`, `model`, `prompt`,
  `retriever`, `tool`.
  - A model decorator/wrapper — anything implementing `model.BaseChatModel` or
    `model.ToolCallingChatModel` — is a **model** component and belongs under
    `components/model/...`, **not** a "middleware". The canonical example is
    `cachestab` (`components/model/cachestab`), a `ToolCallingChatModel`
    decorator.
- `callbacks/` (top level) — `callbacks.Handler` implementations, e.g.
  `callbacks/activity`.
- `libs/` (top level) — shared, non-component support libraries that are not
  tied to a specific eino abstraction, e.g. `libs/contentcomp`.

### Project-specific extensions

These directories deviate from the official eino-ext layout and exist only for
this project:

- `components/middleware/` — reserved strictly for eino adk middlewares
  (`adk.ChatModelAgentMiddleware`), e.g. `contextopt`. Do not place plain model
  decorators here.
- `components/memory/` — conversation-history persistence (no eino-ext
  equivalent).

## Components

- Follow the existing component layout: a `Config` struct with `validate` and
  `jsonschema` tags, a `New...` constructor, `emperror.dev/errors` for error
  wrapping, and `github.com/go-playground/validator/v10` for validation.
- Add tests alongside the implementation and a `README.md` per component.

## Validation

Before opening a pull request:

```bash
go build ./...
go vet ./...
go test ./...
```
