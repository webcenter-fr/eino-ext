package activity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// collect drains up to n events from ch, returning fewer on timeout.
func collect(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()
	out := make([]Event, 0, n)
	for len(out) < n {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-time.After(time.Second):
			return out
		}
	}
	return out
}

func types(events []Event) []Type {
	ts := make([]Type, len(events))
	for i, e := range events {
		ts[i] = e.Type
	}
	return ts
}

func setup(t *testing.T) (*Handler, Bus, <-chan Event, context.Context) {
	t.Helper()
	b := mustBus(t, Config{})
	t.Cleanup(func() { _ = b.Close() })
	ch, unsub := b.Subscribe(context.Background(), "s", "")
	t.Cleanup(unsub)
	ctx := WithSession(context.Background(), "s")
	return NewHandler(b), b, ch, ctx
}

func TestHandlerChatModelNonStreaming(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "OpenAI"}

	ctx = h.OnStart(ctx, info, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})
	out := &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "hello", ReasoningContent: "think"},
		TokenUsage: &model.TokenUsage{PromptTokens: 3, CompletionTokens: 2},
	}
	h.OnEnd(ctx, info, out)

	got := types(collect(t, ch, 6))
	want := []Type{TypeStepStarted, TypeTextStarted, TypeReasoningStarted, TypeReasoningDelta, TypeReasoningEnded, TypeTextEnded}
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Fatalf("got %v, want prefix %v", got, want)
		}
	}
}

func TestHandlerChatModelStepEndedTokens(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	out := &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "x", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
		TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
	}
	h.OnEnd(ctx, info, out)

	var stepEnd *Event
	for _, e := range collect(t, ch, 2) {
		e := e
		if e.Type == TypeStepEnded {
			stepEnd = &e
		}
	}
	if stepEnd == nil {
		t.Fatal("no step.ended emitted")
	}
	se := stepEnd.Data.(StepEnded)
	if se.Finish != "stop" || se.Tokens.Input != 10 || se.Tokens.Output != 5 {
		t.Fatalf("unexpected step.ended: %+v", se)
	}
}

// stubPricer is a test Pricer that always returns Price, recording the last
// (model, tokens) it was called with.
type stubPricer struct {
	Price     float64
	lastModel string
	lastTok   Tokens
}

func (p *stubPricer) Cost(model string, t Tokens) float64 {
	p.lastModel = model
	p.lastTok = t
	return p.Price
}

func TestHandlerChatModelStepEndedCostWithPricer(t *testing.T) {
	b := mustBus(t, Config{})
	t.Cleanup(func() { _ = b.Close() })
	ch, unsub := b.Subscribe(context.Background(), "s", "")
	t.Cleanup(unsub)
	ctx := WithSession(context.Background(), "s")

	pricer := &stubPricer{Price: 1.5}
	h := NewHandlerWithConfig(b, WithPricer(pricer))
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "gpt-5"}
	out := &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "x", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
		TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
	}
	h.OnEnd(ctx, info, out)

	var stepEnd *Event
	for _, e := range collect(t, ch, 2) {
		e := e
		if e.Type == TypeStepEnded {
			stepEnd = &e
		}
	}
	if stepEnd == nil {
		t.Fatal("no step.ended emitted")
	}
	se := stepEnd.Data.(StepEnded)
	if se.Cost != 1.5 {
		t.Fatalf("Cost = %v, want 1.5", se.Cost)
	}
	if pricer.lastModel != "gpt-5" {
		t.Fatalf("pricer called with model %q, want %q", pricer.lastModel, "gpt-5")
	}
	if pricer.lastTok.Input != 10 || pricer.lastTok.Output != 5 {
		t.Fatalf("pricer called with tokens %+v", pricer.lastTok)
	}
}

func TestHandlerChatModelStepEndedCostWithoutPricer(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	out := &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "x", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
		TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
	}
	h.OnEnd(ctx, info, out)

	var stepEnd *Event
	for _, e := range collect(t, ch, 2) {
		e := e
		if e.Type == TypeStepEnded {
			stepEnd = &e
		}
	}
	if stepEnd == nil {
		t.Fatal("no step.ended emitted")
	}
	se := stepEnd.Data.(StepEnded)
	if se.Cost != 0 {
		t.Fatalf("Cost = %v, want 0 (NewHandler backward compatibility)", se.Cost)
	}
}

func TestHandlerChatModelStepEndedNoUsageNoFallbackStaysZero(t *testing.T) {
	// Without WithTokenCounter configured, a step with no gateway usage keeps
	// reporting all-zero tokens (pre-existing behavior, unaffected).
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	ctx = h.OnStart(ctx, info, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hello there")}})
	out := &model.CallbackOutput{
		Message: &schema.Message{Role: schema.Assistant, Content: "hi", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
		// TokenUsage intentionally nil: gateway did not report usage.
	}
	h.OnEnd(ctx, info, out)

	var stepEnd *Event
	for _, e := range collect(t, ch, 4) {
		e := e
		if e.Type == TypeStepEnded {
			stepEnd = &e
		}
	}
	if stepEnd == nil {
		t.Fatal("no step.ended emitted")
	}
	se := stepEnd.Data.(StepEnded)
	if se.Tokens.Input != 0 || se.Tokens.Output != 0 || se.Estimated {
		t.Fatalf("expected all-zero, non-estimated tokens without a TokenCounter, got %+v", se)
	}
}

func TestHandlerChatModelStepEndedFallbackTokenCounter(t *testing.T) {
	// With WithTokenCounter configured, a step with no gateway usage falls
	// back to the heuristic counter for both input and output tokens, and is
	// flagged Estimated.
	b := mustBus(t, Config{})
	t.Cleanup(func() { _ = b.Close() })
	ch, unsub := b.Subscribe(context.Background(), "s", "")
	t.Cleanup(unsub)
	ctx := WithSession(context.Background(), "s")

	counter := func(msgs []*schema.Message) int {
		total := 0
		for _, m := range msgs {
			total += len(m.Content)
		}
		return total
	}
	h := NewHandlerWithConfig(b, WithTokenCounter(counter))
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	ctx = h.OnStart(ctx, info, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hello there")}}) // 11 chars
	out := &model.CallbackOutput{
		Message: &schema.Message{Role: schema.Assistant, Content: "hi", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}}, // 2 chars
		// TokenUsage intentionally nil: gateway did not report usage.
	}
	h.OnEnd(ctx, info, out)

	var stepEnd *Event
	for _, e := range collect(t, ch, 4) {
		e := e
		if e.Type == TypeStepEnded {
			stepEnd = &e
		}
	}
	if stepEnd == nil {
		t.Fatal("no step.ended emitted")
	}
	se := stepEnd.Data.(StepEnded)
	if se.Tokens.Input != 11 || se.Tokens.Output != 2 {
		t.Fatalf("estimated tokens = %+v, want Input=11 Output=2", se.Tokens)
	}
	if !se.Estimated {
		t.Fatal("expected Estimated=true for fallback-counted tokens")
	}
}

func TestHandlerChatModelStepEndedRealUsageNotEstimated(t *testing.T) {
	// Real gateway usage takes priority over the fallback counter and is
	// never flagged Estimated, even when a TokenCounter is configured.
	b := mustBus(t, Config{})
	t.Cleanup(func() { _ = b.Close() })
	ch, unsub := b.Subscribe(context.Background(), "s", "")
	t.Cleanup(unsub)
	ctx := WithSession(context.Background(), "s")

	h := NewHandlerWithConfig(b, WithTokenCounter(func([]*schema.Message) int { return 999 }))
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	ctx = h.OnStart(ctx, info, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hello there")}})
	out := &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "hi", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
		TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
	}
	h.OnEnd(ctx, info, out)

	var stepEnd *Event
	for _, e := range collect(t, ch, 4) {
		e := e
		if e.Type == TypeStepEnded {
			stepEnd = &e
		}
	}
	if stepEnd == nil {
		t.Fatal("no step.ended emitted")
	}
	se := stepEnd.Data.(StepEnded)
	if se.Tokens.Input != 10 || se.Tokens.Output != 5 || se.Estimated {
		t.Fatalf("real usage should win over fallback counter, got %+v", se)
	}
}


func TestHandlerChatModelStreaming(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}

	chunks := []callbacks.CallbackOutput{
		&model.CallbackOutput{Message: &schema.Message{Content: "he"}},
		&model.CallbackOutput{Message: &schema.Message{Content: "llo"}},
		&model.CallbackOutput{Message: &schema.Message{ReasoningContent: "why"}},
		&model.CallbackOutput{
			Message:    &schema.Message{ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
			TokenUsage: &model.TokenUsage{PromptTokens: 1, CompletionTokens: 2},
		},
	}
	sr := schema.StreamReaderFromArray(chunks)
	h.OnEndWithStreamOutput(ctx, info, sr)

	events := collect(t, ch, 10)
	gotTypes := types(events)
	// expect text deltas, reasoning start/delta/end, text.ended, step.ended.
	counts := map[Type]int{}
	for _, e := range events {
		counts[e.Type]++
	}
	if counts[TypeTextDelta] != 2 {
		t.Fatalf("want 2 text.delta, got %d (%v)", counts[TypeTextDelta], gotTypes)
	}
	if counts[TypeReasoningDelta] != 1 || counts[TypeReasoningStarted] != 1 || counts[TypeReasoningEnded] != 1 {
		t.Fatalf("reasoning events wrong: %v", gotTypes)
	}
	if counts[TypeTextEnded] != 1 || counts[TypeStepEnded] != 1 {
		t.Fatalf("missing ended events: %v", gotTypes)
	}
	// verify accumulated text.
	for _, e := range events {
		if e.Type == TypeTextEnded {
			if te := e.Data.(TextEnded); te.Text != "hello" {
				t.Fatalf("want accumulated 'hello', got %q", te.Text)
			}
		}
	}
}

func TestHandlerToolNonStreaming(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfTool, Name: "search"}

	ctx = h.OnStart(ctx, info, &tool.CallbackInput{ArgumentsInJSON: `{"q":"go"}`})
	h.OnEnd(ctx, info, &tool.CallbackOutput{Response: "result"})

	events := collect(t, ch, 4)
	gt := types(events)
	want := []Type{TypeToolInputStarted, TypeToolInputEnded, TypeToolCalled, TypeToolSuccess}
	for i, w := range want {
		if i >= len(gt) || gt[i] != w {
			t.Fatalf("got %v want %v", gt, want)
		}
	}
	// callID consistency.
	var callID string
	for _, e := range events {
		switch d := e.Data.(type) {
		case ToolInputStarted:
			callID = d.CallID
		case ToolSuccess:
			if d.CallID != callID || d.Content != "result" {
				t.Fatalf("tool.success mismatch: %+v (callID %s)", d, callID)
			}
		}
	}
}

func TestHandlerToolStreamingInput(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfTool, Name: "search"}

	chunks := []callbacks.CallbackInput{
		&tool.CallbackInput{ArgumentsInJSON: `{"q":`},
		&tool.CallbackInput{ArgumentsInJSON: `"go"}`},
	}
	sr := schema.StreamReaderFromArray(chunks)
	h.OnStartWithStreamInput(ctx, info, sr)

	events := collect(t, ch, 5)
	counts := map[Type]int{}
	for _, e := range events {
		counts[e.Type]++
	}
	if counts[TypeToolInputStarted] != 1 || counts[TypeToolInputDelta] != 2 || counts[TypeToolInputEnded] != 1 || counts[TypeToolCalled] != 1 {
		t.Fatalf("unexpected tool input stream events: %v", types(events))
	}
	for _, e := range events {
		if e.Type == TypeToolInputEnded {
			if d := e.Data.(ToolInputEnded); d.Text != `{"q":"go"}` {
				t.Fatalf("accumulated args wrong: %q", d.Text)
			}
		}
	}
}

func TestHandlerToolError(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfTool, Name: "search"}
	ctx = h.OnStart(ctx, info, &tool.CallbackInput{ArgumentsInJSON: "{}"})
	h.OnError(ctx, info, errors.New("boom"))

	var failed *Event
	for _, e := range collect(t, ch, 4) {
		e := e
		if e.Type == TypeToolFailed {
			failed = &e
		}
	}
	if failed == nil {
		t.Fatal("no tool.failed")
	}
	if d := failed.Data.(ToolFailed); d.Error.Message != "boom" {
		t.Fatalf("bad error payload: %+v", d)
	}
}

func TestHandlerChatModelError(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}
	h.OnError(ctx, info, errors.New("kaboom"))
	e := collect(t, ch, 1)
	if len(e) != 1 || e[0].Type != TypeStepFailed {
		t.Fatalf("want step.failed, got %v", types(e))
	}
}

func TestHandlerStreamMidErrorClosesStartedBlocks(t *testing.T) {
	h, _, ch, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}

	sr, sw := schema.Pipe[callbacks.CallbackOutput](4)
	go func() {
		sw.Send(&model.CallbackOutput{Message: &schema.Message{ReasoningContent: "th"}}, nil)
		sw.Send(&model.CallbackOutput{Message: &schema.Message{Content: "he"}}, nil)
		sw.Send(nil, errors.New("boom"))
		sw.Close()
	}()
	h.OnEndWithStreamOutput(ctx, info, sr)

	counts := map[Type]int{}
	for _, e := range collect(t, ch, 8) {
		counts[e.Type]++
	}
	// started blocks must be closed before the failure so consumers don't see
	// dangling started events.
	if counts[TypeReasoningEnded] != 1 {
		t.Fatalf("want reasoning.ended after mid-stream error, got %v", counts)
	}
	if counts[TypeTextEnded] != 1 {
		t.Fatalf("want text.ended after mid-stream error, got %v", counts)
	}
	if counts[TypeStepFailed] != 1 {
		t.Fatalf("want step.failed, got %v", counts)
	}
}

func TestHandlerNeededStreamSkippedWithoutSubscribers(t *testing.T) {
	b := mustBus(t, Config{})
	defer func() { _ = b.Close() }()
	h := NewHandler(b)
	ctx := WithSession(context.Background(), "lonely")
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel}

	if h.Needed(ctx, info, callbacks.TimingOnEndWithStreamOutput) {
		t.Fatal("stream timing should be skipped when no subscribers")
	}
	if !h.Needed(ctx, info, callbacks.TimingOnStart) {
		t.Fatal("coarse timing should always be needed")
	}

	_, unsub := b.Subscribe(context.Background(), "lonely", "")
	defer unsub()
	if !h.Needed(ctx, info, callbacks.TimingOnEndWithStreamOutput) {
		t.Fatal("stream timing should be needed once subscribed")
	}
}

func TestHandlerStreamReadersClosed(t *testing.T) {
	// Non-target components must still close the copied reader (leak guard).
	h, _, _, ctx := setup(t)
	info := &callbacks.RunInfo{Component: components.ComponentOfEmbedding}

	sr := schema.StreamReaderFromArray([]callbacks.CallbackOutput{})
	h.OnEndWithStreamOutput(ctx, info, sr)
	sr2 := schema.StreamReaderFromArray([]callbacks.CallbackInput{})
	h.OnStartWithStreamInput(ctx, info, sr2)
	// No assertion beyond not panicking / not hanging; race detector covers leaks.
}

func TestHandlerAgentAttribution(t *testing.T) {
	h, _, ch, ctx := setup(t)
	ctx = WithAgent(ctx, "supervisor")
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "OpenAI"}

	h.OnStart(ctx, info, &model.CallbackInput{})

	events := collect(t, ch, 3)
	if len(events) < 3 {
		t.Fatalf("want at least 3 events, got %v", types(events))
	}
	// First event must be agent.switched, emitted before the first step.started.
	if events[0].Type != TypeAgentSwitched {
		t.Fatalf("want agent.switched first, got %v", types(events))
	}
	if as := events[0].Data.(AgentSwitched); as.Agent != "supervisor" {
		t.Fatalf("agent.switched payload = %+v", as)
	}
	// Every event carries the agent on the envelope.
	for _, e := range events {
		if e.Agent != "supervisor" {
			t.Fatalf("event %s missing agent attribution: %q", e.Type, e.Agent)
		}
	}
	// step.started also carries the agent in its payload.
	for _, e := range events {
		if e.Type == TypeStepStarted {
			if ss := e.Data.(StepStarted); ss.Agent != "supervisor" {
				t.Fatalf("step.started payload agent = %q", ss.Agent)
			}
		}
	}
}

func TestHandlerAgentSwitchedOncePerAgent(t *testing.T) {
	h, _, ch, ctx := setup(t)
	infoModel := &callbacks.RunInfo{Component: components.ComponentOfChatModel}

	sup := WithAgent(ctx, "supervisor")
	sub := WithAgent(ctx, "researcher")

	h.OnError(sup, infoModel, errors.New("a")) // supervisor -> 1 switch + step.failed
	h.OnError(sup, infoModel, errors.New("b")) // same agent, no new switch
	h.OnError(sub, infoModel, errors.New("c")) // researcher -> 1 switch + step.failed
	h.OnError(sup, infoModel, errors.New("d")) // back to supervisor -> 1 switch + step.failed

	events := collect(t, ch, 8)
	switches := map[string]int{}
	for _, e := range events {
		if e.Type == TypeAgentSwitched {
			switches[e.Data.(AgentSwitched).Agent]++
		}
	}
	if switches["supervisor"] != 2 || switches["researcher"] != 1 {
		t.Fatalf("unexpected agent.switched counts: %v (events %v)", switches, types(events))
	}
}
