package contextopt

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/memory"
)

func user(content string) *schema.Message      { return schema.UserMessage(content) }
func assistant(content string) *schema.Message { return schema.AssistantMessage(content, nil) }

func toolMsg(callID, content string) *schema.Message {
	return &schema.Message{Role: schema.Tool, ToolCallID: callID, Content: content}
}

func assistantCall(callID, name, args string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: callID, Function: schema.FunctionCall{Name: name, Arguments: args}},
		},
	}
}

func TestIsOverflow(t *testing.T) {
	// 4 chars per token. ContextLimit 1000, Reserved 900 => usable 100 tokens => 400 chars.
	o, err := NewOptimizer(&Config{ContextLimit: 1000, ReservedTokens: 900})
	if err != nil {
		t.Fatal(err)
	}
	if o.usable() != 100 {
		t.Fatalf("usable = %d, want 100", o.usable())
	}
	if o.IsOverflow([]*schema.Message{user(strings.Repeat("a", 100))}) {
		t.Fatal("should not overflow at 25 tokens")
	}
	if !o.IsOverflow([]*schema.Message{user(strings.Repeat("a", 800))}) {
		t.Fatal("should overflow at 200 tokens")
	}

	// No limit configured => never overflow.
	o2, _ := NewOptimizer(&Config{})
	if o2.IsOverflow([]*schema.Message{user(strings.Repeat("a", 100000))}) {
		t.Fatal("no limit should never overflow")
	}
}

func TestMaxInputTokensPrecedence(t *testing.T) {
	o, _ := NewOptimizer(&Config{ContextLimit: 1000, MaxInputTokens: 500, ReservedTokens: 100})
	if o.usable() != 400 {
		t.Fatalf("usable = %d, want 400", o.usable())
	}
}

func TestSplitTurns(t *testing.T) {
	msgs := []*schema.Message{
		user("u1"), assistant("a1"),
		user("u2"), assistant("a2"), toolMsg("c", "t"),
		user("u3"),
	}
	turns := splitTurns(msgs)
	if len(turns) != 3 {
		t.Fatalf("got %d turns, want 3", len(turns))
	}
	if turns[0] != (turn{0, 2}) || turns[1] != (turn{2, 5}) || turns[2] != (turn{5, 6}) {
		t.Fatalf("unexpected turns: %+v", turns)
	}
}

func TestSelectTailKeepsRecentTurns(t *testing.T) {
	o, _ := NewOptimizer(&Config{TailTurns: 2, PreserveRecentTokens: 1000})
	msgs := []*schema.Message{
		user("u1"), assistant("a1"),
		user("u2"), assistant("a2"),
		user("u3"), assistant("a3"),
	}
	head, tailStart := o.selectTail(msgs)
	if tailStart != 2 {
		t.Fatalf("tailStart = %d, want 2", tailStart)
	}
	if len(head) != 2 {
		t.Fatalf("head len = %d, want 2", len(head))
	}
}

func TestSelectTailBudgetTooSmallSummarizesAll(t *testing.T) {
	o, _ := NewOptimizer(&Config{TailTurns: 2, PreserveRecentTokens: 1})
	msgs := []*schema.Message{
		user(strings.Repeat("a", 100)), assistant(strings.Repeat("b", 100)),
		user(strings.Repeat("c", 100)), assistant(strings.Repeat("d", 100)),
	}
	head, tailStart := o.selectTail(msgs)
	if tailStart != len(msgs) || len(head) != len(msgs) {
		t.Fatalf("expected summarize-all, got tailStart=%d head=%d", tailStart, len(head))
	}
}

func TestSelectTailSplitsTurn(t *testing.T) {
	// One large turn whose suffix fits the budget partway through.
	o, _ := NewOptimizer(&Config{TailTurns: 1, PreserveRecentTokens: 30})
	msgs := []*schema.Message{
		user(strings.Repeat("a", 100)),
		assistant(strings.Repeat("b", 100)),
		toolMsg("c", strings.Repeat("d", 40)), // 10 tokens, fits 30 budget
	}
	head, tailStart := o.selectTail(msgs)
	if tailStart != 2 {
		t.Fatalf("tailStart = %d, want 2 (split inside turn)", tailStart)
	}
	if len(head) != 2 {
		t.Fatalf("head len = %d, want 2", len(head))
	}
}

func TestTruncateToolOutput(t *testing.T) {
	o, _ := NewOptimizer(&Config{ToolOutputMaxChars: 10})
	got := o.truncateToolOutput(strings.Repeat("x", 50))
	if !strings.HasPrefix(got, strings.Repeat("x", 10)) {
		t.Fatalf("prefix not preserved: %q", got)
	}
	if !strings.Contains(got, "pruned") {
		t.Fatalf("missing placeholder: %q", got)
	}
	if same := o.truncateToolOutput("short"); same != "short" {
		t.Fatalf("short content modified: %q", same)
	}
}

func TestPruneToolOutputs(t *testing.T) {
	big := strings.Repeat("x", 200_000) // 50k tokens
	msgs := []*schema.Message{
		user("u1"),
		assistantCall("c1", "read", "{}"),
		toolMsg("c1", big),
		user("u2"),
		assistantCall("c2", "read", "{}"),
		toolMsg("c2", "recent"),
		user("u3"),
		assistant("done"),
	}
	o, _ := NewOptimizer(&Config{
		PruneToolOutputs:   true,
		PruneProtectTokens: 40_000,
		PruneMinimum:       20_000,
		ToolOutputMaxChars: 100,
		TailTurns:          2,
	})
	out, _ := o.pruneToolOutputs(context.Background(), msgs)
	if len(out[2].Content) > 200 {
		t.Fatalf("old tool output not pruned, len=%d", len(out[2].Content))
	}
	if !isPruned(out[2]) {
		t.Fatal("pruned marker not set")
	}
	// Input not mutated.
	if len(msgs[2].Content) != 200_000 {
		t.Fatal("input was mutated")
	}

	// Idempotence: second pass is a no-op (already pruned -> break).
	out2, _ := o.pruneToolOutputs(context.Background(), out)
	if out2[2].Content != out[2].Content {
		t.Fatal("second prune changed content")
	}
}

func TestPruneProtectedTool(t *testing.T) {
	big := strings.Repeat("x", 200_000)
	msgs := []*schema.Message{
		user("u1"),
		assistantCall("c1", "skill", "{}"),
		toolMsg("c1", big),
		user("u2"),
		user("u3"),
	}
	o, _ := NewOptimizer(&Config{
		PruneToolOutputs: true,
		ProtectedTools:   []string{"skill"},
		TailTurns:        2,
	})
	out, _ := o.pruneToolOutputs(context.Background(), msgs)
	if len(out[2].Content) != 200_000 {
		t.Fatal("protected tool output was pruned")
	}
}

func TestPruneBelowMinimumNoop(t *testing.T) {
	small := strings.Repeat("x", 1000) // 250 tokens, below PruneMinimum
	msgs := []*schema.Message{
		user("u1"), assistantCall("c1", "read", "{}"), toolMsg("c1", small),
		user("u2"), user("u3"),
	}
	o, _ := NewOptimizer(&Config{PruneToolOutputs: true, TailTurns: 2})
	out, _ := o.pruneToolOutputs(context.Background(), msgs)
	if isPruned(out[2]) {
		t.Fatal("pruned below minimum")
	}
}

func TestTrimBeforeLastSummary(t *testing.T) {
	if got := trimBeforeLastSummary([]*schema.Message{user("a"), assistant("b")}); len(got) != 2 {
		t.Fatalf("no summary should be unchanged, got %d", len(got))
	}
	s1 := memory.NewSummaryMessage("sum1")
	s2 := memory.NewSummaryMessage("sum2")
	msgs := []*schema.Message{user("a"), s1, user("b"), s2, user("c")}
	got := trimBeforeLastSummary(msgs)
	if len(got) != 2 || !memory.IsSummary(got[0]) || got[0].Content != "sum2" {
		t.Fatalf("expected suffix from last summary, got %+v", got)
	}
	if lastSummaryText(msgs) != "sum2" {
		t.Fatalf("lastSummaryText = %q", lastSummaryText(msgs))
	}
}

func TestOptimizeWithSummarizer(t *testing.T) {
	var gotPrev string
	fake := SummarizerFunc(func(_ context.Context, _ []*schema.Message, prev string) (string, error) {
		gotPrev = prev
		return "SUMMARY", nil
	})
	o, _ := NewOptimizer(&Config{
		ContextLimit:         1000,
		ReservedTokens:       900, // usable 100 tokens = 400 chars
		TailTurns:            1,
		PreserveRecentTokens: 1000,
		Summarizer:           fake,
	})
	msgs := []*schema.Message{
		user(strings.Repeat("a", 400)), assistant(strings.Repeat("b", 400)),
		user("recent"), assistant("reply"),
	}
	out, err := o.Optimize(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !memory.IsSummary(out[0]) || out[0].Content != "SUMMARY" {
		t.Fatalf("expected summary first, got %+v", out[0])
	}
	if out[len(out)-1].Content != "reply" {
		t.Fatalf("tail not preserved, got %+v", out[len(out)-1])
	}
	if gotPrev != "" {
		t.Fatalf("previousSummary should be empty, got %q", gotPrev)
	}
}

func TestOptimizeNilSummarizerNoError(t *testing.T) {
	o, _ := NewOptimizer(&Config{ContextLimit: 1000, ReservedTokens: 900})
	msgs := []*schema.Message{user(strings.Repeat("a", 800))}
	out, err := o.Optimize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("nil summarizer should not error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected trim/prune passthrough, got %d", len(out))
	}
}
