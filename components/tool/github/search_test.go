package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goccy/go-json"
)

func TestRepoSearchTool(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/search/") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"total_count":1,"incomplete_results":false,"items":[{"name":"searched-repo","full_name":"testorg/searched-repo","description":"Search result","language":"Go","private":false,"stargazers_count":5,"open_issues_count":0,"default_branch":"main","html_url":"https://github.com/testorg/searched-repo"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	configs := Configs{
		"test": {
			Token:    "test-token",
			CloneDir: "/tmp/test-github-clones",
			BaseURL:  server.URL + "/",
		},
	}

	ctx := context.Background()
	tool, err := NewRepoSearchTool(ctx, configs)
	if err != nil {
		t.Fatalf("NewRepoSearchTool: %v", err)
	}

	_, err = tool.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}

	result, err := tool.InvokableRun(ctx, `{"instance": "test", "query": "language:go"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	var outputs []RepoSearchOutput
	if err := json.Unmarshal([]byte(result), &outputs); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("expected 1 result, got %d", len(outputs))
	}
	if outputs[0].Name != "searched-repo" {
		t.Errorf("expected searched-repo, got %s", outputs[0].Name)
	}

	_, err = tool.InvokableRun(ctx, `{"instance": "invalid-instance", "query": "test"}`)
	if err == nil {
		t.Error("expected error for invalid instance")
	}
}
