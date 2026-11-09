# `libs/toolkit/dagger`

A wrapper around the Dagger engine client for building and managing OCI
containers with shared cache volumes, egress-proxy bindings, and an explicit
workspace mount/export. It is a reusable, non-component library (mirrors
`libs/toolkit/osclient`).

## Usage

```go
cfg := &dagger.EngineConfig{}
client, err := dagger.NewClient(ctx, cfg)
defer client.Close()

cont, err := client.Container(ctx, "golang:1.22",
    dagger.WithWorkspace("/path/to/project"),
    dagger.WithCacheVolume("/go/pkg/mod", dagger.CacheKeyForProfile("golang:1.22", "golang")),
)
```

`WithWorkspace(hostPath)` mounts the host directory at `/workspace` and sets it
as the container working directory. `WithWorkdir` is retained as an alias.

## Exporting container changes

Dagger mounts a snapshot of the host directory; container writes do not reach
the host automatically. `ExportWorkspace` writes the container `/workspace`
back to a host path:

```go
if err := client.ExportWorkspace(ctx, cont, "/path/to/project"); err != nil {
    return err
}
```

The export goes to a temporary directory first, then syncs the non-`.git`
entries onto the host. Deletions are propagated. The live `.git` directory is
never exported back, so container `git` commands cannot corrupt the refs that
the GitHub commit/push tool later pushes.

The real Dagger path is covered by an opt-in test:

```bash
EINO_DAGGER_E2E=1 go test ./libs/toolkit/dagger -run TestDaggerWorkspaceE2E -v
```
