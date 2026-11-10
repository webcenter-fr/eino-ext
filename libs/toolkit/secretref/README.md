# secretref — resolve secret references

Resolves a secret from a file, an environment variable, or a literal value, in
that order. A file wins when both a file and an environment variable are set.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/secretref"

value, err := secretref.Resolve("/run/secrets/token", "TOKEN", "", "bearer token")
```

- A file is trimmed of trailing `\n`/`\r\n`, rejected when empty, and rejected
  when larger than 64 KiB.
- An environment variable is rejected when set but empty.
- The value is never logged; only the chosen source is logged at debug level.
