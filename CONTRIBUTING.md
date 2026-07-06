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

## Tool Design Principles

When creating new tools (components under `components/tool/`), follow these principles to ensure maintainability, usability, and security.

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

- Follow Go naming conventions: `ToJSON` not `ToJson`, `ID` not `Id`, `URL` not `Url`.
- Use descriptive names that reveal intent: `ApplicationListOutput` is better than `Output`.
- Keep parameter struct names consistent: `XxxParams` for input, `XxxOutput` for output.

### Security Guidelines

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
    "prod": argocd.Config{Url: "https://argocd.example.com"},
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
