# eino-ext: Remove `disaster37/opensearch/v3` from `components/tool/opensearch`

## Audience note
This plan is written to be executed in a **separate repository/IDE session**
with no access to the `rancher-doc-chat-api-k8s` project. It targets the Go
module `github.com/webcenter-fr/eino-ext`. All file paths below are relative
to that module's root. Every current file's exact content needed to perform
the migration is included inline so no other repo access is required.

## Background

`components/tool/opensearch` already uses
`github.com/disaster37/opensearch/v4` internally for everything (client type,
query DSL, search API — `opensearchv4`, `opensearchv4api`, `querydsl`). The
**only** remaining use of `github.com/disaster37/opensearch/v3` in this
package is the **exported parameter type** of two public functions:

- `NewOpensearchLogKubernetesTool(ctx context.Context, cfg *config.Config) (*OpensearchLogKubernetesTool, error)`
- `Check(ctx context.Context, cfg *config.Config) checkup.Results`

where `config.Config` is `github.com/disaster37/opensearch/v3/config.Config`:

```go
// v3/config.Config (github.com/disaster37/opensearch/v3@v3.4.3/config/config.go)
type Config struct {
	URLs        []string
	Index       string
	Username    string
	Password    string
	Shards      int
	Replicas    int
	Sniff       *bool
	Healthcheck *bool
	Transport   http.RoundTripper
	Logger      *logrus.Logger
}
```

Both functions immediately convert this into a v4 client via
`NewClient` (`components/tool/opensearch/opensearch.go`), which — critically —
**only reads `cfg.URLs`, `cfg.Username`, `cfg.Password`**. `Index`, `Shards`,
`Replicas`, `Sniff`, `Healthcheck`, `Transport`, and `Logger` are all silently
ignored today. There is no way for a caller to configure TLS verification
skip through this API at all (v3's `config.Config` has no such field; the
`Transport` field exists but is dropped).

There is a stale, unrelated migration-plan file already sitting in this
module at `.kilo/plans/1783341138102-opensearch-v3-to-v4-migration.md` — it
describes migrating the *query-building* layer (bool/term/range queries,
search API) from v3 to v4, which has **already been done** (the current code
uses `querydsl`/`opensearchv4api` throughout). Ignore that file; it is
outdated. This plan only covers the remaining exported-signature cleanup.

## Goal

Remove `github.com/disaster37/opensearch/v3` as a dependency of
`components/tool/opensearch` entirely, by changing the public API to accept
a v4-native, already-established shape: `libs/toolkit/osclient.Config`
(already used by every other opensearch-backed component in this module —
memory, agent/memory, retriever, indexer — so this also makes
`components/tool/opensearch` consistent with the rest of the module).

```go
// libs/toolkit/osclient/osclient.go — already exists, do not modify
type Config struct {
	URLs          []string // first entry is used
	Username      string
	Password      string
	TLSSkipVerify bool
}
```

## Files to change

### 1. `components/tool/opensearch/opensearch.go`

Current:
```go
package opensearch

import (
	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v3/config"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

// NewClient creates a new OpenSearch v4 client using the provided v3-compatible
// configuration. It delegates to the shared osclient builder to keep the
// connection logic centralized.
func NewClient(cfg *config.Config) (opensearchv4.Client, error) {
	if cfg == nil || len(cfg.URLs) == 0 {
		return nil, errors.New("at least one OpenSearch URL is required")
	}
	return osclient.New(osclient.Config{
		URLs:     cfg.URLs,
		Username: cfg.Username,
		Password: cfg.Password,
	}, 0)
}
```

New:
```go
package opensearch

import (
	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

// NewClient creates a new OpenSearch v4 client from the shared connection
// configuration used across every eino-ext OpenSearch-backed component.
func NewClient(cfg *osclient.Config) (opensearchv4.Client, error) {
	if cfg == nil || len(cfg.URLs) == 0 {
		return nil, errors.New("at least one OpenSearch URL is required")
	}
	return osclient.New(*cfg, 0)
}
```

Note: `osclient.New` now receives `cfg.TLSSkipVerify` for real (previously
always defaulted to `false` since the v3-typed caller had no such field) —
this is the actual bug fix motivating this migration.

### 2. `components/tool/opensearch/opensearch_log_kubernetes.go`

Only the import block and the `NewOpensearchLogKubernetesTool` signature
change; nothing else in this file references `config.Config` directly (it
only uses `t.client`, already `opensearchv4.Client`).

Current imports (top of file):
```go
import (
	"context"
	"fmt"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/disaster37/opensearch/v3/config"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	opensearchv4api "github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/sirupsen/logrus"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)
```

New imports:
```go
import (
	"context"
	"fmt"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	opensearchv4 "github.com/disaster37/opensearch/v4"
	opensearchv4api "github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/sirupsen/logrus"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)
```

Current function (end of file):
```go
// NewOpensearchLogKubernetesTool creates a new instance of the OpensearchLogKubernetesTool.
func NewOpensearchLogKubernetesTool(ctx context.Context, cfg *config.Config) (*OpensearchLogKubernetesTool, error) {

	c, err := NewClient(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Opensearch client")
	}
	...
}
```

New function (only the parameter type changes):
```go
// NewOpensearchLogKubernetesTool creates a new instance of the OpensearchLogKubernetesTool.
func NewOpensearchLogKubernetesTool(ctx context.Context, cfg *osclient.Config) (*OpensearchLogKubernetesTool, error) {

	c, err := NewClient(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Opensearch client")
	}
	...
}
```
(everything else in the function body is unchanged — omitted here for
brevity, copy verbatim from the current file.)

### 3. `components/tool/opensearch/check.go`

Current:
```go
package opensearch

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/disaster37/opensearch/v3/config"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const osCheckTimeout = 10 * time.Second

func Check(ctx context.Context, cfg *config.Config) checkup.Results {
	if cfg == nil || len(cfg.URLs) == 0 {
		...
	}
	...
	client, err := NewClient(cfg)
	...
	results = append(results, checkup.Result{
		Component: "opensearch_log_kubernetes",
		Status:    checkup.StatusLimited,
		Message:   fmt.Sprintf("requires pod-specific parameters for invoke, connectivity verified via %s", cfg.URLs),
	})
	return results
}
```

New: replace only the import and function signature; the body is otherwise
identical (still references `cfg.URLs`, which exists on `osclient.Config`
too):
```go
package opensearch

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)

const osCheckTimeout = 10 * time.Second

func Check(ctx context.Context, cfg *osclient.Config) checkup.Results {
	// ...body unchanged, cfg.URLs / NewClient(cfg) still compile identically...
}
```

### 4. `components/tool/opensearch/check_test.go`

Current relevant lines:
```go
import (
	...
	"github.com/disaster37/opensearch/v3/config"
)
...
results := Check(ctx, &config.Config{URLs: nil})
```

New:
```go
import (
	...
	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
)
...
results := Check(ctx, &osclient.Config{URLs: nil})
```
Search the rest of this test file (and `opensearch_test.go`, if it also
constructs a `config.Config{...}` for `NewOpensearchLogKubernetesTool`) for
every other `config.Config{...}` literal and replace with the equivalent
`osclient.Config{...}` literal (field names `URLs`, `Username`, `Password`
are identical; drop any `Index`/`Shards`/`Replicas`/`Sniff`/`Healthcheck`/
`Transport`/`Logger` fields set in test fixtures — they were never read
by `NewClient` anyway, so removing them changes no test behavior. Add
`TLSSkipVerify: true/false` to any test that specifically wants to exercise
that behavior, since it is newly meaningful).

### 5. `components/tool/opensearch/README.md`

Update the usage example from:
```go
import "github.com/disaster37/opensearch/v3/config"

cfg := config.Config{
    URLs: []string{"https://opensearch:9200"},
    ...
}
tool, err := opensearch.NewOpensearchLogKubernetesTool(ctx, cfg)
```
to:
```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"

cfg := &osclient.Config{
    URLs:          []string{"https://opensearch:9200"},
    Username:      "...",
    Password:      "...",
    TLSSkipVerify: false,
}
tool, err := opensearch.NewOpensearchLogKubernetesTool(ctx, cfg)
```
Also document that `TLSSkipVerify` is now honored (it wasn't before this
change) and that only `URLs[0]` is ever connected to (matches every other
`osclient`-based component's documented behavior — check
`libs/toolkit/osclient/osclient.go`'s doc comment for the exact wording to
reuse).

### 6. `go.mod` / `go.sum`

Remove the now-unused direct dependency:
```
github.com/disaster37/opensearch/v3 v3.4.3
```
Run `go mod tidy` after the code changes above (v3 will only actually drop
if nothing else in the module still imports it — confirm with
`grep -rln "disaster37/opensearch/v3" --include="*.go" .` returning no
results before tidying).

## Validation
```bash
grep -rln "disaster37/opensearch/v3" --include="*.go" .   # expect: no output
go build ./...
go vet ./...
go test ./components/tool/opensearch/...
go mod tidy && git diff go.mod go.sum   # confirm v3 line removed, nothing else changed
```

## Downstream consumer note (informational only, not part of this plan)
The consuming application `rancher-doc-chat-api-k8s` currently calls
`opensearch.NewOpensearchLogKubernetesTool` and `opensearchtool.Check` with a
hand-built `*disaster37/opensearch/v3/config.Config`
(`internal/server/agent/agent_opensearch.go`,
`internal/server/agent/chat.go`). That repo has already rewritten its
checkup log probe to bypass `opensearchtool.Check` entirely (calls v4
directly), so only `agent_opensearch.go`'s `buildOpensearchAgent` call site
needs updating there, once this module ships this change and the consumer
bumps its `go.mod` pseudo-version: change the `cfg *config.Config` parameter
type on `buildOpensearchAgent` to `*osclient.Config` and update its single
construction site in `chat.go` accordingly. That follow-up is tracked in the
consumer repo's own plan, not here.

## Open question (resolve before implementing)
Keep `Index` out of `osclient.Config` (it's connection-level, not
tool-level) and instead leave `runSearch`'s hardcoded `Indices: []string{"*"}`
exactly as-is — today it never reads an index from config at all, so there
is nothing to wire yet. Recommendation: do **not** add an `Index` field to
this migration; none of the removed v3 fields (`Index`, `Shards`,
`Replicas`, `Sniff`, `Healthcheck`, `Transport`, `Logger`) were ever
actually read by this package regardless.
