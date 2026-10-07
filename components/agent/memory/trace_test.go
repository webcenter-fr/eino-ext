package memory

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMemoryStore is an in-memory MemoryStore used by the agent/trace tests.
type fakeMemoryStore struct {
	mu      sync.Mutex
	docs    []*schema.Document
	topK    int
	hasTopK bool
}

func (s *fakeMemoryStore) Store(_ context.Context, docs []*schema.Document, _ ...indexer.Option) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = append(s.docs, docs...)
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids, nil
}

func (s *fakeMemoryStore) Retrieve(_ context.Context, _ string, opts ...retriever.Option) ([]*schema.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	common := retriever.GetCommonOptions(&retriever.Options{}, opts...)
	if common.TopK != nil {
		s.topK = *common.TopK
		s.hasTopK = true
	}
	return nil, nil
}

func (s *fakeMemoryStore) Delete(_ context.Context, _ string) error { return nil }

func (s *fakeMemoryStore) DeleteByFilter(_ context.Context, _ map[string]any) (int, error) {
	return 0, nil
}

func (s *fakeMemoryStore) List(_ context.Context, _, _ int) ([]*schema.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs, nil
}

func (s *fakeMemoryStore) Count(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.docs), nil
}

// capturingModel is a model.BaseChatModel stub that records the prompts it
// receives and the ctx error observed at Generate time.
type capturingModel struct {
	mu       sync.Mutex
	contents []string
	response string
	ctxErr   error
}

func (m *capturingModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctxErr = ctx.Err()
	for _, msg := range input {
		m.contents = append(m.contents, msg.Content)
	}
	return schema.AssistantMessage(m.response, nil), nil
}

func (m *capturingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (m *capturingModel) lastPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.contents) == 0 {
		return ""
	}
	return m.contents[len(m.contents)-1]
}

func TestRecordAssistantTraceSteps(t *testing.T) {
	msg := schema.AssistantMessage("thinking", []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "attempt_completion", Arguments: `{"result":"final answer"}`}},
	})

	steps := recordAssistantTraceSteps(msg, "supervisor", map[string]string{"attempt_completion": "result"})

	require.Len(t, steps, 3)
	assert.Equal(t, TraceStepAssistantText, steps[0].Kind)
	assert.Equal(t, "thinking", steps[0].Text)
	assert.Equal(t, "supervisor", steps[0].Agent)

	assert.Equal(t, TraceStepToolCall, steps[1].Kind)
	assert.Equal(t, "attempt_completion", steps[1].Name)
	assert.Equal(t, `{"result":"final answer"}`, steps[1].Text)

	assert.Equal(t, TraceStepTerminalAnswer, steps[2].Kind)
	assert.Equal(t, "attempt_completion", steps[2].Name)
	assert.Equal(t, "final answer", steps[2].Text)
}

func TestRecordAssistantTraceSteps_MissingTerminalArg(t *testing.T) {
	msg := schema.AssistantMessage("", []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "attempt_completion", Arguments: `{"other":"x"}`}},
	})

	steps := recordAssistantTraceSteps(msg, "supervisor", map[string]string{"attempt_completion": "result"})

	require.Len(t, steps, 1)
	assert.Equal(t, TraceStepToolCall, steps[0].Kind)
}

func TestRecordAssistantTraceSteps_MalformedArgs(t *testing.T) {
	msg := schema.AssistantMessage("", []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "attempt_completion", Arguments: `{not json`}},
	})

	steps := recordAssistantTraceSteps(msg, "supervisor", map[string]string{"attempt_completion": "result"})

	require.Len(t, steps, 1)
	assert.Equal(t, TraceStepToolCall, steps[0].Kind)
}

func TestRecordAssistantTraceSteps_NonStringTerminalArg(t *testing.T) {
	msg := schema.AssistantMessage("", []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "attempt_completion", Arguments: `{"result":{"a":1}}`}},
	})

	steps := recordAssistantTraceSteps(msg, "supervisor", map[string]string{"attempt_completion": "result"})

	require.Len(t, steps, 2)
	assert.Equal(t, TraceStepTerminalAnswer, steps[1].Kind)
	assert.Equal(t, `{"a":1}`, steps[1].Text)
}

func TestRecordToolResultStep(t *testing.T) {
	step := recordToolResultStep("kubernetes_pod_exec", "stdout", "supervisor")
	assert.Equal(t, TraceStepToolResult, step.Kind)
	assert.Equal(t, "kubernetes_pod_exec", step.Name)
	assert.Equal(t, "stdout", step.Text)
	assert.Equal(t, "supervisor", step.Agent)
}

func TestMonitorRun_TraceCollection(t *testing.T) {
	ctx := context.Background()

	assistantWithTerminal := adk.EventFromMessage(
		schema.AssistantMessage("thinking", []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "attempt_completion", Arguments: `{"result":"final answer"}`}},
		}), nil, schema.Assistant, "")
	assistantWithTerminal.AgentName = "supervisor"

	toolResult := adk.EventFromMessage(
		schema.ToolMessage("stdout from pod", "call-1"), nil, schema.Tool, "kubernetes_pod_exec")
	toolResult.AgentName = "supervisor"

	streamedAssistant := adk.EventFromMessage(
		nil,
		schema.StreamReaderFromArray([]*schema.Message{
			schema.AssistantMessage("streamed ", nil),
			schema.AssistantMessage("assistant text", nil),
		}),
		schema.Assistant, "")
	streamedAssistant.AgentName = "supervisor"

	streamedTool := adk.EventFromMessage(
		nil,
		schema.StreamReaderFromArray([]*schema.Message{
			schema.ToolMessage("streamed tool result", "call-2"),
		}),
		schema.Tool, "kubernetes_pod_exec")
	streamedTool.AgentName = "supervisor"

	subAgent := adk.EventFromMessage(schema.AssistantMessage("doc found", nil), nil, schema.Assistant, "")
	subAgent.AgentName = "documentation_retriever"

	events := []*adk.AgentEvent{
		assistantWithTerminal,
		toolResult,
		streamedAssistant,
		streamedTool,
		subAgent,
		{Action: adk.NewExitAction()},
	}

	mdl := &capturingModel{response: "[]"}
	store := &fakeMemoryStore{}
	agent, err := NewAgent(ctx, Config{
		InnerAgent: &sequenceAgent{events: events},
		Store:      store,
		Model:      mdl,
		Trace: TraceConfig{
			Enabled:       true,
			TerminalTools: map[string]string{"attempt_completion": "result"},
		},
	})
	require.NoError(t, err)

	iter := agent.Run(ctx, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("do it")}})
	for {
		if _, ok := iter.Next(); !ok {
			break
		}
	}

	prompt := mdl.lastPrompt()
	require.NotEmpty(t, prompt)

	ordered := []string{
		"[assistant_text@supervisor] thinking",
		`[tool_call:attempt_completion@supervisor] {"result":"final answer"}`,
		"[terminal_answer:attempt_completion@supervisor] final answer",
		"[tool_result:kubernetes_pod_exec@supervisor] stdout from pod",
		"[assistant_text@supervisor] streamed assistant text",
		"[tool_result:kubernetes_pod_exec@supervisor] streamed tool result",
		"[assistant_text@documentation_retriever] doc found",
	}
	last := -1
	for _, want := range ordered {
		idx := strings.Index(prompt, want)
		require.GreaterOrEqual(t, idx, 0, "missing %q in prompt:\n%s", want, prompt)
		assert.Greater(t, idx, last, "step %q out of order", want)
		last = idx
	}
}

func TestMonitorRun_CancelledContextStillExtracts(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	cancel() // run ctx is already cancelled before the run completes

	events := []*adk.AgentEvent{
		adk.EventFromMessage(schema.AssistantMessage("answer", nil), nil, schema.Assistant, ""),
		{Action: adk.NewExitAction()},
	}

	mdl := &capturingModel{response: "[]"}
	agent, err := NewAgent(context.Background(), Config{
		InnerAgent: &sequenceAgent{events: events},
		Store:      &fakeMemoryStore{},
		Model:      mdl,
		Trace:      TraceConfig{Enabled: true},
	})
	require.NoError(t, err)

	iter := agent.Run(runCtx, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("do it")}})
	for {
		if _, ok := iter.Next(); !ok {
			break
		}
	}

	require.NotEmpty(t, mdl.lastPrompt())
	assert.NoError(t, mdl.ctxErr, "extraction must run on a live (detached) context")
}

func TestRenderTrace_PerStepCapAndHeadTail(t *testing.T) {
	long := strings.Repeat("0123456789", 10) // 100 runes
	trace := RunTrace{{Kind: TraceStepAssistantText, Agent: "a", Text: long}}

	got := RenderTrace(trace, TraceConfig{MaxStepChars: 10, MaxChars: 100000})

	assert.Equal(t, "[assistant_text@a] 0123456…[truncated 90 chars]…789", got)
}

func TestRenderTrace_TrimsLowestPriorityOldestFirst(t *testing.T) {
	trace := RunTrace{
		{Kind: TraceStepAssistantText, Agent: "a", Text: "old assistant"},
		{Kind: TraceStepToolResult, Agent: "a", Name: "t", Text: "result"},
		{Kind: TraceStepToolCall, Agent: "a", Name: "t", Text: "call"},
		{Kind: TraceStepTerminalAnswer, Agent: "a", Name: "t", Text: "answer"},
		{Kind: TraceStepAssistantText, Agent: "a", Text: "new assistant"},
	}

	full := RenderTrace(trace, TraceConfig{MaxStepChars: 1000, MaxChars: 100000})
	oldest := RenderTrace(trace[:1], TraceConfig{MaxStepChars: 1000, MaxChars: 100000})
	budget := len(full) - len(oldest) - 1 // drop the oldest assistant_text + one newline

	got := RenderTrace(trace, TraceConfig{MaxStepChars: 1000, MaxChars: budget})

	assert.NotContains(t, got, "old assistant")
	assert.Contains(t, got, "new assistant")
	assert.Contains(t, got, "result")
	assert.Contains(t, got, "call")
	assert.Contains(t, got, "answer")
}

func TestRenderTrace_NeverDropsTerminalAnswer(t *testing.T) {
	trace := RunTrace{
		{Kind: TraceStepAssistantText, Agent: "a", Text: "lots of prose"},
		{Kind: TraceStepTerminalAnswer, Agent: "a", Name: "t", Text: "the answer"},
		{Kind: TraceStepAssistantText, Agent: "a", Text: "more prose"},
	}

	got := RenderTrace(trace, TraceConfig{MaxStepChars: 1000, MaxChars: 1})

	assert.Contains(t, got, "the answer")
	assert.NotContains(t, got, "lots of prose")
	assert.NotContains(t, got, "more prose")
}

func TestRenderTrace_Redaction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"password", "password=hunter2", "password=<redacted>"},
		{"suffixed password", "DB_PASSWORD=hunter2", "DB_PASSWORD=<redacted>"},
		{"token", "token: abc123", "token: <redacted>"},
		{"secret", "secret=xyz", "secret=<redacted>"},
		{"api_key", "api_key=xyz", "api_key=<redacted>"},
		{"apikey", "apikey=xyz", "apikey=<redacted>"},
		{"bearer", "Authorization: Bearer abc.def", "Authorization: Bearer <redacted>"},
		{"pem", "-----BEGIN PRIVATE KEY-----\nMIIsecret\n-----END PRIVATE KEY-----", "<PEM removed>"},
		{"sasl", "sasl.jaas.config=PlainLoginModule required;", "sasl.jaas.config=<redacted>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderTrace(RunTrace{{Kind: TraceStepToolResult, Agent: "a", Text: tt.input}}, TraceConfig{})
			assert.Contains(t, got, tt.want)
		})
	}
}

func TestRenderTrace_StepFilterDropsTool(t *testing.T) {
	trace := RunTrace{
		{Kind: TraceStepAssistantText, Agent: "a", Text: "prose"},
		{Kind: TraceStepToolResult, Agent: "a", Name: "noisy", Text: "noise"},
	}

	got := RenderTrace(trace, TraceConfig{
		StepFilter: func(s TraceStep) bool { return s.Kind != TraceStepToolResult },
	})

	assert.Contains(t, got, "prose")
	assert.NotContains(t, got, "noise")
}

func TestDefaultTraceRedactor(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"key value", "password=hunter2", "password=<redacted>"},
		{"bearer", "authorization: bearer tok", "authorization: bearer <redacted>"},
		{"pem", "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----", "<PEM removed>"},
		{"sasl", "sasl.jaas.config=abc;", "sasl.jaas.config=<redacted>;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, defaultTraceRedactor(tt.input))
		})
	}
}

func TestRenderTrace_Empty(t *testing.T) {
	assert.Empty(t, RenderTrace(nil, TraceConfig{}))
	assert.Empty(t, RenderTrace(RunTrace{}, TraceConfig{}))
}

func TestTruncateHeadTail_Short(t *testing.T) {
	assert.Equal(t, "short", truncateHeadTail("short", 10))
}

func TestTerminalArgValue(t *testing.T) {
	v, ok := terminalArgValue(`{"result":"x"}`, "result")
	assert.True(t, ok)
	assert.Equal(t, "x", v)

	_, ok = terminalArgValue(`{"result":"x"}`, "missing")
	assert.False(t, ok)

	_, ok = terminalArgValue(`not json`, "result")
	assert.False(t, ok)

	_, ok = terminalArgValue(`{"result":"x"}`, "")
	assert.False(t, ok)
}

func TestToolName(t *testing.T) {
	mo := &adk.MessageVariant{ToolName: "from-variant"}
	assert.Equal(t, "from-variant", toolName(schema.ToolMessage("c", "id"), mo))
	assert.Equal(t, "from-msg", toolName(schema.ToolMessage("c", "id", schema.WithToolName("from-msg")), &adk.MessageVariant{}))
	assert.Empty(t, toolName(nil, nil))
}

// TestMonitorRun_LegacyExtractionInput is the golden test for the opt-in
// guarantee: with Trace.Enabled == false the extractor receives exactly the
// concatenated assistant prose — no tool results, no trace formatting — and
// streamed tool events are forwarded untouched (no stream copy).
func TestMonitorRun_LegacyExtractionInput(t *testing.T) {
	ctx := context.Background()

	streamedTool := adk.EventFromMessage(
		nil,
		schema.StreamReaderFromArray([]*schema.Message{
			schema.ToolMessage("streamed ", "call-2"),
			schema.ToolMessage("tool result", "call-2"),
		}),
		schema.Tool, "search")

	events := []*adk.AgentEvent{
		adk.EventFromMessage(
			schema.AssistantMessage("turn 1", []schema.ToolCall{
				{Function: schema.FunctionCall{Name: "search", Arguments: `{"q":"a"}`}},
			}), nil, schema.Assistant, ""),
		adk.EventFromMessage(schema.ToolMessage("result 1", "call-1"), nil, schema.Tool, "search"),
		streamedTool,
		adk.EventFromMessage(schema.AssistantMessage("final answer", nil), nil, schema.Assistant, ""),
		{Action: adk.NewExitAction()},
	}

	mdl := &capturingModel{response: "[]"}
	agent, err := NewAgent(ctx, Config{
		InnerAgent: &sequenceAgent{events: events},
		Store:      &fakeMemoryStore{},
		Model:      mdl,
		// Trace disabled: legacy path.
	})
	require.NoError(t, err)

	iter := agent.Run(ctx, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("do it")}})

	var streamedContent string
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil || ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mo := ev.Output.MessageOutput
		if mo.IsStreaming && mo.MessageStream != nil {
			s := mo.MessageStream
			defer s.Close()
			for {
				chunk, err := s.Recv()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				streamedContent += chunk.Content
			}
		}
	}

	// The streamed tool event was forwarded untouched and intact.
	assert.Equal(t, "streamed tool result", streamedContent)

	// The extractor received the system prompt plus the user template filled
	// with the concatenated assistant prose only.
	require.Len(t, mdl.contents, 2)
	prompt := mdl.contents[1]
	assert.Contains(t, prompt, "turn 1final answer")
	assert.NotContains(t, prompt, "result 1", "tool results must not reach the legacy extractor")
	assert.NotContains(t, prompt, "streamed tool result", "tool results must not reach the legacy extractor")
	assert.NotContains(t, prompt, "[tool_call:", "trace formatting must not reach the legacy extractor")
}

// TestMonitorRun_TraceEnabled_NoSteps_SkipsExtraction verifies the plan's edge
// case: trace enabled but no collectible steps → extraction is skipped and no
// LLM call is made.
func TestMonitorRun_TraceEnabled_NoSteps_SkipsExtraction(t *testing.T) {
	ctx := context.Background()

	events := []*adk.AgentEvent{
		{Action: adk.NewExitAction()},
	}

	mdl := &capturingModel{response: "[]"}
	agent, err := NewAgent(ctx, Config{
		InnerAgent: &sequenceAgent{events: events},
		Store:      &fakeMemoryStore{},
		Model:      mdl,
		Trace:      TraceConfig{Enabled: true},
	})
	require.NoError(t, err)

	iter := agent.Run(ctx, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("do it")}})
	for {
		if _, ok := iter.Next(); !ok {
			break
		}
	}

	assert.Empty(t, mdl.contents, "no extraction LLM call expected without trace steps")
}

// TestMonitorRun_AsyncExtractDrainedByEndSession verifies that EndSession
// waits for in-flight async extraction (extractWG) before returning, so the
// extraction LLM call has been made by the time EndSession returns.
func TestMonitorRun_AsyncExtractDrainedByEndSession(t *testing.T) {
	ctx := context.Background()

	events := []*adk.AgentEvent{
		adk.EventFromMessage(schema.AssistantMessage("answer", nil), nil, schema.Assistant, ""),
		{Action: adk.NewExitAction()},
	}

	mdl := &capturingModel{response: "[]"}
	agent, err := NewAgent(ctx, Config{
		InnerAgent:   &sequenceAgent{events: events},
		Store:        &fakeMemoryStore{},
		Model:        mdl,
		Trace:        TraceConfig{Enabled: true},
		AsyncExtract: true,
	})
	require.NoError(t, err)

	iter := agent.Run(ctx, &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage("do it")}})
	for {
		if _, ok := iter.Next(); !ok {
			break
		}
	}

	// The async extraction may still be in flight; EndSession must wait for it.
	require.NoError(t, agent.EndSession(ctx))
	assert.NotEmpty(t, mdl.lastPrompt())
}
