# Log Callback Handler

A `callbacks.Handler` implementation that logs eino component lifecycle events
via [logrus](https://github.com/sirupsen/logrus) with structured fields.

## Usage

```go
import (
    "github.com/sirupsen/logrus"
    "github.com/cloudwego/eino/callbacks"
    loghandler "github.com/webcenter-fr/eino-ext/callbacks/log"
)

func main() {
    logger := logrus.WithField("service", "my-agent")
    logger.Logger.SetLevel(logrus.TraceLevel)

    h := loghandler.NewHandler(logger)
    callbacks.AppendGlobalHandlers(h)

    // ... run your eino graph ...
}
```

## Log Levels

| Event     | Level |
|-----------|-------|
| OnStart   | Trace |
| OnEnd     | Trace |
| OnError   | Debug |

Set `logrus.TraceLevel` to see full lifecycle events.

## Structured Fields

Every log entry includes:

| Field            | Source            |
|------------------|-------------------|
| `component`      | `RunInfo.Component` (e.g. `ChatModel`, `Tool`) |
| `component_name` | `RunInfo.Name` (graph node name) |
| `component_type` | `RunInfo.Type` (e.g. `openai/gpt-4o`) |
| `agent`          | Context value set by `activity.WithAgent` or `agentattr` middleware |

### ChatModel entries

- **start**: `messages` (message count)
- **end**: `content`, `reasoning`, `prompt_tokens`, `completion_tokens`, `total_tokens`, `finish_reason`

### Tool entries

- **start**: `input` (JSON arguments)
- **end**: `output` (response)

### Error entries

- `error` (error string)

## Multi-Agent Support

When using the `agentattr` ADK middleware (`components/middleware/agentattr`),
the handler automatically picks up the agent name from the context and includes
it in the `agent` field, making it easy to trace which agent produced each log
line in supervisor / sub-agent topologies.
