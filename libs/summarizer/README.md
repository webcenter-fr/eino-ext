# summarizer — Conversation summarization interface

`summarizer` defines the interface used for condensing conversation history into
anchored summaries.

## Interface

```go
type Summarizer interface {
    Summarize(ctx context.Context, history []*schema.Message,
               previousSummary string) (string, error)
}
```

- `history` — the messages to summarize (including any previously summarized
  content).
- `previousSummary` — the prior summary text, if any (empty string for the first
  summarization pass).
- Returns the updated summary text.

## Adapter

```go
type SummarizerFunc func(ctx context.Context, history []*schema.Message,
                         previousSummary string) (string, error)

func (f SummarizerFunc) Summarize(ctx context.Context, history []*schema.Message,
                                   previousSummary string) (string, error)
```

Use `SummarizerFunc` to convert a plain function into a `Summarizer`.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/summarizer"

var s summarizer.Summarizer = summarizer.SummarizerFunc(
    func(ctx context.Context, history []*schema.Message, prev string) (string, error) {
        // call an LLM to produce a summary
        return result, nil
    },
)
```

## Related

- `components/memory/session` — uses a `Summarizer` for conversation
  condensation during turn lifecycle.
