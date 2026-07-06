# memory/opensearch

An OpenSearch-backed implementation of the eino `memory.Memory` interface for conversation history persistence.

## How it works

Each conversation is stored as a single document in an OpenSearch index. The document ID is `{userId}:{conversationId}`. Messages are stored as a JSON array within the document. On first access, the document is loaded into memory.

- **Index creation** — On construction, the index is created automatically if it does not exist, with the configured number of replicas (default 1) and appropriate mappings.
- **Window management** — `GetMessages` returns up to `MaxWindowSize` most recent messages. `GetWindow(budget)` returns the last summary + following messages bounded by a token budget (binary search over an additive `TokenCounter`).
- **Summaries** — `AppendSummary` marks a message as a summary. `GetWindow` always preserves the most recent summary as a fixed prefix.
- **Persistence** — Messages are persisted to OpenSearch immediately on `Append`.

## Configuration

```go
import (
    "github.com/webcenter-fr/eino-ext/components/memory"
    "github.com/webcenter-fr/eino-ext/components/memory/opensearch"
)

mem, err := opensearch.NewOpenSearchMemory(opensearch.Config{
    URLs:            []string{"https://localhost:9200"},
    Username:        "admin",
    Password:        "admin",
    IndexName:       "eino_memory",         // defaults to "eino_memory"
    NumReplicas:     1,                     // defaults to 1
    MaxWindowSize:   20,                    // max messages in GetMessages
    MaxWindowTokens: 8192,                  // token budget for GetWindow, 0 disables
    TokenCounter:    memory.DefaultTokenCounter,
    TLSSkipVerify:   false,
})
if err != nil {
    return err
}

conv, err := mem.GetConversation("user-123", "chat-456", true)
if err != nil {
    return err
}
conv.Append(userMsg)
conv.Append(assistantMsg)

window := conv.GetWindow(4096)
```

## Index mapping

The index is created with the following mapping:

| Field            | Type   | Description                                        |
|------------------|--------|----------------------------------------------------|
| `userId`         | keyword | User identifier, used for listing conversations    |
| `conversationId`  | keyword | Conversation identifier, part of the document ID   |
| `messages`       | object (dynamic: false) | JSON array of message objects, stored but not indexed |
| `updatedAt`      | date   | ISO 8601 timestamp of last update                  |

Dynamic mapping is disabled at index level to prevent mapping explosion from `schema.Message.Extra` fields.

## Conversation API

```go
type Conversation interface {
    Append(msg *schema.Message)
    AppendSummary(summary *schema.Message)
    GetMessages() []*schema.Message        // bounded by MaxWindowSize
    GetFullMessages() []*schema.Message
    GetWindow(budget int) []*schema.Message  // [last summary + following], token-bounded
    LastSummaryIndex() int
    CountTokens() int
    Load() error
    Save(msg *schema.Message) error
}
```
