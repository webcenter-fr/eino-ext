# guidance — LLM guidance text generation

`guidance` produces formatted guidance text appended to tool descriptions to
instruct LLMs on how to use tools effectively and limit output size.

## Types

```go
type ListField struct {
    Name        string
    Description string
}
```

## Functions

```go
func List(toolName string, fields ...ListField) string
func Describe(excludeFields ...string) string
```

- `List` — generates guidance for list tools: narrow queries, use labels,
  paginate, and specify which fields to include.
- `Describe` — generates guidance for describe tools: use the `excludeFields`
  parameter to limit output.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/guidance"

desc := "List pods in a Kubernetes cluster." + guidance.List("pod", guidance.ListField{
    Name:        "namespace",
    Description: "Set `namespace` whenever you know it.",
})
```

The generated guidance is appended to the tool description string, advising the
LLM to provide specific field values, use label selectors, and set reasonable
limits to avoid overwhelming outputs.
