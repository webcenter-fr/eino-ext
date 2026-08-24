package memory

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAgent struct {
	name        string
	description string
}

func (m *mockAgent) Name(ctx context.Context) string        { return m.name }
func (m *mockAgent) Description(ctx context.Context) string { return m.description }
func (m *mockAgent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		gen.Send(adk.EventFromMessage(schema.AssistantMessage("response from mock", nil), nil, schema.Assistant, ""))
		gen.Send(&adk.AgentEvent{Action: adk.NewExitAction()})
		gen.Close()
	}()
	return iter
}

func TestBuildQuery_JoinsLastTwoUserMessages(t *testing.T) {
	agent := &Agent{}
	messages := []*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.UserMessage("first question"),
		schema.AssistantMessage("first answer", nil),
		schema.UserMessage("second question"),
	}
	assert.Equal(t, "first question\nsecond question", agent.buildQuery(messages))
}

func TestBuildQuery_SingleUserMessage(t *testing.T) {
	agent := &Agent{}
	messages := []*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.UserMessage("only question"),
		schema.AssistantMessage("answer", nil),
	}
	assert.Equal(t, "only question", agent.buildQuery(messages))
}

func TestBuildQuery_ThreeUserMessages_KeepsLastTwo(t *testing.T) {
	agent := &Agent{}
	messages := []*schema.Message{
		schema.UserMessage("oldest"),
		schema.AssistantMessage("answer 1", nil),
		schema.UserMessage("middle"),
		schema.AssistantMessage("answer 2", nil),
		schema.UserMessage("latest"),
	}
	// Only the last two (middle + latest) in chronological order.
	assert.Equal(t, "middle\nlatest", agent.buildQuery(messages))
}

func TestBuildQuery_NoUserMessage(t *testing.T) {
	agent := &Agent{}
	assert.Empty(t, agent.buildQuery([]*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.AssistantMessage("answer", nil),
	}))
}

func TestFormatMemories(t *testing.T) {
	agent := &Agent{}
	docs := []*schema.Document{
		{Content: "user prefers Go", MetaData: map[string]any{"category": "preference"}},
		{Content: "project uses PostgreSQL", MetaData: map[string]any{"category": "fact"}},
	}
	msg := agent.formatMemories(docs)
	assert.True(t, IsMemoryContext(msg))
	assert.Contains(t, msg.Content, "[Memory context")
	assert.Contains(t, msg.Content, "preference: user prefers Go")
	assert.Contains(t, msg.Content, "fact: project uses PostgreSQL")
}

func TestInjectContext_WithSystemMessage(t *testing.T) {
	agent := &Agent{}
	ctxMsg := NewMemoryContextMessage("memory content")
	result := agent.injectContext([]*schema.Message{
		schema.SystemMessage("original system prompt"),
		schema.UserMessage("hello"),
	}, ctxMsg)
	assert.Len(t, result, 2)
	assert.Contains(t, result[0].Content, "memory content")
	assert.Contains(t, result[0].Content, "original system prompt")
	assert.Equal(t, "hello", result[1].Content)
}

func TestInjectContext_NoSystemMessage(t *testing.T) {
	agent := &Agent{}
	ctxMsg := NewMemoryContextMessage("memory content")
	result := agent.injectContext([]*schema.Message{schema.UserMessage("hello")}, ctxMsg)
	assert.Len(t, result, 2)
	assert.Equal(t, schema.System, result[0].Role)
	assert.Contains(t, result[0].Content, "memory content")
}

func TestInjectContext_WithSystemPromptPrefix(t *testing.T) {
	agent := &Agent{systemPromptPrefix: "CUSTOM PREFIX"}
	ctxMsg := NewMemoryContextMessage("memory content")
	result := agent.injectContext([]*schema.Message{
		schema.SystemMessage("original system prompt"),
	}, ctxMsg)
	assert.Contains(t, result[0].Content, "memory content")
	assert.Contains(t, result[0].Content, "CUSTOM PREFIX")
	assert.Contains(t, result[0].Content, "original system prompt")
}

func TestNewMemoryAgent(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		InnerAgent:             &mockAgent{name: "test", description: "test agent"},
		AutoExtract:            true,
		MaxMemoriesPerRetrieve: 3,
	}
	agent, err := NewAgent(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, "test", agent.Name(ctx))
	assert.Equal(t, "test agent", agent.Description(ctx))
	assert.True(t, agent.autoExtract)
	assert.Equal(t, 3, agent.maxMemoriesPerRetrieve)
}

func TestNewMemoryAgent_NilInnerAgent(t *testing.T) {
	_, err := NewAgent(context.Background(), Config{})
	assert.Error(t, err)
}

func TestMemoryAgent_SetUserID(t *testing.T) {
	ctx := context.Background()
	agent, err := NewAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
	require.NoError(t, err)
	agent.SetUserID("user-1")
	assert.Equal(t, "user-1", agent.userID)
}

func TestMemoryAgent_SetSessionID(t *testing.T) {
	ctx := context.Background()
	agent, err := NewAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
	require.NoError(t, err)
	agent.SetSessionID("session-1")
	assert.Equal(t, "session-1", agent.sessionID)
}

func TestResolveIdentity_Defaults(t *testing.T) {
	ctx := context.Background()
	agent, err := NewAgent(ctx, Config{
		InnerAgent: &mockAgent{name: "test"},
		UserID:     "default-user",
		SessionID:  "default-session",
	})
	require.NoError(t, err)
	uid, sid := agent.resolveIdentity(ctx)
	assert.Equal(t, "default-user", uid)
	assert.Equal(t, "default-session", sid)
}

func TestResolveIdentity_SettersOverrideConfig(t *testing.T) {
	agent, err := NewAgent(context.Background(), Config{
		InnerAgent: &mockAgent{name: "test"},
		UserID:     "config-user",
	})
	require.NoError(t, err)
	agent.SetUserID("setter-user")
	agent.SetSessionID("setter-session")

	uid, sid := agent.resolveIdentity(context.Background())
	assert.Equal(t, "setter-user", uid)
	assert.Equal(t, "setter-session", sid)
}

// TestResolveIdentity_ContextOverride verifies that context values set via
// adk.AddSessionValue take precedence over agent defaults. This only works
// inside an actual adk.Runner session (runctx initialized by the runner).
// In unit tests we verify the fallback to agent defaults.
func TestResolveIdentity_ContextFallbackToDefaults(t *testing.T) {
	agent, err := NewAgent(context.Background(), Config{
		InnerAgent: &mockAgent{name: "test"},
		UserID:     "default-user",
	})
	require.NoError(t, err)

	// Plain context.Background() has no run session, so GetSessionValue
	// returns nil and defaults are used.
	uid, sid := agent.resolveIdentity(context.Background())
	assert.Equal(t, "default-user", uid)
	assert.Empty(t, sid)
}

func TestResolveIdentity_NoDefaults(t *testing.T) {
	agent, err := NewAgent(context.Background(), Config{
		InnerAgent: &mockAgent{name: "test"},
	})
	require.NoError(t, err)
	uid, sid := agent.resolveIdentity(context.Background())
	assert.Empty(t, uid)
	assert.Empty(t, sid)
}

func TestFilterByUser(t *testing.T) {
	docs := []*schema.Document{
		{Content: "memory from user-a", MetaData: map[string]any{"user_id": "user-a"}},
		{Content: "memory from user-b", MetaData: map[string]any{"user_id": "user-b"}},
		{Content: "shared memory", MetaData: map[string]any{}},
	}

	filtered := filterByUser(docs, "user-a")
	require.Len(t, filtered, 1)
	assert.Equal(t, "memory from user-a", filtered[0].Content)
}

func TestFilterByUser_EmptyUserID(t *testing.T) {
	// When userID is empty, filterByUser is not called. This test verifies
	// the function itself returns empty when filtering with empty userID.
	docs := []*schema.Document{
		{Content: "memory", MetaData: map[string]any{"user_id": "user-a"}},
	}
	assert.Empty(t, filterByUser(docs, ""))
}

func TestMemoryAgent_Run(t *testing.T) {
	ctx := context.Background()
	agent, err := NewAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
	require.NoError(t, err)

	iter := agent.Run(ctx, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("hello")},
	})

	event, ok := iter.Next()
	require.True(t, ok)
	require.NotNil(t, event)
	assert.Nil(t, event.Err)
	require.NotNil(t, event.Output)
	require.NotNil(t, event.Output.MessageOutput)
	assert.Equal(t, "response from mock", event.Output.MessageOutput.Message.Content)

	event, ok = iter.Next()
	assert.True(t, ok)
	assert.True(t, event.Action.Exit)
}

// TestConcatAssistantContent_MultiTurnToolCalls reproduces the error
// "cannot concat ToolCalls with different tool id" reported when an agent
// run produces multiple assistant turns that each carry a tool call at the
// same Index but with different IDs. schema.ConcatMessages (which we used
// previously) groups tool calls by Index and rejects differing IDs, so it
// cannot merge distinct assistant turns. concatAssistantContent only joins
// text and is the correct helper here.
func TestConcatAssistantContent_MultiTurnToolCalls(t *testing.T) {
	idx0 := 0
	turn1 := schema.AssistantMessage("thinking about step 1", []schema.ToolCall{
		{Index: &idx0, ID: "toolu_01FRMyxYeZ2QwVcb4i7y48z3", Type: "function",
			Function: schema.FunctionCall{Name: "search", Arguments: `{"q":"a"}`}},
	})
	turn2 := schema.AssistantMessage("now step 2", []schema.ToolCall{
		{Index: &idx0, ID: "toolu_01GjXXNi4egB6ibEmzGBWEdL", Type: "function",
			Function: schema.FunctionCall{Name: "search", Arguments: `{"q":"b"}`}},
	})
	turn3 := schema.AssistantMessage("final answer", nil)

	msgs := []*schema.Message{turn1, turn2, turn3}

	// Sanity check: the previous approach cannot merge these messages.
	_, err := schema.ConcatMessages(msgs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot concat ToolCalls with different tool id")

	// The new helper joins the textual content of each turn without
	// attempting to fuse tool calls.
	got := concatAssistantContent(msgs)
	assert.Equal(t, "thinking about step 1now step 2final answer", got)
}

func TestConcatAssistantContent_SkipsEmptyAndNil(t *testing.T) {
	msgs := []*schema.Message{
		nil,
		schema.AssistantMessage("", nil),
		schema.AssistantMessage("hello", nil),
		schema.AssistantMessage("", nil),
		schema.AssistantMessage("world", nil),
	}
	assert.Equal(t, "helloworld", concatAssistantContent(msgs))
}

func TestConcatAssistantContent_Empty(t *testing.T) {
	assert.Equal(t, "", concatAssistantContent(nil))
	assert.Equal(t, "", concatAssistantContent([]*schema.Message{}))
}

// TestMonitorRun_MultiTurnToolCalls verifies that monitorRun correctly
// forwards events from a multi-turn tool-calling agent run without the
// "cannot concat ToolCalls with different tool id" error that occurred
// when schema.ConcatMessages was (mis)used to merge distinct assistant
// turns. Each turn carries a tool call at index 0 with a different ID,
// and all events must reach the output iterator in order.
func TestMonitorRun_MultiTurnToolCalls(t *testing.T) {
	ctx := context.Background()

	idx0 := 0
	toolCall1 := schema.ToolCall{Index: &idx0, ID: "call-aaa", Type: "function",
		Function: schema.FunctionCall{Name: "search", Arguments: `{"q":"a"}`}}
	toolCall2 := schema.ToolCall{Index: &idx0, ID: "call-bbb", Type: "function",
		Function: schema.FunctionCall{Name: "search", Arguments: `{"q":"b"}`}}

	turns := []*adk.AgentEvent{
		// Turn 1: assistant emits a tool call.
		adk.EventFromMessage(
			schema.AssistantMessage("turn 1", []schema.ToolCall{toolCall1}),
			nil, schema.Assistant, ""),
		// Tool result (role=Tool) – forwarded but not collected for extraction.
		adk.EventFromMessage(
			schema.ToolMessage("result 1", "call-aaa"),
			nil, schema.Tool, ""),
		// Turn 2: assistant emits another tool call at same index, different ID.
		adk.EventFromMessage(
			schema.AssistantMessage("turn 2", []schema.ToolCall{toolCall2}),
			nil, schema.Assistant, ""),
		// Tool result.
		adk.EventFromMessage(
			schema.ToolMessage("result 2", "call-bbb"),
			nil, schema.Tool, ""),
		// Turn 3: final assistant answer (no tool calls).
		adk.EventFromMessage(
			schema.AssistantMessage("final answer", nil),
			nil, schema.Assistant, ""),
		// Exit.
		{Action: adk.NewExitAction()},
	}

	inner := &sequenceAgent{events: turns}

	agent, err := NewAgent(ctx, Config{InnerAgent: inner})
	require.NoError(t, err)

	iter := agent.Run(ctx, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("hello")},
	})

	var received []*adk.AgentEvent
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		received = append(received, ev)
	}

	// All events forwarded, none dropped.
	require.Len(t, received, len(turns))

	for i, ev := range received {
		require.NotNil(t, ev, "event[%d] is nil", i)
		assert.Nilf(t, ev.Err, "event[%d] has error: %v", i, ev.Err)
	}

	// Confirm assistant messages arrived with their original content intact.
	var assistantTexts []string
	for _, ev := range received {
		mo := ev.Output
		if mo == nil || mo.MessageOutput == nil || mo.MessageOutput.Role != schema.Assistant {
			continue
		}
		if mo.MessageOutput.Message != nil {
			assistantTexts = append(assistantTexts, mo.MessageOutput.Message.Content)
		}
	}
	assert.Equal(t, []string{"turn 1", "turn 2", "final answer"}, assistantTexts)

	assert.True(t, received[len(received)-1].Action.Exit)
}

// sequenceAgent replays a pre-defined list of events in order.
type sequenceAgent struct {
	events []*adk.AgentEvent
}

func (s *sequenceAgent) Name(_ context.Context) string        { return "seq" }
func (s *sequenceAgent) Description(_ context.Context) string { return "seq" }
func (s *sequenceAgent) Run(ctx context.Context, _ *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer gen.Close()
		for _, e := range s.events {
			gen.Send(e)
		}
	}()
	return iter
}

func TestMemoryAgent_EnrichInput_NoStore(t *testing.T) {
	ctx := context.Background()
	agent, err := NewAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
	require.NoError(t, err)

	enriched, userQuery, err := agent.enrichInput(ctx, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("hello")},
	}, "")
	require.NoError(t, err)
	assert.Len(t, enriched.Messages, 1)
	assert.Equal(t, "hello", userQuery)
}

func TestBuildQuery_MaxQueryChars_Truncates(t *testing.T) {
	agent := &Agent{maxQueryChars: 10}
	result := agent.buildQuery([]*schema.Message{
		schema.UserMessage("this is a very long query that exceeds the limit"),
	})
	assert.Equal(t, "this is a ", result)
}

func TestBuildQuery_MaxQueryChars_Zero_NoTruncation(t *testing.T) {
	agent := &Agent{maxQueryChars: 0}
	result := agent.buildQuery([]*schema.Message{
		schema.UserMessage("hello world"),
	})
	assert.Equal(t, "hello world", result)
}

func TestBuildQuery_MaxQueryChars_ShorterThanLimit(t *testing.T) {
	agent := &Agent{maxQueryChars: 100}
	result := agent.buildQuery([]*schema.Message{
		schema.UserMessage("short"),
	})
	assert.Equal(t, "short", result)
}

func TestBuildQuery_MaxQueryChars_TwoMessages(t *testing.T) {
	agent := &Agent{maxQueryChars: 5}
	result := agent.buildQuery([]*schema.Message{
		schema.UserMessage("hello"),
		schema.UserMessage("world"),
	})
	assert.Equal(t, "hello", result)
}

func TestNewAgent_MaxQueryChars(t *testing.T) {
	ctx := context.Background()
	agent, err := NewAgent(ctx, Config{
		InnerAgent:    &mockAgent{name: "test"},
		MaxQueryChars: 512,
	})
	require.NoError(t, err)
	assert.Equal(t, 512, agent.maxQueryChars)
}
