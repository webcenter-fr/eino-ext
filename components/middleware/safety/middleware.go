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

package safety

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	emperrors "emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	safety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// Middleware is an adk.ChatModelAgentMiddleware that enforces a safety control
// layer: audit trails, policy evaluation, and gate logic (dry-run/confirmed).
type Middleware struct {
	*adk.BaseChatModelAgentMiddleware
	cfg        *Config
	writeTools map[string]bool
}

var _ adk.ChatModelAgentMiddleware = (*Middleware)(nil)

// New builds a Middleware from the given Config.
func New(cfg *Config) (*Middleware, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	c := *cfg

	if c.AuditSink == nil {
		c.AuditSink = &safety.LogSink{}
	}

	writeTools := make(map[string]bool, len(c.WriteToolNames))
	for _, name := range c.WriteToolNames {
		writeTools[name] = true
	}

	return &Middleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		cfg:                          &c,
		writeTools:                   writeTools,
	}, nil
}

// WrapInvokableToolCall wraps a synchronous tool call endpoint with safety controls.
func (m *Middleware) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	toolName := tCtx.Name
	callID := tCtx.CallID
	isWrite := m.writeTools[toolName]

	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		args := argumentsInJSON

		// Parse arguments for policy evaluation.
		params, parseErr := parseArgs(argumentsInJSON)
		if parseErr != nil {
			// If we can't parse, still allow through — tools do their own validation.
			params = map[string]any{}
		}

		// Policy evaluation (applies to ALL tools).
		if m.cfg.Policy != nil {
			if err := m.cfg.Policy.Evaluate(ctx, toolName, params); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: false,
				})
				return "", err
			}
		}

		// Gate check (write tools only).
		if isWrite {
			gp, gpErr := safety.ExtractGateParams(argumentsInJSON)
			if gpErr != nil {
				// Can't parse gate params — reject the write call.
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      gpErr.Error(),
					PolicyPass: true,
				})
				return "", gpErr
			}
			if err := safety.ShouldGate(toolName, m.writeTools, gp); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return "", err
			}

			// Determine phase based on gate params.
			phase := safety.PhaseExecute
			if gp.DryRun {
				phase = safety.PhaseDryRun
			}

			// Execute.
			result, err := endpoint(ctx, argumentsInJSON, opts...)
			if err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      phase,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return "", err
			}
			m.audit(safety.AuditEvent{
				Timestamp:  time.Now(),
				ToolName:   toolName,
				CallID:     callID,
				Phase:      phase,
				Arguments:  json.RawMessage(args),
				Result:     result,
				PolicyPass: true,
			})

			// Append dry-run guidance.
			if gp.DryRun {
				result += "\n\nDRY-RUN RESULT: This is a preview of what would happen. " +
					"Show this to the user and ask for confirmation before re-calling with confirmed=true."
			}
			return result, nil
		}

		// Read-only tool.
		result, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			m.audit(safety.AuditEvent{
				Timestamp:  time.Now(),
				ToolName:   toolName,
				CallID:     callID,
				Phase:      safety.PhaseRead,
				Arguments:  json.RawMessage(args),
				Error:      err.Error(),
				PolicyPass: true,
			})
			return "", err
		}
		m.audit(safety.AuditEvent{
			Timestamp:  time.Now(),
			ToolName:   toolName,
			CallID:     callID,
			Phase:      safety.PhaseRead,
			Arguments:  json.RawMessage(args),
			Result:     result,
			PolicyPass: true,
		})
		return result, nil
	}, nil
}

// WrapStreamableToolCall wraps a streaming tool call endpoint with safety controls.
// Auditing occurs after the stream completes.
func (m *Middleware) WrapStreamableToolCall(_ context.Context, endpoint adk.StreamableToolCallEndpoint, tCtx *adk.ToolContext) (adk.StreamableToolCallEndpoint, error) {
	toolName := tCtx.Name
	callID := tCtx.CallID
	isWrite := m.writeTools[toolName]

	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		args := argumentsInJSON

		params, parseErr := parseArgs(argumentsInJSON)
		if parseErr != nil {
			params = map[string]any{}
		}

		// Policy evaluation.
		if m.cfg.Policy != nil {
			if err := m.cfg.Policy.Evaluate(ctx, toolName, params); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: false,
				})
				return nil, err
			}
		}

		// Gate check for write tools.
		if isWrite {
			gp, gpErr := safety.ExtractGateParams(argumentsInJSON)
			if gpErr != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      gpErr.Error(),
					PolicyPass: true,
				})
				return nil, gpErr
			}
			if err := safety.ShouldGate(toolName, m.writeTools, gp); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return nil, err
			}

			phase := safety.PhaseExecute
			if gp.DryRun {
				phase = safety.PhaseDryRun
			}

			sr, err := endpoint(ctx, argumentsInJSON, opts...)
			if err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      phase,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return nil, err
			}

			// Wrap the stream to audit on completion.
			return wrapStreamAudit(sr, toolName, callID, phase, args, gp.DryRun, m.audit), nil
		}

		// Read-only streaming.
		sr, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			m.audit(safety.AuditEvent{
				Timestamp:  time.Now(),
				ToolName:   toolName,
				CallID:     callID,
				Phase:      safety.PhaseRead,
				Arguments:  json.RawMessage(args),
				Error:      err.Error(),
				PolicyPass: true,
			})
			return nil, err
		}
		return wrapStreamAudit(sr, toolName, callID, safety.PhaseRead, args, false, m.audit), nil
	}, nil
}

// WrapEnhancedInvokableToolCall wraps an enhanced synchronous tool call endpoint.
func (m *Middleware) WrapEnhancedInvokableToolCall(_ context.Context, endpoint adk.EnhancedInvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.EnhancedInvokableToolCallEndpoint, error) {
	toolName := tCtx.Name
	callID := tCtx.CallID
	isWrite := m.writeTools[toolName]

	return func(ctx context.Context, toolArg *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
		args := toolArg.Text

		params, parseErr := parseArgs(args)
		if parseErr != nil {
			params = map[string]any{}
		}

		// Policy evaluation.
		if m.cfg.Policy != nil {
			if err := m.cfg.Policy.Evaluate(ctx, toolName, params); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: false,
				})
				return nil, err
			}
		}

		// Gate check for write tools.
		if isWrite {
			gp, gpErr := safety.ExtractGateParams(args)
			if gpErr != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      gpErr.Error(),
					PolicyPass: true,
				})
				return nil, gpErr
			}
			if err := safety.ShouldGate(toolName, m.writeTools, gp); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return nil, err
			}

			phase := safety.PhaseExecute
			if gp.DryRun {
				phase = safety.PhaseDryRun
			}

			result, err := endpoint(ctx, toolArg, opts...)
			if err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      phase,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return nil, err
			}
			m.audit(safety.AuditEvent{
				Timestamp:  time.Now(),
				ToolName:   toolName,
				CallID:     callID,
				Phase:      phase,
				Arguments:  json.RawMessage(args),
				PolicyPass: true,
			})
			return result, nil
		}

		// Read-only enhanced.
		result, err := endpoint(ctx, toolArg, opts...)
		if err != nil {
			m.audit(safety.AuditEvent{
				Timestamp:  time.Now(),
				ToolName:   toolName,
				CallID:     callID,
				Phase:      safety.PhaseRead,
				Arguments:  json.RawMessage(args),
				Error:      err.Error(),
				PolicyPass: true,
			})
			return nil, err
		}
		m.audit(safety.AuditEvent{
			Timestamp:  time.Now(),
			ToolName:   toolName,
			CallID:     callID,
			Phase:      safety.PhaseRead,
			Arguments:  json.RawMessage(args),
			PolicyPass: true,
		})
		return result, nil
	}, nil
}

// WrapEnhancedStreamableToolCall wraps an enhanced streaming tool call endpoint.
func (m *Middleware) WrapEnhancedStreamableToolCall(_ context.Context, endpoint adk.EnhancedStreamableToolCallEndpoint, tCtx *adk.ToolContext) (adk.EnhancedStreamableToolCallEndpoint, error) {
	toolName := tCtx.Name
	callID := tCtx.CallID
	isWrite := m.writeTools[toolName]

	return func(ctx context.Context, toolArg *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
		args := toolArg.Text

		params, parseErr := parseArgs(args)
		if parseErr != nil {
			params = map[string]any{}
		}

		// Policy evaluation.
		if m.cfg.Policy != nil {
			if err := m.cfg.Policy.Evaluate(ctx, toolName, params); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: false,
				})
				return nil, err
			}
		}

		// Gate check for write tools.
		if isWrite {
			gp, gpErr := safety.ExtractGateParams(args)
			if gpErr != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      gpErr.Error(),
					PolicyPass: true,
				})
				return nil, gpErr
			}
			if err := safety.ShouldGate(toolName, m.writeTools, gp); err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      safety.PhaseRejected,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return nil, err
			}

			phase := safety.PhaseExecute
			if gp.DryRun {
				phase = safety.PhaseDryRun
			}

			sr, err := endpoint(ctx, toolArg, opts...)
			if err != nil {
				m.audit(safety.AuditEvent{
					Timestamp:  time.Now(),
					ToolName:   toolName,
					CallID:     callID,
					Phase:      phase,
					Arguments:  json.RawMessage(args),
					Error:      err.Error(),
					PolicyPass: true,
				})
				return nil, err
			}
			return wrapEnhancedStreamAudit(sr, toolName, callID, phase, args, m.audit), nil
		}

		// Read-only enhanced streaming.
		sr, err := endpoint(ctx, toolArg, opts...)
		if err != nil {
			m.audit(safety.AuditEvent{
				Timestamp:  time.Now(),
				ToolName:   toolName,
				CallID:     callID,
				Phase:      safety.PhaseRead,
				Arguments:  json.RawMessage(args),
				Error:      err.Error(),
				PolicyPass: true,
			})
			return nil, err
		}
		return wrapEnhancedStreamAudit(sr, toolName, callID, safety.PhaseRead, args, m.audit), nil
	}, nil
}

// WrapModel is a pass-through; safety controls are on tool calls only.
func (m *Middleware) WrapModel(_ context.Context, cm model.BaseChatModel, _ *adk.ModelContext) (model.BaseChatModel, error) {
	return cm, nil
}

// --- internal helpers ---

// audit sends an audit event to the configured sink. Errors from the sink are
// silently dropped (audit is best-effort, not a critical path).
func (m *Middleware) audit(event safety.AuditEvent) {
	_ = m.cfg.AuditSink.Write(context.Background(), event)
}

// parseArgs unmarshals a JSON string into a map.
func parseArgs(raw string) (map[string]any, error) {
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, emperrors.Wrap(err, "failed to parse tool arguments")
	}
	return params, nil
}

// wrapStreamAudit wraps a StreamReader[string] to audit on stream completion.
// When the stream is consumed to EOF, it emits a single audit event with the
// full accumulated result and appends dry-run guidance if applicable.
func wrapStreamAudit(
	sr *schema.StreamReader[string],
	toolName, callID string,
	phase safety.Phase,
	args string,
	dryRun bool,
	auditFn func(safety.AuditEvent),
) *schema.StreamReader[string] {
	out, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()
		var fullResult string
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// EOF — audit the full result.
					auditFn(safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						Result:     fullResult,
						PolicyPass: true,
					})
				} else {
					auditFn(safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						Error:      err.Error(),
						PolicyPass: true,
					})
				}
				return
			}
			fullResult += chunk
			sw.Send(chunk, nil)
		}
	}()

	return out
}

// wrapEnhancedStreamAudit wraps a StreamReader[*schema.ToolResult] to audit on
// stream completion.
func wrapEnhancedStreamAudit(
	sr *schema.StreamReader[*schema.ToolResult],
	toolName, callID string,
	phase safety.Phase,
	args string,
	auditFn func(safety.AuditEvent),
) *schema.StreamReader[*schema.ToolResult] {
	out, sw := schema.Pipe[*schema.ToolResult](100)

	go func() {
		defer sw.Close()
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					auditFn(safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						PolicyPass: true,
					})
				} else {
					auditFn(safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						Error:      err.Error(),
						PolicyPass: true,
					})
				}
				return
			}
			sw.Send(chunk, nil)
		}
	}()

	return out
}
