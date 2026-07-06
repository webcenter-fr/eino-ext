# memory/file

A JSONL file-backed implementation of the eino `memory.Memory` interface for
conversation history persistence.

## How it works

Each conversation is stored as a separate JSONL file under
`<dir>/<userId>/<conversationId>.jsonl`. Messages are appended one-per-line
as JSON objects. On first access, the file is loaded into memory.

- **Window management** — `GetMessages` returns up to `MaxWindowSize` most
  recent messages. `GetWindow(budget)` returns the last summary + following
  messages bounded by a token budget (binary search over an additive
  `TokenCounter`).
- **Summaries** — `AppendSummary` marks a message as a summary. `GetWindow`
  always preserves the most recent summary as a fixed prefix.
- **Persistence** — Messages are written to disk immediately on `Append`.

## Configuration

```go
import (
    "github.com/webcenter-fr/eino-ext/components/memory"
    "github.com/webcenter-fr/eino-ext/components/memory/file"
)

mem := file.NewFileMemory(file.FileMemoryConfig{
    Dir:             "/var/data/eino/memory",  // defaults to /tmp/eino/memory
    MaxWindowSize:   20,                       // max messages in GetMessages
    MaxWindowTokens: 8192,                     // token budget for GetWindow, 0 disables
    TokenCounter:    memory.DefaultTokenCounter, // defaults to DefaultTokenCounter
})

conv, err := mem.GetConversation("user-123", "chat-456", true)
if err != nil {
    return err
}
conv.Append(userMsg)
conv.Append(assistantMsg)

// Read window bounded by token budget
window := conv.GetWindow(4096)
```

## Conversation API

```go
type Conversation interface {
    Append(msg *schema.Message)
    AppendSummary(summary *schema.Message)
    GetMessages() []*schema.Message       // bounded by MaxWindowSize
    GetFullMessages() []*schema.Message
    GetWindow(budget int) []*schema.Message // [last summary + following], token-bounded
    LastSummaryIndex() int
    CountTokens() int
}
```

## Utility

```go
// Default in-memory instance with MaxWindowSize=10
mem := file.GetDefaultMemory()
```
