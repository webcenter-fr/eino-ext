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

## How it works

1. Sends a system prompt instructing the model to rewrite, not answer
2. Wraps the draft in a user message with `<draft>` tags
3. Post-processes the output: strips markdown fences, surrounding quotes, and whitespace

## Configuration

| Field | Required | Description |
|-------|----------|-------------|
| `Model` | Yes | Small/fast model for enhancement (claude-haiku, gemini-flash, gpt-5-nano) |
| `SystemPrompt` | No | Override the default enhancement system prompt |
