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

// Package runner bridges an eino adk.Runner run to the cross-request session
// lifecycle (session.Turn) so that any eino project gets streaming + history
// persistence out of the box, without re-implementing the glue per project.
//
// Run consumes the adk.AsyncIterator returned by adk.Runner.Run and splits it
// into two concurrent halves over a single duplicated stream (schema Copy(2)):
//
//   - the proxy goroutine reads the iterator and forwards the selected
//     assistant tokens/messages to the returned StreamReader, token by token for
//     streaming events; it is what the caller streams back to the client;
//   - the persistence goroutine drains the second copy, concatenates the full
//     assistant answer and commits it through the Turn.
//
// Guarantees:
//   - no-dangling-user: if no assistant content is produced (or concatenation
//     fails), the Turn is Discard()ed so the pending user message is never
//     persisted alone, and a retry cannot duplicate it;
//   - incomplete: if the iterator reports an error or a stream is truncated, the
//     committed assistant message is tagged via memory.MarkIncomplete;
//   - ephemeral: messages produced by Config.OnError (memory.NewEphemeralMessage)
//     are streamed to the client but excluded from persistence, as are tool-call
//     messages.
//
// The run is driven under context.Background() inside the bridge so that a
// client disconnection neither aborts the generation nor the persistence. Any
// condensation must be performed by the caller (under the request context)
// before calling Run. This package owns the only adk import in components/memory
// so that the session package stays policy-free and adk-free.
package runner

import (
	"io"
	"sync/atomic"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/go-playground/validator/v10"

	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/components/memory/session"
)

// DefaultBufferSize is the pipe buffer size used when Config.BufferSize <= 0.
const DefaultBufferSize = 1000

// Config configures a single bridged run.
type Config struct {
	// Turn is the locked session turn that will persist the assistant answer.
	// Required. The bridge takes ownership: it calls CommitAssistant or Discard
	// exactly once, so the caller must NOT also release the turn (a plain
	// `defer turn.Discard()` stays safe thanks to its idempotent release).
	Turn *session.Turn `validate:"required"`

	// Iterator is the async iterator returned by adk.Runner.Run. Required.
	Iterator *adk.AsyncIterator[*adk.AgentEvent] `validate:"required"`

	// Predicate selects which events are streamed and persisted, keyed by the
	// emitting agent name and message role. When nil, the bridge streams and
	// persists every assistant-role message (assistant-only default).
	Predicate MessagePredicate

	// OnError, when non-nil, is invoked with an iterator error to build an
	// ephemeral notice streamed to the client (never persisted). When nil, the
	// error is only forwarded on the stream and the answer marked incomplete.
	OnError func(err error) *schema.Message

	// OnSkip, when non-nil, observes events filtered out by Predicate (debug/trace).
	OnSkip func(event *adk.AgentEvent)

	// BufferSize overrides the pipe buffer size. <= 0 uses DefaultBufferSize.
	BufferSize int `validate:"gte=0"`
}

// Run starts the bridge and returns the StreamReader the caller forwards to the
// client. The returned stream is closed by the bridge when the run completes;
// the caller must Close it if it stops reading early. The session turn is
// released asynchronously once persistence finishes.
func Run(cfg Config) (*schema.StreamReader[*schema.Message], error) {
	if err := validator.New().Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid runner.Config")
	}

	bufferSize := cfg.BufferSize
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}

	predicate := cfg.Predicate
	if predicate == nil {
		predicate = Role(schema.Assistant)
	}

	sr, sw := schema.Pipe[*schema.Message](bufferSize)
	srs := sr.Copy(2)

	var incomplete atomic.Bool

	go proxy(cfg, predicate, sw, &incomplete)
	go persist(cfg.Turn, srs[1], &incomplete)

	return srs[0], nil
}

// proxy reads the adk iterator and forwards the selected messages onto sw,
// token by token for streaming events.
func proxy(cfg Config, predicate MessagePredicate, sw *schema.StreamWriter[*schema.Message], incomplete *atomic.Bool) {
	defer sw.Close()

	it := cfg.Iterator
	for {
		event, ok := it.Next()
		if !ok {
			return
		}
		if event == nil {
			continue
		}

		if event.Err != nil {
			incomplete.Store(true)
			if cfg.OnError != nil {
				if notice := cfg.OnError(event.Err); notice != nil {
					sw.Send(notice, nil)
				}
			}
			sw.Send(nil, event.Err)
			continue
		}

		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mo := event.Output.MessageOutput

		if !predicate(event.AgentName, mo.Role) {
			if cfg.OnSkip != nil {
				cfg.OnSkip(event)
			}
			continue
		}

		if mo.IsStreaming {
			proxyStream(mo.MessageStream, sw, incomplete)
			continue
		}
		if mo.Message != nil {
			sw.Send(mo.Message, nil)
		}
	}
}

// proxyStream forwards a streaming message chunk by chunk. A truncated stream
// (non-EOF error) marks the answer incomplete and is reported downstream.
func proxyStream(stream *schema.StreamReader[*schema.Message], sw *schema.StreamWriter[*schema.Message], incomplete *atomic.Bool) {
	if stream == nil {
		return
	}
	defer stream.Close()
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			incomplete.Store(true)
			sw.Send(nil, err)
			return
		}
		sw.Send(chunk, nil)
	}
}

// persist drains the persistence copy of the stream, concatenates the full
// assistant answer and commits it through the turn (or discards when empty).
func persist(turn *session.Turn, stream *schema.StreamReader[*schema.Message], incomplete *atomic.Bool) {
	defer stream.Close()

	var fullMsgs []*schema.Message
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Stream error already surfaced to the client by the proxy and
			// reflected via the incomplete flag; stop accumulating here.
			incomplete.Store(true)
			break
		}
		if chunk == nil || memory.IsEphemeral(chunk) || len(chunk.ToolCalls) > 0 {
			continue
		}
		fullMsgs = append(fullMsgs, chunk)
	}

	if len(fullMsgs) == 0 {
		// No assistant content: never persist the pending user alone.
		turn.Discard()
		return
	}

	assistantMsg, err := schema.ConcatMessages(fullMsgs)
	if err != nil {
		turn.Discard()
		return
	}

	if incomplete.Load() {
		memory.MarkIncomplete(assistantMsg)
	}

	// CommitAssistant persists the pending user message then the assistant
	// answer, and releases the session lock.
	_ = turn.CommitAssistant(assistantMsg)
}
