# validate — Struct validation wrapper

`validate` provides a singleton wrapper around `go-playground/validator` for
validating structs with `validate` tags.

## Functions

```go
func Struct(s interface{}) error
func StructName(s interface{}, name string) error
```

- `Struct` — validates a struct and wraps any error with the type name.
- `StructName` — validates a struct and wraps any error with a custom label.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"

type Config struct {
    URL string `validate:"required,url"`
}

cfg := Config{}
if err := validate.Struct(cfg); err != nil {
    // "Config.URL: required"
}
```

Errors are wrapped with `emperror.dev/errors` and include the validated struct's
type name or the custom name for easier debugging.
