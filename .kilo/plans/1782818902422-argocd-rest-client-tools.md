# ArgoCD REST Client & Tools

## Goal

Create a thin ArgoCD REST client and a set of eino tools under `components/tool/argocd/` that interact with ArgoCD servers via their REST API (grpc-gateway). **Zero ArgoCD Go dependencies** — only `net/http`, `encoding/json`, and existing project dependencies.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| API approach | REST (JSON over HTTPS) | Avoids pulling in ArgoCD's 200+ transitive deps and k8s v0.36 collision |
| Authentication | Pre-existing JWT token only | Simplest, avoids credential handling. Tokens created externally via `argocd account generate-token` |
| Multi-instance | Yes (map of named instances) | Matches kubernetes tool pattern. Each tool param requires an `instance` field |
| TLS | Configurable `insecure` boolean | Default secure; `insecure: true` skips TLS verification for self-signed certs |
| Initial scope | Application CRUD + sync + Project list/describe | Covers the most common operational workflows |

## Package Layout

```
components/tool/argocd/
├── README.md
├── config.go              # Config struct, Configs map type
├── client.go              # Thin REST client (net/http)
├── types.go               # ArgoCD API response/request structs
├── helper.go              # Shared helpers (filter, marshal, etc.)
├── instance_list.go       # List known ArgoCD instances tool
├── application_list.go    # List applications tool
├── application_describe.go # Get application details tool
├── application_sync.go    # Sync application tool
├── application_create.go  # Create application tool
├── application_delete.go  # Delete application tool
├── project_list.go        # List projects tool
├── project_describe.go    # Get project details tool
├── suite_test.go          # Test suite setup with httptest server
└── application_test.go    # Tests for application tools
```

## Task List

### Task 1: Config & Types (`config.go`, `types.go`)

**`config.go`** — Follow the kubernetes `Configs` pattern:

```go
type Config struct {
    ServerURL string `json:"serverURL" validate:"required"`
    Token     string `json:"token" validate:"required"`
    Insecure  bool   `json:"insecure,omitempty"`
}

type Configs map[string]*Config
```

- `GetConfig(instanceName string) *Config`
- `GetInstanceNames() []string`

**`types.go`** — Minimal structs matching ArgoCD REST API JSON responses. Use `json.RawMessage` for large nested objects (spec, status) to avoid defining the full ArgoCD type tree:

```go
// Application list response
type ApplicationListResponse struct {
    Items []ApplicationSummary `json:"items"`
}

type ApplicationSummary struct {
    Metadata ObjectMeta       `json:"metadata"`
    Status   *ApplicationStatus `json:"status,omitempty"`
}

type ObjectMeta struct {
    Name              string            `json:"name"`
    Namespace         string            `json:"namespace,omitempty"`
    Labels            map[string]string `json:"labels,omitempty"`
    Annotations       map[string]string `json:"annotations,omitempty"`
    CreationTimestamp string            `json:"creationTimestamp,omitempty"`
}

type ApplicationStatus struct {
    Health     *HealthStatus `json:"health,omitempty"`
    Sync       *SyncStatus   `json:"sync,omitempty"`
    Summary    *AppSummary   `json:"summary,omitempty"`
    Resources  []ResourceStatus `json:"resources,omitempty"`
}

type HealthStatus struct {
    Status  string `json:"status"`
    Message string `json:"message,omitempty"`
}

type SyncStatus struct {
    Status   string `json:"status"`
    Revision string `json:"revision,omitempty"`
}

// ... etc for project types, sync request, create request
```

### Task 2: REST Client (`client.go`)

Thin HTTP client wrapping `net/http.Client`:

```go
type Client struct {
    serverURL  string
    token      string
    httpClient *http.Client
}

func NewClient(cfg *Config) (*Client, error)
func NewClients(configs Configs) (map[string]*Client, error)
```

Methods (all take `context.Context`):
- `ListApplications(ctx, selector, project, appNamespace string) (*ApplicationListResponse, error)` — `GET /api/v1/applications`
- `GetApplication(ctx, name, appNamespace, project string) (*ApplicationResponse, error)` — `GET /api/v1/applications/{name}`
- `SyncApplication(ctx, name string, req *SyncRequest) (*ApplicationResponse, error)` — `POST /api/v1/applications/{name}/sync`
- `CreateApplication(ctx, req *ApplicationCreateRequest) (*ApplicationResponse, error)` — `POST /api/v1/applications`
- `DeleteApplication(ctx, name string, cascade bool) error` — `DELETE /api/v1/applications/{name}`
- `ListProjects(ctx, name string) (*ProjectListResponse, error)` — `GET /api/v1/projects`
- `GetProject(ctx, name string) (*ProjectResponse, error)` — `GET /api/v1/projects/{name}`

Internal helpers:
- `doRequest(ctx, method, path string, body, result interface{}) error` — sets `Authorization: Bearer <token>`, `Content-Type: application/json`, handles JSON encode/decode, checks HTTP status codes
- Error handling: non-2xx responses decoded as `{"error": "...", "message": "..."}` and wrapped with `emperror.dev/errors`

### Task 3: Instance List Tool (`instance_list.go`)

Lists all configured ArgoCD instances. Same pattern as `cluster_list.go`:
- Tool name: `argocd_instance_list`
- Params: none
- Output: JSON array of instance names

### Task 4: Application List Tool (`application_list.go`)

- Tool name: `argocd_application_list`
- Params:
  ```go
  type ApplicationListParams struct {
      Instance     string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
      Project      string `json:"project,omitempty" jsonschema:"(optional) Filter by project name."`
      Selector     string `json:"selector,omitempty" jsonschema:"(optional) Label selector (e.g. 'app=nginx,env=prod')."`
      AppNamespace string `json:"appNamespace,omitempty" jsonschema:"(optional) Application namespace filter."`
      Filter       string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex applied on each application JSON output."`
  }
  ```
- Output: JSON array of `ApplicationListOutput` structs (name, namespace, project, health, syncStatus, revision)
- Follows the `listOutputGuidance` pattern from kubernetes tools

### Task 5: Application Describe Tool (`application_describe.go`)

- Tool name: `argocd_application_describe`
- Params:
  ```go
  type ApplicationDescribeParams struct {
      Instance         string   `json:"instance" validate:"required"`
      Name             string   `json:"name" validate:"required"`
      AppNamespace     string   `json:"appNamespace,omitempty"`
      Project          string   `json:"project,omitempty"`
      ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec status" jsonschema:"(optional) Fields to exclude: 'metadata', 'spec', 'status'."`
  }
  ```
- Output: Full application JSON with optional field exclusion (same pattern as kubernetes describe)
- Follows the `describeOutputGuidance` pattern

### Task 6: Application Sync Tool (`application_sync.go`)

- Tool name: `argocd_application_sync`
- Params:
  ```go
  type ApplicationSyncParams struct {
      Instance     string `json:"instance" validate:"required"`
      Name         string `json:"name" validate:"required"`
      AppNamespace string `json:"appNamespace,omitempty"`
      Project      string `json:"project,omitempty"`
      Revision     string `json:"revision,omitempty" jsonschema:"(optional) Target revision to sync to."`
      DryRun       bool   `json:"dryRun,omitempty" jsonschema:"(optional) Simulate sync without applying changes."`
      Prune        bool   `json:"prune,omitempty" jsonschema:"(optional) Delete resources no longer in git."`
  }
  ```
- Output: Application status after sync initiation

### Task 7: Application Create Tool (`application_create.go`)

- Tool name: `argocd_application_create`
- Params:
  ```go
  type ApplicationCreateParams struct {
      Instance     string `json:"instance" validate:"required"`
      Name         string `json:"name" validate:"required"`
      Project      string `json:"project,omitempty" jsonschema:"(optional) ArgoCD project name. Defaults to 'default'."`
      AppNamespace string `json:"appNamespace,omitempty"`
      RepoURL      string `json:"repoURL" validate:"required" jsonschema:"(required) Git repository URL."`
      TargetRevision string `json:"targetRevision,omitempty" jsonschema:"(optional) Git branch/tag/commit. Defaults to 'HEAD'."`
      Path         string `json:"path,omitempty" jsonschema:"(optional) Path within the repo."`
      DestServer   string `json:"destServer" validate:"required" jsonschema:"(required) Destination cluster API server URL (e.g. 'https://kubernetes.default.svc')."`
      DestNamespace string `json:"destNamespace,omitempty" jsonschema:"(optional) Destination namespace."`
      Upsert       bool   `json:"upsert,omitempty" jsonschema:"(optional) Update if application already exists."`
  }
  ```
- Output: Created application JSON

### Task 8: Application Delete Tool (`application_delete.go`)

- Tool name: `argocd_application_delete`
- Params:
  ```go
  type ApplicationDeleteParams struct {
      Instance     string `json:"instance" validate:"required"`
      Name         string `json:"name" validate:"required"`
      AppNamespace string `json:"appNamespace,omitempty"`
      Project      string `json:"project,omitempty"`
      Cascade      bool   `json:"cascade,omitempty" jsonschema:"(optional) Also delete application resources. Defaults to true."`
  }
  ```
- Output: Confirmation message

### Task 9: Project List Tool (`project_list.go`)

- Tool name: `argocd_project_list`
- Params:
  ```go
  type ProjectListParams struct {
      Instance string `json:"instance" validate:"required"`
      Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex on each project JSON."`
  }
  ```
- Output: JSON array of `ProjectListOutput` (name, description, sourceRepos count, destinations count)

### Task 10: Project Describe Tool (`project_describe.go`)

- Tool name: `argocd_project_describe`
- Params:
  ```go
  type ProjectDescribeParams struct {
      Instance            string   `json:"instance" validate:"required"`
      Name                string   `json:"name" validate:"required"`
      ExcludeFieldsOutput []string `json:"excludeFieldsOutput,omitempty" validate:"omitempty,dive,oneof=metadata spec" jsonschema:"(optional) Fields to exclude: 'metadata', 'spec'."`
  }
  ```
- Output: Full project JSON with optional field exclusion

### Task 11: Tests (`suite_test.go`, `application_test.go`)

Use `net/http/httptest` to mock the ArgoCD API server:

**`suite_test.go`**:
- Set up an `httptest.Server` that returns canned JSON responses for each endpoint
- Create a `Configs` pointing at the test server
- Use `testify/suite` pattern (matching kubernetes tests)

**`application_test.go`**:
- Test each tool's `Invoke` method with valid params
- Test error cases: instance not found, application not found, API error responses
- Test filter/exclude functionality

### Task 12: README (`README.md`)

Document:
- Package purpose and design rationale (thin REST client to avoid dependency conflicts)
- Configuration example
- Available tools with parameter descriptions
- Usage example

## Conventions to Follow

- `emperror.dev/errors` for error wrapping
- `github.com/go-playground/validator/v10` for param validation
- `github.com/goccy/go-json` for JSON marshaling (matches existing tools)
- `github.com/cloudwego/eino/components/tool/utils.InferTool` for tool registration
- Tool descriptions follow the `** General Purpose **` / `** Output **` / `** How to limit output **` format
- No comments in code unless explicitly requested (per AGENTS.md)
- `fmt.Sprintf` over string concatenation

## Validation

```bash
go build ./...
go vet ./...
go test ./components/tool/argocd/...
```

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| ArgoCD API response shapes change between versions | Use `json.RawMessage` for large nested objects; only define fields we actually surface in tool output |
| Token expiry mid-session | Return clear error messages from HTTP 401 responses; document that tokens should have appropriate TTL |
| Large application lists blowing up context | Support `selector` and `filter` params on list tools; document output guidance |
| Self-signed TLS certs | Configurable `insecure` flag per instance |
