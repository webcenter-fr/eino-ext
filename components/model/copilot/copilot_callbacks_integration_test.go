package copilot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type spyHandler struct {
	chatModelSeen atomic.Bool
}

func (s *spyHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
	if info != nil && info.Component == components.ComponentOfChatModel {
		s.chatModelSeen.Store(true)
	}
	return ctx
}

func (s *spyHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
	if info != nil && info.Component == components.ComponentOfChatModel {
		s.chatModelSeen.Store(true)
	}
	return ctx
}

func (s *spyHandler) OnError(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context { return ctx }

func (s *spyHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, in *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	in.Close()
	return ctx
}

func (s *spyHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, out *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if info != nil && info.Component == components.ComponentOfChatModel {
		s.chatModelSeen.Store(true)
	}
	out.Close()
	return ctx
}

func TestCopilotModelCallbacksFireThroughComposeChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range []string{
			`data: {"choices":[{"delta":{"content":"hi"}}]}`,
			`data: {"choices":[{"delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
			`data: [DONE]`,
		} {
			fmt.Fprintf(w, "%s\n\n", line)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	cm, err := NewCopilotChatModel(ctx, &Config{
		CopilotToken: "test-token",
		BaseURL:      srv.URL,
		Model:        "gpt-4o",
		Timeout:      10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCopilotChatModel: %v", err)
	}

	spy := &spyHandler{}

	chain := compose.NewChain[[]*schema.Message, *schema.Message]()
	chain.AppendChatModel(cm)
	r, err := chain.Compile(ctx)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	sr, err := r.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}}, compose.WithCallbacks(spy))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()
	for {
		if _, err := sr.Recv(); err != nil {
			break
		}
	}

	if !spy.chatModelSeen.Load() {
		t.Fatal("expected a ComponentOfChatModel callback to fire through the compose chain; " +
			"got none — IsCallbacksEnabled() is likely (re)claiming self-instrumentation it doesn't do")
	}
}
