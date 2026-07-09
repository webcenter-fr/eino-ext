package promptenhance

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/cloudwego/eino/adk"

	libspromptenhance "github.com/webcenter-fr/eino-ext/libs/promptenhance"
)

func user(content string) *schema.Message {
	return &schema.Message{Role: schema.User, Content: content}
}

func assistant(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content}
}

type fakeModel struct {
	generateFunc func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
	streamFunc   func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
}

func (m *fakeModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.generateFunc(ctx, input, opts...)
}

func (m *fakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.streamFunc(ctx, input, opts...)
}

func newTestMiddleware(t *testing.T, enhanceResult string, enhanceErr error, autoAccept bool, shouldEnhance ShouldEnhanceFunc) *Middleware {
	t.Helper()
	mock := &fakeModel{
		generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			if enhanceErr != nil {
				return nil, enhanceErr
			}
			if enhanceResult != "" {
				return &schema.Message{Role: schema.Assistant, Content: enhanceResult}, nil
			}
			return &schema.Message{Role: schema.Assistant, Content: "enhanced: " + input[len(input)-1].Content}, nil
		},
	}
	enhancer, err := libspromptenhance.NewEnhancer(context.Background(), &libspromptenhance.Config{Model: mock})
	if err != nil {
		t.Fatal(err)
	}
	mw, err := NewMiddleware(&Config{
		Enhancer:      enhancer,
		AutoAccept:     autoAccept,
		ShouldEnhance: shouldEnhance,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mw
}

func TestMiddleware_BeforeModelRewriteState_FirstCall(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		assistant("hello"), user("original draft"),
	}}

	_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)

	var intErr *InterruptError
	if !errors.As(err, &intErr) {
		t.Fatalf("expected InterruptError, got %v", err)
	}
	if intErr.Original != "original draft" {
		t.Fatalf("original = %q", intErr.Original)
	}
	if intErr.Enhanced == "" {
		t.Fatal("expected non-empty enhanced")
	}
	if state.Messages[1].Content != "original draft" {
		t.Fatalf("message was modified before interrupt: %q", state.Messages[1].Content)
	}
}

func TestMiddleware_BeforeModelRewriteState_ResumeOriginal(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	choice := &Choice{Action: "original"}
	ctx := WithChoice(context.Background(), choice)

	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Messages[0].Content != "original draft" {
		t.Fatalf("content changed: %q", state.Messages[0].Content)
	}
	if !isEnhanced(state.Messages[0]) {
		t.Fatal("not marked enhanced")
	}
}

func TestMiddleware_BeforeModelRewriteState_ResumeEnhanced(t *testing.T) {
	mw := newTestMiddleware(t, "better prompt", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	choice := &Choice{Action: "enhanced"}
	ctx := WithChoice(context.Background(), choice)

	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Messages[0].Content != "better prompt" {
		t.Fatalf("content not enhanced: %q", state.Messages[0].Content)
	}
	if !isEnhanced(state.Messages[0]) {
		t.Fatal("not marked enhanced")
	}
}

func TestMiddleware_BeforeModelRewriteState_ResumeModified(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	choice := &Choice{Action: "modified", Text: "user's edit"}
	ctx := WithChoice(context.Background(), choice)

	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Messages[0].Content != "user's edit" {
		t.Fatalf("content: %q", state.Messages[0].Content)
	}
	if !isEnhanced(state.Messages[0]) {
		t.Fatal("not marked enhanced")
	}
}

func TestMiddleware_BeforeModelRewriteState_ResumeModifiedEmptyText(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	choice := &Choice{Action: "modified", Text: ""}
	ctx := WithChoice(context.Background(), choice)

	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err == nil {
		t.Fatal("expected error for modified without text")
	}
}

func TestMiddleware_BeforeModelRewriteState_ResumeSkipAlways(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	choice := &Choice{Action: "skip_always"}
	ctx := WithChoice(context.Background(), choice)

	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Messages[0].Content != "original draft" {
		t.Fatalf("content changed: %q", state.Messages[0].Content)
	}
	if !isEnhanced(state.Messages[0]) {
		t.Fatal("not marked enhanced")
	}
}

func TestMiddleware_AutoAccept(t *testing.T) {
	mw := newTestMiddleware(t, "enhanced version", nil, true, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Messages[0].Content != "enhanced version" {
		t.Fatalf("content: %q", state.Messages[0].Content)
	}
	if !isEnhanced(state.Messages[0]) {
		t.Fatal("not marked enhanced")
	}
}

func TestMiddleware_ShouldEnhance(t *testing.T) {
	t.Run("false skips", func(t *testing.T) {
		mw := newTestMiddleware(t, "", nil, false, func(ctx context.Context) bool {
			return false
		})

		state := &adk.ChatModelAgentState{Messages: []*schema.Message{
			user("original draft"),
		}}

		_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !isEnhanced(state.Messages[0]) {
			t.Fatal("not marked enhanced")
		}
	})

	t.Run("true proceeds", func(t *testing.T) {
		mw := newTestMiddleware(t, "", nil, false, func(ctx context.Context) bool {
			return true
		})

		state := &adk.ChatModelAgentState{Messages: []*schema.Message{
			user("original draft"),
		}}

		_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
		var intErr *InterruptError
		if !errors.As(err, &intErr) {
			t.Fatalf("expected InterruptError, got %v", err)
		}
	})
}

func TestMiddleware_Idempotency(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}
	markEnhanced(state.Messages[0])

	_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMiddleware_EmptyState(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	t.Run("nil state", func(t *testing.T) {
		_, _, err := mw.BeforeModelRewriteState(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		state := &adk.ChatModelAgentState{}
		_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no user message", func(t *testing.T) {
		state := &adk.ChatModelAgentState{Messages: []*schema.Message{
			schema.SystemMessage("sys"),
			assistant("hi"),
		}}
		_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestMiddleware_NoChange(t *testing.T) {
	mw := newTestMiddleware(t, "original draft", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !isEnhanced(state.Messages[0]) {
		t.Fatal("not marked enhanced")
	}
}

func TestMiddleware_EnhancerError(t *testing.T) {
	mw := newTestMiddleware(t, "", errors.New("model failure"), false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMiddleware_LastUserMessageOnly(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("first message"),
		assistant("reply"),
		user("last message"),
	}}

	_, _, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	var ie *InterruptError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InterruptError, got %v", err)
	}
	if ie.Original != "last message" {
		t.Fatalf("enhanced wrong message: %q", ie.Original)
	}
}

func TestMiddleware_ResumeUnknownAction(t *testing.T) {
	mw := newTestMiddleware(t, "", nil, false, nil)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		user("original draft"),
	}}

	choice := &Choice{Action: "unknown"}
	ctx := WithChoice(context.Background(), choice)

	_, _, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
