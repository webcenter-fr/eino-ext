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

func (m *mockAgent) Name(ctx context.Context) string         { return m.name }
func (m *mockAgent) Description(ctx context.Context) string  { return m.description }
func (m *mockAgent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		gen.Send(adk.EventFromMessage(schema.AssistantMessage("response from mock", nil), nil, schema.Assistant, ""))
		gen.Send(&adk.AgentEvent{Action: adk.NewExitAction()})
		gen.Close()
	}()
	return iter
}

func TestBuildQuery_FindsLastUserMessage(t *testing.T) {
	agent := &MemoryAgent{}
	messages := []*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.UserMessage("first question"),
		schema.AssistantMessage("first answer", nil),
		schema.UserMessage("second question"),
	}
	assert.Equal(t, "second question", agent.buildQuery(messages))
}

func TestBuildQuery_NoUserMessage(t *testing.T) {
	agent := &MemoryAgent{}
	assert.Empty(t, agent.buildQuery([]*schema.Message{
		schema.SystemMessage("system prompt"),
		schema.AssistantMessage("answer", nil),
	}))
}

func TestFormatMemories(t *testing.T) {
	agent := &MemoryAgent{}
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
	agent := &MemoryAgent{}
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
	agent := &MemoryAgent{}
	ctxMsg := NewMemoryContextMessage("memory content")
	result := agent.injectContext([]*schema.Message{schema.UserMessage("hello")}, ctxMsg)
	assert.Len(t, result, 2)
	assert.Equal(t, schema.System, result[0].Role)
	assert.Contains(t, result[0].Content, "memory content")
}

func TestInjectContext_WithSystemPromptPrefix(t *testing.T) {
	agent := &MemoryAgent{systemPromptPrefix: "CUSTOM PREFIX"}
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
	agent, err := NewMemoryAgent(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, "test", agent.Name(ctx))
	assert.Equal(t, "test agent", agent.Description(ctx))
	assert.True(t, agent.autoExtract)
	assert.Equal(t, 3, agent.maxMemoriesPerRetrieve)
}

func TestNewMemoryAgent_NilInnerAgent(t *testing.T) {
	_, err := NewMemoryAgent(context.Background(), Config{})
	assert.Error(t, err)
}

func TestMemoryAgent_SetUserID(t *testing.T) {
	ctx := context.Background()
	agent, err := NewMemoryAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
	require.NoError(t, err)
	agent.SetUserID("user-1")
	assert.Equal(t, "user-1", agent.userID)
}

func TestMemoryAgent_SetSessionID(t *testing.T) {
	ctx := context.Background()
	agent, err := NewMemoryAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
	require.NoError(t, err)
	agent.SetSessionID("session-1")
	assert.Equal(t, "session-1", agent.sessionID)
}

func TestResolveIdentity_Defaults(t *testing.T) {
	ctx := context.Background()
	agent, err := NewMemoryAgent(ctx, Config{
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
	agent, err := NewMemoryAgent(context.Background(), Config{
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
	agent, err := NewMemoryAgent(context.Background(), Config{
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
	agent, err := NewMemoryAgent(context.Background(), Config{
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
	agent, err := NewMemoryAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
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

func TestMemoryAgent_EnrichInput_NoStore(t *testing.T) {
	ctx := context.Background()
	agent, err := NewMemoryAgent(ctx, Config{InnerAgent: &mockAgent{name: "test"}})
	require.NoError(t, err)

	enriched, userQuery, err := agent.enrichInput(ctx, &adk.AgentInput{
		Messages: []*schema.Message{schema.UserMessage("hello")},
	}, "")
	require.NoError(t, err)
	assert.Len(t, enriched.Messages, 1)
	assert.Equal(t, "hello", userQuery)
}
