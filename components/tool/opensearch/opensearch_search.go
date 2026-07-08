package opensearch

import (
	"context"
	"encoding/json"
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

const (
	defaultSearchTimeField  = "@timestamp"
	defaultSearchSort       = "@timestamp:desc"
	defaultSearchMaxResults = 100
	pitKeepAlive            = "2m"
	searchBatchSize         = 500
	defaultOpensearchTimeout = 30 * time.Second
)

// SearchResultParser converts a raw OpenSearch search hit (source fields plus
// metadata like _id, _index, _score) into a formatted string for output.
// Returned errors are surfaced to the caller.
type SearchResultParser func(ctx context.Context, hit map[string]any) (string, error)

// SearchConfig holds the constructor-level configuration for the generic
// OpenSearch search tool.
type SearchConfig struct {
	// OpenSearch connection configuration (URLs, Username, Password, TLSSkipVerify).
	osclient.Config

	// DefaultIndex is the default index to search when no indices are provided per-call.
	DefaultIndex string `validate:"required" jsonschema:"description=Default OpenSearch index to search"`

	// TimeField is the field used for date range filtering. Defaults to "@timestamp".
	TimeField string `validate:"omitempty" jsonschema:"description=Timestamp field for date range queries, defaults to @timestamp"`

	// DefaultSort specifies the default sort field and direction.
	// Format: "field:asc" or "field:desc". Defaults to "@timestamp:desc".
	DefaultSort string `validate:"omitempty" jsonschema:"description=Default sort field and direction (e.g. @timestamp:desc)"`

	// MaxResults is the default maximum number of results to return.
	// Defaults to 100. Per-call maxResults takes precedence.
	MaxResults int `validate:"omitempty,min=1,max=10000" jsonschema:"description=Maximum number of results to return, defaults to 100"`

	// ResultParser converts each raw hit into a formatted string.
	// If nil, the hit is serialized as compact JSON.
	ResultParser SearchResultParser `validate:"-" jsonschema:"-"`
}

// applySearchDefaults applies default values for optional fields.
func (cfg *SearchConfig) applySearchDefaults() {
	if cfg.TimeField == "" {
		cfg.TimeField = defaultSearchTimeField
	}
	if cfg.DefaultSort == "" {
		cfg.DefaultSort = defaultSearchSort
	}
	if cfg.MaxResults == 0 {
		cfg.MaxResults = defaultSearchMaxResults
	}
}

// SearchParams defines the parameters for a single search invocation.
type SearchParams struct {
	// Indices to search. Overrides the configured DefaultIndex.
	Indices []string `json:"indices,omitempty" jsonschema:"description=OpenSearch indices to search, defaults to configured DefaultIndex"`

	// QueryString is a Lucene query string to filter results.
	// Use "*" or omit for all documents.
	QueryString string `json:"queryString,omitempty" jsonschema:"description=Lucene query string (e.g. 'level:error AND service:api'). Use '*' for all documents."`

	// From is the start time for the date range filter.
	// Supports relative expressions ("now-1h") or RFC3339 absolute times.
	// When omitted, no lower-bound time filter is applied.
	From string `json:"from,omitempty" jsonschema:"description=Start time for date range (relative like 'now-1h' or absolute RFC3339). Omit for no lower bound."`

	// To is the end time for the date range filter.
	// Supports relative expressions ("now") or RFC3339 absolute times.
	// When omitted, no upper-bound time filter is applied.
	To string `json:"to,omitempty" jsonschema:"description=End time for date range (relative like 'now' or absolute RFC3339). Omit for no upper bound."`

	// TimeField overrides the default timestamp field for date range filtering.
	TimeField string `json:"timeField,omitempty" jsonschema:"description=Override the default timestamp field for date range (e.g. 'created_at')"`

	// Sort specifies the sort field and direction. Format: "field:asc" or "field:desc".
	// Overrides the configured DefaultSort.
	Sort string `json:"sort,omitempty" jsonschema:"description=Sort field and direction (e.g. '@timestamp:desc'), overrides configured default"`

	// MaxResults limits the number of results returned.
	// Overrides the configured default. Capped at 10000.
	MaxResults int `json:"maxResults,omitempty" validate:"omitempty,min=1,max=10000" jsonschema:"description=Maximum results to return, overrides configured default (capped at 10000)"`
}

// applyDefaults applies defaults to optional fields.
func (params *SearchParams) applyDefaults(cfg *SearchConfig) {
	if len(params.Indices) == 0 && cfg.DefaultIndex != "" {
		params.Indices = []string{cfg.DefaultIndex}
	}
	if params.QueryString == "" {
		params.QueryString = "*"
	}
	if params.TimeField == "" {
		params.TimeField = cfg.TimeField
	}
	if params.Sort == "" {
		params.Sort = cfg.DefaultSort
	}
	if params.MaxResults == 0 {
		params.MaxResults = cfg.MaxResults
	}
}

// SearchTool is a generic OpenSearch search tool that supports PIT-based
// scrolling, configurable result formatting, and both batch/streaming modes.
type SearchTool struct {
	tool.InvokableTool
	tool.StreamableTool
	client opensearchv4.Client
	config SearchConfig
}

// NewSearchTool creates a generic OpenSearch search tool.
func NewSearchTool(ctx context.Context, cfg *SearchConfig) (*SearchTool, error) {
	if cfg == nil {
		cfg = &SearchConfig{}
	}
	cfg.applySearchDefaults()

	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	c, err := NewClient(ctx, &cfg.Config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create OpenSearch client")
	}

	toolInst := &SearchTool{
		client: c,
		config: *cfg,
	}

	t, err := utils.InferTool("opensearch_search", searchDescription, toolInst.Invoke)
	if err != nil {
		return nil, err
	}
	toolInst.InvokableTool = t

	streamable, err := utils.InferStreamTool("opensearch_search", searchDescription, toolInst.InvokeAsStream)
	if err != nil {
		return nil, err
	}
	toolInst.StreamableTool = streamable

	return toolInst, nil
}

const searchDescription = `
** General Purpose **
Search OpenSearch indices with a Lucene query string and optional date range filtering on a configurable timestamp field. Results are scrolled using PIT (Point-in-Time) for consistent deep pagination.

** Parameters **
- indices: indices to search (defaults to configured index).
- queryString: Lucene query string (e.g. 'level:error AND service:api'). Use '*' for all documents.
- from / to: optional date range on the time field (relative like 'now-1h' or absolute RFC3339).
- timeField: override the default timestamp field for date range.
- sort: sort field and direction (e.g. '@timestamp:desc').
- maxResults: maximum number of results (default 100, capped at 10000).

** Output **
Returns formatted search results as a string. Format depends on the configured ResultParser. Defaults to compact JSON objects, one per line.
`

// pitResponse is the response from the OpenSearch PIT creation endpoint.
type pitResponse struct {
	PitID string `json:"pit_id"`
}

// openPIT creates a Point-in-Time for the given indices.
func (t *SearchTool) openPIT(ctx context.Context, indices []string) (string, error) {
	if len(indices) == 0 {
		return "", errors.New("no indices provided for PIT creation")
	}
	indexPath := strings.Join(indices, ",")
	url := fmt.Sprintf("/%s/_search/point_in_time?keep_alive=%s", indexPath, pitKeepAlive)

	rc := t.client.RestyClient()
	resp, err := rc.R().SetContext(ctx).Post(url)
	if err != nil {
		return "", errors.Wrap(err, "failed to create PIT")
	}
	if resp.IsError() {
		return "", errors.Errorf("PIT creation failed (status %d): %s", resp.StatusCode(), resp.String())
	}

	var pr pitResponse
	if err := json.Unmarshal(resp.Body(), &pr); err != nil {
		return "", errors.Wrap(err, "failed to parse PIT response")
	}
	return pr.PitID, nil
}

// closePIT releases the Point-in-Time.
func (t *SearchTool) closePIT(ctx context.Context, pitID string) {
	if pitID == "" {
		return
	}
	url := "/_search/point_in_time"
	body := map[string]string{"pit_id": pitID}
	rc := t.client.RestyClient()
	resp, err := rc.R().SetContext(ctx).SetBody(body).Delete(url)
	if err != nil || resp.IsError() {
		logrus.WithError(err).WithField("pit_id", pitID).Warn("failed to close PIT")
	}
}

// resolveTimeField returns the effective time field for this search.
func (params *SearchParams) resolveTimeField(cfgTimeField string) string {
	if params.TimeField != "" {
		return params.TimeField
	}
	if cfgTimeField != "" {
		return cfgTimeField
	}
	return defaultSearchTimeField
}

// parseSort parses a sort specification like "@timestamp:desc" into (field, ascending).
func parseSort(sortSpec string) (field string, ascending bool) {
	field, direction, _ := strings.Cut(sortSpec, ":")
	switch strings.ToLower(direction) {
	case "asc", "ascending":
		return field, true
	default:
		return field, false
	}
}

// buildSearchQuery constructs the OpenSearch query from search parameters.
func buildSearchQuery(params *SearchParams, cfg *SearchConfig) querydsl.Query {
	timeField := params.resolveTimeField(cfg.TimeField)

	hasTimeRange := params.From != "" || params.To != ""

	if !hasTimeRange {
		if params.QueryString == "" || params.QueryString == "*" {
			return querydsl.NewMatchAllQuery()
		}
		return querydsl.NewQueryStringQuery(params.QueryString).WithAnalyzeWildcard(true)
	}

	boolQuery := querydsl.NewBoolQuery()

	rangeQuery := querydsl.NewRangeQuery(timeField)
	// Gte/Lte have value receivers and return RangeQuery by value.
	// Assign through the pointer to mutate the original allocation.
	if params.From != "" {
		*rangeQuery = rangeQuery.Gte(params.From)
	}
	if params.To != "" {
		*rangeQuery = rangeQuery.Lte(params.To)
	}
	boolQuery.Must(rangeQuery)

	if params.QueryString != "" && params.QueryString != "*" {
		boolQuery.Must(querydsl.NewQueryStringQuery(params.QueryString).WithAnalyzeWildcard(true))
	}

	return boolQuery
}

// searchRequest builds a querydsl.SearchRequest for the given parameters.
func (t *SearchTool) searchRequest(params *SearchParams, maxResults int, pitID string, searchAfter []any) (*querydsl.SearchRequest, string, bool) {
	query := buildSearchQuery(params, &t.config)

	sortField, sortAsc := parseSort(params.Sort)

	batchSize := searchBatchSize
	if maxResults < batchSize {
		batchSize = maxResults
	}

	sr := querydsl.NewSearchRequest().
		Query(query).
		Size(batchSize).
		Sort(sortField, sortAsc).
		TrackTotalHits("true")

	if pitID != "" {
		sr = sr.PointInTime(&querydsl.PointInTime{
			Id:        pitID,
			KeepAlive: pitKeepAlive,
		})
	}

	if len(searchAfter) > 0 {
		sr = sr.SearchAfter(searchAfter...)
	}

	return sr, sortField, sortAsc
}

// pitScroll iterates over all matched documents using PIT-based scrolling,
// calling the handler for each batch. Returns the total number of hits fetched.
func (t *SearchTool) pitScroll(
	ctx context.Context,
	params *SearchParams,
	maxResults int,
	handler func(batchHits []*querydsl.SearchHit) error,
) (int, error) {
	pitID, err := t.openPIT(ctx, params.Indices)
	if err != nil {
		return 0, errors.Wrap(err, "failed to open PIT")
	}
	defer t.closePIT(context.WithoutCancel(ctx), pitID)

	var searchAfter []any
	fetched := 0
	firstBatch := true

	for fetched < maxResults {
		req, sortField, _ := t.searchRequest(params, maxResults-fetched, pitID, searchAfter)

		body, err := req.Body()
		if err != nil {
			return fetched, errors.Wrap(err, "failed to serialize search request body")
		}

		searchRes, err := t.client.Search().Search(ctx, &opensearchv4api.SearchRequest{
			Indices: nil,
			Body:    body,
		})
		if err != nil {
			return fetched, errors.Wrap(err, "failed to execute search")
		}

		if searchRes == nil || searchRes.Hits == nil || len(searchRes.Hits.Hits) == 0 {
			break
		}

		hits := searchRes.Hits.Hits

		// On the first batch, update pitID from response if needed
		if firstBatch && searchRes.PitId != "" {
			pitID = searchRes.PitId
			firstBatch = false
		} else {
			firstBatch = false
		}

		remaining := maxResults - fetched
		if len(hits) > remaining {
			hits = hits[:remaining]
		}

		if err := handler(hits); err != nil {
			return fetched, err
		}

		fetched += len(hits)

		if len(hits) < searchBatchSize {
			break
		}

		lastHit := hits[len(hits)-1]
		if len(lastHit.Sort) > 0 {
			searchAfter = lastHit.Sort
		} else {
			searchAfter = t.buildSortAfter(lastHit, sortField)
		}
	}

	return fetched, nil
}

// buildSortAfter extracts sort values from a hit for search_after when Sort
// is not populated by OpenSearch (edge case for fields not in docvalue_fields).
func (t *SearchTool) buildSortAfter(hit *querydsl.SearchHit, field string) []any {
	src := map[string]any{}
	if err := json.Unmarshal(hit.Source, &src); err != nil {
		return nil
	}
	if v, ok := src[field]; ok {
		return []any{v}
	}
	return []any{hit.Id}
}

// searchHitToMap converts a search hit to a map representation, adding
// metadata fields (_id, _index, _score, _version).
func searchHitToMap(hit *querydsl.SearchHit) (map[string]any, error) {
	src := map[string]any{}
	if err := json.Unmarshal(hit.Source, &src); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal search hit source")
	}
	src["_id"] = hit.Id
	src["_index"] = hit.Index
	if hit.Score != nil {
		src["_score"] = *hit.Score
	}
	src["_version"] = hit.Version
	return src, nil
}

// formatHits converts a slice of search hits to strings using the configured
// ResultParser or the default JSON formatter.
func (t *SearchTool) formatHits(ctx context.Context, hits []*querydsl.SearchHit) ([]string, error) {
	formatted := make([]string, 0, len(hits))
	for _, hit := range hits {
		hitMap, err := searchHitToMap(hit)
		if err != nil {
			return nil, err
		}
		var s string
		if t.config.ResultParser != nil {
			s, err = t.config.ResultParser(ctx, hitMap)
		} else {
			s, err = defaultSearchResultParser(hitMap)
		}
		if err != nil {
			return nil, err
		}
		formatted = append(formatted, s)
	}
	return formatted, nil
}

// defaultSearchResultParser serializes the hit map as compact JSON.
func defaultSearchResultParser(hit map[string]any) (string, error) {
	b, err := json.Marshal(hit)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal search hit")
	}
	return string(b), nil
}

// Invoke executes the search and returns all results as a single string.
func (t *SearchTool) Invoke(ctx context.Context, params *SearchParams) (result string, err error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultOpensearchTimeout)
		defer cancel()
	}

	params.applyDefaults(&t.config)
	if err := validate.Struct(params); err != nil {
		return "", err
	}

	var allLines []string
	total, err := t.pitScroll(ctx, params, params.MaxResults, func(batchHits []*querydsl.SearchHit) error {
		lines, ferr := t.formatHits(ctx, batchHits)
		if ferr != nil {
			return ferr
		}
		allLines = append(allLines, lines...)
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(allLines) == 0 {
		logrus.Debug("No result found")
		return "No result found", nil
	}

	logrus.Debugf("Retrieved %d results out of %d fetched", len(allLines), total)

	return strings.Join(allLines, "\n"), nil
}

// InvokeAsStream executes the search and streams results line-by-line.
func (t *SearchTool) InvokeAsStream(ctx context.Context, params *SearchParams) (stream *schema.StreamReader[string], err error) {
	params.applyDefaults(&t.config)
	if err := validate.Struct(params); err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()

		// Derive a timeout inside the goroutine so it lives as long as
		// the search itself, not just the InvokeAsStream call frame.
		searchCtx := ctx
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			searchCtx, cancel = context.WithTimeout(ctx, defaultOpensearchTimeout)
			defer cancel()
		}

		hasResults := false
		_, err := t.pitScroll(searchCtx, params, params.MaxResults, func(batchHits []*querydsl.SearchHit) error {
			lines, ferr := t.formatHits(searchCtx, batchHits)
			if ferr != nil {
				return ferr
			}
			for _, line := range lines {
				sw.Send(line, nil)
			}
			hasResults = true
			return nil
		})
		if err != nil {
			sw.Send("", err)
			return
		}
		if !hasResults {
			logrus.Debug("No result found")
			sw.Send("No result found", nil)
		}
	}()

	return sr, nil
}
