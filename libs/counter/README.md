# counter — Token counting utilities

`counter` provides a `TokenCounter` function type and a default implementation
for estimating token counts from message content.

## Interface

```go
type TokenCounter func(msgs []*schema.Message) int
```

`DefaultTokenCounter` estimates tokens as `total_characters / 4`, with a minimum
of 1 token if content exists. It counts characters from both message content and
tool call arguments.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/counter"

count := counter.DefaultTokenCounter(msgs)
```

## When to replace

The default is a fast approximation. For accurate token counting, replace with a
provider-specific tokenizer (e.g. `tiktoken` for OpenAI models) by providing a
custom `TokenCounter` to components that accept one.
