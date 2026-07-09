// Package promptenhance provides an ADK middleware that enhances user prompts
// through a small model and optionally asks for human approval before sending
// the enhanced version to the supervisor.
package promptenhance

import (
	"context"

	libspromptenhance "github.com/webcenter-fr/eino-ext/libs/promptenhance"
)

// ShouldEnhanceFunc is called before any enhancement. When it returns false,
// the middleware skips enhancement entirely for this call (no LLM call, no
// interrupt). The consumer implements this to check persistent user preferences
// (e.g., a "skip always" toggle in the UI stored in a session or database).
type ShouldEnhanceFunc func(ctx context.Context) bool

// Config configures the prompt-enhance middleware.
type Config struct {
	// Enhancer is the prompt enhancer used to rewrite drafts.
	Enhancer *libspromptenhance.Enhancer `validate:"required" jsonschema:"required,description=Prompt enhancer instance"`

	// AutoAccept, when true, skips user approval and always uses the enhanced
	// version. Useful for CI, automated runs, or when the enhancer is already
	// trusted.
	AutoAccept bool `jsonschema:"description=Skip user approval and always use enhanced prompt"`

	// ShouldEnhance, when non-nil, is called before any enhancement. When it
	// returns false, the middleware skips enhancement entirely (no LLM call,
	// no interrupt). Use this to implement per-user "skip always" preferences.
	// When nil, enhancement always proceeds (subject to AutoAccept).
	ShouldEnhance ShouldEnhanceFunc `jsonschema:"-"`
}

// InterruptInfo is the data presented to the user when the middleware interrupts.
type InterruptInfo struct {
	// Original is the user's original draft prompt.
	Original string `json:"original" jsonschema:"description=The user's original draft prompt"`

	// Enhanced is the enhanced version suggested by the model.
	Enhanced string `json:"enhanced" jsonschema:"description=The enhanced version suggested by the model"`
}

// Choice is the user's response when resuming after an interrupt.
type Choice struct {
	// Action is one of "original", "enhanced", "modified", or "skip_always".
	// "skip_always" tells the consumer to persist the preference; the
	// middleware itself does NOT store this — the consumer must use
	// ShouldEnhance to enforce it on subsequent calls.
	Action string `json:"action" validate:"oneof=original enhanced modified skip_always" jsonschema:"description=User's decision: original, enhanced, modified, or skip_always"`

	// Text contains the user's modified prompt when Action is "modified".
	Text string `json:"text,omitempty" jsonschema:"description=User's modified prompt, required when action is modified"`
}

// InterruptError is returned by BeforeModelRewriteState when user approval is needed.
// The consumer catches this error from the runner event stream, presents the UI,
// and re-runs the agent with the user's choice in context via WithChoice.
type InterruptError struct {
	InterruptInfo
}

func (e *InterruptError) Error() string {
	return "promptenhance: waiting for user approval"
}

type choiceCtxKey struct{}

// WithChoice stores the user's choice in context for the next agent run.
func WithChoice(ctx context.Context, choice *Choice) context.Context {
	return context.WithValue(ctx, choiceCtxKey{}, choice)
}

func getChoiceFromCtx(ctx context.Context) *Choice {
	if v := ctx.Value(choiceCtxKey{}); v != nil {
		return v.(*Choice)
	}
	return nil
}
