# chatmodel — provider-generic chat-model factory (thinking levels + output cap)

`chatmodel` builds an eino `model.ToolCallingChatModel` from a single, additive
`Config`. It maps a provider-generic thinking *level* onto each provider's
reasoning configuration and caps output tokens, so the same construction logic
is reusable across eino projects regardless of the concrete provider.

It is a sibling of `components/model/cachestab`: the value returned by `New` is
a plain `model.ToolCallingChatModel` you can further decorate (for example with
`cachestab.NewToolCallingChatModel`).

## Thinking levels

`ThinkingLevel` is provider-generic with four values: `Off`, `Low`, `Medium`,
`High`. Use `ParseThinkingLevel` to parse user-facing strings:

| input                 | result   |
| --------------------- | -------- |
| `""`, `off`           | `Off`    |
| `false`, `none`       | `Off`    |
| `true`                | `Medium` |
| `low` / `medium` / `high` | matching level |
| anything else         | error    |

Parsing is case-insensitive and trims whitespace.

### Provider semantics

- **OpenAI / github-copilot** (`openai@v0.1.13`) supports only `Low`/`Medium`/`High`
  reasoning effort — there is no `none`/`minimal`/`xhigh`. `Off` means *omit
  reasoning*: the `ReasoningEffort` field is left unset, so non-reasoning models
  are unaffected.
- **Ollama** has no reasoning *levels* — only a boolean think toggle. Any
  non-`Off` level therefore collapses to `true`, and `Off` to `false`.

## Config

| field             | meaning                                                         |
| ----------------- | --------------------------------------------------------------- |
| `Plan`            | provider: `ollama`, `github-copilot`, or `openai`               |
| `BaseURL`         | provider endpoint URL                                           |
| `Model`           | model ID                                                        |
| `Temperature`     | sampling temperature                                            |
| `Thinking`        | `ThinkingLevel` (`Off` omits reasoning)                         |
| `MaxOutputTokens` | output-token cap; `0` leaves the provider default unset         |
| `Timeout`         | request timeout (openai/github-copilot path); `0` uses the 60m default |

`github-copilot` and `openai` share the OpenAI-compatible construction path.

For the Ollama path, `MaxOutputTokens` maps to `Options.NumPredict` (Ollama's
output-token equivalent) when greater than zero; `Timeout` is openai/github-copilot only.

## Output-token capping

`CapOutputTokens(modelOutputLimit, ceiling int) int` mirrors kilocode's
output-token capping:

- if `ceiling <= 0` it defaults to `OutputTokenMax` (32000);
- if `modelOutputLimit <= 0` (unknown) it returns the ceiling;
- otherwise it returns `min(modelOutputLimit, ceiling)`.

## Usage

```go
ctx := context.Background()

m, err := chatmodel.New(ctx, &chatmodel.Config{
    Plan:            "openai", // or "github-copilot" / "ollama"
    BaseURL:         baseURL,
    Model:           modelID,
    Temperature:     0.7,
    Thinking:        chatmodel.High,
    MaxOutputTokens: chatmodel.CapOutputTokens(modelLimit, 0),
})
if err != nil {
    return err
}

// Optionally decorate for prompt-cache stability:
m, err = cachestab.NewToolCallingChatModel(m)
```

## Runnable examples

See [`example_test.go`](./example_test.go):

- `ExampleParseThinkingLevel` — string → level mapping.
- `ExampleCapOutputTokens` — output-token capping semantics.
- `ExampleNew` — construct a model for an OpenAI-compatible endpoint.

```bash
go test ./components/model/chatmodel/ -run Example -v
```
