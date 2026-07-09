# Plan: Activity event storage on Conversation

## Goal

Add activity event persistence to the `Conversation` interface so the backend can store activity timeline events alongside chat messages. Events are collected during a run from the activity `Bus`, serialized as JSON, and stored per-conversation. The UI replays stored events when revisiting old discussions.

## Changes

### 1. `components/memory/conversation.go`

Add import: `"github.com/goccy/go-json"`.

Add to `Conversation` interface:

```go
// GetActivities returns all stored activity events.
GetActivities() []json.RawMessage

// SetActivities replaces all stored activity events (batch write after run).
SetActivities(raw []json.RawMessage)
```

Each `json.RawMessage` is one stored event:
```json
{"type":"step.started","agent":"supervisor","data":{"model":"gpt-4"}}
```
(`data` is `activity.MarshalSSEData()` output — identical to SSE wire payload.)

### 2. `components/memory/file/file.go`

**`FileConversation` struct** — add field:
```go
Activities []json.RawMessage
```

**New methods:**
```go
func (c *FileConversation) GetActivities() []json.RawMessage {
    c.mu.Lock(); defer c.mu.Unlock(); return c.Activities
}

func (c *FileConversation) SetActivities(raw []json.RawMessage) {
    c.mu.Lock(); defer c.mu.Unlock()
    c.Activities = raw
    c.saveActivities()
}
```

**Side-file persistence** (keeps message JSONL unchanged):
```go
func (c *FileConversation) activitiesPath() string {
    return filepath.Dir(c.filePath) + "/" + filepath.Base(c.filePath) + ".activities"
}

func (c *FileConversation) saveActivities() error {
    data, err := json.Marshal(c.Activities)
    if err != nil { return errors.Wrap(err, "failed to marshal activities") }
    return os.WriteFile(c.activitiesPath(), data, 0o644)
}
```

**`Load()`** — append after existing JSONL loop:
```go
data, err := os.ReadFile(c.activitiesPath())
if err != nil && !os.IsNotExist(err) {
    return errors.Wrap(err, "failed to read activities file")
}
if len(data) > 0 {
    if err := json.Unmarshal(data, &c.Activities); err != nil {
        return errors.Wrap(err, "failed to unmarshal activities")
    }
}
```

**`FileMemory.DeleteConversation`** — also remove side-file:
```go
os.Remove(filePath + ".activities")
```

### 3. `components/memory/opensearch/opensearch.go`

**`OpenSearchConversation`** — add `Activities []json.RawMessage`

**`conversationDoc`** — add `Activities []json.RawMessage \`json:"activities"\``

**`GetActivities` / `SetActivities`** — lock + save, same pattern as file backend.

**`Load()`** — after `c.Messages = doc.Messages`: `if doc.Activities != nil { c.Activities = doc.Activities }`

**Index mapping** in `createIndex` — add to `"properties"`:
```go
"activities": map[string]any{"type": "object", "dynamic": false},
```

### 4. Validation

```bash
cd /projects/eino-ext && go build ./components/memory/... && go test ./components/memory/...
```

No tests modified unless existing tests break (unlikely — new methods only, no change to existing interface compliance).

## Design notes

- **Batch write**: Activities are written once per run end (not per-event). The consumer (`rancher-doc-chat-api-k8s`) calls `SetActivities` after the run completes with all collected events.
- **Side-file**: File backend uses `{id}.activities` so message JSONL stays append-only.
- **No migration**: Old conversations without activity data → `GetActivities()` returns empty nil slice.
