package opensearch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/disaster37/opensearch/v4/querydsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestSearchBuildQuery — table-driven tests for buildSearchQuery
// ---------------------------------------------------------------------------

// extractMustClauses returns must clauses from a bool query source, handling
// both single-object and array serialization forms used by querydsl.
func extractMustClauses(boolClause map[string]interface{}) []map[string]interface{} {
	must := boolClause["must"]
	switch v := must.(type) {
	case []interface{}:
		result := make([]map[string]interface{}, len(v))
		for i, item := range v {
			result[i] = item.(map[string]interface{})
		}
		return result
	case map[string]interface{}:
		return []map[string]interface{}{v}
	default:
		return nil
	}
}

func TestSearchBuildQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		params     *SearchParams
		cfg        *SearchConfig
		wantType   string // "match_all", "query_string", "bool"
		checkQuery func(t *testing.T, query querydsl.Query)
	}{
		{
			name: "empty queryString and no time range => match_all",
			params: &SearchParams{
				QueryString: "*",
			},
			cfg:      &SearchConfig{TimeField: "@timestamp"},
			wantType: "match_all",
			checkQuery: func(t *testing.T, query querydsl.Query) {
				_, ok := query.(*querydsl.MatchAllQuery)
				assert.True(t, ok, "expected MatchAllQuery")
			},
		},
		{
			name: "missing queryString defaults to match_all when no time range",
			params: &SearchParams{
				QueryString: "",
			},
			cfg:      &SearchConfig{TimeField: "@timestamp"},
			wantType: "match_all",
			checkQuery: func(t *testing.T, query querydsl.Query) {
				_, ok := query.(*querydsl.MatchAllQuery)
				assert.True(t, ok, "expected MatchAllQuery")
			},
		},
		{
			name: "queryString only => query_string",
			params: &SearchParams{
				QueryString: "level:error AND service:api",
			},
			cfg:      &SearchConfig{TimeField: "@timestamp"},
			wantType: "query_string",
			checkQuery: func(t *testing.T, query querydsl.Query) {
				src, _ := query.Source()
				srcMap := src.(map[string]interface{})
				qsObj := srcMap["query_string"].(map[string]interface{})
				assert.Equal(t, "level:error AND service:api", qsObj["query"])
			},
		},
		{
			name: "time range with from only => bool with range query",
			params: &SearchParams{
				From: "now-1h",
			},
			cfg:      &SearchConfig{TimeField: "@timestamp"},
			wantType: "bool",
			checkQuery: func(t *testing.T, query querydsl.Query) {
				src, _ := query.Source()
				srcMap := src.(map[string]interface{})
				boolClause := srcMap["bool"].(map[string]interface{})
				clauses := extractMustClauses(boolClause)
				require.Len(t, clauses, 1, "should have range clause only")
				rangeObj := clauses[0]["range"].(map[string]interface{})
				tsRange := rangeObj["@timestamp"].(map[string]interface{})
				assert.Equal(t, "now-1h", tsRange["from"])
			},
		},
		{
			name: "time range with to only => bool with range query",
			params: &SearchParams{
				To: "now",
			},
			cfg:      &SearchConfig{TimeField: "@timestamp"},
			wantType: "bool",
			checkQuery: func(t *testing.T, query querydsl.Query) {
				src, _ := query.Source()
				srcMap := src.(map[string]interface{})
				boolClause := srcMap["bool"].(map[string]interface{})
				clauses := extractMustClauses(boolClause)
				require.Len(t, clauses, 1)
				rangeObj := clauses[0]["range"].(map[string]interface{})
				tsRange := rangeObj["@timestamp"].(map[string]interface{})
				assert.Equal(t, "now", tsRange["to"])
			},
		},
		{
			name: "time range + queryString => bool with range and query_string",
			params: &SearchParams{
				QueryString: "status:500",
				From:        "now-24h",
				To:          "now",
			},
			cfg:      &SearchConfig{TimeField: "@timestamp"},
			wantType: "bool",
			checkQuery: func(t *testing.T, query querydsl.Query) {
				src, _ := query.Source()
				srcMap := src.(map[string]interface{})
				boolClause := srcMap["bool"].(map[string]interface{})
				clauses := extractMustClauses(boolClause)
				assert.Len(t, clauses, 2, "should have range and query_string clauses")
				hasRange := false
				hasQueryString := false
				for _, cm := range clauses {
					if _, ok := cm["range"]; ok {
						hasRange = true
					}
					if qs, ok := cm["query_string"]; ok {
						hasQueryString = true
						qsm := qs.(map[string]interface{})
						assert.Equal(t, "status:500", qsm["query"])
					}
				}
				assert.True(t, hasRange, "should have range clause")
				assert.True(t, hasQueryString, "should have query_string clause")
			},
		},
		{
			name: "custom timeField overrides config",
			params: &SearchParams{
				TimeField: "created_at",
				From:      "2024-01-01T00:00:00Z",
				To:        "2024-01-02T00:00:00Z",
			},
			cfg:      &SearchConfig{TimeField: "@timestamp"},
			wantType: "bool",
			checkQuery: func(t *testing.T, query querydsl.Query) {
				src, _ := query.Source()
				srcMap := src.(map[string]interface{})
				boolClause := srcMap["bool"].(map[string]interface{})
				clauses := extractMustClauses(boolClause)
				require.Len(t, clauses, 1)
				rangeObj := clauses[0]["range"].(map[string]interface{})
				_, ok := rangeObj["created_at"]
				assert.True(t, ok, "range should be on 'created_at', not @timestamp")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			query := buildSearchQuery(tt.params, tt.cfg)
			require.NotNil(t, query)

			switch tt.wantType {
			case "match_all":
				_, ok := query.(*querydsl.MatchAllQuery)
				assert.True(t, ok, "expected MatchAllQuery")
			case "query_string":
				_, ok := query.(*querydsl.QueryStringQuery)
				assert.True(t, ok, "expected QueryStringQuery")
			case "bool":
				_, ok := query.(*querydsl.BoolQuery)
				assert.True(t, ok, "expected BoolQuery")
			}

			if tt.checkQuery != nil {
				tt.checkQuery(t, query)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestParseSort
// ---------------------------------------------------------------------------

func TestParseSort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sortSpec  string
		wantField string
		wantAsc   bool
	}{
		{"field:asc", "@timestamp:asc", "@timestamp", true},
		{"field:desc", "@timestamp:desc", "@timestamp", false},
		{"field only (no colon)", "myfield", "myfield", false},
		{"field:ASCENDING (uppercase)", "myfield:ASCENDING", "myfield", true},
		{"empty string", "", "", false},
		{"field with explicit desc", "created_at:desc", "created_at", false},
		{"field with explicit asc", "created_at:asc", "created_at", true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			field, asc := parseSort(tt.sortSpec)
			assert.Equal(t, tt.wantField, field)
			assert.Equal(t, tt.wantAsc, asc)
		})
	}
}

// ---------------------------------------------------------------------------
// TestSearchHitToMap
// ---------------------------------------------------------------------------

func TestSearchHitToMap(t *testing.T) {
	t.Parallel()

	score := 1.5
	ver := int64(42)
	normalHit := &querydsl.SearchHit{
		Source:  []byte(`{"message": "hello", "level": "info"}`),
		Id:      "doc-1",
		Index:   "logs-2024.01",
		Score:   &score,
		Version: &ver,
	}

	hitMap, err := searchHitToMap(normalHit)
	require.NoError(t, err)
	assert.Equal(t, "doc-1", hitMap["_id"])
	assert.Equal(t, "logs-2024.01", hitMap["_index"])
	assert.Equal(t, 1.5, hitMap["_score"])
	assert.Equal(t, &ver, hitMap["_version"])
	assert.Equal(t, "hello", hitMap["message"])
	assert.Equal(t, "info", hitMap["level"])
}

func TestSearchHitToMapNilScore(t *testing.T) {
	t.Parallel()

	ver := int64(1)
	hit := &querydsl.SearchHit{
		Source:  []byte(`{"a": 1}`),
		Id:      "doc-nil-score",
		Index:   "test",
		Score:   nil,
		Version: &ver,
	}

	hitMap, err := searchHitToMap(hit)
	require.NoError(t, err)
	assert.Equal(t, "doc-nil-score", hitMap["_id"])
	assert.Equal(t, "test", hitMap["_index"])
	_, hasScore := hitMap["_score"]
	assert.False(t, hasScore, "_score should not be present when nil")
}

func TestSearchHitToMapNilVersion(t *testing.T) {
	t.Parallel()

	// Version is *int64, nil is valid.
	hit := &querydsl.SearchHit{
		Source:  []byte(`{"x": "y"}`),
		Id:      "doc-zero-ver",
		Index:   "idx",
		Score:   nil,
		Version: nil,
	}

	hitMap, err := searchHitToMap(hit)
	require.NoError(t, err)
	assert.Nil(t, hitMap["_version"])
}

func TestSearchHitToMapInvalidJSON(t *testing.T) {
	t.Parallel()

	hit := &querydsl.SearchHit{
		Source: []byte(`{invalid}`),
		Id:     "bad",
		Index:  "idx",
	}

	_, err := searchHitToMap(hit)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// TestDefaultSearchResultParser
// ---------------------------------------------------------------------------

func TestDefaultSearchResultParser(t *testing.T) {
	t.Parallel()

	hit := map[string]any{
		"message": "hello world",
		"level":   "info",
		"_id":     "abc123",
		"_index":  "logs-2024",
		"_score":  0.95,
	}

	result, err := defaultSearchResultParser(hit)
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	// Verify it's valid compact JSON
	var parsed map[string]any
	err = json.Unmarshal([]byte(result), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "hello world", parsed["message"])
	assert.Equal(t, "abc123", parsed["_id"])
	assert.Equal(t, 0.95, parsed["_score"])
}

// ---------------------------------------------------------------------------
// TestSearchParamsDefaults
// ---------------------------------------------------------------------------

func TestSearchParamsDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		params     *SearchParams
		cfg        *SearchConfig
		wantIndices []string
		wantQueryString string
		wantTimeField   string
		wantSort        string
		wantMaxResults  int
	}{
		{
			name: "all fields empty => fall back to config defaults",
			params: &SearchParams{},
			cfg: &SearchConfig{
				DefaultIndex: "logs-*",
				TimeField:    "@timestamp",
				DefaultSort:  "@timestamp:desc",
				MaxResults:   50,
			},
			wantIndices:     []string{"logs-*"},
			wantQueryString: "*",
			wantTimeField:   "@timestamp",
			wantSort:        "@timestamp:desc",
			wantMaxResults:  50,
		},
		{
			name: "params override config",
			params: &SearchParams{
				Indices:     []string{"app-logs-*"},
				QueryString: "level:error",
				TimeField:   "created_at",
				Sort:        "created_at:asc",
				MaxResults:  200,
			},
			cfg: &SearchConfig{
				DefaultIndex: "logs-*",
				TimeField:    "@timestamp",
				DefaultSort:  "@timestamp:desc",
				MaxResults:   100,
			},
			wantIndices:     []string{"app-logs-*"},
			wantQueryString: "level:error",
			wantTimeField:   "created_at",
			wantSort:        "created_at:asc",
			wantMaxResults:  200,
		},
		{
			name: "partial overrides",
			params: &SearchParams{
				QueryString: "status:500",
			},
			cfg: &SearchConfig{
				DefaultIndex: "logs-*",
				TimeField:    "@timestamp",
				DefaultSort:  "@timestamp:desc",
				MaxResults:   500,
			},
			wantIndices:     []string{"logs-*"},
			wantQueryString: "status:500",
			wantTimeField:   "@timestamp",
			wantSort:        "@timestamp:desc",
			wantMaxResults:  500,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.params.applyDefaults(tt.cfg)
			assert.Equal(t, tt.wantIndices, tt.params.Indices)
			assert.Equal(t, tt.wantQueryString, tt.params.QueryString)
			assert.Equal(t, tt.wantTimeField, tt.params.TimeField)
			assert.Equal(t, tt.wantSort, tt.params.Sort)
			assert.Equal(t, tt.wantMaxResults, tt.params.MaxResults)
		})
	}
}

// ---------------------------------------------------------------------------
// TestSearchConfigDefaults
// ---------------------------------------------------------------------------

func TestSearchConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := &SearchConfig{}
	cfg.applySearchDefaults()

	assert.Equal(t, "@timestamp", cfg.TimeField)
	assert.Equal(t, "@timestamp:desc", cfg.DefaultSort)
	assert.Equal(t, 100, cfg.MaxResults)
}

// ---------------------------------------------------------------------------
// TestBuildSearchQuerySerializable
// ---------------------------------------------------------------------------

func TestBuildSearchQuerySerializable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params *SearchParams
		cfg    *SearchConfig
	}{
		{
			name:   "match_all",
			params: &SearchParams{QueryString: "*"},
			cfg:    &SearchConfig{TimeField: "@timestamp"},
		},
		{
			name:   "query_string",
			params: &SearchParams{QueryString: "error"},
			cfg:    &SearchConfig{TimeField: "@timestamp"},
		},
		{
			name: "bool_with_range_and_query_string",
			params: &SearchParams{
				QueryString: "status:500",
				From:        "now-1h",
				To:          "now",
			},
			cfg: &SearchConfig{TimeField: "@timestamp"},
		},
		{
			name: "bool_with_range_only",
			params: &SearchParams{
				From: "2024-01-01T00:00:00Z",
				To:   "2024-01-02T00:00:00Z",
			},
			cfg: &SearchConfig{TimeField: "@timestamp"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			query := buildSearchQuery(tt.params, tt.cfg)
			src, err := query.Source()
			require.NoError(t, err)
			require.NotNil(t, src)

			// Serialize as JSON to ensure the query is valid for OpenSearch
			_, err = json.Marshal(src)
			require.NoError(t, err)
		})
	}
}

// ---------------------------------------------------------------------------
// TestBuildSearchAfter
// ---------------------------------------------------------------------------

func TestBuildSearchAfter(t *testing.T) {
	t.Parallel()

	tool := &SearchTool{}

	// Hit with the sort field in source
	hit := &querydsl.SearchHit{
		Source: []byte(`{"@timestamp": "2024-01-01T00:00:00Z"}`),
		Id:     "doc-1",
	}
	sortAfter := tool.buildSortAfter(hit, "@timestamp")
	require.NotEmpty(t, sortAfter)
	assert.Equal(t, "2024-01-01T00:00:00Z", sortAfter[0])

	// Hit without the sort field in source — falls back to hit.Id
	hitNoField := &querydsl.SearchHit{
		Source: []byte(`{"message": "hello"}`),
		Id:     "fallback-id",
	}
	sortAfter = tool.buildSortAfter(hitNoField, "@timestamp")
	require.NotEmpty(t, sortAfter)
	assert.Equal(t, "fallback-id", sortAfter[0])

	// Hit with invalid JSON — falls back to nil
	hitBadJSON := &querydsl.SearchHit{
		Source: []byte(`{bad}`),
		Id:     "bad-id",
	}
	sortAfter = tool.buildSortAfter(hitBadJSON, "@timestamp")
	assert.Nil(t, sortAfter)
}

// ---------------------------------------------------------------------------
// TestParseSortEdgeCases
// ---------------------------------------------------------------------------

func TestParseSortEdgeCases(t *testing.T) {
	t.Parallel()

	// Multiple colons — field is everything before first colon, direction is trimmed
	field, asc := parseSort("some:field:with:colons:desc")
	assert.Equal(t, "some", field)
	assert.Equal(t, false, asc)

	// Direction with extra spaces — " asc" does not match "asc" so defaults to desc
	field, asc = parseSort("field: asc")
	assert.Equal(t, "field", field)
	assert.Equal(t, false, asc)
}

// ---------------------------------------------------------------------------
// TestResolveTimeField
// ---------------------------------------------------------------------------

func TestResolveTimeField(t *testing.T) {
	t.Parallel()

	// Params override
	params := &SearchParams{TimeField: "custom_field"}
	assert.Equal(t, "custom_field", params.resolveTimeField("@timestamp"))

	// Falls back to config
	params = &SearchParams{}
	assert.Equal(t, "@timestamp", params.resolveTimeField("@timestamp"))

	// Falls back to global default when config is empty
	params = &SearchParams{}
	assert.Equal(t, defaultSearchTimeField, params.resolveTimeField(""))
}

// ---------------------------------------------------------------------------
// TestNewSearchToolValidation
// ---------------------------------------------------------------------------

func TestNewSearchToolRequiresURLs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := &SearchConfig{
		DefaultIndex: "logs-*",
	}

	// validate.Struct catches missing URLs before the DefaultIndex check
	_, err := NewSearchTool(ctx, cfg)
	require.Error(t, err)
	// The error should mention URLs (from validate.Struct on embedded osclient.Config)
	assert.Contains(t, err.Error(), "URLs")
}

// ---------------------------------------------------------------------------
// TestSearchParamsValidate
// ---------------------------------------------------------------------------

func TestSearchParamsMaxResults(t *testing.T) {
	t.Parallel()

	// MaxResults = 0 is valid (applies default)
	params := &SearchParams{MaxResults: 0}
	params.applyDefaults(&SearchConfig{MaxResults: 100, DefaultIndex: "logs-*", TimeField: "@timestamp", DefaultSort: "@timestamp:desc"})
	assert.Equal(t, 100, params.MaxResults)

	// MaxResults = 5 is custom
	params = &SearchParams{MaxResults: 5}
	params.applyDefaults(&SearchConfig{MaxResults: 100, DefaultIndex: "logs-*", TimeField: "@timestamp", DefaultSort: "@timestamp:desc"})
	assert.Equal(t, 5, params.MaxResults)
}

// ---------------------------------------------------------------------------
// TestQueryTypes ensures querydsl types used in tests compile correctly.
func TestQueryTypes(t *testing.T) {
	t.Parallel()

	// Verify that the query types we use in tests are valid
	mq := querydsl.NewMatchAllQuery()
	assert.NotNil(t, mq)

	qs := querydsl.NewQueryStringQuery("test")
	assert.NotNil(t, qs)

	bq := querydsl.NewBoolQuery()
	assert.NotNil(t, bq)

	rq := querydsl.NewRangeQuery("@timestamp")
	assert.NotNil(t, rq)
}
