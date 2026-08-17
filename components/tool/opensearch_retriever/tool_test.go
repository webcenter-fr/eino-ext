package opensearch_retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestNewToolValidationErrors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "nil config",
			cfg:  nil,
		},
		{
			name: "missing URLs",
			cfg: &Config{
				Index:       "test",
				ToolName:    "test",
				Description: "test",
			},
		},
		{
			name: "missing Index",
			cfg: &Config{
				URLs:        []string{"http://localhost:9200"},
				ToolName:    "test",
				Description: "test",
			},
		},
		{
			name: "missing ToolName",
			cfg: &Config{
				URLs:        []string{"http://localhost:9200"},
				Index:       "test",
				Description: "test",
			},
		},
		{
			name: "missing Description",
			cfg: &Config{
				URLs:     []string{"http://localhost:9200"},
				Index:    "test",
				ToolName: "test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTool(ctx, tt.cfg)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestToolInvoke(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/test-index/_search" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"hits": map[string]any{
				"total": map[string]any{"value": 2},
				"hits": []map[string]any{
					{
						"_id":      "1",
						"_index":   "test-index",
						"_score":   0.95,
						"_version": 1,
						"_source": map[string]any{
							"content": "First document content",
							"title":   "Doc One",
							"url":     "http://example.com/1",
						},
					},
					{
						"_id":      "2",
						"_index":   "test-index",
						"_score":   0.85,
						"_version": 1,
						"_source": map[string]any{
							"content": "Second document content",
							"title":   "Doc Two",
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx := context.Background()
	cfg := &Config{
		URLs:        []string{srv.URL},
		Index:       "test-index",
		ToolName:    "search_docs",
		Description: "Search documentation",
		HeaderFields: []HeaderField{
			{MetaKey: "_id", Label: "Document ID"},
			{MetaKey: "_score", Label: "Score"},
		},
	}

	tool, err := NewTool(ctx, cfg)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}

	result, err := tool.Invoke(ctx, &Query{Query: "test query"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if !strings.Contains(result, "Document ID: 1") {
		t.Errorf("expected Document ID header, got: %s", result)
	}
	if !strings.Contains(result, "First document content") {
		t.Errorf("expected first document content, got: %s", result)
	}
	if !strings.Contains(result, "---") {
		t.Errorf("expected separator between documents, got: %s", result)
	}
}

func TestToolInvokeNoHits(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/empty-index/_search" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"hits": map[string]any{
				"total": map[string]any{"value": 0},
				"hits":  []any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx := context.Background()
	cfg := &Config{
		URLs:        []string{srv.URL},
		Index:       "empty-index",
		ToolName:    "search_docs",
		Description: "Search documentation",
	}

	tool, err := NewTool(ctx, cfg)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}

	result, err := tool.Invoke(ctx, &Query{Query: "no match"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if !strings.Contains(result, "No documents found") {
		t.Errorf("expected no documents message, got: %s", result)
	}
}

func TestToolInvokeDefaultTopK(t *testing.T) {
	var receivedSize int

	handler := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if sz, ok := body["size"].(float64); ok {
			receivedSize = int(sz)
		}
		resp := map[string]any{
			"hits": map[string]any{
				"total": map[string]any{"value": 0},
				"hits":  []any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx := context.Background()
	cfg := &Config{
		URLs:        []string{srv.URL},
		Index:       "test-index",
		ToolName:    "search_docs",
		Description: "Search documentation",
		DefaultTopK: 7,
	}

	tool, err := NewTool(ctx, cfg)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}

	_, err = tool.Invoke(ctx, &Query{Query: "test", Limit: 0})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if receivedSize != 7 {
		t.Errorf("expected size 7 (DefaultTopK), got %d", receivedSize)
	}

	receivedSize = 0
	_, err = tool.Invoke(ctx, &Query{Query: "test", Limit: 3})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if receivedSize != 3 {
		t.Errorf("expected size 3 (from Limit param), got %d", receivedSize)
	}
}

func TestToolInvokeCustomFormatter(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"hits": map[string]any{
				"total": map[string]any{"value": 1},
				"hits": []map[string]any{
					{
						"_id":      "1",
						"_index":   "test-index",
						"_score":   0.95,
						"_version": 1,
						"_source": map[string]any{
							"content": "document content",
							"author":  "test author",
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx := context.Background()
	cfg := &Config{
		URLs:        []string{srv.URL},
		Index:       "test-index",
		ToolName:    "search_docs",
		Description: "Search documentation",
		Formatter:   &customFormatter{},
	}

	tool, err := NewTool(ctx, cfg)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}

	result, err := tool.Invoke(ctx, &Query{Query: "test"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	expected := fmt.Sprintf("CUSTOM: %s", "document content")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

type customFormatter struct{}

func (f *customFormatter) FormatHit(doc *schema.Document) string {
	return fmt.Sprintf("CUSTOM: %s", doc.Content)
}

func TestToolInvokeServerError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx := context.Background()
	cfg := &Config{
		URLs:        []string{srv.URL},
		Index:       "test-index",
		ToolName:    "search_docs",
		Description: "Search documentation",
	}

	tool, err := NewTool(ctx, cfg)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}

	_, err = tool.Invoke(ctx, &Query{Query: "test"})
	if err == nil {
		t.Error("expected error from server error, got nil")
	}
}

func TestHeaderFieldLabelDefaults(t *testing.T) {
	f := NewDefaultHitFormatter([]HeaderField{
		{MetaKey: "_id"},
		{MetaKey: "url", Label: "Source URL"},
	})

	doc := &schema.Document{
		Content: "body content",
		MetaData: map[string]any{
			"_id": "doc-1",
			"url": "http://example.com",
		},
	}

	result := f.FormatHit(doc)
	if !strings.Contains(result, "_id: doc-1") {
		t.Errorf("expected MetaKey as default label, got: %s", result)
	}
	if !strings.Contains(result, "Source URL: http://example.com") {
		t.Errorf("expected custom label, got: %s", result)
	}
	if !strings.Contains(result, "body content") {
		t.Errorf("expected content, got: %s", result)
	}
}

func TestHeaderFieldEmptyValue(t *testing.T) {
	f := NewDefaultHitFormatter([]HeaderField{
		{MetaKey: "_id"},
		{MetaKey: "missing", Label: "Missing Field"},
	})

	doc := &schema.Document{
		Content: "body content",
		MetaData: map[string]any{
			"_id": "doc-1",
		},
	}

	result := f.FormatHit(doc)
	if strings.Contains(result, "Missing Field") {
		t.Errorf("missing fields should not appear, got: %s", result)
	}
	if !strings.Contains(result, "_id: doc-1") {
		t.Errorf("expected _id, got: %s", result)
	}
}

func TestToolInvokeEmptyQuery(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"hits": map[string]any{
				"total": map[string]any{"value": 0},
				"hits":  []any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	ctx := context.Background()
	cfg := &Config{
		URLs:        []string{srv.URL},
		Index:       "test-index",
		ToolName:    "search_docs",
		Description: "Search documentation",
	}

	tool, err := NewTool(ctx, cfg)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}

	_, err = tool.Invoke(ctx, &Query{Query: ""})
	if err == nil {
		t.Error("expected error for empty query, got nil")
	}
}
