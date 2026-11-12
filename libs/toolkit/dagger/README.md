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

## Egress policy

`WithEgressPolicy(pol)` binds a working egress proxy to the container and sets
`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` (plus lowercase) in its environment:

```go
pol := &egress.Policy{
    DefaultDeny: true,
    AllowHosts:  []string{"registry.npmjs.org", "proxy.golang.org"},
}
cont, err := client.Container(ctx, "golang:1.22", dagger.WithEgressPolicy(pol))
```

The proxy runs in the **client process** (the MCP server), not in a container.
`EgressProxyService` starts the real `egress.Proxy` on `127.0.0.1:0` and exposes
it to containers through a Dagger host tunnel on port `3128`, bound as
`eino-egress`. No proxy image is published and nothing is compiled in-engine.

- A nil policy is a strict no-op: no proxy, no service binding, no env var.
- One proxy is started per distinct policy fingerprint and reused across
  containers. `Client.Close()` stops every proxy.
- If the proxy cannot start, `Container()` returns an error wrapping
  `ErrEgressProxyUnavailable`. It never silently returns an unrestricted
  container.
- The proxy is a **soft control**: raw TCP ignores `HTTP_PROXY`. See
  [`libs/toolkit/egress`](../egress/README.md) for the verified bypass and the
  nftables/`NetworkPolicy` recommendation.

## Language cache presets

`CacheMountsForProfile(name)` returns the container paths to back with cache
volumes for a profile. `WithProfileCaches` mounts all of them with stable
per-path keys:

```go
cont, err := client.Container(ctx, "golang:1.22",
    dagger.WithProfileCaches("golang", "golang:1.22"),
)
```

| Profile | Paths |
|---|---|
| `golang` | `/go/pkg/mod`, `/root/.cache/go-build`, apt |
| `node` | `/root/.npm`, apt |
| `python` | `/root/.cache/pip`, apt |
| `java` | `/root/.m2`, `/root/.gradle/caches`, apt |
| `rust` | `/usr/local/cargo/registry`, `/usr/local/cargo/git`, apt |
| `php` | `/root/.composer/cache`, apt |
| `default`, `""`, unknown | apk only |

where apt is `/var/cache/apt` + `/var/lib/apt` and apk is `/var/cache/apk`.
Profile names are matched case-insensitively.

All paths are outside `/workspace`, so a cache volume never shadows part of the
tree that `ExportWorkspace` copies back. For a cached cargo target directory,
set `CARGO_TARGET_DIR=/cache/cargo-target` and mount that path explicitly.

The real Dagger path is covered by opt-in tests:

```bash
EINO_DAGGER_E2E=1 go test ./libs/toolkit/dagger -run 'TestDagger.*E2E' -v
```
