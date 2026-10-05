# Plan: Fix ArgoCD Application List Filter Not Working

- **Issue**: [webcenter-fr/eino-ext#11](https://github.com/webcenter-fr/eino-ext/issues/11)
- **Branch**: `fix/argocd-application-list-filter`
- **Base commit**: `51fb4d0868e6d40a18ba8f92dddbce89a8254008`
- **Two repos**: goargocdclient (dependency) + eino-ext (PR target)

## Summary

The `argocd_application_list` filter returns empty results because `goargocdclient`'s `ObjectMeta` is embedded with flat JSON tags (`json:"name,omitempty"`) in `ApplicationModel`, `ApplicationSetModel`, and `ProjectModel`, but the real ArgoCD REST API nests metadata under a `"metadata"` key. This causes `Name`/`Namespace` to be empty after unmarshalling, so the regex filter matches nothing.

The fix adds `json:"metadata"` to the embedded `ObjectMeta` in the three affected model types in goargocdclient, then bumps the dependency in eino-ext and removes the now-obsolete raw-HTTP workaround in `check.go`.

## Root Cause (with file:line evidence)

### goargocdclient (the bug)

- `api/models.go:3-13`: `ObjectMeta` has flat tags: `Name string \`json:"name,omitempty"\``
- `api/application.go:42-48`: `ApplicationModel` embeds `ObjectMeta` without a `json:"metadata"` tag → fields promoted to top-level JSON
- `api/applicationset.go:23-28`: Same for `ApplicationSetModel`
- `api/project.go:24-28`: Same for `ProjectModel`
- `api/cluster.go:20-36`: `ClusterModel` also embeds `ObjectMeta` flat, but has explicit `Name string \`json:"name"\`` at line 24 that shadows the embedded field. The cluster API is flat (no `metadata` wrapper in swagger), so this is correct.
- `api/repository.go:30-50`: `RepositoryModel` same pattern as ClusterModel — explicit `Name string \`json:"name,omitempty"\`` at line 41. Flat API, correct.
- `api/applicationset.go:260-271`: `ApplicationSetTemplate` embeds `ApplicationSetTemplateMeta` with `json:"metadata"` — this is intentionally flat template metadata and must NOT change.

The real ArgoCD REST API nests metadata:
```json
{"metadata":{"name":"kafka-hpd1","namespace":"argocd"},"spec":{...},"status":{...}}
```
Confirmed by ArgoCD swagger (`v1alpha1Application` has `metadata` property of `v1ObjectMeta`) and ArgoCD source (`type Application struct { metav1.ObjectMeta \`json:"metadata"\` ... }`).

### eino-ext (the symptom)

- `components/tool/argocd/application_list.go:78-88`: Maps `item.Name` → `ApplicationListOutput.Name`. Since `item.Name` is empty (flat unmarshal missed the nested field), the output JSON contains `"name":""`, so regex `kafka` matches nothing.
- `components/tool/argocd/application_describe.go:64`: `Metadata: &app.ObjectMeta` → metadata empty
- `components/tool/argocd/project_list.go:63-68`: `item.Name` → empty
- `components/tool/argocd/project_describe.go:59`: `Metadata: &project.ObjectMeta` → metadata empty
- `components/tool/argocd/application_create.go:81-84`: Builds `ObjectMeta{Name: params.Name}` with flat tags → request body sends flat `"name"` instead of nested `"metadata":{"name":...}`. The real API may reject or ignore this.
- `components/tool/argocd/suite_test.go:44-60`: Mock returns flat JSON (`"name": "my-app"` at top level), which is why existing tests pass despite the bug.

## Impact Matrix

| Tool | Impacted? | Why |
|------|-----------|-----|
| `argocd_application_list` | **YES** | `item.Name`/`item.Namespace` empty → filter broken (reported bug) |
| `argocd_application_describe` | **YES** | `&app.ObjectMeta` → metadata empty in output |
| `argocd_application_create` | **YES** | Request body sends flat `"name"` instead of nested `"metadata"`; response name also empty |
| `argocd_project_list` | **YES** | `item.Name` empty → filter broken |
| `argocd_project_describe` | **YES** | `&project.ObjectMeta` → metadata empty |
| `argocd_cluster_list` | NO | Explicit `item.Name` shadows embedded field; flat API |
| `argocd_cluster_describe` | NO | Explicit `cluster.Name` shadows embedded; flat API; already copies `cluster.ObjectMeta.Name = cluster.Name` |
| `argocd_repository_list` | NO | Explicit `item.Name` shadows embedded; flat API |
| `argocd_repository_describe` | NO | Explicit `repository.Name` shadows embedded; flat API; already copies |
| `argocd_certificate_list` | NO | `CertificateModel` does not embed `ObjectMeta`; flat API |
| `argocd_application_sync` | NO | Uses name parameter only, no metadata deserialization |
| `argocd_application_delete` | NO | Uses name parameter only; dry-run fetches app but only for display |
| `check.go` | **YES** | Has raw-HTTP workaround for this exact bug; will be removed |

## Chosen Design

### goargocdclient: Add `json:"metadata"` tag to embedded ObjectMeta

Add `json:"metadata"` to the embedded `ObjectMeta` field in three types:

```go
// api/application.go
type ApplicationModel struct {
    TypeMeta
    ObjectMeta `json:"metadata"`   // ← add tag
    Spec      ApplicationSpec   `json:"spec"`
    Status    ApplicationStatus `json:"status,omitempty"`
    Operation *Operation        `json:"operation,omitempty"`
}

// api/applicationset.go
type ApplicationSetModel struct {
    TypeMeta
    ObjectMeta `json:"metadata"`   // ← add tag
    Spec   ApplicationSetSpec   `json:"spec"`
    Status ApplicationSetStatus `json:"status,omitempty"`
}

// api/project.go
type ProjectModel struct {
    ObjectMeta `json:"metadata"`   // ← add tag
    Spec   ProjectSpec   `json:"spec"`
    Status ProjectStatus `json:"status,omitempty"`
}
```

**Why this approach**:
- Minimal change: one tag per type, no new code
- Source-compatible: `app.Name`, `app.ObjectMeta` still work (Go promotes embedded fields)
- Both marshal and unmarshal use the real nested wire format
- Does NOT affect `ClusterModel` or `RepositoryModel` (which need flat tags for their flat APIs)
- Does NOT affect `ApplicationSetTemplateMeta` (different type, already has `json:"metadata"`)

**Alternatives rejected**:
- Custom `MarshalJSON`/`UnmarshalJSON`: More complex, fragile, harder to maintain
- Changing `ObjectMeta` itself: Would break `ClusterModel`/`RepositoryModel` which need flat tags
- Adding a separate `Metadata` field: Would duplicate data, confusing API

**Compatibility verification**:
- `ProjectDetailed` and `ProjectGlobalResponse` embed `ProjectModel` → inherit the fix automatically
- `ApplicationWatchEvent` and `ApplicationSetWatchEvent` embed `*ApplicationModel`/`*ApplicationSetModel` → SSE parsing now correctly unmarshals nested metadata
- `ApplicationSetTemplate` embeds `ApplicationSetTemplateMeta` (different type) → NOT affected
- Create/Update request bodies now correctly send nested `"metadata":{"name":...}` matching the real API
- `app.Name` promoted field access still works for Go callers

### eino-ext: Remove check.go workaround, fix mocks, add regression tests

1. **Remove raw-HTTP workaround from check.go**: Delete local types (`metadataName`, `appListItem`, `appList`, `clusterListItem`, `clusterList`, `projectListItem`, `projectList`), raw HTTP helpers (`newArgoCDHTTPClient`, `doArgoCDListGET`, `fetchFirstApp`, `fetchFirstProject`, `fetchClusterServers`), and the `httpClient` parameter from `probeInstance`. Use goargocdclient types directly for name extraction (they now correctly unmarshal nested metadata).

2. **Fix mock server in suite_test.go**: Change application, project, and application-set mock responses to use nested `"metadata"` format. Keep cluster, repository, and certificate mocks flat (real APIs are flat).

3. **Add regression tests**: Prove filter works with substring matches, names/namespaces are populated, describe returns correct metadata, invalid regex errors, invalid instance errors.

4. **Bump goargocdclient dependency**: After user releases new version, update go.mod.

## goargocdclient File-by-File Changes

### 1. `api/application.go` (line 42-48)

Change:
```go
type ApplicationModel struct {
    TypeMeta
    ObjectMeta
    Spec      ApplicationSpec   `json:"spec"`
    ...
}
```
To:
```go
type ApplicationModel struct {
    TypeMeta
    ObjectMeta `json:"metadata"`
    Spec      ApplicationSpec   `json:"spec"`
    ...
}
```

### 2. `api/applicationset.go` (line 23-28)

Change:
```go
type ApplicationSetModel struct {
    TypeMeta
    ObjectMeta
    Spec   ApplicationSetSpec   `json:"spec"`
    ...
}
```
To:
```go
type ApplicationSetModel struct {
    TypeMeta
    ObjectMeta `json:"metadata"`
    Spec   ApplicationSetSpec   `json:"spec"`
    ...
}
```

### 3. `api/project.go` (line 24-28)

Change:
```go
type ProjectModel struct {
    ObjectMeta
    Spec   ProjectSpec   `json:"spec"`
    ...
}
```
To:
```go
type ProjectModel struct {
    ObjectMeta `json:"metadata"`
    Spec   ProjectSpec   `json:"spec"`
    ...
}
```

### 4. `api/application_test.go` — Add marshal/unmarshal tests

Add new test functions after existing tests:

**TestApplicationModel_UnmarshalNestedMetadata**: Unmarshal raw nested JSON fixture and assert `Name`/`Namespace` are populated.

```go
func TestApplicationModel_UnmarshalNestedMetadata(t *testing.T) {
    raw := `{"metadata":{"name":"kafka-hpd1","namespace":"argocd"},"spec":{"project":"default","destination":{"server":"https://kubernetes.default.svc","namespace":"default"}},"status":{"health":{"status":"Healthy"},"sync":{"status":"Synced"}}}`
    var app ApplicationModel
    if err := json.Unmarshal([]byte(raw), &app); err != nil {
        t.Fatal(err)
    }
    if app.Name != "kafka-hpd1" {
        t.Errorf("expected Name 'kafka-hpd1', got %q", app.Name)
    }
    if app.Namespace != "argocd" {
        t.Errorf("expected Namespace 'argocd', got %q", app.Namespace)
    }
}
```

**TestApplicationModel_MarshalNestedMetadata**: Marshal a model and assert JSON contains `"metadata":{"name":...}` and NOT a top-level `"name"`.

```go
func TestApplicationModel_MarshalNestedMetadata(t *testing.T) {
    app := ApplicationModel{ObjectMeta: ObjectMeta{Name: "myapp", Namespace: "argocd"}}
    data, err := json.Marshal(app)
    if err != nil {
        t.Fatal(err)
    }
    s := string(data)
    if !strings.Contains(s, `"metadata":{"name":"myapp"`) && !strings.Contains(s, `"metadata":{"name":"myapp","namespace":"argocd"}`) {
        t.Errorf("expected nested metadata, got: %s", s)
    }
    // Must NOT have top-level "name" (outside metadata)
    // Use a simple check: the first "name" should be inside "metadata"
    if idx := strings.Index(s, `"name"`); idx > 0 {
        before := s[:idx]
        if !strings.Contains(before, `"metadata"`) {
            t.Errorf("found top-level 'name' outside metadata: %s", s)
        }
    }
}
```

**TestApplicationModel_RoundTrip**: Marshal then unmarshal, verify Name preserved.

```go
func TestApplicationModel_RoundTrip(t *testing.T) {
    original := ApplicationModel{
        ObjectMeta: ObjectMeta{Name: "myapp", Namespace: "argocd", Labels: map[string]string{"env": "prod"}},
        Spec:       ApplicationSpec{Project: "default"},
    }
    data, err := json.Marshal(original)
    if err != nil {
        t.Fatal(err)
    }
    var restored ApplicationModel
    if err := json.Unmarshal(data, &restored); err != nil {
        t.Fatal(err)
    }
    if restored.Name != "myapp" {
        t.Errorf("round-trip Name: expected 'myapp', got %q", restored.Name)
    }
    if restored.Namespace != "argocd" {
        t.Errorf("round-trip Namespace: expected 'argocd', got %q", restored.Namespace)
    }
}
```

### 5. `api/project_test.go` — Add marshal/unmarshal tests

Same pattern as application tests but for `ProjectModel`:

- `TestProjectModel_UnmarshalNestedMetadata`
- `TestProjectModel_MarshalNestedMetadata`
- `TestProjectModel_RoundTrip`

### 6. `api/applicationset_test.go` — Add marshal/unmarshal tests

Same pattern for `ApplicationSetModel`:

- `TestApplicationSetModel_UnmarshalNestedMetadata`
- `TestApplicationSetModel_MarshalNestedMetadata`
- `TestApplicationSetModel_RoundTrip`

### 7. Verify existing tests still pass

The existing tests build responses by marshalling the same model types (`jsonResponse(w, 200, ApplicationModel{ObjectMeta: ObjectMeta{Name: "myapp"}})`), so they are self-consistent. After the tag change, the marshalled JSON will now contain `"metadata":{"name":"myapp"}` instead of `"name":"myapp"` at top level. The tests that check `app.Name` use the Go struct field (promoted), not JSON parsing, so they will still pass. The tests that decode request bodies (`json.NewDecoder(r.Body).Decode(&app)`) will now correctly decode nested metadata.

**One test needs updating**: `TestApplicationCreate_Success` (line 48-64) decodes the request body and echoes it back. The decoded `app.Name` will now be populated from nested metadata, so the assertion `app.Name != "newapp"` will still pass. No change needed.

## eino-ext File-by-File Changes

### 1. `components/tool/argocd/suite_test.go` — Fix mock JSON to nested format

**Applications list** (lines 44-60): Change flat JSON to nested metadata:
```go
// OLD (flat):
{"name": "my-app", "namespace": "argocd", "spec": {...}, "status": {...}}

// NEW (nested):
{"metadata": {"name": "my-app", "namespace": "argocd"}, "spec": {...}, "status": {...}}
```

**Applications get** (lines 79-84): Same change — wrap name/namespace in `"metadata"`.

**Application create response** (lines 64-67): Change to nested:
```go
{"metadata": {"name": "my-new-app"}, "spec": {"project": "default"}}
```

**Application sync response** (lines 97-100): Change to nested:
```go
{"metadata": {"name": "my-app"}, "status": {"sync": {"status": "Synced"}}}
```

**Projects list** (lines 114-119): Change flat `"name"` to `"metadata":{"name":...}`:
```go
// OLD: {"name": "default", "spec": {"description": "Default project"}}
// NEW: {"metadata": {"name": "default"}, "spec": {"description": "Default project"}}
```

**Project describe** (lines 126-129): Same change.

**Clusters list** (lines 158-175): Keep flat (real API is flat). No change.

**Cluster get** (lines 208-218): Keep flat. No change.

**Repositories list** (lines 225-242): Keep flat. No change.

**Repository get** (lines 249-258): Keep flat. No change.

**Certificates** (lines 136-151): Keep flat. No change.

**DryRunNoMutation test** (lines 130-191): The inline mock handlers at lines 136, 143, 152 already use nested format (`{"metadata":{"name":"my-app"}...}`). These are already correct. No change needed.

### 2. `components/tool/argocd/application_test.go` — Update assertions + add regression tests

**TestApplicationList** (lines 18-59): The assertions at lines 36-46 check `outputs[0].Name == "my-app"` etc. These will still pass because the mock now returns nested JSON, goargocdclient correctly unmarshals it, and `item.Name` is populated. No assertion changes needed.

**TestApplicationDescribe** (lines 61-89): Line 73 checks `Contains(describeResult, '"name":"my-app"')`. After the fix, the JSON will contain `"metadata":{"name":"my-app"...}`. Update assertion to check for `"name":"my-app"` inside the metadata block, or simply check that the describe result contains the name string.

**TestApplicationCreate** (lines 193-209): Line 205 checks `Contains(createResult, "my-new-app")`. The mock response now returns nested metadata, so the marshalled output will contain `"metadata":{"name":"my-new-app"}`. The assertion still passes (substring match).

**Add new regression tests** after existing tests:

```go
func (t *ToolTestSuite) TestApplicationListFilterSubstring() {
    ctx := context.Background()
    listTool, err := NewApplicationListTool(ctx, t.configs)
    assert.NoError(t.T(), err)

    // filter "kafka" should match application named "kafka-hpd1" (substring)
    listResult, err := listTool.InvokableRun(ctx, `{"instance": "test", "filter": "kafka"}`)
    assert.NoError(t.T(), err)
    // The mock doesn't have "kafka-hpd1", so this test needs a mock update.
    // See mock changes below.
}

func (t *ToolTestSuite) TestApplicationListFilterNoMatch() {
    ctx := context.Background()
    listTool, err := NewApplicationListTool(ctx, t.configs)
    assert.NoError(t.T(), err)

    listResult, err := listTool.InvokableRun(ctx, `{"instance": "test", "filter": "nonexistent"}`)
    assert.NoError(t.T(), err)
    assert.Equal(t.T(), "[]", strings.TrimSpace(listResult))
}

func (t *ToolTestSuite) TestApplicationListFilterInvalidRegex() {
    ctx := context.Background()
    listTool, err := NewApplicationListTool(ctx, t.configs)
    assert.NoError(t.T(), err)

    _, err = listTool.InvokableRun(ctx, `{"instance": "test", "filter": "[invalid"}`)
    assert.Error(t.T(), err)
}

func (t *ToolTestSuite) TestApplicationDescribeMetadataName() {
    ctx := context.Background()
    describeTool, err := NewApplicationDescribeTool(ctx, t.configs)
    assert.NoError(t.T(), err)

    describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "my-app"}`)
    assert.NoError(t.T(), err)
    // After fix, metadata should contain the name
    assert.Contains(t.T(), describeResult, `"name":"my-app"`)
}

func (t *ToolTestSuite) TestProjectListNames() {
    ctx := context.Background()
    listTool, err := NewProjectListTool(ctx, t.configs)
    assert.NoError(t.T(), err)

    listResult, err := listTool.InvokableRun(ctx, `{"instance": "test"}`)
    assert.NoError(t.T(), err)

    var outputs []ProjectListOutput
    err = json.Unmarshal([]byte(listResult), &outputs)
    assert.NoError(t.T(), err)
    assert.Len(t.T(), outputs, 2)
    assert.Equal(t.T(), "default", outputs[0].Name)
    assert.Equal(t.T(), "production", outputs[1].Name)
}

func (t *ToolTestSuite) TestProjectDescribeMetadataName() {
    ctx := context.Background()
    describeTool, err := NewProjectDescribeTool(ctx, t.configs)
    assert.NoError(t.T(), err)

    describeResult, err := describeTool.InvokableRun(ctx, `{"instance": "test", "name": "default"}`)
    assert.NoError(t.T(), err)
    assert.Contains(t.T(), describeResult, `"name":"default"`)
}
```

### 3. `components/tool/argocd/suite_test.go` — Add kafka-hpd1 test data

Add a third application to the applications list mock to enable the substring filter regression test:

```go
{
    "metadata": {"name": "kafka-hpd1", "namespace": "argocd"},
    "spec": {"project": "data", "source": {"repoURL": "https://git.example.com/kafka"}},
    "status": {"health": {"status": "Healthy"}, "sync": {"status": "Synced", "revision": "def456"}}
}
```

Also add a get handler for `kafka-hpd1`:
```go
mux.HandleFunc("/api/v1/applications/kafka-hpd1", func(w http.ResponseWriter, r *http.Request) {
    // ... nested metadata response
})
```

### 4. `components/tool/argocd/check.go` — Remove raw-HTTP workaround

**Delete** (lines 76-212):
- Local types: `metadataName`, `appListItem`, `appList`, `clusterListItem`, `clusterList`, `projectListItem`, `projectList`
- Functions: `newArgoCDHTTPClient`, `doArgoCDListGET`, `fetchFirstApp`, `fetchFirstProject`, `fetchClusterServers`

**Modify `Check` function** (lines 24-58):
- Remove `rawHTTP := newArgoCDHTTPClient(cfg)` (line 49)
- Change `probeInstance(baseCtx, client, rawHTTP, instance, cfg)` to `probeInstance(baseCtx, client, instance, cfg)` (line 53)

**Modify `probeInstance` signature** (line 216):
- Remove `httpClient *http.Client` and `cfg Config` parameters
- New signature: `func probeInstance(ctx context.Context, client api.API, instance string) checkup.Results`

**Rewrite name extraction in `probeInstance`** (lines 221-358):
- For application describe: use `apps[0].Name` and `apps[0].Namespace` directly (now correctly populated)
- For cluster describe: use `clusters[0].Name` directly (already works via explicit field)
- For project describe: use `projects[0].Name` directly (now correctly populated)
- Remove the `fetchFirstApp`/`fetchFirstProject`/`fetchClusterServers` calls
- Remove the `httpClient` and `cfg` parameters from all probe helper calls

**Remove unused imports**: `crypto/tls`, `encoding/json`, `io`, `net/http`, `strings`, `time` (check which are still needed after removal).

### 5. `components/tool/argocd/check_test.go` — Verify check tests still pass

The existing check tests (`TestCheckEmptyConfigs`, `TestCheckNilConfigs`, `TestCheckInvalidInstance`, `TestCheckResultStatuses`, `TestCheckClientErrorResults`) should continue to pass without changes since they test error paths and don't depend on the raw HTTP workaround.

### 6. `go.mod` — Dependency bump strategy

**During development/testing** (NOT committed):
```bash
cd /projects/eino-ext-worktrees/fix-argocd-application-list-filter
go mod edit -replace github.com/disaster37/goargocdclient=/projects/goargocdclient
go mod tidy
# Run tests to verify
go test ./components/tool/argocd/...
# Remove replace before committing
go mod edit -dropreplace github.com/disaster37/goargocdclient
go mod tidy
```

**After user releases new goargocdclient version**:
```bash
cd /projects/eino-ext-worktrees/fix-argocd-application-list-filter
go get github.com/disaster37/goargocdclient@<new-version>
go mod tidy
# Commit go.mod and go.sum
```

The final committed `go.mod` must contain the new released version (e.g., `v0.0.0-20261005...` or a proper semver tag), NOT the replace directive.

## Test Plan

### goargocdclient tests

| Test file | New tests | What they verify |
|-----------|-----------|-----------------|
| `api/application_test.go` | `TestApplicationModel_UnmarshalNestedMetadata` | Raw nested JSON → Name/Namespace populated |
| | `TestApplicationModel_MarshalNestedMetadata` | Marshal → JSON has `"metadata":{"name":...}` |
| | `TestApplicationModel_RoundTrip` | Marshal→Unmarshal preserves Name |
| `api/project_test.go` | `TestProjectModel_UnmarshalNestedMetadata` | Same for ProjectModel |
| | `TestProjectModel_MarshalNestedMetadata` | Same for ProjectModel |
| | `TestProjectModel_RoundTrip` | Same for ProjectModel |
| `api/applicationset_test.go` | `TestApplicationSetModel_UnmarshalNestedMetadata` | Same for ApplicationSetModel |
| | `TestApplicationSetModel_MarshalNestedMetadata` | Same for ApplicationSetModel |
| | `TestApplicationSetModel_RoundTrip` | Same for ApplicationSetModel |

All existing tests must continue to pass.

### eino-ext tests

| Test file | Test function | What it verifies |
|-----------|--------------|-----------------|
| `application_test.go` | `TestApplicationList` | Names/namespaces populated, filter works |
| | `TestApplicationListFilterSubstring` (NEW) | `filter:"kafka"` matches `kafka-hpd1` |
| | `TestApplicationListFilterNoMatch` (NEW) | Non-matching filter returns `[]` |
| | `TestApplicationListFilterInvalidRegex` (NEW) | Invalid regex returns error |
| | `TestApplicationDescribe` | Metadata name in output |
| | `TestApplicationDescribeMetadataName` (NEW) | Explicit metadata name check |
| | `TestApplicationCreate` | Create response contains name |
| | `TestApplicationSync` | Sync works |
| | `TestApplicationDelete` | Delete works |
| | `TestDryRunNoMutation` | Dry-run doesn't mutate |
| `additional_test.go` | `TestCertificateList` | Certificates still work (flat API) |
| | `TestClusterList` | Clusters still work (flat API) |
| | `TestClusterDescribe` | Cluster describe still works |
| | `TestRepositoryList` | Repositories still work (flat API) |
| | `TestRepositoryDescribe` | Repository describe still works |
| `application_test.go` | `TestProjectList` | Project names populated |
| | `TestProjectListNames` (NEW) | Explicit project name check |
| | `TestProjectDescribe` | Project metadata name in output |
| | `TestProjectDescribeMetadataName` (NEW) | Explicit project metadata name check |
| `check_test.go` | All existing tests | Check logic still works after workaround removal |

## Edge Cases and Error Scenarios

1. **Empty list**: `filterMapMarshal` handles empty `items` slice → returns `[]`
2. **No matches**: Filter regex matches nothing → returns `[]`
3. **Regex special characters**: `filter.Compile` validates RE2 syntax → returns error for invalid regex
4. **App names with URL-special characters**: `application_describe` uses name as URL path segment; goargocdclient's `Get` uses `fmt.Sprintf` without escaping. This is a pre-existing issue (not introduced by this fix) and out of scope.
5. **appNamespace/project/selector query params**: Passed through to goargocdclient unchanged; not affected by metadata fix
6. **HTTP error mapping**: 401/403/404/5xx handled by goargocdclient's `parseError`; not affected
7. **Context cancellation/timeouts**: Handled by resty client timeout; not affected
8. **Nil/empty configs**: `newBaseTool` returns error for empty configs; `validateParams` catches nil params
9. **Multiple instances**: Each instance gets its own client; not affected
10. **Filter applied to full output JSON**: The filter regex matches against the entire marshalled output JSON (not just name). This is by design and documented. After the fix, the output JSON contains the real name, so substring filters like `kafka` will match `kafka-hpd1`.
11. **Create request body shape**: After the fix, `ApplicationModel` marshals with nested `"metadata":{"name":...}`, matching the real API's expected format. Previously it sent flat `"name"` which the real API may have rejected or ignored.
12. **ClusterModel.ObjectMeta**: The cluster API is flat, so `ClusterModel` keeps its flat `ObjectMeta` embedding. The `cluster_describe.go` already copies `cluster.ObjectMeta.Name = cluster.Name` as a workaround. After the fix, this copy is still needed because the cluster API is genuinely flat. No change needed.

## Step-by-Step Implementation Checklist

### Phase 1: goargocdclient (in /projects/goargocdclient)

- [ ] 1. Edit `api/application.go`: Add `json:"metadata"` tag to embedded `ObjectMeta` in `ApplicationModel`
- [ ] 2. Edit `api/applicationset.go`: Add `json:"metadata"` tag to embedded `ObjectMeta` in `ApplicationSetModel`
- [ ] 3. Edit `api/project.go`: Add `json:"metadata"` tag to embedded `ObjectMeta` in `ProjectModel`
- [ ] 4. Add `TestApplicationModel_UnmarshalNestedMetadata`, `TestApplicationModel_MarshalNestedMetadata`, `TestApplicationModel_RoundTrip` to `api/application_test.go`
- [ ] 5. Add `TestProjectModel_UnmarshalNestedMetadata`, `TestProjectModel_MarshalNestedMetadata`, `TestProjectModel_RoundTrip` to `api/project_test.go`
- [ ] 6. Add `TestApplicationSetModel_UnmarshalNestedMetadata`, `TestApplicationSetModel_MarshalNestedMetadata`, `TestApplicationSetModel_RoundTrip` to `api/applicationset_test.go`
- [ ] 7. Run `go build ./... && go vet ./... && go test ./...` in /projects/goargocdclient
- [ ] 8. Commit with message: `fix(api): add json:"metadata" tag to embedded ObjectMeta in ApplicationModel, ApplicationSetModel, and ProjectModel`

### Phase 2: eino-ext (in /projects/eino-ext-worktrees/fix-argocd-application-list-filter)

- [ ] 9. Temporarily add replace directive: `go mod edit -replace github.com/disaster37/goargocdclient=/projects/goargocdclient && go mod tidy`
- [ ] 10. Edit `components/tool/argocd/suite_test.go`: Change application and project mock JSON to nested `"metadata"` format; add `kafka-hpd1` test application
- [ ] 11. Edit `components/tool/argocd/check.go`: Remove raw-HTTP workaround (local types, raw HTTP helpers); simplify `probeInstance` to use goargocdclient types directly
- [ ] 12. Edit `components/tool/argocd/application_test.go`: Add regression tests (`TestApplicationListFilterSubstring`, `TestApplicationListFilterNoMatch`, `TestApplicationListFilterInvalidRegex`, `TestApplicationDescribeMetadataName`, `TestProjectListNames`, `TestProjectDescribeMetadataName`)
- [ ] 13. Run `go build ./... && go vet ./... && go test ./components/tool/argocd/...` in the worktree
- [ ] 14. Run `golangci-lint run ./components/tool/argocd/...` (if installed at `/home/user/bin/golangci-lint`)
- [ ] 15. Remove replace directive: `go mod edit -dropreplace github.com/disaster37/goargocdclient && go mod tidy`
- [ ] 16. **Wait for user to create new goargocdclient release**
- [ ] 17. Run `go get github.com/disaster37/goargocdclient@<new-version> && go mod tidy`
- [ ] 18. Run `go build ./... && go vet ./... && go test ./components/tool/argocd/...` again
- [ ] 19. Commit with message: `fix(argocd): bump goargocdclient to fix application/project/application-set metadata deserialization`

## Verification Commands

### goargocdclient
```bash
cd /projects/goargocdclient
go build ./...
go vet ./...
go test ./... -v
```

### eino-ext (with replace directive for testing)
```bash
cd /projects/eino-ext-worktrees/fix-argocd-application-list-filter
go mod edit -replace github.com/disaster37/goargocdclient=/projects/goargocdclient
go mod tidy
go build ./...
go vet ./...
go test ./components/tool/argocd/... -v
# Optional: golangci-lint
/home/user/bin/golangci-lint run ./components/tool/argocd/...
# Remove replace
go mod edit -dropreplace github.com/disaster37/goargocdclient
go mod tidy
```

### eino-ext (after release, final verification)
```bash
cd /projects/eino-ext-worktrees/fix-argocd-application-list-filter
go get github.com/disaster37/goargocdclient@<new-version>
go mod tidy
go build ./...
go vet ./...
go test ./components/tool/argocd/... -v
```

## Risks / Open Questions

1. **Release timing**: The eino-ext PR cannot be merged until the new goargocdclient release exists. The user will create the release after our goargocdclient changes are committed. The eino-ext PR should be held as a draft until the release is available.

2. **Backward compatibility**: The goargocdclient change is a breaking change for JSON wire format (marshal now produces nested metadata, unmarshal now expects nested metadata). However, the library is pre-v1 (v0.0.0 pseudo-version), and the previous behavior was buggy (names were empty against real servers). Go source compatibility is preserved (`app.Name` still works).

3. **ApplicationSetTemplateMeta**: Must NOT be changed. It is intentionally flat (template metadata). Verified at `api/applicationset.go:265-271`.

4. **SSE watch parsing**: `ApplicationWatchEvent` and `ApplicationSetWatchEvent` embed the model types. After the fix, SSE data lines with nested metadata will correctly unmarshal. The existing SSE tests marshal the model types (self-consistent), so they'll continue to pass.

5. **Cluster describe workaround**: `cluster_describe.go:62` has `cluster.ObjectMeta.Name = cluster.Name`. This is still needed because the cluster API is genuinely flat. No change needed.

6. **Repository describe workaround**: `repository_describe.go:68` has `repository.ObjectMeta.Name = repository.Name`. Same reason — repository API is flat. No change needed.

7. **README updates**: The `components/tool/argocd/README.md` does not need changes (the tool behavior is unchanged from the user's perspective — names now work correctly). The goargocdclient `README.md` may need a note about the metadata format if it documents the JSON structure, but this is optional.
