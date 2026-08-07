package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// cleanupAutoRefresh cancels the auto-model background refresh goroutine if one
// was started, preventing goroutine leaks in tests.
func cleanupAutoRefresh(m *CopilotModel) {
	if m.cancelAutoRefresh != nil {
		m.cancelAutoRefresh()
	}
}

func TestIsAutoModel(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"auto", true},
		{"AUTO", true},
		{" Auto ", true},
		{"auto ", true},
		{"", false},
		{"gpt-4o", false},
		{"automatic", false},
	}
	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.input), func(t *testing.T) {
			if got := IsAutoModel(tt.input); got != tt.want {
				t.Errorf("IsAutoModel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAcquireAutoSessionSuccess(t *testing.T) {
	future := time.Now().Add(1 * time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, sessionPath) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		body, _ := io.ReadAll(r.Body)

		// Verify the raw JSON uses [] (empty array) for model_hints, not null.
		// The Copilot API expects an explicitly empty array to trigger auto-selection.
		if !strings.Contains(string(body), `"model_hints":[]`) {
			http.Error(w, fmt.Sprintf("expected model_hints:[] in body, got %s", redactErrorBody(body)), http.StatusBadRequest)
			return
		}

		var reqBody sessionRequestBody
		if err := json.Unmarshal(body, &reqBody); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if len(reqBody.AutoMode.ModelHints) != 0 {
			http.Error(w, "expected empty model_hints", http.StatusBadRequest)
			return
		}

		_ = json.NewEncoder(w).Encode(sessionResponse{
			SelectedModel:   "gpt-4o",
			SessionToken:    "jwt",
			ExpiresAt:       future,
			AvailableModels: []string{"gpt-4o", "gpt-4o-mini"},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	resp, err := acquireAutoSession(ctx, srv.URL, "test-token", http.DefaultClient, "machine-id")
	if err != nil {
		t.Fatalf("acquireAutoSession: %v", err)
	}
	if resp.SelectedModel != "gpt-4o" {
		t.Errorf("expected selected_model 'gpt-4o', got %q", resp.SelectedModel)
	}
	if resp.SessionToken != "jwt" {
		t.Errorf("expected session_token 'jwt', got %q", resp.SessionToken)
	}
	if resp.ExpiresAt != future {
		t.Errorf("expected expires_at %d, got %d", future, resp.ExpiresAt)
	}
	if len(resp.AvailableModels) != 2 {
		t.Errorf("expected 2 available_models, got %d", len(resp.AvailableModels))
	}
}

func TestAcquireAutoSessionEmptySelectedModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sessionResponse{
			SessionToken: "jwt",
			ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := acquireAutoSession(ctx, srv.URL, "t", http.DefaultClient, "m")
	if err == nil {
		t.Fatal("expected error for empty selected_model")
	}
	if !strings.Contains(err.Error(), "empty selected_model") {
		t.Errorf("expected 'empty selected_model' in error, got %v", err)
	}
}

func TestAcquireAutoSessionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := acquireAutoSession(ctx, srv.URL, "t", http.DefaultClient, "m")
	if err == nil {
		t.Fatal("expected error for 402 response")
	}
	if !strings.Contains(err.Error(), "status 402") {
		t.Errorf("expected 'status 402' in error, got %v", err)
	}
}

func TestEnsureAutoModelResolvesAndCaches(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, sessionPath) {
			atomic.AddInt32(&callCount, 1)
			_ = json.NewEncoder(w).Encode(sessionResponse{
				SelectedModel:   "gpt-4o",
				SessionToken:    "jwt",
				ExpiresAt:       time.Now().Add(1 * time.Hour).Unix(),
				AvailableModels: []string{"gpt-4o"},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("newTestModel: %v", err)
	}
	// Cancel the refresh goroutine started by ensureAutoModel so it doesn't
	// leak and accidentally call the session endpoint again.
	defer cleanupAutoRefresh(m)

	m.cfg.Model = "auto"
	ctx := context.Background()

	selected, err := m.ensureAutoModel(ctx)
	if err != nil {
		t.Fatalf("first ensureAutoModel: %v", err)
	}
	if selected != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got %q", selected)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 session call, got %d", atomic.LoadInt32(&callCount))
	}

	selected, err = m.ensureAutoModel(ctx)
	if err != nil {
		t.Fatalf("second ensureAutoModel: %v", err)
	}
	if selected != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got %q", selected)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 session call (cache hit), got %d", atomic.LoadInt32(&callCount))
	}
}

func TestGenerateAutoModel(t *testing.T) {
	var chatModelField string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, sessionPath) {
			_ = json.NewEncoder(w).Encode(sessionResponse{
				SelectedModel:   "gpt-4o",
				SessionToken:    "jwt",
				ExpiresAt:       time.Now().Add(1 * time.Hour).Unix(),
				AvailableModels: []string{"gpt-4o"},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			var body copilotChatRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			chatModelField = body.Model
			_ = json.NewEncoder(w).Encode(copilotChatResponse{
				ID:    "chat-1",
				Model: body.Model,
				Choices: []copilotChatChoice{{
					Index:   0,
					Message: copilotMessage{Role: "assistant", Content: "Hello from chat"},
				}},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("newTestModel: %v", err)
	}
	defer cleanupAutoRefresh(m)

	m.cfg.Model = "auto"
	ctx := context.Background()

	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content == "" {
		t.Fatal("expected non-empty content")
	}
	if chatModelField != "gpt-4o" {
		t.Errorf("expected chat request model 'gpt-4o', got %q", chatModelField)
	}
}

func TestGenerateAutoModelRoutesToResponses(t *testing.T) {
	var hitResponses bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, sessionPath) {
			_ = json.NewEncoder(w).Encode(sessionResponse{
				SelectedModel:   "gpt-5",
				SessionToken:    "jwt",
				ExpiresAt:       time.Now().Add(1 * time.Hour).Unix(),
				AvailableModels: []string{"gpt-5"},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/responses") {
			hitResponses = true
			_ = json.NewEncoder(w).Encode(responsesResponse{
				ID:    "resp-1",
				Model: "gpt-5",
				Output: []responsesOutputItem{
					{
						Type: "message",
						Role: "assistant",
						ID:   "msg-1",
						Content: []responsesContentPart{
							{Type: "output_text", Text: "Hello from responses"},
						},
					},
				},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("newTestModel: %v", err)
	}
	defer cleanupAutoRefresh(m)

	m.cfg.Model = "auto"
	ctx := context.Background()

	msg, err := m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content == "" {
		t.Fatal("expected non-empty content")
	}
	if !hitResponses {
		t.Error("expected /responses endpoint to be hit")
	}
}

func TestStreamAutoModel(t *testing.T) {
	var chatModelField string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, sessionPath) {
			_ = json.NewEncoder(w).Encode(sessionResponse{
				SelectedModel:   "gpt-4o",
				SessionToken:    "jwt",
				ExpiresAt:       time.Now().Add(1 * time.Hour).Unix(),
				AvailableModels: []string{"gpt-4o"},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			var body copilotChatRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			chatModelField = body.Model

			// Stream SSE response.
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming unsupported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"Hello"}}]}`+"\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, `data: [DONE]`+"\n")
			flusher.Flush()
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("newTestModel: %v", err)
	}
	defer cleanupAutoRefresh(m)

	m.cfg.Model = "auto"
	ctx := context.Background()

	sr, err := m.Stream(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var gotContent string
	for {
		msg, recvErr := sr.Recv()
		if recvErr != nil {
			break
		}
		gotContent += msg.Content
	}
	if gotContent == "" {
		t.Fatal("expected non-empty stream content")
	}
	if chatModelField != "gpt-4o" {
		t.Errorf("expected chat request model 'gpt-4o', got %q", chatModelField)
	}
}

func TestGenerateAutoModelSessionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("newTestModel: %v", err)
	}
	defer cleanupAutoRefresh(m)

	m.cfg.Model = "auto"
	ctx := context.Background()

	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err == nil {
		t.Fatal("expected error for session failure")
	}
	if !strings.Contains(err.Error(), "failed to resolve auto model") {
		t.Errorf("expected 'failed to resolve auto model' in error, got %v", err)
	}
}

func TestResolvedAutoModelAccessor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, sessionPath) {
			_ = json.NewEncoder(w).Encode(sessionResponse{
				SelectedModel:   "gpt-4o",
				SessionToken:    "jwt",
				ExpiresAt:       time.Now().Add(1 * time.Hour).Unix(),
				AvailableModels: []string{"gpt-4o"},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			_ = json.NewEncoder(w).Encode(copilotChatResponse{
				ID:    "chat-1",
				Model: "gpt-4o",
				Choices: []copilotChatChoice{{
					Index:   0,
					Message: copilotMessage{Role: "assistant", Content: "ok"},
				}},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	// Before any call: ResolvedAutoModel returns empty.
	m, err := newTestModel(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("newTestModel: %v", err)
	}
	defer cleanupAutoRefresh(m)

	m.cfg.Model = "auto"
	ctx := context.Background()

	id, ok := m.ResolvedAutoModel()
	if ok || id != "" {
		t.Errorf("before any call: ResolvedAutoModel() = (%q, %v), want (\"\", false)", id, ok)
	}

	// After a successful Generate, ResolvedAutoModel returns the resolved model.
	_, err = m.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "Hello"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	id, ok = m.ResolvedAutoModel()
	if !ok {
		t.Fatal("expected ResolvedAutoModel to return true after successful Generate")
	}
	if id != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got %q", id)
	}
}

func TestEnsureAutoModelConcurrent(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, sessionPath) {
			atomic.AddInt32(&callCount, 1)
			_ = json.NewEncoder(w).Encode(sessionResponse{
				SelectedModel:   "gpt-4o",
				SessionToken:    "jwt",
				ExpiresAt:       time.Now().Add(1 * time.Hour).Unix(),
				AvailableModels: []string{"gpt-4o"},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	m, err := newTestModel(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("newTestModel: %v", err)
	}
	defer cleanupAutoRefresh(m)

	m.cfg.Model = "auto"
	ctx := context.Background()

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			selected, err := m.ensureAutoModel(ctx)
			if err != nil {
				errs[idx] = err
				return
			}
			if selected != "gpt-4o" {
				errs[idx] = fmt.Errorf("expected 'gpt-4o', got %q", selected)
			}
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d: %v", i, e)
		}
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 session call, got %d (double-checked locking failure)", atomic.LoadInt32(&callCount))
	}
}
