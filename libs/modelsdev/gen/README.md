# gen — models.dev catalog fetcher

`gen` is a command-line tool that fetches the models.dev API catalog
(`api.json`) and writes it to disk. It is invoked via `go generate` or the
`models-dev-refresh` Makefile target to refresh the embedded catalog snapshot
in `libs/modelsdev`.

## Usage

```bash
go run ./libs/modelsdev/gen -out ../api.json
```

Or via the Makefile:

```bash
make models-dev-refresh
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `-url` | `https://models.dev/api.json` | Catalog API endpoint. |
| `-out` | `api.json` | Output file path. |
| `-timeout` | `30s` | HTTP request timeout. |

## Design

The fetched `api.json` is embedded into the `modelsdev` package via
`//go:embed`, providing a reproducible, offline-capable catalog. Review the
diff before committing — this is a deliberate, reviewed snapshot refresh, not
an automatic one.
