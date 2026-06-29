// Package contextopt provides context-window optimization strategies (inspired by
// kilocode's session compaction) for the eino ecosystem.
//
// The core Optimizer is pure (no LLM/I-O dependency): it trims history before the
// last summary, prunes stale tool outputs, and — when an optional Summarizer is
// provided and the history overflows the usable context window — replaces the
// summarizable head with an anchored summary while preserving the most recent
// turns ("tail").
//
// Two surfaces are exposed on top of the core:
//   - Middleware: an adk.ChatModelAgentMiddleware that rewrites state.Messages.
//   - ChatModel: a model.BaseChatModel decorator that optimizes the input before
//     delegating to the wrapped model.
package contextopt

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	"github.com/go-playground/validator/v10"

	"github.com/webcenter-fr/eino-ext/components/memory"
)

// PruneMarkerKey is the key used in schema.Message.Extra to mark a tool message
// whose output has already been pruned/truncated. It guarantees idempotence when
// Optimize is called repeatedly (e.g. once per turn).
const PruneMarkerKey = "__eino_ext_contextopt_pruned"

// Default configuration values (ported from kilocode).
const (
	DefaultReservedTokens     = 20_000
	DefaultTailTurns          = 2
	DefaultPruneProtectTokens = 40_000
	DefaultPruneMinimum       = 20_000
	DefaultToolOutputMaxChars = 2_000

	minPreserveRecentTokens = 2_000
	maxPreserveRecentTokens = 8_000

	// pruneProtectedRecentTurns is the minimum number of most-recent turns whose
	// tool outputs are never pruned, independently of TailTurns (port of the
	// hardcoded recent-turn guard in kilocode's prune).
	pruneProtectedRecentTurns = 2
)

// Config configures an Optimizer. Fields left at their zero value receive sane
// defaults in NewOptimizer (see the Default* constants).
type Config struct {
	// ContextLimit is the model's total context window (in tokens).
	ContextLimit int `validate:"gte=0"`
	// MaxInputTokens, when > 0, takes precedence over ContextLimit-ReservedTokens
	// (equivalent to kilocode's model.limit.input).
	MaxInputTokens int `validate:"gte=0"`
	// ReservedTokens is the buffer reserved for the model output (default 20_000).
	ReservedTokens int `validate:"gte=0"`

	// TailTurns is the number of most-recent turns to preserve verbatim.
	// <= 0 disables tail truncation (default 2).
	TailTurns int
	// PreserveRecentTokens is the token budget for the preserved tail.
	// Default: clamp(usable*0.25, 2_000, 8_000).
	PreserveRecentTokens int `validate:"gte=0"`

	// PruneToolOutputs enables pruning of stale tool outputs.
	PruneToolOutputs bool
	// PruneProtectTokens is the protected recent window (default 40_000) inside
	// which tool outputs are never pruned.
	PruneProtectTokens int `validate:"gte=0"`
	// PruneMinimum is the minimum amount of eligible tokens required before any
	// pruning is actually applied (default 20_000).
	PruneMinimum int `validate:"gte=0"`
	// ToolOutputMaxChars is the maximum number of characters kept when truncating
	// a tool output (default 2_000).
	ToolOutputMaxChars int `validate:"gte=0"`
	// ProtectedTools lists tool names whose outputs are never pruned.
	ProtectedTools []string

	// TokenCounter estimates the number of tokens for a set of messages.
	// Default: memory.DefaultTokenCounter (~4 chars/token).
	TokenCounter memory.TokenCounter
	// Summarizer, when non-nil, performs LLM-backed compaction on overflow.
	// When nil, Optimize never returns an error on overflow: it simply returns
	// the trimmed/pruned history.
	Summarizer Summarizer
}

// Optimizer applies context-window optimization strategies to a message history.
type Optimizer struct {
	cfg *Config
}

// NewOptimizer validates the configuration, applies defaults and returns an
// Optimizer.
func NewOptimizer(cfg *Config) (*Optimizer, error) {
	if cfg == nil {
		cfg = &Config{}
	}

	// Work on a copy so we don't mutate the caller's struct.
	c := *cfg

	if err := validator.New().Struct(&c); err != nil {
		return nil, errors.Wrap(err, "invalid contextopt.Config")
	}

	if c.ReservedTokens == 0 {
		c.ReservedTokens = DefaultReservedTokens
	}
	if c.TailTurns == 0 {
		c.TailTurns = DefaultTailTurns
	}
	if c.PruneProtectTokens == 0 {
		c.PruneProtectTokens = DefaultPruneProtectTokens
	}
	if c.PruneMinimum == 0 {
		c.PruneMinimum = DefaultPruneMinimum
	}
	if c.ToolOutputMaxChars == 0 {
		c.ToolOutputMaxChars = DefaultToolOutputMaxChars
	}
	if c.TokenCounter == nil {
		c.TokenCounter = memory.DefaultTokenCounter
	}

	o := &Optimizer{cfg: &c}

	if c.PreserveRecentTokens == 0 {
		c.PreserveRecentTokens = clamp(o.usable()/4, minPreserveRecentTokens, maxPreserveRecentTokens)
	}

	return o, nil
}

// usable returns the usable context window in tokens (port of overflow.ts:usable).
// Returns 0 when no limit is configured (overflow detection disabled).
func (o *Optimizer) usable() int {
	base := o.cfg.ContextLimit
	if o.cfg.MaxInputTokens > 0 {
		base = o.cfg.MaxInputTokens
	}
	if base <= 0 {
		return 0
	}
	return max(0, base-o.cfg.ReservedTokens)
}

// IsOverflow reports whether the token count of msgs reaches the usable window.
// When no limit is configured (usable == 0), it always returns false.
func (o *Optimizer) IsOverflow(msgs []*schema.Message) bool {
	usable := o.usable()
	if usable <= 0 {
		return false
	}
	return o.cfg.TokenCounter(msgs) >= usable
}

// turn represents a half-open message range [start, end) starting at a user
// message that is not a summary.
type turn struct {
	start int
	end   int
}

// splitTurns groups messages into turns. A turn starts at a non-summary user
// message and runs until the next non-summary user message (port of compaction.ts:turns).
func splitTurns(msgs []*schema.Message) []turn {
	var result []turn
	for i, msg := range msgs {
		if msg == nil || msg.Role != schema.User || memory.IsSummary(msg) {
			continue
		}
		result = append(result, turn{start: i, end: len(msgs)})
	}
	for i := 0; i < len(result)-1; i++ {
		result[i].end = result[i+1].start
	}
	return result
}

// splitTurn finds the smallest tail start within a turn whose suffix fits the
// budget (port of compaction.ts:splitTurn). Returns -1 when no split is possible.
func (o *Optimizer) splitTurn(msgs []*schema.Message, t turn, budget int) int {
	if budget <= 0 {
		return -1
	}
	if t.end-t.start <= 1 {
		return -1
	}
	for start := t.start + 1; start < t.end; start++ {
		if o.cfg.TokenCounter(msgs[start:t.end]) <= budget {
			return start
		}
	}
	return -1
}

// selectTail splits msgs into a summarizable head and a preserved tail (port of
// compaction.ts:select). It returns the head messages and the index at which the
// tail begins. When no separate tail is kept, head == msgs and tailStart == len(msgs).
func (o *Optimizer) selectTail(msgs []*schema.Message) (head []*schema.Message, tailStart int) {
	if o.cfg.TailTurns <= 0 {
		return msgs, len(msgs)
	}

	budget := o.cfg.PreserveRecentTokens
	all := splitTurns(msgs)
	if len(all) == 0 {
		return msgs, len(msgs)
	}

	recent := all
	if len(recent) > o.cfg.TailTurns {
		recent = recent[len(recent)-o.cfg.TailTurns:]
	}

	total := 0
	keep := -1
	for i := len(recent) - 1; i >= 0; i-- {
		t := recent[i]
		size := o.cfg.TokenCounter(msgs[t.start:t.end])
		if total+size <= budget {
			total += size
			keep = t.start
			continue
		}
		if split := o.splitTurn(msgs, t, budget-total); split >= 0 {
			keep = split
		}
		break
	}

	if keep <= 0 {
		// Either nothing fit, or the whole history would be preserved: summarize all.
		return msgs, len(msgs)
	}
	return msgs[:keep], keep
}

// truncateToolOutput truncates a tool output to ToolOutputMaxChars, appending a
// terse marker (port of TOOL_OUTPUT_MAX_CHARS).
func (o *Optimizer) truncateToolOutput(content string) string {
	limit := o.cfg.ToolOutputMaxChars
	if limit <= 0 || len(content) <= limit {
		return content
	}
	return fmt.Sprintf("%s\n\n[output pruned to save context]", content[:limit])
}

// toolNamesByCallID maps tool call IDs to the tool name issued by the assistant.
func toolNamesByCallID(msgs []*schema.Message) map[string]string {
	names := make(map[string]string)
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				names[tc.ID] = tc.Function.Name
			}
		}
	}
	return names
}

// isPruned reports whether a message's output has already been pruned.
func isPruned(msg *schema.Message) bool {
	if msg == nil || msg.Extra == nil {
		return false
	}
	b, ok := msg.Extra[PruneMarkerKey].(bool)
	return ok && b
}

// pruneToolOutputs erases (truncates) outputs of older tool calls beyond the
// protected window when the eligible total exceeds PruneMinimum (port of
// compaction.ts:prune). It never mutates input messages: pruned entries are
// replaced by copies marked with PruneMarkerKey for idempotence.
func (o *Optimizer) pruneToolOutputs(msgs []*schema.Message) []*schema.Message {
	protected := make(map[string]struct{}, len(o.cfg.ProtectedTools))
	for _, name := range o.cfg.ProtectedTools {
		protected[name] = struct{}{}
	}
	names := toolNamesByCallID(msgs)

	total := 0
	pruned := 0
	toPrune := make(map[int]struct{})
	turns := 0

	// Protect at least the 2 most recent turns from pruning, independently of
	// TailTurns (which may be <= 0 to disable summary tail truncation).
	protectRecentTurns := max(o.cfg.TailTurns, pruneProtectedRecentTurns)

loop:
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg == nil {
			continue
		}
		if msg.Role == schema.User {
			turns++
		}
		if turns < protectRecentTurns {
			continue
		}
		if msg.Role == schema.Assistant && memory.IsSummary(msg) {
			break loop
		}
		if msg.Role != schema.Tool {
			continue
		}
		if _, ok := protected[names[msg.ToolCallID]]; ok {
			continue
		}
		if isPruned(msg) {
			break loop
		}
		est := o.cfg.TokenCounter([]*schema.Message{msg})
		total += est
		if total <= o.cfg.PruneProtectTokens {
			continue
		}
		pruned += est
		toPrune[i] = struct{}{}
	}

	if pruned <= o.cfg.PruneMinimum || len(toPrune) == 0 {
		return msgs
	}

	out := make([]*schema.Message, len(msgs))
	copy(out, msgs)
	for i := range toPrune {
		clone := *out[i]
		clone.Content = o.truncateToolOutput(clone.Content)
		extra := make(map[string]any, len(clone.Extra)+1)
		for k, v := range clone.Extra {
			extra[k] = v
		}
		extra[PruneMarkerKey] = true
		clone.Extra = extra
		out[i] = &clone
	}
	return out
}

// trimBeforeLastSummary returns the suffix of msgs starting at the last summary
// message (reusing memory.IsSummary). When no summary is present, msgs is returned
// unchanged.
func trimBeforeLastSummary(msgs []*schema.Message) []*schema.Message {
	last := -1
	for i, msg := range msgs {
		if memory.IsSummary(msg) {
			last = i
		}
	}
	if last < 0 {
		return msgs
	}
	return msgs[last:]
}

// lastSummaryText returns the content of the last summary message, or "".
func lastSummaryText(msgs []*schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if memory.IsSummary(msgs[i]) {
			return msgs[i].Content
		}
	}
	return ""
}

// Optimize applies the full optimization pipeline to msgs:
//
//  1. trim everything before the last summary;
//  2. prune stale tool outputs (when enabled);
//  3. on overflow with a Summarizer set, replace the summarizable head with an
//     anchored summary and keep the preserved tail.
//
// When the Summarizer is nil, overflow never produces an error: the trimmed and
// pruned history is returned as-is.
func (o *Optimizer) Optimize(ctx context.Context, msgs []*schema.Message) ([]*schema.Message, error) {
	out := trimBeforeLastSummary(msgs)

	if o.cfg.PruneToolOutputs {
		out = o.pruneToolOutputs(out)
	}

	if !o.IsOverflow(out) || o.cfg.Summarizer == nil {
		return out, nil
	}

	head, tailStart := o.selectTail(out)
	prev := lastSummaryText(out)

	text, err := o.cfg.Summarizer.Summarize(ctx, head, prev)
	if err != nil {
		return nil, errors.Wrap(err, "contextopt: summarize failed")
	}

	result := make([]*schema.Message, 0, 1+len(out)-tailStart)
	result = append(result, memory.NewSummaryMessage(text))
	result = append(result, out[tailStart:]...)
	return result, nil
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}
