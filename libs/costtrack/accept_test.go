package costtrack

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/components/middleware/agentattr"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

// scriptedToolModel implements model.ToolCallingChatModel for tests. It replays
// pre-scripted *schema.Message steps and can simulate tool-call delegations.
type scriptedToolModel struct {
	steps []*schema.Message
	idx   atomic.Int32
}

func (m *scriptedToolModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *scriptedToolModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	i := int(m.idx.Add(1)) - 1
	if i >= len(m.steps) {
		return m.steps[len(m.steps)-1], nil
	}
	return m.steps[i], nil
}

func (m *scriptedToolModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	i := int(m.idx.Add(1)) - 1
	var msg *schema.Message
	if i >= len(m.steps) {
		msg = m.steps[len(m.steps)-1]
	} else {
		msg = m.steps[i]
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

var _ model.ToolCallingChatModel = (*scriptedToolModel)(nil)

func TestAcceptance_SupervisorSubAgents(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer bus.Close()

	holder := new(atomic.Pointer[modelsdev.Catalog])
	c := modelsdev.Load(context.Background(), modelsdev.LoadOptions{
		URL:     "http://127.0.0.1:1",
		Timeout: 100 * time.Millisecond,
	})
	holder.Store(c)

	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve: func(gw string) (string, string, bool) {
			return "anthropic", gw, true
		},
		CatalogHolder: holder,
		Savings: activity.ComplexityAnalyzerConfig{
			HumanHourlyRate: 50,
			BaseTaskTime:    5 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	callbacks.AppendGlobalHandlers(tracker.ActivityHandler())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	supervisorModel := &scriptedToolModel{
		steps: []*schema.Message{
			{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      "sub_agent_a",
							Arguments: "{}",
						},
					},
				},
				ResponseMeta: &schema.ResponseMeta{
					Usage: &schema.TokenUsage{
						PromptTokens:        1000,
						CompletionTokens:    200,
						PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 100},
					},
				},
			},
			{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{
					{
						ID:   "call-2",
						Type: "function",
						Function: schema.FunctionCall{
							Name:      "sub_agent_b",
							Arguments: "{}",
						},
					},
				},
				ResponseMeta: &schema.ResponseMeta{
					Usage: &schema.TokenUsage{
						PromptTokens:        800,
						CompletionTokens:    150,
						PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 50},
					},
				},
			},
			{
				Role:         schema.Assistant,
				Content:      "Done",
				ResponseMeta: &schema.ResponseMeta{
					Usage: &schema.TokenUsage{
						PromptTokens:     500,
						CompletionTokens: 100,
					},
				},
			},
		},
	}

	subAModel := &scriptedToolModel{
		steps: []*schema.Message{
			{Role: schema.Assistant, Content: "sub-A response"},
		},
	}

	subBModel := &scriptedToolModel{
		steps: []*schema.Message{
			{Role: schema.Assistant, Content: "sub-B response"},
		},
	}

	supervisorMW, _ := agentattr.New(&agentattr.Config{AgentName: "supervisor"})
	subAMW, _ := agentattr.New(&agentattr.Config{AgentName: "sub_a"})
	subBMW, _ := agentattr.New(&agentattr.Config{AgentName: "sub_b"})

	subAgentA, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "sub_a",
		Description: "sub agent A",
		Model:       subAModel,
		Handlers:    []adk.ChatModelAgentMiddleware{subAMW},
	})
	if err != nil {
		t.Fatalf("NewChatModelAgent(sub_a): %v", err)
	}

	subAgentB, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "sub_b",
		Description: "sub agent B",
		Model:       subBModel,
		Handlers:    []adk.ChatModelAgentMiddleware{subBMW},
	})
	if err != nil {
		t.Fatalf("NewChatModelAgent(sub_b): %v", err)
	}

	agentToolA := adk.NewAgentTool(ctx, subAgentA)
	agentToolB := adk.NewAgentTool(ctx, subAgentB)

	supervisor, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "supervisor",
		Description: "supervisor agent",
		Model:       supervisorModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{agentToolA, agentToolB},
			},
			EmitInternalEvents: true,
		},
		Handlers: []adk.ChatModelAgentMiddleware{supervisorMW},
	})
	if err != nil {
		t.Fatalf("NewChatModelAgent(supervisor): %v", err)
	}

	sessionID := "accept-session"
	runCtx := activity.WithSession(ctx, sessionID)
	go tracker.Watch(ctx, sessionID)

	time.Sleep(50 * time.Millisecond)

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: supervisor, EnableStreaming: true})
	iter := runner.Run(runCtx, []*schema.Message{schema.UserMessage("do it")})

	for {
		_, more := iter.Next()
		if !more {
			break
		}
	}

	time.Sleep(500 * time.Millisecond)

	snap := tracker.Snapshot(sessionID)
	t.Logf("Snapshot: steps=%d cost=%f compactions=%d models=%d",
		snap.Steps, snap.Totals.Cost, snap.Compactions, len(snap.Models))

	if snap.Steps == 0 {
		t.Fatal("expected at least one step in snapshot")
	}
}
