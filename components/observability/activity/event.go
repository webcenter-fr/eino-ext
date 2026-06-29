/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
	"encoding/json"
	"time"

	"emperror.dev/errors"
)

// Type is the string enum identifying the kind of an Event. Values mirror the
// Kilocode session-event catalog with the "session.next." prefix removed.
type Type string

const (
	// Lifecycle.
	TypeStepStarted   Type = "step.started"
	TypeStepEnded     Type = "step.ended"
	TypeStepFailed    Type = "step.failed"
	TypeAgentSwitched Type = "agent.switched"
	TypeModelSwitched Type = "model.switched"
	TypePrompted      Type = "prompted"

	// Text.
	TypeTextStarted Type = "text.started"
	TypeTextDelta   Type = "text.delta"
	TypeTextEnded   Type = "text.ended"

	// Reasoning.
	TypeReasoningStarted Type = "reasoning.started"
	TypeReasoningDelta   Type = "reasoning.delta"
	TypeReasoningEnded   Type = "reasoning.ended"

	// Tool.
	TypeToolInputStarted Type = "tool.input.started"
	TypeToolInputDelta   Type = "tool.input.delta"
	TypeToolInputEnded   Type = "tool.input.ended"
	TypeToolCalled       Type = "tool.called"
	TypeToolProgress     Type = "tool.progress"
	TypeToolSuccess      Type = "tool.success"
	TypeToolFailed       Type = "tool.failed"

	// Misc (parity).
	TypeRetried           Type = "retried"
	TypeCompactionStarted Type = "compaction.started"
	TypeCompactionDelta   Type = "compaction.delta"
	TypeCompactionEnded   Type = "compaction.ended"
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

// MarshalSSEData renders the JSON body written after the SSE "data:" field for an
// Event. It marshals only the Data payload, mirroring the Kilocode wire format
// where the envelope metadata (id, type) travels in the SSE frame fields.
func MarshalSSEData(e Event) ([]byte, error) {
	b, err := json.Marshal(e.Data)
	if err != nil {
		return nil, errors.Wrap(err, "activity: failed to marshal event data")
	}
	return b, nil
}
