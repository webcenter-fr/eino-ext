package opensearch

import (
	"context"
	"testing"

	"github.com/disaster37/opensearch/v3"
	"github.com/stretchr/testify/assert"
)

func TestFieldAsString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name:     "plain string",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "slice with string",
			input:    []interface{}{"log line"},
			expected: "log line",
		},
		{
			name:     "empty slice",
			input:    []interface{}{},
			expected: "",
		},
		{
			name:     "nil input",
			input:    nil,
			expected: "",
		},
		{
			name:     "slice with non-string first element",
			input:    []interface{}{123},
			expected: "",
		},
		{
			name:     "slice with multiple strings",
			input:    []interface{}{"first", "second"},
			expected: "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fieldAsString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name          string
		params        *OpensearchLogKubernetesParams
		expectedMusts int
		checkTerms    map[string]string
	}{
		{
			name: "podName only",
			params: &OpensearchLogKubernetesParams{
				Cluster:     "prod-cluster",
				Namespace:   "default",
				PodName:     "my-pod-123",
				From:        "now-1h",
				To:          "now",
				LuceneQuery: "error",
			},
			expectedMusts: 5,
			checkTerms: map[string]string{
				"labels.cluster":          "prod-cluster",
				"kubernetes.namespace":    "default",
				"kubernetes.pod.name":     "my-pod-123",
				"kubernetes.container.name": "",
			},
		},
		{
			name: "containerName only",
			params: &OpensearchLogKubernetesParams{
				Cluster:       "staging-cluster",
				Namespace:     "kube-system",
				ContainerName: "nginx",
				From:          "now-24h",
				To:            "now",
				LuceneQuery:   "500",
			},
			expectedMusts: 5,
			checkTerms: map[string]string{
				"labels.cluster":            "staging-cluster",
				"kubernetes.namespace":      "kube-system",
				"kubernetes.pod.name":       "",
				"kubernetes.container.name": "nginx",
			},
		},
		{
			name: "both podName and containerName",
			params: &OpensearchLogKubernetesParams{
				Cluster:       "prod-cluster",
				Namespace:     "default",
				PodName:       "my-pod-456",
				ContainerName: "app",
				From:          "now-12h",
				To:            "now-1h",
				LuceneQuery:   "exception",
			},
			expectedMusts: 6,
			checkTerms: map[string]string{
				"labels.cluster":            "prod-cluster",
				"kubernetes.namespace":      "default",
				"kubernetes.pod.name":       "my-pod-456",
				"kubernetes.container.name": "app",
			},
		},
		{
			name: "neither podName nor containerName",
			params: &OpensearchLogKubernetesParams{
				Cluster:   "prod-cluster",
				Namespace: "default",
				From:      "now-24h",
				To:        "now",
			},
			expectedMusts: 4,
			checkTerms: map[string]string{
				"labels.cluster":            "prod-cluster",
				"kubernetes.namespace":      "default",
				"kubernetes.pod.name":       "",
				"kubernetes.container.name": "",
			},
		},
		{
			name: "default lucene query when empty",
			params: &OpensearchLogKubernetesParams{
				Cluster:   "prod-cluster",
				Namespace: "default",
				PodName:   "my-pod",
				From:      "now-24h",
				To:        "now",
			},
			expectedMusts: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &OpensearchLogKubernetesTool{}
			_ = ctx

			query := tool.buildQuery(tt.params)
			assert.NotNil(t, query)

			src, err := query.Source()
			assert.NoError(t, err)

			srcMap, ok := src.(map[string]interface{})
			assert.True(t, ok, "source should be a map")

			boolClause, ok := srcMap["bool"].(map[string]interface{})
			assert.True(t, ok, "bool clause should be present")

			mustClauses, ok := boolClause["must"].([]interface{})
			assert.True(t, ok, "must should be a slice")

			assert.Len(t, mustClauses, tt.expectedMusts)

			for _, clause := range mustClauses {
				clauseMap, ok := clause.(map[string]interface{})
				if !ok {
					continue
				}

				if termVal, ok := clauseMap["term"]; ok {
					termMap, ok := termVal.(map[string]interface{})
					if !ok {
						continue
					}
					for field, value := range termMap {
						if expectedVal, exists := tt.checkTerms[field]; exists {
							assert.Equal(t, expectedVal, value)
						}
					}
				}

				if _, ok := clauseMap["range"]; ok {
					assert.True(t, ok, "range query should be present for @timestamp")
				}

				if qsVal, ok := clauseMap["query_string"]; ok {
					qsMap, ok := qsVal.(map[string]interface{})
					assert.True(t, ok)
					if tt.params.LuceneQuery == "" {
						assert.Equal(t, "*", qsMap["query"])
					} else {
						assert.Equal(t, tt.params.LuceneQuery, qsMap["query"])
					}
					assert.Equal(t, true, qsMap["analyze_wildcard"])
				}
			}
		})
	}
}

func TestBuildQueryReturnsValidQuery(t *testing.T) {
	t.Parallel()

	params := &OpensearchLogKubernetesParams{
		Cluster:   "test",
		Namespace: "default",
		PodName:   "test-pod",
		From:      "now-24h",
		To:        "now",
	}

	tool := &OpensearchLogKubernetesTool{}
	query := tool.buildQuery(params)
	assert.NotNil(t, query)

	_, err := query.Source()
	assert.NoError(t, err)

	_, ok := query.(*opensearch.BoolQuery)
	assert.True(t, ok)
}
