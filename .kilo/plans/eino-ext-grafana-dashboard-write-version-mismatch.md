# Fix plan: `grafana_dashboard_write` returns spurious `version-mismatch` (HTTP 412)

**Target repository:** `github.com/webcenter-fr/eino-ext`
**Target package:** `components/tool/grafana`
**Observed with:** `v0.0.0-20260824134516-4811a624e8ad`
**Consumer that reported it:** `rancher-doc-chat-api-k8s` (`grafana_agent`)

This document is written to be implemented by an external agent working in the
`eino-ext` repository. It contains the diagnosis, the required behaviour, and a
concrete change list with tests. Nothing here needs to be changed in the
consumer repository.

---

## 1. Symptom

Updating an existing dashboard through `grafana_dashboard_write` fails with:

```
failed to save dashboard: Grafana API error: HTTP 412 POST /api/dashboards/db:
{"message":"The dashboard has been changed by someone else","status":"version-mismatch"}
```

The message is misleading. In the captured incidents no other user or process
touched the dashboard. The failure is produced by the tool itself.

## 2. Diagnosis

`POST /api/dashboards/db` performs an optimistic-concurrency check: Grafana
compares `dashboard.version` in the request body against the stored version and
rejects the save with `412 / version-mismatch` when they differ — **unless**
the top-level `overwrite` flag is `true`. An **absent** `version` is read as
`0`, which never matches an existing dashboard, so it is rejected as well. A
missing `version` is therefore not "skip the check", it is "guaranteed
conflict".

The tool does not participate in this protocol at all:

- `dashboard_write.go`, `create/update` branch: the request body is built as
  `saveDashboardRequest{Dashboard: dashboardModel, FolderUID:, Message:,
  Overwrite: params.Overwrite}`. The current version is never fetched and
  never injected.
- `saveDashboardRequest` (`client.go`) has no `version` field. The only
  `version` that reaches Grafana is whatever the LLM happened to write inside
  the `dashboard` JSON string.
- `Overwrite` defaults to `false`.
- A stale numeric `id` inside the model is passed through untouched.

Consequently a correct update requires the model author to have guessed the
exact current version. Field evidence from three captured `grafana_dashboard_write`
executions against the same dashboard (`uid 3ce913db-…`, `id 1471`):

| `overwrite` | `dashboard.version` in model | result |
|---|---|---|
| `true` | 6 | success |
| absent (`false`) | 7 | HTTP 412 |
| absent (`false`) | **absent** | HTTP 412 |

The only successful call is the one that bypassed the check entirely.

### 2.1 Why this is worse under a human-in-the-loop gate

The consumer implements a dry-run → approve → execute flow. The arguments
previewed at `dryRun=true` are stored verbatim and replayed at execute time, so
the `version` embedded in the model is frozen at preview time. Any save between
preview and approval — **including a previous approved write by the same agent
in the same conversation** — invalidates it. The agent cannot repair this after
approval, so a stale or missing `version` becomes an unrecoverable loop:
412 → re-preview → approve → 412.

The tool must therefore resolve the version at **execute** time, not rely on
the caller's snapshot.

### 2.2 Secondary defect: stale `id`

`Grafana` keys the upsert on `uid`, but a non-zero `id` that belongs to a
different dashboard causes either a cross-dashboard write or a 400. Models are
routinely produced by copying an existing dashboard's JSON, so the stale `id`
is common. Nothing strips it.

### 2.3 Tertiary defect: preview/request key mismatch

The dry-run preview emits the folder under the key `folderUID`, while the real
request body uses `folderUid`. The preview therefore does not faithfully show
the payload that will be sent. Low severity, but it undermines the point of a
preview and should be aligned.

## 3. Required behaviour

1. For `operation=update` on an existing dashboard, the save MUST carry the
   dashboard's current `version`, resolved by the tool at execute time.
2. The caller MUST NOT need to know or supply `version`.
3. Optimistic locking MUST remain effective: a genuine concurrent modification
   MUST still fail rather than silently clobber. `overwrite=true` remains the
   explicit, caller-chosen escape hatch and MUST NOT become the default.
4. A stale `id` MUST NOT be able to redirect the write to another dashboard.
5. A `version-mismatch` that survives the fix MUST produce an actionable error
   that states what the tool observed (expected vs. current version) rather
   than forwarding Grafana's "changed by someone else" text unqualified.
6. The dry-run preview MUST show the payload that will actually be sent.

## 4. Change list

All paths are relative to `components/tool/grafana/`.

### 4.1 `client.go` — expose the status code and a version reader

- Add a typed error so callers can detect the conflict without string
  matching:

  ```go
  // ErrVersionMismatch is returned by SaveDashboard when Grafana rejects the
  // save because the submitted dashboard.version is not the current one
  // (HTTP 412, status "version-mismatch").
  var ErrVersionMismatch = errors.New("dashboard version mismatch")
  ```

- In `SaveDashboard`, inspect the status code already returned by `doRequest`
  and wrap 412 responses in `ErrVersionMismatch` (keep the raw body in the
  error message).

- Add a helper that returns the current version of a dashboard, reusing the
  existing `GetDashboard` (`GET /api/dashboards/uid/:uid`), whose response is
  `{"meta":{...},"dashboard":{...,"version":N}}`:

  ```go
  // dashboardVersion returns the current version of the dashboard identified
  // by uid, and false when the dashboard does not exist.
  func (c *grafanaClient) dashboardVersion(ctx context.Context, uid string) (int, bool, error)
  ```

  A 404 from `GetDashboard` MUST map to `(0, false, nil)` — that is the
  "create a new dashboard" case, not an error.

### 4.2 `dashboard_write.go` — resolve the version before saving

In the `create`/`update` branch, after the blocklist checks and **after** the
`DryRun` short-circuit (so a dry-run performs no extra write-path work beyond
what it already does), and only when `!params.Overwrite`:

```go
// Grafana's save endpoint enforces optimistic concurrency on
// dashboard.version: a stale — or missing, which reads as 0 — version is
// rejected with 412 version-mismatch. Callers (and LLMs) cannot be relied on
// to carry the right version, and under a dry-run/approve flow the model is
// frozen before approval, so resolve the version here, at execute time.
if uid != "" && !params.Overwrite {
    current, exists, err := c.dashboardVersion(ctx, uid)
    if err != nil {
        return "", errors.Wrapf(err, "failed to read current version of dashboard %q", uid)
    }
    switch {
    case !exists:
        // Upsert of a dashboard that no longer exists: it must be created,
        // so any inherited version/id would be rejected.
        delete(dashboardModel, "version")
        delete(dashboardModel, "id")
    default:
        dashboardModel["version"] = current
        // Grafana upserts on uid; a stale numeric id (typically inherited by
        // copying another dashboard's JSON) can retarget the write.
        delete(dashboardModel, "id")
    }
}
```

Notes for the implementer:

- Do this for `create` too when the model carries a `uid` that already exists,
  because Grafana treats that as an upsert. The `uid != ""` condition above
  already covers both operations; do not special-case on `params.Operation`.
- When `params.Overwrite` is true, leave the model untouched: the caller has
  explicitly opted out of the check, and mutating the model would only mask
  what they asked for.
- Perform the mutation on `dashboardModel` **before** marshalling
  `saveDashboardRequest`, and after `checkProtectedModel`, so blocklist
  evaluation still sees the model the caller submitted.

### 4.3 `dashboard_write.go` — actionable conflict error

Wrap the save error:

```go
body, err := c.SaveDashboard(ctx, payload)
if err != nil {
    if errors.Is(err, ErrVersionMismatch) {
        return "", errors.Wrapf(err,
            "dashboard %q was modified concurrently: the tool submitted version %v, "+
                "which Grafana rejected. Re-read the dashboard, re-apply your change on "+
                "top of the newer model, and retry. Set overwrite=true only to "+
                "deliberately discard the concurrent change",
            uid, dashboardModel["version"])
    }
    return "", errors.Wrap(err, "failed to save dashboard")
}
```

After 4.2 this path means a real race (the dashboard changed between the
version read and the save), so the "someone else" wording is finally accurate.

Optionally, retry once on `ErrVersionMismatch` by re-reading the version and
re-posting. Keep it to a single retry and only when `!params.Overwrite`. This
is a convenience, not a substitute for 4.2.

### 4.4 `dashboard_write.go` — faithful dry-run preview

Change the dry-run preview key `folderUID` to `folderUid` to match
`saveDashboardRequest`'s JSON tag, and document in the preview that `version`
is resolved at execute time, e.g. add `"versionResolvedAtExecute": true` when
`!params.Overwrite`. Do not resolve the version during dry-run: a version read
at preview time would be stale by execute time and would reintroduce exactly
the bug being fixed.

### 4.5 Tool description and prompt

- `dashboardWriteDescription`: under `** Safety **`, state that `version` is
  managed by the tool and MUST NOT be supplied by the caller, and that
  `overwrite=true` disables the concurrency check and discards concurrent
  edits.
- `DashboardWriteParams.Overwrite` jsonschema: change
  `"Overwrite without version checking."` to
  `"(optional, create/update) Force the save, discarding any concurrent modification. Leave false unless the user explicitly asked to force it; the tool resolves the current version automatically."`
- `Dashboard` jsonschema: add "Do not include `version` or `id`; they are
  resolved by the tool."
- Check `prompts/` for any text that instructs the caller to carry `version`
  or to retry with `overwrite=true`, and update it.

## 5. Tests

Add to `dashboard_write_test.go` (the existing suite already stubs the Grafana
HTTP API; reuse that harness). No test currently asserts any `version` or `id`
behaviour, so all of these are new.

1. **Version is injected on update.** Stub `GET /api/dashboards/uid/:uid`
   returning `version: 7`; submit a model with no `version`; assert the
   captured `POST /api/dashboards/db` body has `dashboard.version == 7`.
2. **A caller-supplied stale version is replaced.** Same as above but the model
   carries `version: 3`; assert the body carries `7`.
3. **Stale `id` is stripped.** Model carries `id: 1471`; assert the POST body
   has no `id`.
4. **`overwrite=true` is passed through untouched.** Assert no
   `GET /api/dashboards/uid/:uid` is issued and the model is sent verbatim,
   including any caller-supplied `version`/`id`.
5. **Unknown uid.** `GET` returns 404; assert the save still proceeds and the
   POST body carries neither `version` nor `id`.
6. **Genuine conflict.** `GET` returns `version: 7`, `POST` returns 412; assert
   the error is `errors.Is(err, ErrVersionMismatch)` and the message names the
   dashboard uid and version 7.
7. **Dry-run performs no version read.** Assert `dryRun=true` issues no
   `GET /api/dashboards/uid/:uid` beyond the blocklist check and that the
   preview uses the key `folderUid`.
8. **Regression for the reported incident.** Model with `id: 1471`, no
   `version`, `overwrite` unset, `operation=update` → save succeeds.

## 6. Out of scope

- The dry-run/approve replay semantics live in the consumer, not in
  `eino-ext`, and are not changed by this plan.
- `grafana_dashboard_validate` and `grafana_query` are unaffected.

## 7. Acceptance

- `operation=update` with a model containing neither `version` nor `id`
  succeeds against a real Grafana, repeatedly, without `overwrite=true`.
- Two writes racing on the same dashboard still produce exactly one success and
  one `ErrVersionMismatch`.
- `go test ./components/tool/grafana/...` passes.
