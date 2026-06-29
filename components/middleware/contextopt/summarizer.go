package contextopt

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// DefaultSummaryTemplate is the anchored Markdown summary template (faithful copy
// of kilocode's SUMMARY_TEMPLATE). The prompt text lives in
// prompts/summary_template.md and is embedded at build time.
//
//go:embed prompts/summary_template.md
var DefaultSummaryTemplate string

// Summarizer produces an anchored summary of a conversation history. previousSummary
// is the text of the last summary (empty when none), allowing incremental updates.
type Summarizer interface {
	Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)
}

// SummarizerFunc adapts a function to the Summarizer interface.
type SummarizerFunc func(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)

// Summarize implements Summarizer.
func (f SummarizerFunc) Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error) {
	return f(ctx, history, previousSummary)
}

// modelSummarizer is an LLM-backed Summarizer.
type modelSummarizer struct {
	model              model.BaseChatModel
	template           string
	toolOutputMaxChars int
}

// ModelSummarizerOption customizes a model-backed Summarizer.
type ModelSummarizerOption func(*modelSummarizer)

// WithSummaryTemplate overrides the default anchored summary template/instruction.
func WithSummaryTemplate(template string) ModelSummarizerOption {
	return func(s *modelSummarizer) {
		s.template = template
	}
}

// WithToolOutputMaxChars overrides the max number of characters kept per tool
// output when serializing the history into the summarization prompt.
func WithToolOutputMaxChars(n int) ModelSummarizerOption {
	return func(s *modelSummarizer) {
		s.toolOutputMaxChars = n
	}
}

// NewModelSummarizer returns a Summarizer that builds the anchored prompt (with a
// conditional <previous-summary> block) and calls m.Generate.
func NewModelSummarizer(m model.BaseChatModel, opts ...ModelSummarizerOption) Summarizer {
	s := &modelSummarizer{
		model:              m,
		template:           DefaultSummaryTemplate,
		toolOutputMaxChars: DefaultToolOutputMaxChars,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// buildPrompt assembles the summarization instruction (port of compaction.ts:buildPrompt).
func (s *modelSummarizer) buildPrompt(previousSummary string) string {
	var anchor string
	if strings.TrimSpace(previousSummary) != "" {
		anchor = strings.Join([]string{
			"Update the anchored summary below using the conversation history above.",
			"Preserve still-true details, remove stale details, and merge in the new facts.",
			"<previous-summary>",
			previousSummary,
			"</previous-summary>",
		}, "\n")
	} else {
		anchor = "Create a new anchored summary from the conversation history above."
	}
	return fmt.Sprintf("%s\n\n%s", anchor, s.template)
}

// renderHistory serializes the conversation history into a single text block,
// truncating tool outputs to toolOutputMaxChars.
func (s *modelSummarizer) renderHistory(history []*schema.Message) string {
	var b strings.Builder
	for _, msg := range history {
		if msg == nil {
			continue
		}
		content := msg.Content
		if msg.Role == schema.Tool && s.toolOutputMaxChars > 0 && len(content) > s.toolOutputMaxChars {
			content = fmt.Sprintf("%s\n\n[output truncated]", content[:s.toolOutputMaxChars])
		}
		fmt.Fprintf(&b, "%s: %s\n", msg.Role, content)
		for _, tc := range msg.ToolCalls {
			fmt.Fprintf(&b, "%s -> tool_call %s(%s)\n", msg.Role, tc.Function.Name, tc.Function.Arguments)
		}
	}
	return b.String()
}

// Summarize implements Summarizer.
func (s *modelSummarizer) Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error) {
	prompt := fmt.Sprintf("%s\n\n%s", s.renderHistory(history), s.buildPrompt(previousSummary))

	out, err := s.model.Generate(ctx, []*schema.Message{schema.UserMessage(prompt)})
	if err != nil {
		return "", errors.Wrap(err, "contextopt: model summarization failed")
	}
	if out == nil {
		return "", errors.New("contextopt: model returned a nil message")
	}
	return out.Content, nil
}
