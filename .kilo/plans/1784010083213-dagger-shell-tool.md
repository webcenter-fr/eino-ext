# Dagger-Backed Shell Tool for LLM Agents/Supervisors

## Goal

Give LLM agents and supervisors a **secure, sandboxed shell** backed by the
**Dagger.io engine**, so an LLM can run commands inside a **per-language OCI base
image** (golang, node/vue, python, java, …), install ephemeral tools with
`sudo`/root, and **share that installed context across multiple agents and
supervisors working on the same project** — all without touching the host
program's own context. A **profile supervisor** dynamically selects, at runtime,
the sub-agent whose shell sandbox uses the right base image for the task.

This is a **plan only**. No source code is written here; sketches below are
design-level type/signature outlines (matching the repo's plan conventions),
not implementations.

## Requirements (precise)

1. Shell access with isolated **process + filesystem** — provided by Dagger
   containers (engine runs in its own process; installs land in layers/cache,
   never the host program).
2. **Network isolation**: "standard local network forbidden by default, with an
   option to allow it." Dagger has **no per-exec network flag** (verified:
   `ContainerWithExecOpts` has no `network` field), so this is enforced by an
   **in-library egress proxy** (see §Security).
3. Allow the LLM to use **sudo/install** needed tools — containers run as root by
   default; `apt-get`/`pip`/`go install` work directly. All installs happen
   **outside the current program context** (inside the engine/container).
4. **Consume Docker/OCI base images** per project type — `Container.From(...)`.
5. **Share context by base image across agents/supervisors** to avoid repeated
   installs — Dagger `cacheVolume` + `WithMountedCache`
   (`CacheSharingMode{SHARED,PRIVATE,LOCKED}`) keyed by base image + profile.
6. **Provide a Dagger engine URL at tool registration** — via `DAGGER_HOST` env
   (canonical) and/or `WithRunnerHost` (CLI-provisioning only); `WithConn` escape
   hatch for direct remote connect.
7. **Discover project type and choose the right base image at runtime** — a
   `ProfileResolver` (file-marker heuristics) + a shipped **profile supervisor
   agent** that selects the matching sub-agent tool.

## Investigation verdict — is Dagger a good idea?

**Yes, with one engineered gap.** Confirmed against `dagger.io/dagger@v0.21.7`
and the current GraphQL API.

**Strengths (verified):**
- OCI base images natively via `Container.From("golang:1.22")` etc.
- Robust shared caching via `cacheVolume`/`WithMountedCache` (persists across
  runs and across agents sharing one engine + cache key) — strictly better than a
  hand-rolled overlay+flock.
- Engine URL flexibility (`docker-container://`, `tcp://`, `unix://`,
  `ssh://`, `kube-pod://`, `podman-container://`, `pipe://`).
- Containers run as root → installs work; `InsecureRootCapabilities` available
  for rare needs (off by default).
- First-class Go SDK, Apache-2.0.

**The gap (engineered, not blocking):** no per-exec network control → "local
network forbidden by default" is enforced by an in-library egress proxy (soft
control: honored by apt/go/pip/npm; bypassable by tools that ignore proxy env).
A hard guarantee requires engine-side firewalling (documented, out of band).

**Conscious trade-off:** Dagger *is* an external engine, which reverses the
earlier "no external engine" constraint. Accepted by the user in exchange for
real OCI images + robust shared caching. **Precondition: a Dagger engine must be
reachable** (local CLI-provisioned `docker-container://`, or a remote
`tcp://`/`ssh://`/`kube-pod://` endpoint).

**Engine-URL caveat (verified):** `WithRunnerHost` "only has effect when
connecting via the CLI… only exposed for testing." Canonical connect path =
`DAGGER_HOST`/`_EXPERIMENTAL_DAGGER_HOSTS` env (or CLI). `WithConn` is the
direct-connect escape hatch for remote engines without the CLI. Implementer must
verify exact mechanics against the pinned SDK version.

## Key decisions

| Decision | Choice | Rationale |
|---|---|---|
| Engine | Dagger.io (accepted engine dependency) | OCI images + shared caching; user decision |
| Engine connect | `DAGGER_HOST` env (+`WithRunnerHost` for CLI; `WithConn` for direct remote) | `WithRunnerHost` is CLI-only (verified) |
| Network isolation | In-library egress proxy (default-deny + allowlist + `AllowLocalNetwork`), forced via `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` env | No native per-exec network flag |
| Base images | `Container.From(ociRef)` per profile | Native Dagger |
| Shared context | `cacheVolume`/`WithMountedCache` keyed by (image digest + profile); `LOCKED` for install steps; project workdir mounted rw (shared host path) | Persists across runs/agents; serializes installs |
| Session model | Per-session chained `*Container` snapshots (`.WithExec(...)` chain + `.Sync()`); FS state persists across the chain | Idiomatic Dagger; leverages layer cache; stateful across LLM turns |
| sudo/install | Run as root in container (default); `InsecureRootCapabilities` off | Installs work directly; privilege gated |
| Safety integration | Shell tool is a WRITE tool → safety-middleware gate (dryRun/confirmed) + CEL policy + audit; shared command blocklist | Reuse existing `libs/toolkit/safety` + `components/middleware/safety` |
| Tool interfaces | `tool.InvokableTool` + `tool.StreamableTool` (blocking + streamed stdout) | Matches pod_exec + CONTRIBUTING (OpenSearch) precedent |
| Scope (v1) | FULL: shell tool + egress proxy + ProfileResolver + default profiles + per-profile shell-tool factory + profile-supervisor agent + check + tests/README | User chose "full + ship a supervisor agent" |
| Package placement | shared libs in `libs/toolkit/*`; tool in `components/tool/shell/`; supervisor in `components/agent/profilesupervisor/` | Per CONTRIBUTING project structure |

## Affected boundaries — new packages

```
libs/toolkit/
├── dagger/                 # NEW: engine client wrapper + container/cache helpers
│   ├── client.go            #   Connect via engine URL; lifecycle; engine version probe
│   ├── container.go         #   Build *Container from base image + mounts + cache volumes
│   ├── cache.go             #   Cache-volume key derivation (image digest + profile); LOCKED install mounts
│   ├── egress_service.go    #   Build the egress proxy as a Dagger Service + bind (optional in-engine transport)
│   ├── client_test.go
│   └── README.md
├── egress/                 # NEW: policy-driven HTTP/HTTPS egress proxy (generic, reusable)
│   ├── policy.go            #   default-deny + allowlist; block RFC1918/link-local/169.254.169.254 unless AllowLocalNetwork
│   ├── proxy.go             #   CONNECT-aware proxy server; denies blocked destinations
│   ├── policy_test.go
│   └── README.md
├── profile/                # NEW: project-type detection -> base image + tool presets
│   ├── resolver.go          #   file-marker heuristics (go.mod, package.json, pyproject.toml, pom.xml/build.gradle, Cargo.toml, composer.json)
│   ├── defaults.go          #   default profile->image map (golang, node/vue, python, java, rust, php)
│   ├── resolver_test.go
│   └── README.md
└── safety/
    └── blocklist.go         # NEW: shared command blocklist (extracted/generalized from kubernetes/pod_exec DefaultBlocklist)

components/tool/shell/      # NEW: the eino shell tool
├── config.go               #   Config (EngineURL, Profiles/DefaultImage, Workdir, NetworkPolicy, Cache, RegistryAuth, Safety)
├── shell.go                #   ShellTool: InvokableTool + StreamableTool; chained *Container per session
├── session.go              #   per-session *Container snapshot manager (WithExec chain + Sync)
├── registry.go             #   NewShellTool; NewShellToolsForProfiles; WriteToolNames; NewAllToolsWithSafety
├── check.go                #   Check(ctx, cfg) -> probe engine version + trivial exec
├── check_test.go
├── shell_test.go
├── prompts/                #   tool description + supervisor guidance (//go:embed, per CONTRIBUTING)
│   └── shell_description.md
└── README.md

components/agent/profilesupervisor/   # NEW: profile supervisor agent factory
├── config.go               #   SupervisorConfig{Model, EngineURL, Workdir, NetworkPolicy, Profiles, SafetyCfg}
├── supervisor.go           #   NewProfileSupervisor(ctx, cfg) -> *adk.ChatModelAgent (builds sub-agent per profile + adk.NewAgentTool)
├── check.go
├── check_test.go
├── supervisor_test.go
├── prompts/
│   └── supervisor_system.md
└── README.md
```

**Touched (non-new):** `go.mod`/`go.sum` (add `dagger.io/dagger`); optionally
update `kubernetes/pod_exec.go` to reuse `safety.DefaultCommandBlocklist`
(non-breaking).

## Component design (signatures only — no implementation)

### `libs/toolkit/dagger` — engine client wrapper

Responsibilities: own the `*dagger.Client` lifecycle; connect via engine URL;
build `*Container` from a base image + workdir mount + shared cache volumes;
expose egress-proxy Service binding. Non-component, reusable (mirrors
`libs/toolkit/osclient`, `argocd/client.go`).

Design outline:
- `type EngineConfig{ EngineURL string; RunnerHostFromEnv bool; RegistryAuth map[string]RegistryAuth; LogOutput io.Writer; Workdir string }`
- `func NewClient(ctx context.Context, cfg *EngineConfig) (*Client, error)` — sets `DAGGER_HOST`/`_EXPERIMENTAL_DAGGER_HOSTS` from `EngineURL` when provided (canonical), applies `dagger.WithRunnerHost` (CLI path) and/or `dagger.WithConn` (direct remote) as appropriate; calls `dagger.Connect`; calls `validate.Struct`. EngineURL empty ⇒ default local engine.
- `(*Client).Version(ctx) (string, error)` — `client.Version()`; used by checkup.
- `(*Client).Container(ctx, baseImage string, opts ...ContainerOpt) (*dagger.Container, error)` — `From(baseImage)` + workdir mount (`client.Host().Directory(workdir)`) + cache mounts + egress env + workdir/user.
- `(*Client).CacheVolume(ctx, profile string) (*dagger.CacheVolume, error)` — key = `sha256(image-digest + profile)`; `sharing=LOCKED` for install paths.
- `(*Client).EgressProxyService(ctx, pol *egress.Policy) (*dagger.Service, error)` — build/bind the in-engine egress proxy (optional transport; see §Security).
- compile-time: `var _ io.Closer = (*Client)(nil)`.

### `libs/toolkit/egress` — policy-driven egress proxy (generic)

Responsibilities: enforce "local network forbidden by default" with an allowlist
and `AllowLocalNetwork` toggle. Generic HTTP/HTTPS CONNECT proxy.

Design outline:
- `type Policy{ AllowHosts []string; AllowCIDRs []string; AllowLocalNetwork bool; DefaultDeny bool }`
- `func (p *Policy) Allows(host string, ip net.IP) error` — deny RFC1918 + link-local + `169.254.169.254` unless `AllowLocalNetwork`; allow only configured hosts/CIDRs/mirrors; default-deny.
- `type Proxy struct{ ... }`; `func NewProxy(pol *Policy) *Proxy`; `func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error` — CONNECT-tunnel with destination check.
- Docker/Dagger integration: container env `HTTP_PROXY=http://<proxy>`, `HTTPS_PROXY=...`, `NO_PROXY=` (empty). Soft-control caveat documented.

### `libs/toolkit/profile` — discovery + base-image selection

Responsibilities: detect project type from a workdir; map to a base image + tool
presets. Reusable, non-component.

Design outline:
- `type Profile{ Name string; BaseImage string; SystemPrompt string; InstallCmd []string; ToolPresets []string; Env map[string]string }`
- `type Resolver struct{ ImageMap map[string]string }` (key = marker set signature)
- `func NewResolver(opts ...ResolverOpt) *Resolver`
- `func (r *Resolver) Resolve(ctx context.Context, workdir string) ([]Profile, error)` — scan for markers → profiles (polyglot ⇒ multiple). Returns `[]Profile` so a repo with golang+vue yields two profiles.
- Default map: `go.mod→golang:1.22`, `package.json(+vue/react)→node:20`, `pyproject.toml|requirements.txt|setup.py→python:3.12`, `pom.xml|build.gradle(.kts)→eclipse-temurin:21`, `Cargo.toml→rust:1.82`, `composer.json→php:8.3`.

### `libs/toolkit/safety/blocklist.go` — shared command blocklist

Extract/generalize the robust blocklist from `components/tool/kubernetes/pod_exec.go`
(`DefaultBlocklist`, `compileBlocklist`, `checkBlocklist`) into the shared
`safety` package so the shell tool reuses it instead of duplicating. Keep the
same robust patterns (word boundaries, absolute/relative paths, shell/script
bypass vectors). AGENTS.md: "If ArgoCD and Kubernetes both need CompileFilter, it
belongs in `libs/toolkit/filter/`" — same principle.

Design outline: `var DefaultCommandBlocklist = []string{...}`;
`func CompileBlocklist(patterns []string) ([]*regexp.Regexp, error)`;
`func CheckBlocklist(compiled []*regexp.Regexp, command []string) error`.

### `components/tool/shell` — the eino shell tool

Responsibilities: expose a shell `tool.InvokableTool` + `tool.StreamableTool`
backed by Dagger; manage a per-session chained `*Container`; enforce blocklist +
safety gate; stream stdout.

Design outline:
- `type Config{ EngineURL string; BaseImage string; Profiles profile.Profiles; Workdir string; NetworkPolicy *egress.Policy; CacheKey string; RegistryAuth map[string]RegistryAuth; Blocklist []string; DefaultTimeout time.Duration; Safety *safety.Config }` (tags `validate`+`jsonschema`).
- `type ShellParams{ Command []string; Profile string; DryRun bool; Confirmed bool; FilterPattern string; Timeout string; AllowLocalNetwork *bool }` — `Profile` optional (overrides default); `DryRun`/`Confirmed` for the gate; `AllowLocalNetwork` per-call override of the egress policy.
- `type ShellTool struct{ ... }`; `var _ tool.InvokableTool = (*ShellTool)(nil)`; `var _ tool.StreamableTool = (*ShellTool)(nil)`.
- `func NewShellTool(ctx context.Context, cfg *Config) (*ShellTool, error)` — `validate.Struct` after defaults; build `*dagger.Client`; pre-compile blocklist; wire invokable + streamable via `utils.InferTool`/`utils.InferStreamTool` (tool name `shell_exec`, description from embedded `prompts/shell_description.md`).
- `func (t *ShellTool) Invoke(ctx, *ShellParams) (string, error)` — validate → blocklist → `confirm.RequireConfirmation(DryRun,Confirmed)` (write tool) → resolve profile/image → get/build session `*Container` (chain) → `WithExec` → `Sync` → return `Stdout`+`Stderr`+`ExitCode`.
- `func (t *ShellTool) InvokeAsStream(ctx, *ShellParams) (*schema.StreamReader[string], error)` — same, stream stdout lines via `schema.Pipe`.
- Session manager (`session.go`): keyed by (engine + profile + workdir); holds the running `*dagger.Container` snapshot; chains `WithExec`; periodic `Sync`; closes on session end. Cache volumes shared across sessions/agents.

### `components/agent/profilesupervisor` — profile supervisor agent factory

Responsibilities: build a supervisor `*adk.ChatModelAgent` whose tools are
sub-agent tools (`adk.NewAgentTool`) — one per profile — each backed by a
shell-tool agent with the right base image. The supervisor's system prompt
instructs it to pick the sub-agent matching the user's task; optionally the
`ProfileResolver` pre-narrows visible profiles by detecting the project type.

Design outline:
- `type SupervisorConfig{ Model model.BaseChatModel; EngineURL string; Workdir string; NetworkPolicy *egress.Policy; Profiles []profile.Profile; Resolver *profile.Resolver; SafetyCfg *safety.Config; SystemPrompt string }` (tags `validate`+`jsonschema`).
- `func NewProfileSupervisor(ctx context.Context, cfg *SupervisorConfig) (*adk.ChatModelAgent, error)` — for each profile: build a `ShellTool` (engine + profile image + cache + egress), wrap a sub-agent `adk.NewChatModelAgent` (model + the shell tool + safety middleware), expose `adk.NewAgentTool(subAgent)`; assemble supervisor `adk.ChatModelAgent` with `ToolsConfig{Tools: agentTools}` + safety middleware; return supervisor. Validates with `validate.Struct`.
- compile-time: `var _ adk.Agent = (*adk.ChatModelAgent)(nil)` (returned type satisfies `adk.Agent`).

Pattern precedent: `components/agent/memory/agent.go` (`Config{InnerAgent adk.Agent}` + `NewMemoryAgent`); supervisor/sub-agent wiring precedent: `libs/costtrack/accept_test.go:179-204` (`adk.NewAgentTool` + supervisor `ChatModelAgent`).

## Security considerations

- **Command blocklist**: reuse `safety.DefaultCommandBlocklist`; user-extensible; robust against `/bin/rm`, `./rm`, shell `-c`, `eval`, etc. (port the patterns from `pod_exec.go`).
- **Safety gate**: shell tool is a WRITE tool → listed in `WriteToolNames`; every call passes the dry-run/confirmed gate (`safety.ShouldGate`), CEL policy, and audit (`safety.LogSink`/`ChannelSink`) via `components/middleware/safety`.
- **Network egress**: default-deny via `egress.Policy`; block RFC1918 (`10/8`,`172.16/12`,`192.168/16`), link-local (`169.254/16` incl. cloud metadata `169.254.169.254`), loopback egress, unless `AllowLocalNetwork`. Allowlist = package mirrors only by default. Forced via `HTTP(S)_PROXY` env. **Soft-control caveat documented**: tools ignoring proxy env can bypass; for a hard guarantee, deploy the engine with nftables/iptables egress rules (documented, out of band).
- **Privileged caps**: `InsecureRootCapabilities` and `ExperimentalPrivilegedNesting` OFF by default; only via explicit, audited config.
- **Secrets**: registry auth via `Container.WithRegistryAuth` + `dagger.SetSecret` (never logged); redact in audit `Arguments`/`Result`.
- **Timeouts**: per-exec timeout via ctx (`defaultExecTimeout` like `pod_exec`); `WithExec` `Expect` to allow non-zero exit codes for `grep`/`test`.
- **Path/SSRF**: workdir is the configured project dir only (never arbitrary host paths); base image refs validated as OCI refs.

## Data flow — end-to-end scenarios

1. **LLM runs a read command**: `shell_exec{command:["go","test","./..."], confirmed:true}` → gate passes → blocklist passes → session `*Container` (`From(golang:1.22)` + workdir mount + go-cache cache volume + egress env) → `WithExec` → `Sync` → return stdout. Cached: re-run reuses layers.
2. **Install shared across agents**: agent A: `sudo apt-get install -y jq` (root in container) → install writes to a `LOCKED` cache volume keyed by (image+profile). Agent B (same engine+key): `jq` already present — cache hit, no re-install. Project workdir is a shared host path mounted rw → both agents see project files.
3. **Supervisor picks the right sub-agent**: user "review this Python PR" → `ProfileSupervisor` → resolver detects `pyproject.toml` → narrows visible sub-agent tools to `python` → supervisor invokes the python sub-agent tool → its `ShellTool` runs in `python:3.12`. Polyglot repo (golang+vue) ⇒ two sub-agent tools visible; supervisor may invoke both.
4. **Network denied by default**: `curl http://10.0.0.5` → egress proxy denies (RFC1918) unless `AllowLocalNetwork=true`. `pip install` from configured mirror → allowed.

## Failure modes & mitigations

| Failure | Mitigation |
|---|---|
| Engine unreachable / wrong version | `Check()` probes `client.Version()` + trivial exec; constructor fails fast with context (`emperror.dev/errors`) |
| `WithRunnerHost` ineffective for remote (CLI-only, verified) | Use `DAGGER_HOST` env canonical path; `WithConn` for direct remote; document CLI precondition for `docker-container://` |
| Egress proxy bypassed (soft control) | Document caveat; recommend engine-side firewall for hard guarantee; proxy still blocks default package-manager paths |
| Concurrent install corruption | `CacheSharingModeLocked` for install cache mounts (serializes writes) |
| Session `*Container` leak | session manager closes on ctx cancel; `Client.Close()` on tool shutdown |
| LLM ignores dry-run/confirmed | Safety middleware hard-gates (write w/o confirmed ⇒ `ErrGateRequired`) |
| Dangerous command | `safety.DefaultCommandBlocklist`; user-extensible |
| SDK/engine version skew | Pin `dagger.io/dagger` to a version matching the target engine; record in README |

## Dependencies to add

- `dagger.io/dagger` (Apache-2.0) — Go SDK; pin a version compatible with the target engine (godoc current: v0.21.7). Brings `engineconn` (for `WithConn` direct connect).
- No new deps for egress/profile/safety (stdlib `net/http`, `regexp`, existing `cel-go`, `validator`).

## Implementation steps (ordered)

**Phase 0 — dependency + scaffolding**
1. `go get dagger.io/dagger@<pinned>`; confirm `go build ./...` clean in Go 1.26.3 module.
2. Create package dirs listed in §Affected boundaries (with package comments + README stubs).

**Phase 1 — shared libs (`libs/toolkit/*`)**
3. `libs/toolkit/safety/blocklist.go` — port `DefaultCommandBlocklist` + `CompileBlocklist` + `CheckBlocklist`; `blocklist_test.go` (table-driven incl. bypass vectors).
4. `libs/toolkit/egress/` — `Policy` + `Proxy`; `policy_test.go` (default-deny, RFC1912/link-local denied, `AllowLocalNetwork` lifts, allowlist honored).
5. `libs/toolkit/profile/` — `Resolver` + defaults; `resolver_test.go` (marker→profile, polyglot, unknown⇒default).
6. `libs/toolkit/dagger/` — `EngineConfig`+`NewClient` (env/`WithRunnerHost`/`WithConn`), `Container`, `CacheVolume`, `EgressProxyService`; `client_test.go` (mock/short-circuit against a real engine only in `-tags=integration`).

**Phase 2 — the shell tool (`components/tool/shell/`)**
7. `config.go` (`Config`/`ShellParams` with `validate`+`jsonschema` tags).
8. `prompts/shell_description.md` + `//go:embed`.
9. `session.go` — per-session chained `*Container` snapshot manager.
10. `shell.go` — `ShellTool` (`Invoke` + `InvokeAsStream`); blocklist + `confirm.RequireConfirmation` + Dagger exec; `var _ tool.InvokableTool/StreamableTool`.
11. `registry.go` — `NewShellTool`, `NewShellToolsForProfiles`, `WriteToolNames`, `NewAllToolsWithSafety`.
12. `check.go` + `check_test.go` — `Check(ctx, cfg) checkup.Results` (engine version + trivial exec).
13. `shell_test.go` — table-driven (param errors, blocklist, dry-run, confirmed, profile override); mocks for the Dagger client.
14. `README.md` — what it does, constructor snippet, which abstraction (`tool.InvokableTool`/`StreamableTool`), engine-URL + egress + sharing notes.

**Phase 3 — profile supervisor (`components/agent/profilesupervisor/`)**
15. `config.go` (`SupervisorConfig`).
16. `prompts/supervisor_system.md` + `//go:embed`.
17. `supervisor.go` — `NewProfileSupervisor` (per-profile sub-agent + `adk.NewAgentTool` + supervisor `ChatModelAgent` + safety middleware).
18. `check.go` + `check_test.go` + `supervisor_test.go` (mock model + mock shell; assert sub-agent tools wired per profile).
19. `README.md`.

**Phase 4 — integration + validation**
20. Wire `NewAllToolsWithSafety` into an example supervisor; add an integration test (build tag) exercising a real engine: `From(alpine)` + `WithExec(["echo","ok"])`.
21. `go build ./... && go vet ./... && go test ./...`.
22. Update `CONTRIBUTING.md`/`AGENTS.md` only if a new convention is introduced (otherwise none).

## Validation plan

- `go build ./...`, `go vet ./...`, `go test ./...` (the required gate per CONTRIBUTING).
- Unit: blocklist bypass vectors, egress default-deny/RFC1912/allowlist, profile marker detection (incl. polyglot), shell tool param/blocklist/dry-run/confirmed, supervisor sub-agent wiring (mock model + mock shell).
- Checkup: `Check()` returns `checkup.Results` with `ok` on reachable engine, `error` with context otherwise; "no resources" ⇒ `limited` per AGENTS.md.
- Integration (`-tags=integration`, requires an engine): pull `alpine`/`golang`/`python`, run a command, assert stdout; assert a `LOCKED` cache volume persists a second install (no re-download); assert egress proxy denies `10.0.0.1` and allows a configured mirror.
- Naming audit: `Dagger`/`URL`/`ID`/`JSON`/`HTTP` casing; no license banners; `emperror.dev/errors` wrapping; `validate.Struct` in every `New...`; `ctx context.Context` first param threaded through.

## Open questions / out of scope

- **Exact engine-connect mechanics** for remote schemes (`WithConn` vs `DAGGER_HOST`) — finalize against the pinned SDK version during Phase 1; documented precondition either way.
- **Egress proxy transport**: in-engine Dagger `Service` bound via `WithServiceBinding` vs in-process host proxy bound via env — choose in Phase 1 based on whether the engine can reach the host; both are soft controls either way.
- **Hard network guarantee** (nftables/iptables on the engine) — documented as operator hardening, out of library scope.
- Optionally refactor `kubernetes/pod_exec.go` to use `safety.DefaultCommandBlocklist` (non-breaking cleanup; can defer).
