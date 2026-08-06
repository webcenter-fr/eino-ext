// Package activity exposes a Kilocode-style live "activity" stream of what
// agents, LLMs and tools are doing during an eino run, suitable for fan-out to a
// UI over SSE (or any other transport).
//
// The package is transport-agnostic: it has zero web dependencies. It is built
// from three layers, mirroring opencode/Kilocode's central typed event bus:
//
//   - Event model (this file): a flat Event envelope plus a catalog of typed
//     Data payloads (ported 1:1 from Kilocode's session-event catalog, dropping
//     the "session.next." type prefix).
//   - Bus (bus.go): an in-memory, per-session fan-out with bounded ring-buffer
//     replay for late subscribers reconnecting with a Last-Event-ID.
//   - Producer (handler.go): a callbacks.Handler that translates eino component
//     lifecycle into events; an optional adk bridge lives in adk.go.
//
// A Hertz SSE adapter lives in the sse sub-package, which is the only place that
// imports a web framework.
package activity

import (
	"time"

	"github.com/goccy/go-json"

	"emperror.dev/errors"
)

// Type is the string enum identifying the kind of an Event. Values mirror the
// Kilocode session-event catalog with the "session.next." prefix removed.
type Type string

const (
	// TypeStepStarted signals the start of a processing step.
	TypeStepStarted Type = "step.started"
	// TypeStepEnded signals the end of a processing step.
	TypeStepEnded Type = "step.ended"
	// TypeStepFailed signals a processing step failure.
	TypeStepFailed Type = "step.failed"
	// TypeAgentSwitched signals that the active agent has changed.
	TypeAgentSwitched Type = "agent.switched"
	// TypeModelSwitched signals that the active model has changed.
	TypeModelSwitched Type = "model.switched"
	// TypePrompted signals that a new prompt was submitted.
	TypePrompted Type = "prompted"

	// TypeTextStarted signals the start of text generation.
	// TypeTextStarted signals the start of text generation.
	TypeTextStarted Type = "text.started"
	// TypeTextDelta carries an incremental text chunk.
	TypeTextDelta Type = "text.delta"
	// TypeTextEnded signals the end of text generation.
	TypeTextEnded Type = "text.ended"

	// TypeReasoningStarted signals the start of reasoning output.
	TypeReasoningStarted Type = "reasoning.started"
	// TypeReasoningDelta carries an incremental reasoning text chunk.
	TypeReasoningDelta Type = "reasoning.delta"
	// TypeReasoningEnded signals the end of reasoning output.
	TypeReasoningEnded Type = "reasoning.ended"

	// TypeToolInputStarted signals that a tool invocation has begun receiving input.
	TypeToolInputStarted Type = "tool.input.started"
	// TypeToolInputDelta carries an incremental chunk of tool input.
	TypeToolInputDelta Type = "tool.input.delta"
	// TypeToolInputEnded signals the end of tool input streaming.
	TypeToolInputEnded Type = "tool.input.ended"
	// TypeToolCalled signals that a tool has been invoked.
	TypeToolCalled Type = "tool.called"
	// TypeToolProgress carries a progress update from a running tool.
	TypeToolProgress Type = "tool.progress"
	// TypeToolSuccess signals that a tool completed successfully.
	TypeToolSuccess Type = "tool.success"
	// TypeToolFailed signals that a tool invocation failed.
	TypeToolFailed Type = "tool.failed"

	// TypeRetried signals that an operation was retried.
	// TypeRetried signals that an operation was retried.
	TypeRetried Type = "retried"
	// TypeCompactionStarted signals the start of context compaction.
	TypeCompactionStarted Type = "compaction.started"
	// TypeCompactionDelta carries an incremental compaction chunk.
	TypeCompactionDelta Type = "compaction.delta"
	// TypeCompactionEnded signals the end of context compaction.
	TypeCompactionEnded Type = "compaction.ended"

	// TypeSessionEnded signals the end of a session.
	// TypeSessionEnded signals the end of a session.
	TypeSessionEnded Type = "session.ended"
)

// Event is the transport-agnostic envelope published on the Bus. Data carries one
// of the typed payloads declared below; it is JSON-tagged for SSE serialization.
type Event struct {
	// ID is a process-monotonic identifier ("evt_<n>") used for ordering and for
	// SSE Last-Event-ID replay. It is assigned by the Bus on Publish.
	ID string `json:"id"`
	// SessionID is the fan-out key. The empty string is a valid bucket.
	SessionID string `json:"sessionID"`
	// Type identifies the concrete Data payload.
	Type Type `json:"type"`
	// Agent is the name of the agent that produced the event, populated from
	// WithAgent on the run context. It is empty for single-agent runs. The
	// Handler also merges it into the SSE data body (see MarshalSSEData) so a UI
	// can route events by agent without reading a non-standard SSE field.
	Agent string `json:"agent,omitempty"`
	// Timestamp is when the event was produced.
	Timestamp time.Time `json:"timestamp"`
	// Data is one of the typed payloads in this file (or nil).
	Data any `json:"data,omitempty"`
}

// Tokens mirrors Kilocode's step.ended token breakdown.
type Tokens struct {
	Input     int         `json:"input"`
	Output    int         `json:"output"`
	Reasoning int         `json:"reasoning"`
	Cache     CacheTokens `json:"cache"`
}

// CacheTokens is the cache-read/write breakdown of Tokens.
type CacheTokens struct {
	Read  int `json:"read"`
	Write int `json:"write"`
}

// ErrorData is the structured error payload shared by failure events.
type ErrorData struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// --- Lifecycle payloads ---

// StepStarted carries the agent and model that began a step.
type StepStarted struct {
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
}

// StepEnded carries the finish reason, cost and token usage of a completed step.
type StepEnded struct {
	Finish string  `json:"finish,omitempty"`
	Cost   float64 `json:"cost"`
	Tokens Tokens  `json:"tokens"`
	// Estimated is true when Tokens (and any derived Cost) were computed by a
	// client-side heuristic TokenCounter fallback (see Handler.WithTokenCounter)
	// because the gateway did not report real usage for this step. Omitted
	// entirely (false) when Tokens came from real gateway usage.
	Estimated bool `json:"estimated,omitempty"`
}

// StepFailed carries the error that aborted a step.
type StepFailed struct {
	Error ErrorData `json:"error"`
}

// AgentSwitched carries the newly active agent name.
type AgentSwitched struct {
	Agent string `json:"agent"`
}

// ModelSwitched carries the newly active model reference.
type ModelSwitched struct {
	Model string `json:"model"`
}

// Prompted carries the prompt text that initiated a run.
type Prompted struct {
	Prompt string `json:"prompt"`
}

// --- Text payloads ---

// TextStarted marks the beginning of assistant text output. It has no fields.
type TextStarted struct{}

// TextDelta carries an incremental chunk of assistant text.
type TextDelta struct {
	Delta string `json:"delta"`
}

// TextEnded carries the full assistant text once complete.
type TextEnded struct {
	Text string `json:"text"`
}

// --- Reasoning payloads ---

// ReasoningStarted marks the beginning of a reasoning block.
type ReasoningStarted struct {
	ReasoningID string `json:"reasoningID"`
}

// ReasoningDelta carries an incremental chunk of reasoning text.
type ReasoningDelta struct {
	ReasoningID string `json:"reasoningID"`
	Delta       string `json:"delta"`
}

// ReasoningEnded carries the full reasoning text once complete.
type ReasoningEnded struct {
	ReasoningID string `json:"reasoningID"`
	Text        string `json:"text"`
}

// --- Tool payloads ---

// ToolInputStarted marks the beginning of streamed tool-call arguments.
type ToolInputStarted struct {
	CallID string `json:"callID"`
	Name   string `json:"name"`
}

// ToolInputDelta carries an incremental chunk of tool-call arguments.
type ToolInputDelta struct {
	CallID string `json:"callID"`
	Delta  string `json:"delta"`
}

// ToolInputEnded carries the full tool-call arguments once complete.
type ToolInputEnded struct {
	CallID string `json:"callID"`
	Text   string `json:"text"`
}

// ToolCalled marks a tool invocation with its resolved arguments.
type ToolCalled struct {
	CallID string `json:"callID"`
	Tool   string `json:"tool"`
	Input  string `json:"input"`
}

// ToolProgress carries an intermediate tool result chunk.
type ToolProgress struct {
	CallID  string `json:"callID"`
	Content string `json:"content"`
}

// ToolSuccess carries the final tool result content.
type ToolSuccess struct {
	CallID  string `json:"callID"`
	Content string `json:"content"`
}

// ToolFailed carries the error a tool returned.
type ToolFailed struct {
	CallID string    `json:"callID"`
	Error  ErrorData `json:"error"`
}

// --- Misc payloads ---

// Retried marks a retried attempt with its error.
type Retried struct {
	Attempt int       `json:"attempt"`
	Error   ErrorData `json:"error"`
}

// CompactionStarted marks the beginning of a context compaction.
type CompactionStarted struct {
	Reason string `json:"reason"`
}

// CompactionDelta carries an incremental chunk of compaction output.
type CompactionDelta struct {
	Text string `json:"text"`
}

// CompactionEnded carries the final compaction output.
type CompactionEnded struct {
	Text string `json:"text"`
}

// SessionEnded marks the end of a session with duration, cost, and usage statistics.
type SessionEnded struct {
	Duration time.Duration `json:"duration"`
	Cost     float64       `json:"cost"`
	Steps    int           `json:"steps"`
	Tools    int           `json:"tools"`
}

// MarshalSSEData renders the JSON body written after the SSE "data:" field for an
// Event. It marshals the Data payload and, when Event.Agent is set, merges an
// "agent" key into the emitted JSON object so a browser can route events by
// agent with const { agent } = JSON.parse(e.data). The envelope metadata (id,
// type) still travels in the SSE frame fields, mirroring the Kilocode wire
// format.
//
// The function is tolerant of non-object payloads: when Data does not marshal to
// a JSON object (e.g. it is nil, a string, or an array), the original payload is
// returned unchanged unless an agent is set, in which case a {"agent": "..."}
// object is emitted for nil Data. This keeps existing consumers working.
func MarshalSSEData(e Event) ([]byte, error) {
	b, err := json.Marshal(e.Data)
	if err != nil {
		return nil, errors.Wrap(err, "activity: failed to marshal event data")
	}
	if e.Agent == "" {
		return b, nil
	}
	// Merge the agent key into the payload object when possible.
	var obj map[string]any
	if e.Data == nil {
		obj = map[string]any{}
	} else if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		// Non-object payload (string/array/number): cannot merge an agent key
		// without changing its shape; preserve the original body.
		return b, nil
	}
	obj["agent"] = e.Agent
	merged, err := json.Marshal(obj)
	if err != nil {
		return nil, errors.Wrap(err, "activity: failed to marshal event data with agent")
	}
	return merged, nil
}
