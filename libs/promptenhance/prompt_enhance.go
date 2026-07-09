// Package promptenhance provides prompt rewriting using a small/cheap model.
//
// It implements the same enhancement strategy as kilocode's "Enhance Prompt"
// button: a lightweight model rewrites a user draft into a clearer, more
// specific request without answering it.
package promptenhance

import (
	"context"
	"fmt"
	"strings"

	_ "embed"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/strutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// DefaultEnhanceSystem is the system prompt instructing the model to improve
// a user draft without answering it.
//
//go:embed prompts/enhance_system.md
var DefaultEnhanceSystem string

// Config configures the Enhancer.
type Config struct {
	// Model is the small model used to enhance prompts.
	// Typically a fast, cheap model (claude-haiku, gemini-flash, gpt-5-nano).
	Model model.BaseChatModel `validate:"required" jsonschema:"required,description=Model used to enhance prompts (should be small/fast)"`

	// SystemPrompt overrides the default enhancement system prompt.
	// When empty, DefaultEnhanceSystem is used.
	SystemPrompt string `jsonschema:"description=Optional override for the enhancement system prompt"`
}

// Enhancer rewrites draft user prompts into clearer, more specific requests.
type Enhancer struct {
	model        model.BaseChatModel
	systemPrompt string
}

// NewEnhancer validates cfg and returns an Enhancer. If cfg is nil, an error
// is returned because Model is required.
func NewEnhancer(ctx context.Context, cfg *Config) (*Enhancer, error) {
	if cfg == nil {
		return nil, errors.New("promptenhance: config is required")
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	sp := cfg.SystemPrompt
	if sp == "" {
		sp = DefaultEnhanceSystem
	}

	return &Enhancer{
		model:        cfg.Model,
		systemPrompt: sp,
	}, nil
}

// Enhance rewrites draft into a clearer prompt without answering it.
func (e *Enhancer) Enhance(ctx context.Context, draft string) (string, error) {
	if draft == "" {
		return "", nil
	}

	messages := []*schema.Message{
		schema.SystemMessage(e.systemPrompt),
		{
			Role:    schema.User,
			Content: fmt.Sprintf("Draft prompt to enhance, not answer:\n\n<draft>%s</draft>", draft),
		},
	}

	result, err := e.model.Generate(ctx, messages)
	if err != nil {
		return "", errors.Wrap(err, "promptenhance: model generation failed")
	}

	cleaned := clean(result.Content)
	return cleaned, nil
}

// clean removes markdown fences and surrounding quotes from the model output.
func clean(s string) string {
	s = strings.TrimSpace(s)
	s = strutil.StripMarkdownFences(s)
	s = stripSurroundingQuotes(s)
	return strings.TrimSpace(s)
}

func stripSurroundingQuotes(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			s = s[1 : len(s)-1]
			return strings.TrimSpace(s)
		}
	}
	return s
}
