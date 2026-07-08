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
  type: one of `agent`, `document`, `embedding`, `indexer`, `model`, `prompt`,
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

### OpenSearch clients

- Two OpenSearch libraries coexist intentionally in this repository:
  - `github.com/cloudwego/eino-ext/components/{retriever,indexer}/opensearch3`
    for eino COMPONENTS (`Retriever`/`Indexer`). These use the eino
    OpenSearch client scaffolding.
  - `github.com/disaster37/opensearch/v4` when you need the full OpenSearch
    CLIENT (scroll, delete-by-query, put-mapping, querydsl) that the eino
    client does not cover.
  Do not unify them. Each OpenSearch package README should recall which
  client it uses and why.

### OpenSearch shared library patterns

When implementing OpenSearch-backed tools:

- **Use PIT scrolling**: Always use Point-in-Time (`POST
  /_search/point_in_time`) with `search_after` for result pagination instead
  of the legacy Scroll API. PIT provides a consistent snapshot view and is
  the recommended approach for deep pagination.

- **Provide a ResultParser callback**: Every OpenSearch tool that returns
  search results MUST accept an optional `ResultParser` function (type
  `func(ctx context.Context, hit map[string]any) (string, error)`) in its
  constructor config. The parser receives the full hit map (source fields +
  `_id`, `_index`, `_score`, `_version`) and returns a formatted string.
  When `ResultParser` is nil, the default formatter serializes each hit as
  compact JSON.

- **Propose both stream and regular modes**: Every OpenSearch tool MUST
  implement both `tool.InvokableTool` and `tool.StreamableTool`.

- **Keep tools generic**: Tool parameters should accept arbitrary query
  strings and indices. Project-specific wrappers (e.g., Kubernetes log search)
  should be built on top of the generic tool.

## Components

- Follow the existing component layout: a `Config` struct with `validate` and
  `jsonschema` tags, a `New...` constructor, `emperror.dev/errors` for error
  wrapping, and the shared validation helper.

- Every `New...` constructor MUST validate its config by calling the shared
  helper `github.com/webcenter-fr/eino-ext/libs/toolkit/validate`:

  ```go
  import "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"

  func NewXxx(ctx context.Context, cfg *Config) (*Xxx, error) {
      if cfg == nil {
          cfg = &Config{}
      }
      // ... apply defaults ...
      if err := validate.Struct(cfg); err != nil {
          return nil, err   // already wrapped by the helper
      }
      ...
  }
  ```

- Do NOT instantiate `validator.New()` directly or write ad hoc manual checks
  (`if len(cfg.URLs) == 0`): declare the constraint in the struct tag
  (`validate:"..."`) and let `validate.Struct` enforce it.
- Call `validate.Struct` AFTER applying defaults, so validation runs against
  the final values. Do not use `required` on a field that receives a default
  (use `omitempty,gte=1` instead).

### Constructor Context

- Every `New...` constructor for a component or shared helper that creates
  remote clients (e.g. `NewClient`, `BuildClients`, `newBaseTool`,
  `osclient.New`) MUST accept `ctx context.Context` as its **first** parameter.
- Thread `ctx` through from the top-level constructor down to every
  underlying client-creation call. Even when the underlying library does
  not yet require a context, having it in the signature keeps the API
  future-proof and consistent across the codebase.

- A component is considered complete only when ALL of the following exist:
  1. `xxx.go` with `Config` (tags `validate`+`jsonschema`), `New...`, and a
     compile-time interface check `var _ <abstraction>.<Interface> = (*Xxx)(nil)`.
  2. `xxx_test.go`: table-driven tests covering the normal case, parameter
     errors, and (with mocks) external dependencies. No test should depend on a
     live external service.
  3. `README.md`: what the component does, a constructor snippet, and which
     eino abstraction it implements.
  4. A package comment `// Package xxx ...` at the top of the file.
  5. **Checkup**: `check.go` + `check_test.go` with a `Check()` function that
     probes connectivity and RBAC permissions for all configured instances.
     Returns `checkup.Results` from `libs/toolkit/checkup/`. See existing
     components (e.g. `components/tool/argocd/check.go`) for patterns.

## License Headers

Do **NOT** add license banner comments to source files. The repository already
has a `LICENSE` file at the root, which covers all code. Duplicating it in every
file adds noise and maintenance overhead with no legal benefit.

```go
// DO NOT do this:
/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * ...
 */

// Just start with the package declaration:
package mypackage
```

## Validation

Before opening a pull request:

```bash
go build ./...
go vet ./...
go test ./...
```

## Component Design Principles

These principles apply to **every** component (`agent`, `document`,
`embedding`, `indexer`, `model`, `prompt`, `retriever`, `tool`), to ADK
middlewares (`components/middleware/`), to callbacks, and to `libs/`.
Subsections marked **(tools only)** concern only `components/tool/`.

### Interfaces and Default Implementations

- **Define interfaces** for tool families that share common behavior. For example, all "list" tools should implement a common interface.
- **Provide default implementations** when a pattern is repeated across multiple tools. Extract common logic into generic helpers or base types.
- **Use generics** to reduce code duplication when tools operate on similar data structures with different types.

```go
// Good: Define an interface for list tools
type ListToolInterface interface {
    Invoke(ctx context.Context, params ListParams) (string, error)
}

// Good: Generic factory for creating list tools
func NewListTool[T client.Object, O OutputObject[T]](
    ctx context.Context, 
    configs Configs, 
    toolName string, 
    description string,
) (tool.InvokableTool, error)
```

### Code Organization

- **Extract common patterns** into shared helpers under `libs/toolkit/` when the same logic appears in multiple tool packages.
- **Avoid duplication** between tool packages. If ArgoCD and Kubernetes both need `CompileFilter`, it belongs in `libs/toolkit/filter/`.
- **Group related tools** with a factory function that creates all tools for a component at once.

```go
// Good: Factory function to create all ArgoCD tools
func NewAllTools(ctx context.Context, configs Configs) ([]tool.InvokableTool, error) {
    tools := make([]tool.InvokableTool, 0)
    
    appList, err := NewApplicationListTool(ctx, configs)
    if err != nil {
        return nil, err
    }
    tools = append(tools, appList)
    
    // ... other tools
    
    return tools, nil
}
```

### Naming Conventions

- Follow Go naming conventions: `ToJSON` not `ToJson`, `ID` not `Id`, `URL`
  not `Url`, **`OpenSearch` not `Opensearch`, `GitHub` not `Github`, `GitLab`,
  `PostgreSQL`, `gRPC`, `API`, `HTTP`**. When in doubt, use the official
  casing of the product for all exported identifiers and `GetType()` return
  values.
- Use descriptive names that reveal intent: `ApplicationListOutput` is better than `Output`.
- Keep parameter struct names consistent: `XxxParams` for input, `XxxOutput` for output.

#### Provider and product names in configuration strings

Configuration string values that name a provider, product, or service (e.g.
`Plan`, `Provider`) must use the official full name in lowercase kebab-case, not
an abbreviation:

```go
// Good: official name, full form
Plan: "github-copilot"

// Bad: abbreviation, not the canonical name
Plan: "copilot"
```

This ensures:
- **Consistency** with `libs/modelsdev` provider buckets (which use the official
  models.dev naming) and with other components in the repository.
- **Clarity**: "github-copilot" is unambiguous; "copilot" could mean any number
  of products.

### Security Guidelines **(tools only)**

Tools that execute commands or access external systems must implement security controls:

- **Command blocklists** must be robust against bypass attempts. Test with variations like `/bin/rm`, `./rm`, absolute paths, and shell builtins.
- **Validate all inputs** including URLs (prevent SSRF), file paths (prevent directory traversal), and command arguments.
- **Implement timeouts** for all external API calls to prevent resource exhaustion.
- **Rate limiting** should be considered for tools that make API calls.
- **Redact sensitive data** in outputs (secrets, passwords, tokens).

```go
// Bad: Easily bypassed blocklist
var blocklist = []string{`^\s*rm\s`}  // Doesn't catch /bin/rm, ./rm, etc.

// Good: More robust blocklist with multiple patterns
var blocklist = []string{
    `\brm\b`,           // Matches rm anywhere
    `^\s*/[^\s]*/rm\b`, // Matches absolute paths
    `^\s*\./rm\b`,      // Matches relative paths
}
```

### Usability

- **Provide helper functions** to simplify common use cases. Users should be able to get started with minimal configuration.
- **Document tool usage** with examples in the component's README.md.
- **Export configuration types** so users can customize behavior without modifying the library.
- **Consider builder patterns** for complex configurations.

```go
// Good: Simple API for common case
tools, err := argocd.NewAllTools(ctx, argocd.Configs{
    "prod": argocd.Config{URL: "https://argocd.example.com"},
})

// Good: Builder for complex configuration
builder := argocd.NewConfigBuilder().
    WithInstance("prod", "https://argocd.example.com").
    WithToken(os.Getenv("ARGOCD_TOKEN")).
    WithTimeout(30 * time.Second)
configs := builder.Build()
```

### Error Handling

- **Wrap errors** with context using `emperror.dev/errors` to help users debug issues.
- **Validate parameters early** and return clear error messages.
- **Don't panic** in tool implementations; return errors instead.

### Testing

- **Write table-driven tests** for tool invocations with various parameter combinations.
- **Test error cases** including invalid parameters, network failures, and permission errors.
- **Use mocks** for external dependencies to ensure tests are fast and reliable.

## Checklist before PR

- [ ] `go build ./...`, `go vet ./...`, `go test ./...` pass.
- [ ] Every new `Config` has `validate`+`jsonschema` tags AND its `New...`
      calls `validate.Struct(cfg)` after defaults.
- [ ] Every new component has: table-driven test, README, package comment,
      and a `var _ Interface = (*T)(nil)` compile-time check.
- [ ] Every new component has a checkup: `check.go` + `check_test.go` with a
      `Check()` function returning `checkup.Results` (see `libs/toolkit/checkup/`).
      Probe each read endpoint, use list→describe chains, and handle "no
      resources" as `"limited"` not `"error"`.
- [ ] Every `New...` constructor accepts `ctx context.Context` as its first
      parameter and threads it through to all client creation calls.
- [ ] Naming: acronyms and brands use official casing (`OpenSearch`, `GitHub`,
      `URL`, `ID`, `JSON`).
- [ ] Errors are wrapped with `emperror.dev/errors` (include operation context).
- [ ] No license banner added.
- [ ] Component is placed under the correct eino abstraction (see Project structure).
- [ ] No duplication of helpers already present in `libs/toolkit/`.
