# eino-ext

Extensions for [eino](https://github.com/cloudwego/eino): tool families and
shared libraries.

## Layout

- `components/` — eino components and tool families (Kubernetes, ArgoCD,
  Prometheus, Grafana, GitHub, shell, …).
- `callbacks/` — `callbacks.Handler` implementations.
- `libs/` — shared support libraries (`libs/toolkit/…`).
- `.dagger/` — Dagger CI module (Go gates only).
- `docs/design/` — bilingual technical design documents.

## Development

```bash
go build ./...
go vet ./...
make test          # never bare `go test ./...`: envtest assets are required
make lint
bash scripts/check_components.sh
```

CI runs the same gates through Dagger:

```bash
dagger call ci --dry-run --version 0.0.0-dev export --path ./ci-out
```

See the [shared libraries design doc](./docs/design/02-eino-ext-shared-libraries.en.md)
and [`CONTRIBUTING.md`](./CONTRIBUTING.md).

## Related projects

MCP servers, the approval service, Helm charts and container images live in
[`webcenter-fr/go-mcp-platform`](https://github.com/webcenter-fr/go-mcp-platform).
