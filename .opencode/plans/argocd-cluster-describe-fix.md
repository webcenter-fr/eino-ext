# Fix: `argocd_cluster_describe` never works (REST path param vs gRPC query semantics)

## Summary

**Issue:** webcenter-fr/eino-ext#6 — "fix - argocd_tools argocd_cluster_describe not working".

The `argocd_cluster_describe` tool fails for **every** user (admin included) with
`API error 7: permission denied`. The reporter's `role:readonly` RBAC is a red herring —
the tool builds a malformed REST request that ArgoCD turns into a permission-denied
response regardless of who calls it.

Fix: build the same request the ArgoCD CLI uses for `argocd cluster get <name>`, i.e. put
the cluster name in the `{id.value}` path segment and set `?id.type=name`, instead of
issuing `GET /api/v1/clusters/?name=<name>` with an empty path segment.

## Root cause (verified against pinned dependency `goargocdclient v0.0.0-20260709162736-32f52f5c5509`)

1. `components/tool/argocd/cluster_describe.go:52` calls:

   ```go
   cluster, err := c.Cluster().Get("", &api.ClusterQueryOptions{Name: params.Name})
   ```

2. `goargocdclient` `api.ClusterStandard.Get(server, opts)` (verified in module source):

   ```go
   func (c *ClusterStandard) Get(server string, opts *ClusterQueryOptions) (*ClusterModel, error) {
       req := c.client.R()
       if opts != nil {
           if opts.Name != "" { req.SetQueryParam("name", opts.Name) }
           if opts.IdType != "" { req.SetQueryParam("id.type", opts.IdType) }
       }
       var result ClusterModel
       resp, err := req.SetResult(&result).
           Get(fmt.Sprintf("/api/v1/clusters/%s", encodeClusterServer(server)))
       // encodeClusterServer(s) == url.PathEscape(s)
   }
   ```

   With `server == ""`, this produces `GET /api/v1/clusters/?name=ran37prd2`.

3. ArgoCD's REST route for `ClusterService.Get` is `GET /api/v1/clusters/{id.value}`.
   grpc-gateway matches the trailing-slash URL with `id.value == ""`, yielding a non-nil
   `ClusterID{Value: ""}`.

4. ArgoCD `server/cluster/cluster.go` `getCluster()` treats a non-nil `q.Id` as
   authoritative, discards the `name` query param, and (with empty `Value`/`Type`) leaves
   `q.Server`/`q.Name` empty → returns `nil, nil` → `getClusterWith403IfNotExist` returns
   `common.PermissionDeniedAPIError` → gRPC code 7 → `API error 7: permission denied`.

5. The **correct** REST encoding is already present in this repo at
   `components/tool/argocd/check.go:266`:

   ```go
   _, cerr := client.Cluster().Get(names[i], &api.ClusterQueryOptions{IdType: "name"})
   ```

   which produces `GET /api/v1/clusters/<name>?id.type=name`. grpc-gateway sets
   `id.value=<name>` and `id.type=name`, so ArgoCD runs `q.Name = q.Id.Value` and finds
   the cluster by name. `check.go` already uses this pattern and is not part of the bug.

## Decision (scope)

- **Primary fix (chosen):** change the call to
  `c.Cluster().Get(params.Name, &api.ClusterQueryOptions{IdType: "name"})`.

- **Rejected alternative** (`List(&{Name: name})` + take first item) because:
  1. `Cluster.List` in `goargocdclient` only sends `server`/`name` query params and does
     not forward `id.type`; the `name` param is not a guaranteed server-side filter, so
     matching by name is client-side guesswork.
  2. ArgoCD filters `List` results by the caller's RBAC. A cluster the token can `get`
     may be hidden from `List`, and vice-versa — so List+filter is not a faithful
     replacement for `Get` by name.
  3. `List` returns cluster summaries without the full `info` block
     (`applicationsCount`, `cacheInfo`, `apiVersions`), which
     `ClusterDescribeOutput.Info` is meant to surface. The describe tool would silently
     lose data.
  4. Not-found vs unauthorized is equally ambiguous on both endpoints (List just returns
     an empty list), so List buys nothing on that axis.
  5. Inconsistent with `check.go`, which already uses the correct `Get(name, {IdType:"name"})`.

- **Error message:** add a short hint that disambiguates the 403 ambiguity (ArgoCD
  returns 403 for both "cluster does not exist" and "token lacks `clusters.get`"). This
  is exactly the confusion that produced issue #6, so it clearly improves UX. It is
  additive and does not break the existing `assert.Error` tests.

- **Keep unchanged:** the `cluster.ObjectMeta.Name = cluster.Name` shadowing fix, the
  `ExcludeFieldsOutput` behavior (`metadata`/`info`), and all other tools.

## Files to change

1. `components/tool/argocd/cluster_describe.go` — fix the `Get` call + error hint.
2. `components/tool/argocd/suite_test.go` — update the cluster-get mock to the
   name-based id encoding and record the request for regression assertions.
3. `components/tool/argocd/additional_test.go` — add a regression assertion in
   `TestClusterDescribe`.

No changes to `check.go`/`check_test.go` (already correct), no README/tool-description
changes required, no `go.mod`/`go.sum` changes.

---

## Implementation steps

### 1. `components/tool/argocd/cluster_describe.go`

Replace the `Get` call (lines 52–55).

Before:

```go
	cluster, err := c.Cluster().Get("", &api.ClusterQueryOptions{Name: params.Name})
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster")
	}
```

After:

```go
	// ArgoCD's REST route for ClusterService.Get is GET /api/v1/clusters/{id.value}.
	// A name lookup must be expressed as the path segment plus ?id.type=name (the same
	// encoding the ArgoCD CLI uses for `argocd cluster get <name>`). Passing "" here
	// and relying on ?name=<name> produces GET /api/v1/clusters/?name=<name>, which
	// ArgoCD turns into "permission denied" for every caller (issue #6).
	cluster, err := c.Cluster().Get(params.Name, &api.ClusterQueryOptions{IdType: "name"})
	if err != nil {
		return "", errors.Wrapf(err, "failed to get cluster %q (ArgoCD returns 403 both when the cluster does not exist and when the token lacks the 'clusters.get' permission)", params.Name)
	}
```

Notes:

- `emperror.dev/errors` `Wrapf` is already used elsewhere (e.g. `client.go:50`); no new
  import needed.
- No import changes required in this file (`api`, `errors`, `json` already imported).

### 2. `components/tool/argocd/suite_test.go`

#### 2a. Add imports

Before:

```go
import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)
```

After:

```go
import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)
```

#### 2b. Add a request-recording field to the suite struct

Before:

```go
type ToolTestSuite struct {
	suite.Suite
	server  *httptest.Server
	configs Configs
}
```

After:

```go
type ToolTestSuite struct {
	suite.Suite
	server  *httptest.Server
	configs Configs

	// lastClusterGet records the most recent cluster-get request line as
	// "METHOD /path?query" so tests can assert the name-based id encoding
	// (GET /api/v1/clusters/<name>?id.type=name) required by issue #6.
	lastClusterGet string
}
```

#### 2c. Rewrite the cluster-get mock (currently lines 170–192)

Before:

```go
	// Cluster get
	mux.HandleFunc("/api/v1/clusters/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "non-existent" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "not found", "message": "cluster not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"name": "my-cluster",
			"server": "https://cluster1.example.com",
			"project": "production",
			"connectionState": {"status": "Successful"},
			"serverVersion": "1.30",
			"info": {
				"applicationsCount": 42,
				"connectionState": {"status": "Successful"}
			}
		}`))
	})
```

After:

```go
	// Cluster get. ArgoCD's REST route is GET /api/v1/clusters/{id.value}; a name
	// lookup is expressed as the path segment plus ?id.type=name. The broken form
	// GET /api/v1/clusters/?name=... (issue #6) is rejected with 400.
	mux.HandleFunc("/api/v1/clusters/", func(w http.ResponseWriter, r *http.Request) {
		t.lastClusterGet = r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery

		if r.URL.Query().Get("id.type") != "name" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": "invalid id.type", "message": "expected id.type=name"}`))
			return
		}

		seg := strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/")
		name, err := url.PathUnescape(seg)
		if err != nil {
			name = seg
		}
		if name == "non-existent" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "not found", "message": "cluster not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"name": "my-cluster",
			"server": "https://cluster1.example.com",
			"project": "production",
			"connectionState": {"status": "Successful"},
			"serverVersion": "1.30",
			"info": {
				"applicationsCount": 42,
				"connectionState": {"status": "Successful"}
			}
		}`))
	})
```

Mock routing notes:

- Go's `http.ServeMux` distinguishes `/api/v1/clusters` (exact, the list handler) from
  `/api/v1/clusters/` (trailing slash → subtree prefix), so the get handler also matches
  `/api/v1/clusters/my-cluster`. This routing already exists and is unchanged.
- The `id.type != "name"` guard makes the mock encode the correct contract: the broken
  `GET /api/v1/clusters/?name=my-cluster` returns 400, which fails `TestClusterDescribe`
  (success case expects no error). The `url.PathUnescape` decodes any escaped path
  segment (e.g. `%2F` for names containing `/`), mirroring grpc-gateway's decoding.

### 3. `components/tool/argocd/additional_test.go`

Add a regression assertion in `TestClusterDescribe` right after the first successful
describe (currently line 89).

Before:

```go
	describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-cluster"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.Contains(t.T(), describeResult, `"name":"my-cluster"`)

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-cluster", "excludeFieldsOutput": ["metadata"]}`)
```

After:

```go
	describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-cluster"}`)
	assert.NoError(t.T(), err)
	assert.NotEmpty(t.T(), describeResult)
	assert.Contains(t.T(), describeResult, `"name":"my-cluster"`)

	// Regression guard for issue #6: the describe request must use the name-based id
	// form GET /api/v1/clusters/<name>?id.type=name, never the broken
	// GET /api/v1/clusters/?name=<name> (empty path segment).
	assert.Equal(t.T(), "GET /api/v1/clusters/my-cluster?id.type=name", t.lastClusterGet)

	describeResult, err = describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-cluster", "excludeFieldsOutput": ["metadata"]}`)
```

The remaining `TestClusterDescribe` cases (`excludeFieldsOutput`, `non-existent`,
`invalid-instance`) are unchanged and must keep passing:

- `non-existent` → `GET /api/v1/clusters/non-existent?id.type=name` → mock returns 404 →
  `assert.Error` still passes.
- `invalid-instance` → `t.client()` returns `instanceNotFoundError` before any HTTP call.

---

## Related-code audit (report only — do NOT change in this PR)

- **`repository_describe.go:63`** calls `c.Repository().Get(params.Name, ...)`.
  `RepositoryStandard.Get(repo, ...)` uses `url.PathEscape(repo)` as the path param, and
  ArgoCD's repository Get path param is the **repo URL**, not the repository's display
  `name`. This is a **naming/documentation** issue, not a functional bug: passing the
  repo URL as `name` works (as `TestRepositoryDescribe` demonstrates), but the parameter
  `Name` + jsonschema `"(required) The repository name."` is misleading. **Follow-up**
  (separate PR): rename the param to `repoURL`/`Repo` and clarify the jsonschema, or add
  a dedicated lookup. Do not touch in this PR.
- **Other describe tools** (no "empty path param" class of bug found):
  - `application_describe.go:55` → `Application().Get(params.Name, ...)`; path param is
    the app name (`fmt.Sprintf("/api/v1/applications/%s", name)`). Correct.
  - `project_describe.go:53` → `Project().Get(params.Name)`; path param is the project
    name (`fmt.Sprintf("/api/v1/projects/%s", name)`). Correct.
  - `cluster_describe.go` is the only caller using the empty-server form
    `Cluster().Get("", ...)` (confirmed by grep; the only other `Cluster().Get` call is
    the already-correct `check.go:266`).
  - Note (cosmetic, out of scope): `project_describe.go`/`application_describe.go` do not
    `url.PathEscape` their name, unlike `cluster.go`/`repository.go`. K8s resource names
    cannot contain `/`, so this is harmless; not fixing here.

## Docs

- `components/tool/argocd/README.md` row for `argocd_cluster_describe`
  ("Get cluster details with optional field exclusion") remains accurate — **no change**.
- Tool description string `clusterDescribeDescription` ("It gets the details of a
  specific ArgoCD cluster.") remains accurate — **no change**.
- Pre-existing, out of scope: the shared `prompts/describe_output_guidance.md` lists the
  generic exclusion set `'metadata', 'spec', 'status'`, but `cluster_describe` only
  accepts `'metadata', 'info'`. The actual per-tool jsonschema (`oneof=metadata info`) is
  already correct; leave the shared guidance unchanged to keep this PR tight.

## Edge cases / error handling / validation

- **Empty cluster name:** `ClusterDescribeParams.Name` is `validate:"required"` and
  `Invoke` calls `validateParams(params)` first; empty name is rejected before any API
  call.
- **Cluster name with URL-special characters** (`/`, `?`, `#`, spaces): `Get` applies
  `url.PathEscape` via `encodeClusterServer`. grpc-gateway path-unescapes the
  `{id.value}` segment, so `a/b` → `a%2Fb` on the wire → `a/b` decoded server-side. The
  mock mirrors this via `url.PathUnescape`.
- **Not found vs permission denied:** ArgoCD returns HTTP 403 for both; `goargocdclient`
  surfaces `API error 7: permission denied` for both. The new error hint tells the user
  about both possibilities. Full disambiguation is impossible from the 403 alone (out of
  scope).
- **Duplicate cluster names:** ArgoCD does not unique-constrain the display `name` (the
  unique key is `server`); `Get` by name resolves via `filterClustersByName`, which
  returns the first match. This is ArgoCD's behavior; not our concern.
- **`excludeFieldsOutput` combinations:** `metadata`/`info` remain handled by
  `applyExcludes`; invalid values are rejected by `validate:"oneof=metadata info"` and,
  defensively, by `applyExcludes` (existing `helper.go`).
- **Instance not configured:** `t.client(params.Instance)` → `instanceNotFoundError`
  (tested via `invalid-instance`).
- **Nil/empty API responses:** unchanged from current behavior (resty returns the zero
  `ClusterModel` on 200 + empty body); not affected by this fix.

## Conventions compliance

- Reuses `emperror.dev/errors` (`Wrapf`) — no new error idiom.
- No new `Config` struct → no new `validate.Struct` constructor call.
- No duplication of `libs/toolkit/` helpers.
- No license banners added.
- Naming/Go conventions unaffected.
- `README.md` + tests already present; component remains complete.

## Branch / PR workflow

From repo root (`main` is the default branch; remote `origin` =
`https://github.com/webcenter-fr/eino-ext.git`):

```bash
git checkout main
git pull --ff-only origin main
git checkout -b fix/argocd-cluster-describe
# ... make the changes ...
git add components/tool/argocd/cluster_describe.go \
        components/tool/argocd/suite_test.go \
        components/tool/argocd/additional_test.go
git commit -m "fix(argocd): cluster_describe uses name-based id lookup (#6)

cluster_describe issued GET /api/v1/clusters/?name=<name>, which ArgoCD
turns into a permission-denied error for every caller. Use the CLI's
encoding: GET /api/v1/clusters/<name>?id.type=name."
git push -u origin fix/argocd-cluster-describe
```

Open the PR (prerequisite: `gh` authenticated, `gh auth status`):

```bash
gh pr create --base main --head fix/argocd-cluster-describe \
  --title "fix(argocd): cluster_describe uses name-based id lookup (#6)" \
  --body "$(cat <<'EOF'
## Problem
\`argocd_cluster_describe\` always failed with \`API error 7: permission denied\`,
even for admins.

## Root cause
The tool issued \`GET /api/v1/clusters/?name=<name>\` (empty \`{id.value}\` path
segment). ArgoCD treats the non-nil empty \`ClusterID\` as authoritative, discards the
\`name\` query param, and returns 403 for every caller. RBAC is not the cause.

## Fix
Use the ArgoCD CLI's encoding: \`GET /api/v1/clusters/<name>?id.type=name\`
(\`Cluster().Get(name, &ClusterQueryOptions{IdType: \"name\"})\`), consistent with
\`check.go\`.

## Tests
Updated the cluster-get mock to the name-based id form and added a regression
assertion in \`TestClusterDescribe\`.

Closes #6
EOF
)"
```

Fallback if `gh` is unavailable: `git push -u origin fix/argocd-cluster-describe` then
open the PR via the web UI using the URL printed by `git push`.

## Verification checklist

Run from repo root:

```bash
# Format (expect no diffs)
gofmt -l components/tool/argocd/

# Build + vet
go build ./...
go vet ./...

# Lint (golangci-lint v2.12 per CI; `make lint` runs `golangci-lint run ./...`)
make lint

# Focused test (no envtest needed; uses httptest)
go test ./components/tool/argocd/ -run 'TestToolSuite/TestClusterDescribe' -v

# Full argocd package
go test ./components/tool/argocd/...

# Component completeness check
bash scripts/check_components.sh
```

**Prove the regression test catches the old code:** temporarily revert only
`cluster_describe.go` to `Get("", &api.ClusterQueryOptions{Name: params.Name})` and run
`go test ./components/tool/argocd/ -run 'TestToolSuite/TestClusterDescribe' -v`. It must
**fail**: the mock returns 400 (`id.type != "name"`) for the broken request, and the new
`assert.Equal(..., "GET /api/v1/clusters/my-cluster?id.type=name", t.lastClusterGet)`
also fails. Re-apply the fix and the test passes. (Do not commit the temporary revert.)

CI (`.github/workflows/ci.yml`) runs `go build ./...`, `go vet ./...`, `make test`,
`golangci-lint` (only-new-issues), and `check-components` (continue-on-error). The full
`make test` requires envtest binaries (`KUBEBUILDER_ASSETS`); the argocd package itself
is httptest-based and does not require envtest.

## Issue comment draft (optional, for maintainer context on #6)

> Root cause is a REST client bug, not your RBAC. `argocd_cluster_describe` was issuing
> `GET /api/v1/clusters/?name=<name>` (empty `{id.value}` path segment). ArgoCD's
> grpc-gateway turns that into a non-nil empty `ClusterID`, which `getCluster()` treats
> as authoritative, discards the `name` query param, and returns 403 for every caller —
> admin or `role:none` alike. The fix uses the same encoding as `argocd cluster get`:
> `GET /api/v1/clusters/<name>?id.type=name`. Your `accounts.ai` / `role:readonly` setup
> is fine.

## Out of scope

- `repository_describe` parameter naming (`name` vs repo URL) — separate follow-up.
- `describe_output_guidance.md` generic exclusion-set wording.
- `url.PathEscape` normalization in `project_describe`/`application_describe` (harmless).
- Any `goargocdclient` upstream change (the library's `Get(server, ...)` signature is
  intentionally reused here; no dependency bump).
