# agentattr

An [eino adk](https://github.com/cloudwego/eino) `ChatModelAgentMiddleware` that
attributes the [`callbacks/activity`](../../../callbacks/activity) event stream
to a named agent.

In a multi-agent run (supervisor + sub-agents) the activity `Handler` emits
`text.*`, `tool.*` and `step.*` events that, by default, identify only the
model. Registering this middleware on an agent's `Handlers` tags every activity
event produced during that agent's run with the agent's name, so a UI can route
a supervisor's final answer separately from a sub-agent's intermediate chatter.

## How it works

The middleware threads `activity.WithAgent(ctx, name)` onto the context that
reaches the component callbacks for both the model and tool calls of the agent:

- `BeforeAgent` / `BeforeModelRewriteState` set it on the context propagated to
  the model invocation (and therefore the ChatModel callbacks);
- the `WrapToolCall` methods set it on the context passed to each tool endpoint
  (and therefore the Tool callbacks).

No adk event bridge is introduced; the single global `callbacks.Handler` stays
in place. The activity `Handler` then sets `Event.Agent`, merges an `agent` key
into the SSE `data` JSON, and emits a single `agent.switched` event per session
on each agent transition.

## Usage

```go
import (
    "github.com/cloudwego/eino/adk"

    "github.com/webcenter-fr/eino-ext/components/middleware/agentattr"
)

mw, err := agentattr.New(&agentattr.Config{AgentName: "supervisor"})
if err != nil {
    return err
}

agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Name:  "supervisor",
    Model: m,
    Handlers: []adk.ChatModelAgentMiddleware{mw},
})
```

`AgentName` is required.

## Without adk

The middleware is a convenience wrapper. Drivers that do not use adk can set
`activity.WithAgent(ctx, name)` directly on each sub-agent's run context and get
the same attribution.
