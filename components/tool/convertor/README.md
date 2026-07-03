# Convertor Tool

An eino tool that converts data between YAML and JSON formats. Accepts input of
either type and produces the other, with a 1MB input limit.

## Parameters

```json
{
    "input": "...",
    "input_type": "yaml",
    "output_type": "json"
}
```

| Field | Description |
|---|---|
| `input` | The raw input string (required). |
| `input_type` | Format of the input: `yaml` or `json` (required). |
| `output_type` | Desired output format: `yaml` or `json` (required). |

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/tool/convertor"

t, err := convertor.NewConvertorTool(ctx)

result, err := t.InvokableRun(ctx, `{
    "input": "{\"key\": \"value\"}",
    "input_type": "json",
    "output_type": "yaml"
}`)
```
