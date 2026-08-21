// Package promptenhance provides prompt rewriting using a small/cheap model.
//
// It implements the same enhancement strategy as kilocode's "Enhance Prompt"
// button: a lightweight model rewrites a user draft into a clearer, more
// specific request without answering it.
package promptenhance

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	_ "embed"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/strutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// promptDelimiterRe matches the structural delimiters used to frame the
// conversation context and draft in the enhancement prompt. It is used to
// neutralize occurrences of these tokens inside untrusted embedded content so
// that a prior message (or tool output) cannot spoof the <context>/<draft>
// structure.
var promptDelimiterRe = regexp.MustCompile(`(?i)</?\s*(?:context|draft)\s*/?>`)

// DefaultEnhanceSystem is the system prompt instructing the model to improve
// a user draft without answering it.
//
//go:embed prompts/enhance_system.md
var DefaultEnhanceSystem string

// defaultMaxContextMessages is the number of prior messages used as context
// when Config.MaxContextMessages is left unset (0).
const defaultMaxContextMessages = 6

// Config configures the Enhancer.
type Config struct {
	// Model is the small model used to enhance prompts.
	// Typically a fast, cheap model (claude-haiku, gemini-flash, gpt-5-nano).
	Model model.BaseChatModel `validate:"required" jsonschema:"required,description=Model used to enhance prompts (should be small/fast)"`

	// SystemPrompt overrides the default enhancement system prompt.
	// When empty, DefaultEnhanceSystem is used.
	SystemPrompt string `jsonschema:"description=Optional override for the enhancement system prompt"`

	// MaxContextMessages bounds how many most-recent prior messages are included
	// as conversation context. 0 (unset) defaults to 6; a negative value is
	// treated as 0. Context is embedded in the single user message, not sent as
	// real role messages.
	MaxContextMessages int `validate:"gte=0" jsonschema:"description=Maximum number of prior messages to include as conversation context (default 6)"`
}

// Enhancer rewrites draft user prompts into clearer, more specific requests.
type Enhancer struct {
	model              model.BaseChatModel
	systemPrompt       string
	maxContextMessages int
}

// NewEnhancer validates cfg and returns an Enhancer. If cfg is nil, an error
// is returned because Model is required.
func NewEnhancer(ctx context.Context, cfg *Config) (*Enhancer, error) {
	if cfg == nil {
		return nil, errors.New("promptenhance: config is required")
	}
	// Any value <= 0 (unset or negative) falls back to the default.
	if cfg.MaxContextMessages <= 0 {
		cfg.MaxContextMessages = defaultMaxContextMessages
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}

	sp := cfg.SystemPrompt
	if sp == "" {
		sp = DefaultEnhanceSystem
	}

	return &Enhancer{
		model:              cfg.Model,
		systemPrompt:       sp,
		maxContextMessages: cfg.MaxContextMessages,
	}, nil
}

// Enhance rewrites draft into a clearer prompt without answering it.
func (e *Enhancer) Enhance(ctx context.Context, draft string) (string, error) {
	return e.EnhanceInContext(ctx, nil, draft)
}

// EnhanceInContext rewrites draft into a clearer prompt, using up to
// e.maxContextMessages most-recent prior messages from history as reference
// context. history may be nil or empty; it is never mutated.
func (e *Enhancer) EnhanceInContext(ctx context.Context, history []*schema.Message, draft string) (string, error) {
	if draft == "" {
		return "", nil
	}

	userContent := buildUserContent(history, draft, e.maxContextMessages)

	messages := []*schema.Message{
		schema.SystemMessage(e.systemPrompt),
		{
			Role:    schema.User,
			Content: userContent,
		},
	}

	result, err := e.model.Generate(ctx, messages)
	if err != nil {
		return "", errors.Wrap(err, "promptenhance: model generation failed")
	}

	return clean(result.Content), nil
}

// buildUserContent renders the draft (plus optional conversation context) into
// the single user message. When there is no context it falls back to the legacy
// single-draft format.
func buildUserContent(history []*schema.Message, draft string, maxContextMessages int) string {
	draft = escapePromptData(draft)
	ctxStr := renderContext(history, maxContextMessages)
	if ctxStr == "" {
		return fmt.Sprintf("Draft prompt to enhance, not answer:\n\n<draft>%s</draft>", draft)
	}
	return fmt.Sprintf("%s\n\nDraft to rewrite (rewrite ONLY this into a clear, standalone prompt, resolving references using the context above; do NOT answer it):\n<draft>%s</draft>", ctxStr, draft)
}

// renderContext builds a role-labelled compact transcript of the last up-to-N
// prior messages, skipping nil and empty-content entries. Returns "" when there
// is nothing to render or context is disabled (maxContextMessages <= 0).
func renderContext(history []*schema.Message, maxContextMessages int) string {
	if maxContextMessages <= 0 || len(history) == 0 {
		return ""
	}

	start := len(history) - min(maxContextMessages, len(history))

	var b strings.Builder
	b.WriteString("Recent conversation (context only — do NOT answer or continue it):\n<context>")
	for _, m := range history[start:] {
		if m == nil || strings.TrimSpace(m.Content) == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(roleLabel(m.Role))
		b.WriteString(": ")
		b.WriteString(escapePromptData(m.Content))
	}
	b.WriteString("\n</context>")
	return b.String()
}

// roleLabel maps a message role to a human-readable label for the transcript.
func roleLabel(role schema.RoleType) string {
	switch role {
	case schema.User:
		return "User"
	case schema.Assistant:
		return "Assistant"
	case schema.Tool:
		return "Tool"
	case schema.System:
		return "System"
	default:
		return "Message"
	}
}

// escapePromptData neutralizes the structural delimiters and control characters
// in untrusted content (conversation history or the draft) before it is embedded
// in the enhancement prompt. Without this, a prior message or tool output could
// contain a literal "</context>" (or "<draft>") to break out of the framing and
// inject instructions into the enhancer model. This is best-effort hardening;
// it is not a substitute for treating the enhancer model's output as untrusted
// (see the system prompt's untrusted-data directive).
func escapePromptData(s string) string {
	s = promptDelimiterRe.ReplaceAllStringFunc(s, func(t string) string {
		// t looks like "<context>", "</context>", "< context>", "<context />", ...
		// Escape the angle brackets so the model treats it as literal text, not
		// a structural tag.
		inner := strings.TrimSuffix(t, ">")
		inner = strings.TrimPrefix(inner, "<")
		return "&lt;" + inner + "&gt;"
	})
	s = strings.Map(func(r rune) rune {
		if isPromptDataUnsafe(r) {
			return -1
		}
		return r
	}, s)
	return s
}

// isPromptDataUnsafe reports whether r is a control or formatting character
// that should not appear inside embedded prompt data: it could disrupt the
// transcript structure (NUL, ANSI escapes) or spoof rendering (Unicode
// bidirectional/formatting controls). Newlines, carriage returns, and tabs are
// preserved so multi-line content (commands, code) survives.
func isPromptDataUnsafe(r rune) bool {
	switch r {
	case '\n', '\r', '\t':
		return false
	}
	if r < 0x20 || r == 0x7f {
		return true
	}
	// Unicode bidi/formatting controls and zero-width marks used for spoofing.
	if (r >= 0x200e && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2066 && r <= 0x2069) || r == 0x061c || r == 0xfeff {
		return true
	}
	return false
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
