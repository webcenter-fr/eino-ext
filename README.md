# eino-ext

Extensions for [eino](https://github.com/cloudwego/eino): tool families, MCP
servers, shared libraries, and the standalone human-approval service.

## Layout

- `components/` — eino components and tool families (Kubernetes, ArgoCD,
  Prometheus, Grafana, GitHub, shell, …).
- `libs/` — shared support libraries (`libs/mcp`, `libs/toolkit/…`).
- `server/approval/` — standalone out-of-band approval service with an embedded
  Nuxt UI.
- `cmd/mcp-server/` — deployable MCP server binary (one binary, four families).
- `helm/eino-ext/` — Helm chart for the MCP servers and the approval service.
- `.dagger/` — Dagger CI module.
- `docs/design/` — bilingual technical design documents.

## MCP servers

One binary serves any family:

```bash
go run ./cmd/mcp-server --server kubernetes --config config.yaml
go run ./cmd/mcp-server --server grafana --config config.yaml --transport http
```

Configuration uses YAML with file/environment secret references. See
[`libs/mcp/serverconfig`](./libs/mcp/serverconfig) and
[`cmd/mcp-server`](./cmd/mcp-server).

## Deployment

Container images:

```bash
make docker-build-mcp
make docker-build-approval
```

Helm chart:

```bash
helm lint helm/eino-ext
helm template t helm/eino-ext
helm install eino-ext helm/eino-ext
```

The chart defaults to read-only Kubernetes RBAC, authenticated MCP, and the
file-backed approval store. `allowWrite` and `allowExec` are dangerous opt-ins.

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

See the [deployment and CI design doc](./docs/design/04-deployment-and-ci.en.md)
and [`CONTRIBUTING.md`](./CONTRIBUTING.md).
