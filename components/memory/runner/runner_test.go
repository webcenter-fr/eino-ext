package runner

import (
	"io"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/components/memory/file"
	"github.com/webcenter-fr/eino-ext/components/memory/session"
)

func newTurn(t *testing.T) (*session.SessionManager, *session.Turn) {
	t.Helper()
	mem, err := file.NewFileMemory(file.FileMemoryConfig{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewFileMemory: %v", err)
	}
	sm, err := session.NewSessionManager(session.Config{
		Memory: mem,
	})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	turn, err := sm.BeginTurn("u1", "c1", schema.UserMessage("hello"))
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	return sm, turn
}

// drain reads the whole client stream into a concatenated message (errors kept).
func drain(t *testing.T, sr *schema.StreamReader[*schema.Message]) ([]*schema.Message, []error) {
	t.Helper()
	defer sr.Close()
	var msgs []*schema.Message
	var errs []error
	for {
		m, err := sr.Recv()
		if err == io.EOF {
			return msgs, errs
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		msgs = append(msgs, m)
	}
}

// persistedAfter reopens the conversation to inspect what was committed.
func persistedAfter(t *testing.T, sm *session.SessionManager) []*schema.Message {
	t.Helper()
	turn, err := sm.BeginTurn("u1", "c1", nil)
	if err != nil {
		t.Fatalf("BeginTurn (reopen): %v", err)
	}
	defer turn.Discard()
	return turn.Conversation().GetFullMessages()
}

func streamingEvent(agentName string, chunks ...string) *adk.AgentEvent {
	sr, sw := schema.Pipe[*schema.Message](len(chunks) + 1)
	go func() {
		for _, c := range chunks {
			sw.Send(schema.AssistantMessage(c, nil), nil)
		}
		sw.Close()
	}()
	return &adk.AgentEvent{
		AgentName: agentName,
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				IsStreaming:   true,
				MessageStream: sr,
				Role:          schema.Assistant,
			},
		},
	}
}

func TestRun_StreamingAssistant_ProxyAndPersist(t *testing.T) {
	sm, turn := newTurn(t)

	it, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(streamingEvent("supervisor", "Hello", ", ", "world"))
	gen.Close()

	sr, err := Run(Config{Turn: turn, Iterator: it})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, errs := drain(t, sr)
	if len(errs) != 0 {
		t.Fatalf("unexpected stream errors: %v", errs)
	}
	got := concat(msgs)
	if got != "Hello, world" {
		t.Fatalf("proxied content = %q, want %q", got, "Hello, world")
	}

	// Wait for persistence by reopening the turn (session lock serializes).
	full := persistedAfter(t, sm)
	if len(full) != 2 {
		t.Fatalf("expected user+assistant persisted, got %d: %#v", len(full), full)
	}
	if full[0].Role != schema.User || full[1].Role != schema.Assistant {
		t.Fatalf("unexpected roles: %#v", full)
	}
	if full[1].Content != "Hello, world" {
		t.Fatalf("persisted assistant = %q", full[1].Content)
	}
	if memory.IsIncomplete(full[1]) {
		t.Fatal("assistant must not be marked incomplete")
	}
}

func TestRun_NonAssistantExcluded(t *testing.T) {
	sm, turn := newTurn(t)

	it, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	// A tool-role event must be excluded from stream + persistence.
	gen.Send(&adk.AgentEvent{
		AgentName: "worker",
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Message: schema.ToolMessage("tool output", "id1"),
				Role:    schema.Tool,
			},
		},
	})
	gen.Send(&adk.AgentEvent{
		AgentName: "supervisor",
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Message: schema.AssistantMessage("final answer", nil),
				Role:    schema.Assistant,
			},
		},
	})
	gen.Close()

	sr, err := Run(Config{Turn: turn, Iterator: it})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs, _ := drain(t, sr)
	if concat(msgs) != "final answer" {
		t.Fatalf("expected only assistant streamed, got %q", concat(msgs))
	}

	full := persistedAfter(t, sm)
	if len(full) != 2 || full[1].Content != "final answer" {
		t.Fatalf("expected user+assistant only, got %#v", full)
	}
}

func TestRun_ToolCallsExcludedFromPersistence(t *testing.T) {
	sm, turn := newTurn(t)

	it, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	// Assistant message carrying tool calls: streamed but not persisted.
	gen.Send(&adk.AgentEvent{
		AgentName: "supervisor",
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Message: schema.AssistantMessage("", []schema.ToolCall{{ID: "1"}}),
				Role:    schema.Assistant,
			},
		},
	})
	gen.Close()

	sr, err := Run(Config{Turn: turn, Iterator: it})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drain(t, sr)

	full := persistedAfter(t, sm)
	if len(full) != 0 {
		t.Fatalf("expected nothing persisted (no-dangling-user), got %#v", full)
	}
}

func TestRun_IteratorError_EphemeralAndIncomplete(t *testing.T) {
	sm, turn := newTurn(t)

	it, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Send(&adk.AgentEvent{
		AgentName: "supervisor",
		Output: &adk.AgentOutput{
			MessageOutput: &adk.MessageVariant{
				Message: schema.AssistantMessage("partial", nil),
				Role:    schema.Assistant,
			},
		},
	})
	gen.Send(&adk.AgentEvent{Err: io.ErrUnexpectedEOF})
	gen.Close()

	noticed := false
	sr, err := Run(Config{
		Turn:     turn,
		Iterator: it,
		OnError: func(err error) *schema.Message {
			noticed = true
			return memory.NewEphemeralMessage(schema.Assistant, "an error occurred")
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs, errs := drain(t, sr)
	if !noticed {
		t.Fatal("OnError must be invoked")
	}
	if len(errs) == 0 {
		t.Fatal("expected error forwarded on stream")
	}
	// Ephemeral notice is streamed.
	foundEphemeral := false
	for _, m := range msgs {
		if memory.IsEphemeral(m) {
			foundEphemeral = true
		}
	}
	if !foundEphemeral {
		t.Fatal("ephemeral notice must be streamed to client")
	}

	full := persistedAfter(t, sm)
	if len(full) != 2 {
		t.Fatalf("expected user+assistant persisted, got %#v", full)
	}
	if full[1].Content != "partial" {
		t.Fatalf("persisted assistant = %q", full[1].Content)
	}
	if memory.IsEphemeral(full[1]) {
		t.Fatal("ephemeral notice must not be persisted")
	}
	if !memory.IsIncomplete(full[1]) {
		t.Fatal("assistant must be marked incomplete after iterator error")
	}
}

func TestRun_NoAssistantContent_Discards(t *testing.T) {
	sm, turn := newTurn(t)

	it, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	gen.Close() // no events at all

	sr, err := Run(Config{Turn: turn, Iterator: it})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drain(t, sr)

	full := persistedAfter(t, sm)
	if len(full) != 0 {
		t.Fatalf("expected nothing persisted (Discard, no dangling user), got %#v", full)
	}
}

func TestRun_RequiresTurnAndIterator(t *testing.T) {
	if _, err := Run(Config{}); err == nil {
		t.Fatal("expected validation error when Turn and Iterator are nil")
	}
}

func concat(msgs []*schema.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	out, err := schema.ConcatMessages(msgs)
	if err != nil {
		// fall back to naive join for mixed messages
		s := ""
		for _, m := range msgs {
			s += m.Content
		}
		return s
	}
	return out.Content
}
