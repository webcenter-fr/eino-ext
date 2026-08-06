package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateFineGrainedPAT_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != userURLPath {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("expected Authorization: Bearer <pat>, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"testuser"}`))
	}))
	defer srv.Close()

	login, err := validateFineGrainedPATToBase(context.Background(), "github_pat_test", srv.URL, 5*time.Second, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "testuser" {
		t.Errorf("expected login testuser, got %q", login)
	}
}

func TestValidateFineGrainedPAT_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := validateFineGrainedPATToBase(context.Background(), "github_pat_test", srv.URL, 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(strings.ToLower(err.Error()), "invalid") {
		t.Errorf("error should mention 401 and invalid: %v", err)
	}
}

func TestValidateFineGrainedPAT_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := validateFineGrainedPATToBase(context.Background(), "github_pat_test", srv.URL, 5*time.Second, nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "Copilot Requests") {
		t.Errorf("error should mention 403 and Copilot Requests: %v", err)
	}
}

func TestValidateFineGrainedPAT_500_BestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	login, err := validateFineGrainedPATToBase(context.Background(), "github_pat_test", srv.URL, 5*time.Second, nil)
	if err != nil {
		t.Errorf("expected nil error for 500 (best-effort), got %v", err)
	}
	if login != "" {
		t.Errorf("expected empty login on non-200, got %q", login)
	}
}

func TestValidateFineGrainedPAT_NetworkError_BestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // kill the server

	login, err := validateFineGrainedPATToBase(context.Background(), "github_pat_test", srv.URL, 5*time.Second, nil)
	if err != nil {
		t.Errorf("expected nil error for network error (best-effort), got %v", err)
	}
	if login != "" {
		t.Errorf("expected empty login on network error, got %q", login)
	}
}
