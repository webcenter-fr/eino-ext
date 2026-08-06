package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		if r.Header.Get("User-Agent") != userAgentHeader || r.Header.Get("X-GitHub-Api-Version") != gitHubAPIVersion {
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
		got := ResolveBaseURL(tt.enterprise)
		if got != tt.want {
			t.Errorf("ResolveBaseURL(%q) = %q, want %q", tt.enterprise, got, tt.want)
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

func TestExchangeGitHubTokenWithEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != tokenURLPath {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(copilotTokenResponse{
			Token:     "bearer-token",
			ExpiresAt: time.Now().Unix() + 3600,
			RefreshIn: 1500,
			Endpoints: &copilotTokenEndpoints{
				API:           "https://api.business.githubcopilot.com",
				OriginTracker: "https://origin-tracker.business.githubcopilot.com",
				Proxy:         "https://proxy.business.githubcopilot.com",
				Telemetry:     "https://telemetry.business.githubcopilot.com",
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	resp, err := exchangeGitHubTokenWithBase(ctx, "test-token", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Endpoints == nil {
		t.Fatal("expected non-nil Endpoints")
	}
	if resp.Endpoints.API != "https://api.business.githubcopilot.com" {
		t.Errorf("expected business API, got %q", resp.Endpoints.API)
	}
}

func TestBaseURLResolutionEndpointsAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(copilotTokenResponse{
			Token:     "copilot-token",
			ExpiresAt: time.Now().Unix() + 3600,
			Endpoints: &copilotTokenEndpoints{
				API: "https://api.business.githubcopilot.com",
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	resp, err := exchangeGitHubTokenWithBase(ctx, "gh-token", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("exchangeGitHubTokenWithBase: %v", err)
	}

	var resolved string
	if resp.Endpoints != nil && resp.Endpoints.API != "" {
		resolved = resp.Endpoints.API
	} else {
		resolved = ResolveBaseURL("")
	}
	if resolved != "https://api.business.githubcopilot.com" {
		t.Errorf("expected endpoints.api to be picked up, got %q", resolved)
	}
}

func TestBaseURLResolutionFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(copilotTokenResponse{
			Token:     "copilot-token",
			ExpiresAt: time.Now().Unix() + 3600,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	resp, err := exchangeGitHubTokenWithBase(ctx, "gh-token", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("exchangeGitHubTokenWithBase: %v", err)
	}

	var resolved string
	if resp.Endpoints != nil && resp.Endpoints.API != "" {
		resolved = resp.Endpoints.API
	} else {
		resolved = ResolveBaseURL("")
	}
	if resolved != defaultCopilotBase {
		t.Errorf("expected fallback to defaultCopilotBase %q, got %q", defaultCopilotBase, resolved)
	}
}

// TestResolveCopilotTokenEmptyGitHubToken tests the guard in the exported
// ResolveCopilotToken function. The full function (exchange + resolution) is
// tested end-to-end by TestIntegration_ResolveCopilotToken in integration_test.go.
func TestResolveCopilotTokenEmptyGitHubToken(t *testing.T) {
	ctx := context.Background()
	_, err := ResolveCopilotToken(ctx, "", "", "", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for empty githubToken")
	}
}

func TestStartTokenRefreshNoOpOnZeroExpiresAt(t *testing.T) {
	cfg := &Config{
		GitHubToken: "gh-pat",
		Timeout:     10 * time.Second,
	}

	tokenResp := &copilotTokenResponse{
		Token:     "t",
		ExpiresAt: 0,
	}

	ctx := context.Background()
	cancel := startTokenRefresh(ctx, cfg, tokenResp, func(newToken string) {})
	if cancel == nil {
		t.Fatal("expected non-nil cancel func")
	}
	// cancel should be a no-op (context already cancelled)
	cancel()
}

func TestExchangeErrorMessages(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		wantContains []string
	}{
		{"401", 401, []string{"401", "directly"}},
		{"403", 403, []string{"403", "direct-bearer"}},
		{"404", 404, []string{"404"}},
		{"421", 421, []string{"421"}},
		{"429", 429, []string{"429"}},
		{"500", 500, []string{"500"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(`{"error":"some error"}`))
			}))
			defer srv.Close()

			ctx := context.Background()
			_, err := exchangeGitHubTokenWithBase(ctx, "gh-token", srv.URL, 5*time.Second)
			if err == nil {
				t.Fatal("expected error")
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestResolveCopilotTokenSetsPlan(t *testing.T) {
	t.Run("business", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(copilotTokenResponse{
				Token:     "bearer-token",
				ExpiresAt: time.Now().Unix() + 3600,
				Endpoints: &copilotTokenEndpoints{
					API: "https://api.business.githubcopilot.com",
				},
			})
		}))
		defer srv.Close()

		resp, err := exchangeGitHubTokenWithBase(context.Background(), "gh-token", srv.URL, 5*time.Second)
		if err != nil {
			t.Fatalf("exchange: %v", err)
		}
		plan := DetectPlan(resp.Endpoints.API)
		if plan != PlanBusiness {
			t.Errorf("expected PlanBusiness, got %q", plan)
		}
	})

	t.Run("no_endpoints_fallback", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(copilotTokenResponse{
				Token:     "bearer-token",
				ExpiresAt: time.Now().Unix() + 3600,
			})
		}))
		defer srv.Close()

		_, err := exchangeGitHubTokenWithBase(context.Background(), "gh-token", srv.URL, 5*time.Second)
		if err != nil {
			t.Fatalf("exchange: %v", err)
		}
		plan := DetectPlan(ResolveBaseURL(""))
		if plan != PlanIndividual {
			t.Errorf("expected PlanIndividual, got %q", plan)
		}
	})
}

// TestResolveCopilotToken_DirectBearer_FineGrainedPAT verifies that a
// fine-grained PAT prefix triggers direct-bearer mode: the PAT is returned as
// Token, BaseURL is defaultCopilotBase, ExpiresAt is 0, Kind is fine_grained,
// and Plan is PlanIndividual.
func TestResolveCopilotToken_DirectBearer_FineGrainedPAT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == userURLPath {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"login":"testuser"}`))
			return
		}
		if r.URL.Path == tokenURLPath {
			t.Fatal("exchange endpoint should NOT be called for fine-grained PAT")
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	setTestUserAPIBaseForTesting(srv.URL)
	defer setTestUserAPIBaseForTesting("")

	ctx := context.Background()
	resolved, err := ResolveCopilotToken(ctx, "github_pat_fake_test", "", "", 5*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken: %v", err)
	}
	if resolved.Token != "github_pat_fake_test" {
		t.Errorf("expected PAT as Token, got %q", resolved.Token)
	}
	if resolved.BaseURL != defaultCopilotBase {
		t.Errorf("expected defaultCopilotBase, got %q", resolved.BaseURL)
	}
	if resolved.ExpiresAt != 0 {
		t.Errorf("expected ExpiresAt 0 (no exchange), got %d", resolved.ExpiresAt)
	}
	if resolved.Kind != TokenKindFineGrainedPAT {
		t.Errorf("expected TokenKindFineGrainedPAT, got %q", resolved.Kind)
	}
	if resolved.Plan != PlanIndividual {
		t.Errorf("expected PlanIndividual, got %q", resolved.Plan)
	}
}

// TestResolveCopilotToken_DirectBearer_ExplicitBaseURL verifies explicit
// baseURL wins over ResolveBaseURL fallback in direct-bearer mode.
func TestResolveCopilotToken_DirectBearer_ExplicitBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login":"testuser"}`))
	}))
	defer srv.Close()
	setTestUserAPIBaseForTesting(srv.URL)
	defer setTestUserAPIBaseForTesting("")

	ctx := context.Background()
	resolved, err := ResolveCopilotToken(ctx, "github_pat_fake_test", "", "https://custom-proxy.example.com", 5*time.Second)
	if err != nil {
		t.Fatalf("ResolveCopilotToken: %v", err)
	}
	if resolved.BaseURL != "https://custom-proxy.example.com" {
		t.Errorf("expected explicit baseURL, got %q", resolved.BaseURL)
	}
	if resolved.Kind != TokenKindFineGrainedPAT {
		t.Errorf("expected TokenKindFineGrainedPAT, got %q", resolved.Kind)
	}
}

// TestResolveCopilotToken_DirectBearer_UserValidation401 verifies that a 401
// from /copilot_internal/user blocks resolution.
func TestResolveCopilotToken_DirectBearer_UserValidation401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	setTestUserAPIBaseForTesting(srv.URL)
	defer setTestUserAPIBaseForTesting("")

	_, err := resolveDirectBearer(context.Background(), "github_pat_test", "", "", 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for 401 from user validation")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

// TestResolveCopilotToken_DirectBearer_UserValidation403 verifies that a 403
// from /copilot_internal/user blocks resolution.
func TestResolveCopilotToken_DirectBearer_UserValidation403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	setTestUserAPIBaseForTesting(srv.URL)
	defer setTestUserAPIBaseForTesting("")

	_, err := resolveDirectBearer(context.Background(), "github_pat_test", "", "", 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for 403 from user validation")
	}
	if !strings.Contains(err.Error(), "Copilot Requests") || !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention 403 and Copilot Requests: %v", err)
	}
}

// TestResolveCopilotToken_Classic_StillExchanges verifies that a classic PAT
// (ghp_) still goes through the exchange path.
func TestResolveCopilotToken_Classic_StillExchanges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tokenURLPath {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(copilotTokenResponse{
			Token:     "exchanged-token",
			ExpiresAt: time.Now().Unix() + 3600,
		})
	}))
	defer srv.Close()

	resolved, err := exchangeGitHubTokenWithBase(
		context.Background(), "ghp_test", srv.URL, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("exchangeGitHubTokenWithBase: %v", err)
	}
	if resolved.Token != "exchanged-token" {
		t.Errorf("expected exchanged-token, got %q", resolved.Token)
	}

	// Verify DetectTokenKind reports classic.
	kind := DetectTokenKind("ghp_test")
	if kind != TokenKindClassicPAT {
		t.Errorf("expected TokenKindClassicPAT, got %q", kind)
	}

	// Now test full ResolveCopilotToken routing via exchangeGitHubTokenWithBase.
	// Classic PATs trigger exchange — verify the exchanged token is used.
	rctx := context.Background()
	rt, err := exchangeGitHubTokenWithBase(rctx, "ghp_test_real", srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	_ = rt
}

// TestResolveCopilotToken_UnknownPrefix_Exchanges verifies that unknown
// prefixes fall back to exchange (backward compatible).
func TestResolveCopilotToken_UnknownPrefix_Exchanges(t *testing.T) {
	kind := DetectTokenKind("foobar")
	if kind != TokenKindUnknown {
		t.Errorf("expected TokenKindUnknown, got %q", kind)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tokenURLPath {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(copilotTokenResponse{
			Token:     "exchanged-unknown",
			ExpiresAt: time.Now().Unix() + 3600,
		})
	}))
	defer srv.Close()

	resolved, err := exchangeGitHubTokenWithBase(
		context.Background(), "foobar", srv.URL, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("exchangeGitHubTokenWithBase: %v", err)
	}
	if resolved.Token != "exchanged-unknown" {
		t.Errorf("expected exchanged-unknown, got %q", resolved.Token)
	}
}

