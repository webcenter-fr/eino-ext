package contextopt

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/memory"
)

func TestMiddlewareRewritesState(t *testing.T) {
	fake := SummarizerFunc(func(_ context.Context, _ []*schema.Message, _ string) (string, error) {
		return "SUMMARY", nil
	})
	mw, err := NewMiddleware(&Config{
		ContextLimit:         1000,
		ReservedTokens:       900,
		TailTurns:            1,
		PreserveRecentTokens: 1000,
		Summarizer:           fake,
	})
	if err != nil {
		t.Fatal(err)
	}

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user(strings.Repeat("a", 400)), assistant(strings.Repeat("b", 400)),
		user("recent"), assistant("reply"),
	}}

	_, newState, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !memory.IsSummary(newState.Messages[0]) {
		t.Fatalf("expected summary first, got %+v", newState.Messages[0])
	}
}

func TestMiddlewareNilState(t *testing.T) {
	mw, _ := NewMiddleware(&Config{})
	ctx, state, err := mw.BeforeModelRewriteState(context.Background(), nil, nil)
	if err != nil || state != nil || ctx == nil {
		t.Fatalf("unexpected: state=%v err=%v", state, err)
	}
}

func TestChatModelDecoratorOptimizesInput(t *testing.T) {
	fm := &fakeModel{reply: "ok"}
	fake := SummarizerFunc(func(_ context.Context, _ []*schema.Message, _ string) (string, error) {
		return "SUMMARY", nil
	})
	cm, err := NewChatModel(fm, &Config{
		ContextLimit:         1000,
		ReservedTokens:       900,
		TailTurns:            1,
		PreserveRecentTokens: 1000,
		Summarizer:           fake,
	})
	if err != nil {
		t.Fatal(err)
	}

	input := []*schema.Message{
		user(strings.Repeat("a", 400)), assistant(strings.Repeat("b", 400)),
		user("recent"), assistant("reply"),
	}
	if _, err := cm.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if !memory.IsSummary(fm.lastInput[0]) {
		t.Fatalf("model did not receive optimized input: %+v", fm.lastInput[0])
	}
}

func TestChatModelDecoratorNoOp(t *testing.T) {
	fm := &fakeModel{reply: "ok"}
	cm, _ := NewChatModel(fm, &Config{}) // no limit, no pruning => passthrough
	input := []*schema.Message{user("hi"), assistant("there")}
	if _, err := cm.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if len(fm.lastInput) != 2 {
		t.Fatalf("expected passthrough, got %d msgs", len(fm.lastInput))
	}
}

func TestChatModelStream(t *testing.T) {
	fm := &fakeModel{reply: "streamed"}
	cm, _ := NewChatModel(fm, &Config{})
	sr, err := cm.Stream(context.Background(), []*schema.Message{user("hi")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	msg, err := sr.Recv()
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if msg.Content != "streamed" {
		t.Fatalf("got %q", msg.Content)
	}
}
