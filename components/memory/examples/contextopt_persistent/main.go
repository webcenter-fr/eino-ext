// Command contextopt_persistent demonstrates how to combine the cross-request
// session condenser (components/memory/session) with the intra-run context
// optimizer (components/middleware/contextopt) WITHOUT paying the LLM
// summarization cost twice.
//
// Two layers can summarize a conversation:
//
//   - session.Turn.Condense     — cross-request, persists an anchored summary
//     to the memory store when the window exceeds CondenseThreshold.
//   - contextopt (middleware)   — intra-run, may summarize again on overflow
//     during a single adk.Runner.Run.
//
// Naively wired, the SAME history is summarized once by each layer, doubling the
// (expensive) LLM bill. The fix is the three-part invariant that makes the cost
// provably paid AT MOST ONCE per turn:
//
//  1. the SAME TokenCounter in the memory store, session.Config and contextopt.Config;
//  2. WindowBudget <= the middleware's usable window (MaxInputTokens here);
//  3. shared summary markers (automatic via memory.NewSummaryMessage).
//
// This program runs a multi-turn workload and counts LLM summarization calls so
// you can verify the cost is paid at most once per turn. The model is a local
// stub, so no API key is required:
//
//	go run ./components/memory/examples/contextopt_persistent
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/components/memory/file"
	"github.com/webcenter-fr/eino-ext/components/memory/runner"
	"github.com/webcenter-fr/eino-ext/components/memory/session"
	"github.com/webcenter-fr/eino-ext/components/middleware/contextopt"
)

const (
	agentName = "assistant"

	// usableWindow is the middleware's usable context (MaxInputTokens). The
	// session WindowBudget is set to the SAME value (invariant #2) so the
	// post-Condense window can never overflow the middleware on the first model
	// call — the middleware therefore never re-summarizes the condensed history.
	usableWindow = 4_000

	// condenseThreshold must be < usableWindow so cross-request condensation
	// fires BEFORE the window reaches the middleware's overflow point.
	condenseThreshold = 3_000
)

func main() {
	const turns = 4
	perTurn := run(turns)

	var max, total int64
	for _, c := range perTurn {
		total += c
		if c > max {
			max = c
		}
	}

	fmt.Printf("\nTotal summarization LLM calls over %d turns: %d (max %d in any single turn).\n",
		turns, total, max)
	if max > 1 {
		panic(fmt.Sprintf("a turn paid the summarization cost %d times", max))
	}
	fmt.Println("The history was summarized AT MOST ONCE per turn — never twice.")
	fmt.Println("Sharing the counter + summarizer and keeping WindowBudget <= the middleware's")
	fmt.Println("usable window is what keeps the (expensive) summarization cost from doubling.")
}

// run executes `turns` requests against a fresh persistent store and returns the
// number of LLM summarization calls observed per turn.
func run(turns int) []int64 {
	ctx := context.Background()

	// (1) ONE TokenCounter, shared by every layer — the keystone of the guarantee.
	counter := memory.DefaultTokenCounter

	// (1) ONE Summarizer, shared by both layers, instrumented to count LLM calls.
	var calls atomic.Int64
	base := contextopt.NewModelSummarizer(&stubModel{})
	shared := contextopt.SummarizerFunc(
		func(ctx context.Context, history []*schema.Message, previousSummary string) (string, error) {
			calls.Add(1)
			return base.Summarize(ctx, history, previousSummary)
		},
	)

	// Persistent, listable/deletable history on disk (JSONL per conversation).
	dir, err := os.MkdirTemp("", "contextopt-persistent-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	mem, err := file.NewFileMemory(file.FileMemoryConfig{
		Dir:             dir,
		TokenCounter:    counter,      // (1) same counter
		MaxWindowTokens: usableWindow, // GetWindow(0) budget
	})
	if err != nil {
		panic(err)
	}

	sm, err := session.NewSessionManager(session.Config{
		Memory:            mem,
		Summarizer:        shared,            // (1) same summarizer
		CondenseThreshold: condenseThreshold, // < usableWindow
		WindowBudget:      usableWindow,      // (2) <= middleware usable window
		TokenCounter:      counter,           // (1) same counter
	})
	if err != nil {
		panic(err)
	}

	mw, err := contextopt.NewMiddleware(&contextopt.Config{
		MaxInputTokens: usableWindow, // (2) == WindowBudget
		TokenCounter:   counter,      // (1) same counter
		Summarizer:     shared,       // (1) same summarizer; only fires on REAL intra-run overflow
	})
	if err != nil {
		panic(err)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        agentName,
		Description: "demo assistant",
		Model:       &stubModel{},
		// contextopt.Middleware is an adk.ChatModelAgentMiddleware → Handlers.
		Handlers: []adk.ChatModelAgentMiddleware{mw},
	})
	if err != nil {
		panic(err)
	}

	agentRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true})

	const (
		userID = "user-1"
		convID = "conv-1"
	)

	perTurn := make([]int64, 0, turns)
	bigFiller := strings.Repeat("context ", 900) // ~1.8k tokens of padding
	for i := 1; i <= turns; i++ {
		userInput := fmt.Sprintf("Turn %d question. %s", i, bigFiller)
		before := calls.Load()
		condensed, err := serveTurn(ctx, sm, agentRunner, userID, convID, userInput)
		if err != nil {
			panic(err)
		}
		delta := calls.Load() - before
		perTurn = append(perTurn, delta)
		fmt.Printf("turn %d: condensed=%-5v summarization LLM calls this turn = %d\n",
			i, condensed, delta)
	}
	return perTurn
}

// serveTurn runs one request end to end, mirroring how an HTTP/SSE handler would
// wire the two layers. Condensation runs under the REQUEST context, before Run;
// the runner bridge persists the assistant answer under a background context.
// It returns whether cross-request condensation fired this turn.
func serveTurn(
	ctx context.Context,
	sm *session.SessionManager,
	agentRunner *adk.Runner,
	userID, convID, userInput string,
) (bool, error) {
	turn, err := sm.BeginTurn(userID, convID, schema.UserMessage(userInput))
	if err != nil {
		return false, err
	}

	// Cross-request condensation, UNDER THE REQUEST CONTEXT, BEFORE Run.
	// This is the only place the LLM summarization cost is normally paid.
	condensed, err := turn.Condense(ctx)
	if err != nil {
		turn.Discard() // not handed to the bridge yet → release ourselves
		return false, err
	}

	// Window = [last summary + recent] + pending user, bounded by WindowBudget.
	window := turn.Window(0)

	iter := agentRunner.Run(context.Background(), window)

	stream, err := runner.Run(runner.Config{
		Turn:      turn,
		Iterator:  iter,
		Predicate: runner.AgentRole(agentName, schema.Assistant),
		OnError: func(err error) *schema.Message {
			return memory.NewEphemeralMessage(schema.Assistant, "an error occurred")
		},
	})
	if err != nil {
		turn.Discard() // bridge did not start → release ourselves
		return false, err
	}
	// From here the runner bridge OWNS the turn: it calls CommitAssistant or
	// Discard exactly once from its persistence goroutine, and releases the
	// per-session lock when done. We must NOT Discard here — doing so would race
	// the async commit and drop the pending user message. The next BeginTurn for
	// this session blocks on that lock until persistence completes, which
	// serializes turns and makes the persisted history deterministic.
	defer stream.Close()

	// Forward to the "client" (here, just drain it).
	for {
		if _, err := stream.Recv(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return false, err
		}
	}
	return condensed, nil
}

// stubModel is a deterministic, offline chat model so the example runs with no
// API key. It returns a short fixed assistant reply. Both the agent and the
// summarizer use it; the summarizer's call count is what we actually assert on.
type stubModel struct{}

var _ model.BaseChatModel = (*stubModel)(nil)

func (m *stubModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("ok", nil), nil
}

func (m *stubModel) Stream(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{
		schema.AssistantMessage("ok", nil),
	}), nil
}
