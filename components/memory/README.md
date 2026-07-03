# memory — Conversation history persistence

`memory` defines the core `Memory` and `Conversation` interfaces for persisting
and retrieving LLM conversation histories, plus marker utilities for metadata
annotation (summary, incomplete, ephemeral).

## Interfaces

```go
type Memory interface {
    GetConversation(userId, id string, createIfNotExist bool) (Conversation, error)
    ListConversations(userId string) ([]string, error)
    DeleteConversation(userId, id string) error
}

type Conversation interface {
    Append(msg *schema.Message) error
    GetFullMessages() []*schema.Message
    GetMessages(lastSummaryIdx int) []*schema.Message
    AppendSummary(summary *schema.Message) error
    LastSummaryIndex() int
    GetWindow(budget int) ([]*schema.Message, error)
    CountTokens() int
    Load() error
    Save() error
}
```

## Markers

Messages can carry metadata markers via their `Extra` map:

- `SummaryMarkerKey` — marks a message as a conversation summary.
- `IncompleteMarkerKey` — marks a message as incomplete (e.g. interrupted
  streaming).
- `EphemeralMarkerKey` — marks a message that should not be persisted.

Helper functions: `IsSummary`, `NewSummaryMessage`, `MarkIncomplete`,
`IsIncomplete`, `NewEphemeralMessage`, `IsEphemeral`.

## Sub-packages

### [`file`](./file) — File-based JSONL implementation

Implements `Memory`/`Conversation` backed by JSONL files. Supports token-budgeted
windowing with binary-search trimming and per-file locking.

### [`session`](./session) — Turn lifecycle manager

Provides the cross-request lifecycle on top of any `Memory` store: per-session
locking, turn lifecycle (`BeginTurn → Condense → Window → CommitAssistant/Discard`),
and optional summarization-based condensation with configurable thresholds.

### [`runner`](./runner) — ADK Runner bridge

Bridges an ADK `Runner.Run` to the session turn lifecycle: splits the agent event
stream into client streaming and persistence copies, with incomplete/ephemeral
message handling and no-dangling-user guarantee.

## Usage

```go
import (
    "github.com/webcenter-fr/eino-ext/components/memory"
    "github.com/webcenter-fr/eino-ext/components/memory/file"
    "github.com/webcenter-fr/eino-ext/components/memory/session"
)

mem, _ := file.NewFileMemory(file.FileMemoryConfig{
    Dir: "/var/eino/conversations",
})

sm, _ := session.NewSessionManager(session.Config{
    Memory: mem,
})

turn, _ := sm.BeginTurn("user-123", "conv-456", userMsg)
defer turn.Discard() // rollback on error

window, _ := turn.Window(4000)
turn.CommitAssistant(assistantMsg)
```
