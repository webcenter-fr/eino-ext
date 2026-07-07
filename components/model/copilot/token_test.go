package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExchangeGitHubTokenWithBaseSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != tokenURLPath {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "token test-gh-pat" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("User-Agent") != userAgentHeader || r.Header.Get("X-GitHub-Api-Version") != apiVersion {
			t.Error("missing required headers")
		}

		json.NewEncoder(w).Encode(copilotTokenResponse{
			Token:     "copilot-bearer-token",
			ExpiresAt: time.Now().Unix() + 3600,
			RefreshIn: 1500,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	resp, err := exchangeGitHubTokenWithBase(ctx, "test-gh-pat", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Token != "copilot-bearer-token" {
		t.Errorf("expected token 'copilot-bearer-token', got %q", resp.Token)
	}
	if resp.ExpiresAt == 0 {
		t.Error("expected non-zero ExpiresAt")
	}
}

func TestExchangeGitHubTokenWithBaseErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := exchangeGitHubTokenWithBase(ctx, "bad-token", srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestExchangeGitHubTokenWithBaseMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := exchangeGitHubTokenWithBase(ctx, "token", srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestExchangeGitHubTokenWithBaseEmptyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(copilotTokenResponse{
			Token:     "",
			ExpiresAt: time.Now().Unix() + 3600,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := exchangeGitHubTokenWithBase(ctx, "token", srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for empty token in response")
	}
}

func TestStartTokenRefreshSkipWhenNoGitHubToken(t *testing.T) {
	cfg := &Config{
		CopilotToken: "direct-token",
		Timeout:      10 * time.Second,
	}
	cancel := startTokenRefresh(context.Background(), cfg, nil, nil)
	if cancel == nil {
		t.Fatal("expected non-nil cancel func")
	}
	cancel()
}

func TestStartTokenRefreshSkipWhenNilResponse(t *testing.T) {
	cfg := &Config{
		GitHubToken: "gh-pat",
		Timeout:     10 * time.Second,
	}
	cancel := startTokenRefresh(context.Background(), cfg, nil, nil)
	if cancel == nil {
		t.Fatal("expected non-nil cancel func")
	}
	cancel()
}

func TestStartTokenRefreshCancellation(t *testing.T) {
	cfg := &Config{
		GitHubToken: "gh-pat",
		Timeout:     10 * time.Second,
	}

	tokenResp := &copilotTokenResponse{
		Token:     "initial-token",
		ExpiresAt: time.Now().Unix() + 1,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelFn := startTokenRefresh(ctx, cfg, tokenResp, func(newToken string) {})
	if cancelFn == nil {
		t.Fatal("expected non-nil cancel func")
	}
	cancel()
	cancelFn()
	_ = tokenResp
}

func TestResolveBaseURL(t *testing.T) {
	tests := []struct {
		enterprise string
		want       string
	}{
		{"", defaultCopilotBase},
		{"mycompany.com", "https://copilot-api.mycompany.com"},
		{"internal.example.com", "https://copilot-api.internal.example.com"},
	}
	for _, tt := range tests {
		got := resolveBaseURL(tt.enterprise)
		if got != tt.want {
			t.Errorf("resolveBaseURL(%q) = %q, want %q", tt.enterprise, got, tt.want)
		}
	}
}

func TestCopilotLockedToken(t *testing.T) {
	lt := &copilotLockedToken{}

	if got := lt.get(); got != "" {
		t.Errorf("expected empty token, got %q", got)
	}

	lt.set("token1")
	if got := lt.get(); got != "token1" {
		t.Errorf("expected token1, got %q", got)
	}

	lt.set("token2")
	if got := lt.get(); got != "token2" {
		t.Errorf("expected token2, got %q", got)
	}
}
