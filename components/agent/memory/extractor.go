package memory

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/strutil"
)

// ExtractionSystemPrompt is the system prompt for the memory extraction LLM call.
//
//go:embed prompts/extraction_system.md
var extractionSystemPrompt string

// ExtractionUserTemplate is the user prompt template for memory extraction.
// It expects two %s arguments: userContent and assistantContent.
//
//go:embed prompts/extraction_user.md
var extractionUserTemplate string

// SummarizeSystemPrompt is the system prompt for session summarization.
//
//go:embed prompts/summarize_system.md
var summarizeSystemPrompt string

// SummarizeUserTemplate is the user prompt template for session summarization.
// It expects two %s arguments: existingSummary and conversation.
//
//go:embed prompts/summarize_user.md
var summarizeUserTemplate string

// ExtractionResult holds the result of a memory extraction operation.
type ExtractionResult struct {
	Content    string  `json:"content"`
	Category   string  `json:"category"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

// Extractor extracts structured memories from conversation turns.
type Extractor struct {
	model model.BaseChatModel
}

// NewExtractor creates a new Extractor backed by the given chat model.
func NewExtractor(m model.BaseChatModel) *Extractor {
	return &Extractor{model: m}
}

// Extract runs the LLM extraction pipeline on the given user and assistant
// content and returns structured memory results with confidence scores.
func (e *Extractor) Extract(ctx context.Context, userContent, assistantContent string) ([]ExtractionResult, error) {
	if e.model == nil {
		return nil, nil
	}

	prompt := fmt.Sprintf(extractionUserTemplate,
		truncate(userContent, 4000),
		truncate(assistantContent, 4000),
	)

	result, err := e.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(extractionSystemPrompt),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return nil, errors.Wrap(err, "extract memories via LLM")
	}

	return parseExtractionResponse(result.Content)
}

// Summarize generates a session summary from the given messages, folding in an
// existing summary if provided.
func (e *Extractor) Summarize(ctx context.Context, messages []*schema.Message, existingSummary string) (string, error) {
	if e.model == nil {
		return "", nil
	}

	var b strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&b, "[%s]: %s\n", m.Role, truncate(m.Content, 2000))
	}

	prompt := fmt.Sprintf(summarizeUserTemplate, existingSummary, b.String())

	result, err := e.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(summarizeSystemPrompt),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return "", errors.Wrap(err, "generate session summary")
	}

	return result.Content, nil
}

func parseExtractionResponse(content string) ([]ExtractionResult, error) {
	content = extractJSONBlock(content)
	if content == "" {
		return nil, nil
	}

	var results []ExtractionResult
	if err := json.Unmarshal([]byte(content), &results); err != nil {
		return nil, errors.Wrap(err, "parse extraction JSON")
	}

	filtered := results[:0]
	for _, r := range results {
		if r.Confidence >= 0.7 && r.Content != "" {
			filtered = append(filtered, r)
		}
	}

	return filtered, nil
}

// extractJSONBlock strips markdown code fences, then extracts the first
// JSON array or object from the remaining text via bracket matching.
func extractJSONBlock(content string) string {
	return strutil.ExtractJSONBlock(content)
}

// stripMarkdownFences removes surrounding ```json ... ``` or ``` ... ``` blocks.
func stripMarkdownFences(s string) string {
	return strutil.StripMarkdownFences(s)
}

func truncate(s string, maxLen int) string {
	return strutil.Truncate(s, maxLen, "...")
}
