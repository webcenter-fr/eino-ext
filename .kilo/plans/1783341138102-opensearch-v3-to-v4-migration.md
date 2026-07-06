# OpenSearch v3 → v4 Migration Plan

## Scope
Migrate the `github.com/disaster37/opensearch/v3` client library to `github.com/disaster37/opensearch/v4` in the `components/tool/opensearch` package.

## Affected Files
- `components/tool/opensearch/opensearch.go` (client creation)
- `components/tool/opensearch/opensearch_log_kubernetes.go` (search operations)
- `components/tool/opensearch/opensearch_test.go` (unit tests)
- `go.mod` (dependency declaration)
- `go.sum` (checksums)
- `components/tool/opensearch/README.md` (documentation)

## Key Breaking Changes

### 1. Import Path Changes
```go
// v3
import "github.com/disaster37/opensearch/v3"
import "github.com/disaster37/opensearch/v3/config"

// v4
import "github.com/disaster37/opensearch/v4"
import "github.com/disaster37/opensearch/v4/querydsl"
```

### 2. Client Creation
```go
// v3 - NewClientFromConfig with config.Config
cfg.Sniff = ptr.To(false)
cfg.Healthcheck = ptr.To(false)
es, err := opensearch.NewClientFromConfig(cfg)

// v4 - New with different Config struct
import "github.com/sirupsen/logrus"

opensearchCfg := &opensearch.Config{
    URL:         cfg.Addresses[0],  // Extract from v3 config
    Username:    cfg.Username,
    Password:    cfg.Password,
    CACert:      cfg.CACert,
    TLSSkipVerify: cfg.TLSClientConfig.InsecureSkipVerify,
    Timeout:     cfg.Timeout,
}
logger := logrus.NewEntry(logrus.New())
es, err := opensearch.New(opensearchCfg, logger)
```

### 3. Query Building Pattern
```go
// v3 - Package-level functions
boolQuery := opensearch.NewBoolQuery()
boolQuery.Must(opensearch.NewRangeQuery("@timestamp").Gte(params.From).Lte(params.To))
boolQuery.Must(opensearch.NewTermQuery("labels.cluster", params.Cluster))
stringQuery := opensearch.NewQueryStringQuery(params.LuceneQuery).AnalyzeWildcard(true)
boolQuery.Must(stringQuery)

// v4 - querydsl package with builder pattern
boolQuery := querydsl.NewBoolQuery()
boolQuery.Must(querydsl.NewRangeQuery("@timestamp").Gte(params.From).Lte(params.To))
boolQuery.Must(querydsl.NewTermQuery("labels.cluster", params.Cluster))
stringQuery := querydsl.NewQueryStringQuery(params.LuceneQuery).WithAnalyzeWildcard(true)
boolQuery.Must(stringQuery)
```

### 4. Search API
```go
// v3 - Builder pattern with chained methods
res, err := t.client.Search().
    Query(boolQuery).
    Sort("@timestamp", false).
    Size(int(params.MaxLines)).
    Fields("event.original").
    FetchSource(false).
    TrackTotalHits(true).
    Do(ctx)

// v4 - Structured request + service method
searchReq := querydsl.NewSearchRequest().
    Query(boolQuery).
    Sort("@timestamp", false).
    Size(int(params.MaxLines)).
    FetchSource(false).
    TrackTotalHits(true)

// Fields need special handling in v4
searchReq.DocvalueFields("event.original")

searchRes, err := t.client.Search().Search(ctx, &api.SearchRequest{
    Indices: []string{"*"},  // Default to all indices if not specified
    Body:   searchReq,
})
```

### 5. Response Structure
```go
// v3 - Access via res.Hits.Hits and res.TotalHits.Value
for _, hit := range res.Hits.Hits {
    if v, ok := hit.Fields["event.original"]; ok {
        ...
    }
}
totalRemaining := res.TotalHits.Value - int64(len(res.Hits.Hits))

// v4 - Same structure but accessed via querydsl types
for _, hit := range searchRes.Hits.Hits {
    if v, ok := hit.Fields["event.original"]; ok {
        ...
    }
}
totalRemaining := searchRes.Hits.TotalHits.Value - int64(len(searchRes.Hits.Hits))
```

## Implementation Steps

### 1. Update go.mod and go.sum
```bash
go get github.com/disaster37/opensearch/v4@latest
go mod tidy
```

### 2. Migrate opensearch.go
- Update imports to use v4
- Replace `NewClientFromConfig` with `New`
- Map v3 config fields to v4 Config struct
- Add logger creation if not present

### 3. Migrate opensearch_log_kubernetes.go
- Update imports
- Update query building to use `querydsl` package
- Replace `AnalyzeWildcard(true)` with `WithAnalyzeWildcard(true)`
- Update search calls to use new API pattern
- Handle field retrieval (DocvalueFields vs Fields)

### 4. Migrate opensearch_test.go
- Update imports
- Update type assertions if any
- Ensure all tests pass

### 5. Update README.md
- Update import examples
- Update configuration examples to show v4 Config usage

### 6. Validate
```bash
go build ./...
go test ./components/tool/opensearch/...
go vet ./...
```

## Risk Assessment

### Low Risk
- Core functionality (bool queries, term queries, range queries) preserved
- Response structure largely compatible
- Query DSL semantics unchanged

### Medium Risk
- Config struct changes require careful field mapping
- Field retrieval (`Fields` vs `DocvalueFields`) needs verification
- Timeout and error handling may differ slightly

### Mitigation
- Run comprehensive tests after migration
- Test with actual OpenSearch instance if available
- Verify log retrieval functionality end-to-end

## Open Questions
1. What should the default search indices be? (v3 didn't specify, v4 requires indices array)
2. Is there a specific index pattern the tool should search against?
3. Should timeout/error handling be enhanced with v4's retry features?

## Post-Migration Validation
- [ ] Build succeeds without errors
- [ ] Unit tests pass
- [ ] Integration tests with live OpenSearch instance pass
- [ ] Log retrieval functionality works correctly
- [ ] README documentation updated and accurate
- [ ] No runtime errors in production usage