# marshal — Panic-on-error JSON utilities

`marshal` provides convenience functions for JSON marshaling and unmarshaling
that panic on error. Designed for use in tool initialization and test setup
where invalid JSON is a programmer error, not a runtime condition.

## Functions

```go
func MustMarshal(v any) []byte
func MustUnmarshal(data []byte, v any)
```

- `MustMarshal` — calls `json.Marshal` and panics on error.
- `MustUnmarshal` — calls `json.Unmarshal` and panics on error.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"

// In tool info construction (data controlled at compile time)
info := &schema.ToolInfo{
    ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
        "params": {
            Type: schema.DataType(gojsonschema.String),
            SubPragmas: map[string]string{
                "oneOf": string(marshal.MustMarshal([]string{"yaml", "json"})),
            },
        },
    }),
}
```

Only use these functions when the data is known to be valid at compile time.
For runtime user input, use `json.Marshal`/`json.Unmarshal` directly and handle
errors.
