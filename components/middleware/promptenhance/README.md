# promptenhance — Prompt enhancement ADK middleware

Middleware that enhances user prompts through a small model and optionally asks
for human approval before the enhanced prompt reaches the supervisor.

## Usage

```go
import (
    promptenhance "github.com/webcenter-fr/eino-ext/components/middleware/promptenhance"
    libspromptenhance "github.com/webcenter-fr/eino-ext/libs/promptenhance"
)

smallModel, _ := chatmodel.New(ctx, &chatmodel.Config{
    Provider: "openai",
    Model:    "gpt-5-nano",
})

enhancer, _ := libspromptenhance.NewEnhancer(ctx, &libspromptenhance.Config{
    Model: smallModel,
})

middleware, _ := promptenhance.NewMiddleware(&promptenhance.Config{
    Enhancer: enhancer,
})

agent := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Handlers: []adk.ChatModelAgentMiddleware{middleware},
    // ...
})
```

## Interrupt/resume flow

When `AutoAccept` is false, the middleware returns an `InterruptError` on first
call. The consumer catches this, presents the UI, and re-runs the agent with the
user's choice in context:

```go
events := runner.Run(ctx, messages)
for event := range events {
    var intErr *promptenhance.InterruptError
    if errors.As(event.Err, &intErr) {
        choice := showEnhanceUI(intErr.Original, intErr.Enhanced)

        if choice.Action == "skip_always" {
            userPrefs.SetSkipAlways(userID, true)
        }

        ctx = promptenhance.WithChoice(ctx, choice)
        // Re-run with the user's choice — middleware detects it via context.
        events = runner.Run(ctx, messages)
        continue
    }
    // normal event processing
}
```

### Choice actions

| Action | Behavior |
|--------|----------|
| `original` | Keep the original draft unchanged |
| `enhanced` | Use the model-suggested enhancement |
| `modified` | Use the user's custom edit (in `Text` field) |
| `skip_always` | Keep the original; consumer persists preference for `ShouldEnhance` |

## Configuration

| Field | Required | Description |
|-------|----------|-------------|
| `Enhancer` | Yes | Prompt enhancer instance from `libs/promptenhance` |
| `AutoAccept` | No | Skip user approval, always use enhanced prompt |
| `ShouldEnhance` | No | Callback to skip enhancement per-user (e.g., "skip always" toggle) |
