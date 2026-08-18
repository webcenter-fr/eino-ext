# validate — Struct validation wrapper

`validate` provides a singleton wrapper around `go-playground/validator` for
validating structs with `validate` tags.

The error messages produced by `Struct` are designed to be read by an LLM
agent that calls a tool: each violated constraint is reported as a short,
prescriptive sentence naming the **JSON parameter**, the **offending value**,
the **expected constraint**, and **how to fix it**, so the model can adapt its
arguments and retry.

## Functions

```go
func Struct(s interface{}) error
```

- `Struct` — validates a struct and wraps any error with the validated type's
  name plus an LLM-actionable description of every violation.

The validator is configured to report fields by their **JSON tag name** (the
same name the caller sends in a tool call), so error messages refer to
parameters the LLM recognizes (e.g. `maxLines`, not `MaxLines`).

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"

type Config struct {
    URL string `validate:"required,url" json:"url"`
}

cfg := Config{}
if err := validate.Struct(cfg); err != nil {
    // "invalid parameters for *Config: parameter 'url' is required but was
    //  not provided; supply a non-empty value and retry"
}
```

## Message format

For each violated field, the helper emits a sentence such as:

| Tag        | Example message                                                                              |
|------------|----------------------------------------------------------------------------------------------|
| `required`  | `parameter 'cluster' is required but was not provided; supply a non-empty value and retry`   |
| `max` (num) | `parameter 'maxLines' (value: 1000) must be <= 500; reduce it and retry`                      |
| `min` (num) | `parameter 'pageSize' (value: 0) must be >= 1; increase it and retry`                        |
| `max` (len) | `parameter 'tags' (length: 65) must contain at most 64 item(s); reduce it and retry`         |
| `oneof`     | `parameter 'health' (value: "foo") must be one of: up, down, unknown; change it ... and retry` |
| `url`       | `parameter 'url' (value: "nope") must be a valid URL; fix it and retry`                      |

Errors are wrapped with `emperror.dev/errors` and prefixed with the validated
struct's type name for easier debugging.

## Why LLM-friendly messages

When a tool rejects an LLM-produced argument, the error string is fed back to
the model. Opaque messages like
`Key: 'PodLogParams.MaxLines' Error:Field validation for 'MaxLines' failed on
the 'max' tag` do not tell the model what bound it broke or how to retry. The
shared helper turns every `validate.Struct` failure across all tools into a
message the model can act on directly.
