# promptenhance — Prompt rewriting library

`promptenhance` provides prompt rewriting using a small/cheap model. It
implements the same enhancement strategy as kilocode's "Enhance Prompt" button.

## Usage

```go
import (
    "github.com/webcenter-fr/eino-ext/libs/promptenhance"
)

smallModel, _ := chatmodel.New(ctx, &chatmodel.Config{
    Provider: "openai",
    Model:    "gpt-5-nano",
})

enhancer, _ := promptenhance.NewEnhancer(ctx, &promptenhance.Config{
    Model: smallModel,
})

enhanced, err := enhancer.Enhance(ctx, "my rough draft prompt")
```

`EnhanceInContext` also accepts the prior conversation history and embeds a
bounded, role-labelled transcript of the most-recent messages into the draft so
the model can resolve references ("it", "that command", a bare identifier):

```go
import (
    "github.com/cloudwego/eino/schema"
)

history := []*schema.Message{
    {Role: schema.User, Content: "deploy the api service"},
    {Role: schema.Assistant, Content: "done, running in cluster prod"},
}

// "re run it" is resolved against the context above.
enhanced, err := enhancer.EnhanceInContext(ctx, history, "re run it")
```

`Enhance` remains available as a no-context wrapper (it delegates to
`EnhanceInContext` with a nil history).

## How it works

1. Sends a system prompt instructing the model to rewrite, not answer
2. Wraps the draft in a user message with `<draft>` tags
3. Post-processes the output: strips markdown fences, surrounding quotes, and whitespace

When conversation context is provided, it is rendered as a role-labelled
compact transcript inside the single user message — never as real role
messages, and never answered — bounded to the most-recent
`MaxContextMessages` messages.

## Configuration

| Field | Required | Description |
|-------|----------|-------------|
| `Model` | Yes | Small/fast model for enhancement (claude-haiku, gemini-flash, gpt-5-nano) |
| `SystemPrompt` | No | Override the default enhancement system prompt |
| `MaxContextMessages` | No | Max prior messages included as conversation context (default 6; 0/negative → default; context is embedded in the user message, bounded for token/cost control) |

## Security considerations

- **Third-party data egress.** Conversation context (including tool outputs) is
  sent to the configured `Model` on every enhancement. Ensure that model's
  provider is authorized to receive the data in your history; the model
  receives only `Content` text (up to `MaxContextMessages` messages), not the
  message `Extra`/metadata fields.
- **Prompt injection is an inherent, residual risk.** Prior user messages,
  assistant text, and tool outputs are embedded into the enhancer prompt and can
  contain hostile instructions (direct or indirect prompt injection). The
  library mitigates this by (a) escaping the `<context>`/`<draft>` structural
  delimiters and stripping control characters from embedded content, and (b)
  instructing the model to treat the context and draft as untrusted data. These
  reduce but do not eliminate the risk — a determined prompt-injection payload
  (e.g. embedded in tool output) may still influence the enhanced output, which
  is then passed to your main model in the user-message position. Treat the
  enhancer's output as untrusted and apply independent guardrails to the
  downstream supervisor.
- **Token/cost amplification.** A single oversized message (e.g. a large tool
  output) is rendered verbatim up to the model's own limits; `MaxContextMessages`
  bounds the message *count*, not the byte size of any one message. Bound tool
  output sizes in your own tools if this is a concern.
