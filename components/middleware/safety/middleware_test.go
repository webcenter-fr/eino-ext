package safety

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	safety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

func TestNewDefaultsToLogSink(t *testing.T) {
	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := m.cfg.AuditSink.(*safety.LogSink); !ok {
		t.Fatalf("expected default LogSink, got %T", m.cfg.AuditSink)
	}
	if !m.writeTools["write_tool"] {
		t.Fatal("expected write_tool in writeTools map")
	}
}

func TestNewNilConfig(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatalf("New(nil): %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil middleware")
	}
}

func TestWrapInvokableToolCallReadOnly(t *testing.T) {
	channelSink := safety.NewChannelSink(10)
	defer channelSink.Close()

	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
		AuditSink:      channelSink,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		return "read result", nil
	}

	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{
		Name:   "read_tool",
		CallID: "call-001",
	})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}

	result, err := wrapped(context.Background(), `{"key":"value"}`)
	if err != nil {
		t.Fatalf("wrapped endpoint: %v", err)
	}
	if result != "read result" {
		t.Fatalf("expected 'read result', got %q", result)
	}

	// Verify audit event.
	event := <-channelSink.Events()
	if event.Phase != safety.PhaseRead {
		t.Fatalf("expected PhaseRead, got %q", event.Phase)
	}
	if event.ToolName != "read_tool" {
		t.Fatalf("expected read_tool, got %q", event.ToolName)
	}
	if event.CallID != "call-001" {
		t.Fatalf("expected call-001, got %q", event.CallID)
	}
	if event.Result != "read result" {
		t.Fatalf("expected 'read result', got %q", event.Result)
	}
	if !event.PolicyPass {
		t.Fatal("expected PolicyPass=true")
	}
}

func TestWrapInvokableToolCallWriteRejected(t *testing.T) {
	channelSink := safety.NewChannelSink(10)
	defer channelSink.Close()

	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
		AuditSink:      channelSink,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		t.Fatal("endpoint should not be called")
		return "", nil
	}

	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{
		Name:   "write_tool",
		CallID: "call-002",
	})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}

	// Call without dryRun or confirmed — should be rejected.
	_, err = wrapped(context.Background(), `{"key":"value"}`)
	if err == nil {
		t.Fatal("expected gate rejection error")
	}
	if !strings.Contains(err.Error(), "SAFETY GATE") {
		t.Fatalf("expected SAFETY GATE error, got: %v", err)
	}

	// Verify audit event.
	event := <-channelSink.Events()
	if event.Phase != safety.PhaseRejected {
		t.Fatalf("expected PhaseRejected, got %q", event.Phase)
	}
}

func TestWrapInvokableToolCallWriteDryRun(t *testing.T) {
	channelSink := safety.NewChannelSink(10)
	defer channelSink.Close()

	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
		AuditSink:      channelSink,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		return "dry run result", nil
	}

	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{
		Name:   "write_tool",
		CallID: "call-003",
	})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}

	result, err := wrapped(context.Background(), `{"dryRun":true}`)
	if err != nil {
		t.Fatalf("wrapped endpoint: %v", err)
	}
	if !strings.Contains(result, "DRY-RUN RESULT") {
		t.Fatalf("expected DRY-RUN RESULT in output, got: %s", result)
	}

	// Verify phase is dry-run.
	event := <-channelSink.Events()
	if event.Phase != safety.PhaseDryRun {
		t.Fatalf("expected PhaseDryRun, got %q", event.Phase)
	}
}

func TestWrapInvokableToolCallWriteConfirmed(t *testing.T) {
	channelSink := safety.NewChannelSink(10)
	defer channelSink.Close()

	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
		AuditSink:      channelSink,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		return "executed result", nil
	}

	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{
		Name:   "write_tool",
		CallID: "call-004",
	})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}

	result, err := wrapped(context.Background(), `{"confirmed":true}`)
	if err != nil {
		t.Fatalf("wrapped endpoint: %v", err)
	}
	if result != "executed result" {
		t.Fatalf("expected 'executed result', got %q", result)
	}

	// Verify phase is execute.
	event := <-channelSink.Events()
	if event.Phase != safety.PhaseExecute {
		t.Fatalf("expected PhaseExecute, got %q", event.Phase)
	}
}

func TestWrapInvokableToolCallPolicyDeny(t *testing.T) {
	channelSink := safety.NewChannelSink(10)
	defer channelSink.Close()

	// CEL policy that blocks any tool named "blocked_tool".
	rules := []safety.CELRule{{
		Name:       "block-tool",
		Expression: `toolName != "blocked_tool"`,
	}}
	pol, err := safety.NewCELPolicy(rules)
	if err != nil {
		t.Fatalf("NewCELPolicy: %v", err)
	}

	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
		AuditSink:      channelSink,
		Policy:         pol,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		t.Fatal("endpoint should not be called")
		return "", nil
	}

	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{
		Name:   "blocked_tool",
		CallID: "call-005",
	})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}

	_, err = wrapped(context.Background(), `{"key":"value"}`)
	if err == nil {
		t.Fatal("expected policy denial error")
	}

	// Verify audit event.
	event := <-channelSink.Events()
	if event.Phase != safety.PhaseRejected {
		t.Fatalf("expected PhaseRejected, got %q", event.Phase)
	}
	if event.PolicyPass {
		t.Fatal("expected PolicyPass=false")
	}
}

func TestWrapInvokableToolCallPolicyScoped(t *testing.T) {
	channelSink := safety.NewChannelSink(10)
	defer channelSink.Close()

	// CEL rule scoped only to "write_tool", not "read_tool".
	rules := []safety.CELRule{{
		Name:       "write-only-rule",
		Expression: `params.test == "ok"`,
		ToolNames:  []string{"write_tool"},
	}}
	pol, err := safety.NewCELPolicy(rules)
	if err != nil {
		t.Fatalf("NewCELPolicy: %v", err)
	}

	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
		AuditSink:      channelSink,
		Policy:         pol,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Read tool should NOT be blocked by scoped policy.
	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		return "read ok", nil
	}
	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{
		Name:   "read_tool",
		CallID: "call-006",
	})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}
	result, err := wrapped(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("read_tool should not be blocked: %v", err)
	}
	if result != "read ok" {
		t.Fatalf("expected 'read ok', got %q", result)
	}
}

func TestWrapStreamableToolCallReadOnly(t *testing.T) {
	channelSink := safety.NewChannelSink(10)
	defer channelSink.Close()

	m, err := New(&Config{
		WriteToolNames: []string{"write_tool"},
		AuditSink:      channelSink,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Create a streaming endpoint that returns a few chunks.
	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
		sr, sw := schema.Pipe[string](3)
		go func() {
			defer sw.Close()
			sw.Send("chunk1", nil)
			sw.Send("chunk2", nil)
		}()
		return sr, nil
	}

	wrapped, err := m.WrapStreamableToolCall(context.Background(), endpoint, &adk.ToolContext{
		Name:   "read_tool",
		CallID: "call-007",
	})
	if err != nil {
		t.Fatalf("WrapStreamableToolCall: %v", err)
	}

	sr, err := wrapped(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("wrapped endpoint: %v", err)
	}
	defer sr.Close()

	var chunks []string
	for {
		chunk, recvErr := sr.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv: %v", recvErr)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 2 || chunks[0] != "chunk1" || chunks[1] != "chunk2" {
		t.Fatalf("expected [chunk1 chunk2], got %v", chunks)
	}

	// Audit event should arrive after stream is consumed.
	event := <-channelSink.Events()
	if event.Phase != safety.PhaseRead {
		t.Fatalf("expected PhaseRead, got %q", event.Phase)
	}
	if event.Result != "chunk1chunk2" {
		t.Fatalf("expected 'chunk1chunk2', got %q", event.Result)
	}
}

func TestWriteToolNamesHelper(t *testing.T) {
	// Just verify the helper doesn't panic.
	mw, err := New(&Config{
		WriteToolNames: []string{"a", "b", "c"},
		AuditSink:      safety.NewChannelSink(1),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !mw.writeTools["a"] || !mw.writeTools["b"] || !mw.writeTools["c"] {
		t.Fatal("expected all write tools in map")
	}
	if mw.writeTools["d"] {
		t.Fatal("expected 'd' not in write tools map")
	}
}
