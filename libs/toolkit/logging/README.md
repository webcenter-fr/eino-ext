# logging — shared logrus configuration

Configures the process-wide `logrus` standard logger from a level and a format.
Both the MCP server and the approval server use it so their `--log-level` and
`--log-format` flags behave identically.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/logging"

if err := logging.Configure("info", "json"); err != nil {
    return err
}
```

- `level`: any `logrus` level name (`trace`, `debug`, `info`, `warn`, `error`,
  `fatal`, `panic`). Empty defaults to `info`.
- `format`: `text` or `json`. Empty defaults to `text`.

Invalid values return an error wrapped with `emperror.dev/errors`; the logger is
left untouched in that case.
