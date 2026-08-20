package safety

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	emperrors "emperror.dev/errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	safety "github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// dryRunGuidance is appended to tool outputs during dry-run mode to instruct
// the LLM to present the preview to the user and request confirmation.
const dryRunGuidance = "\n\nDRY-RUN RESULT: This is a preview of what would happen. Show this to the user and ask for confirmation before re-calling with confirmed=true."

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

	if err := validate.Struct(&c); err != nil {
		return nil, emperrors.Wrap(err, "invalid safety config")
	}

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

		phase, err := m.preflight(ctx, toolName, callID, args, isWrite)
		if err != nil {
			return "", err
		}

		result, err := endpoint(ctx, argumentsInJSON, opts...)
		m.auditResult(ctx, toolName, callID, phase, args, result, err)
		if err != nil {
			return "", err
		}

		// Append dry-run guidance.
		if phase == safety.PhaseDryRun {
			result += dryRunGuidance
		}
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

		phase, err := m.preflight(ctx, toolName, callID, args, isWrite)
		if err != nil {
			return nil, err
		}

		sr, err := endpoint(ctx, argumentsInJSON, opts...)
		if err != nil {
			m.auditResult(ctx, toolName, callID, phase, args, "", err)
			return nil, err
		}
		return wrapStreamAudit(ctx, sr, toolName, callID, phase, args, phase == safety.PhaseDryRun, m.audit), nil
	}, nil
}

// WrapEnhancedInvokableToolCall wraps an enhanced synchronous tool call endpoint.
func (m *Middleware) WrapEnhancedInvokableToolCall(_ context.Context, endpoint adk.EnhancedInvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.EnhancedInvokableToolCallEndpoint, error) {
	toolName := tCtx.Name
	callID := tCtx.CallID
	isWrite := m.writeTools[toolName]

	return func(ctx context.Context, toolArg *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
		args := toolArg.Text

		phase, err := m.preflight(ctx, toolName, callID, args, isWrite)
		if err != nil {
			return nil, err
		}

		result, err := endpoint(ctx, toolArg, opts...)
		m.auditResult(ctx, toolName, callID, phase, args, "", err)
		if err != nil {
			return nil, err
		}
		// Append dry-run guidance for write tools in dry-run mode.
		if phase == safety.PhaseDryRun {
			result.Parts = append(result.Parts, schema.ToolOutputPart{
				Type: schema.ToolPartTypeText,
				Text: dryRunGuidance,
			})
		}
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

		phase, err := m.preflight(ctx, toolName, callID, args, isWrite)
		if err != nil {
			return nil, err
		}

		sr, err := endpoint(ctx, toolArg, opts...)
		if err != nil {
			m.auditResult(ctx, toolName, callID, phase, args, "", err)
			return nil, err
		}
		return wrapEnhancedStreamAudit(ctx, sr, toolName, callID, phase, args, phase == safety.PhaseDryRun, m.audit), nil
	}, nil
}

// WrapModel is a pass-through; safety controls are on tool calls only.
func (m *Middleware) WrapModel(_ context.Context, cm model.BaseChatModel, _ *adk.ModelContext) (model.BaseChatModel, error) {
	return cm, nil
}

// --- internal helpers ---

// preflight runs policy evaluation for every tool and, for write tools, the
// dry-run/confirm gate. It emits reject audit events on failure. On success it
// returns the phase the call should be audited under. proceed is false only
// when an error is returned.
func (m *Middleware) preflight(ctx context.Context, toolName, callID, args string, isWrite bool) (safety.Phase, error) {
	params, parseErr := parseArgs(args)
	if parseErr != nil {
		// If we can't parse, still allow through — tools do their own validation.
		params = map[string]any{}
	}

	// Policy evaluation (applies to ALL tools).
	if m.cfg.Policy != nil {
		if err := m.cfg.Policy.Evaluate(ctx, toolName, params); err != nil {
			m.audit(ctx, safety.AuditEvent{
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

	if !isWrite {
		return safety.PhaseRead, nil
	}

	// Gate check (write tools only).
	gp, gpErr := safety.ExtractGateParams(args)
	if gpErr != nil {
		m.auditReject(ctx, toolName, callID, args, gpErr)
		return "", gpErr
	}
	if err := safety.ShouldGate(toolName, m.writeTools, gp); err != nil {
		m.auditReject(ctx, toolName, callID, args, err)
		return "", err
	}

	if gp.DryRun {
		return safety.PhaseDryRun, nil
	}
	return safety.PhaseExecute, nil
}

// auditReject emits a rejection audit event for a write tool whose gate parsing
// or gate check failed (policy already passed at that point).
func (m *Middleware) auditReject(ctx context.Context, toolName, callID, args string, err error) {
	m.audit(ctx, safety.AuditEvent{
		Timestamp:  time.Now(),
		ToolName:   toolName,
		CallID:     callID,
		Phase:      safety.PhaseRejected,
		Arguments:  json.RawMessage(args),
		Error:      err.Error(),
		PolicyPass: true,
	})
}

// auditResult emits the terminal audit event for a completed call: an error
// event when err is non-nil, otherwise a success event carrying result.
func (m *Middleware) auditResult(ctx context.Context, toolName, callID string, phase safety.Phase, args, result string, err error) {
	event := safety.AuditEvent{
		Timestamp:  time.Now(),
		ToolName:   toolName,
		CallID:     callID,
		Phase:      phase,
		Arguments:  json.RawMessage(args),
		PolicyPass: true,
	}
	if err != nil {
		event.Error = err.Error()
	} else {
		event.Result = result
	}
	m.audit(ctx, event)
}

// audit sends an audit event to the configured sink with the given context.
// The context carries the run's session ID (set via activity.WithSession) so
// bus-backed sinks can correlate audit events to the correct SSE subscriber.
// Errors from the sink are silently dropped (audit is best-effort, not a
// critical path).
func (m *Middleware) audit(ctx context.Context, event safety.AuditEvent) {
	_ = m.cfg.AuditSink.Write(ctx, event)
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
// full accumulated result. When dryRun is true, a final DRY-RUN RESULT guidance
// chunk is appended after EOF so the LLM sees the confirmation instruction.
func wrapStreamAudit(
	ctx context.Context,
	sr *schema.StreamReader[string],
	toolName, callID string,
	phase safety.Phase,
	args string,
	dryRun bool,
	auditFn func(context.Context, safety.AuditEvent),
) *schema.StreamReader[string] {
	out, sw := schema.Pipe[string](100)

	go func() {
		defer sw.Close()
		var fullResult string
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Append dry-run guidance as a final chunk before auditing.
					if dryRun {
						guidance := dryRunGuidance
						fullResult += guidance
						sw.Send(guidance, nil)
					}
					// EOF — audit the full result.
					auditFn(ctx, safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						Result:     fullResult,
						PolicyPass: true,
					})
				} else {
					auditFn(ctx, safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						Error:      err.Error(),
						PolicyPass: true,
					})
					// Surface the error as a final chunk so the agent sees the
					// failure as tool output text and can react. Without this
					// the wrapped stream closes empty and upstream
					// concatenation fails with "stream reader is empty, concat
					// fail" instead of the real error.
					sw.Send(fmt.Sprintf("tool call failed: %s", err), nil)
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
// stream completion. When dryRun is true, a final ToolResult with DRY-RUN
// RESULT guidance is appended after EOF.
func wrapEnhancedStreamAudit(
	ctx context.Context,
	sr *schema.StreamReader[*schema.ToolResult],
	toolName, callID string,
	phase safety.Phase,
	args string,
	dryRun bool,
	auditFn func(context.Context, safety.AuditEvent),
) *schema.StreamReader[*schema.ToolResult] {
	out, sw := schema.Pipe[*schema.ToolResult](100)

	go func() {
		defer sw.Close()
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Append dry-run guidance as a final result before auditing.
					if dryRun {
						sw.Send(&schema.ToolResult{
							Parts: []schema.ToolOutputPart{{
								Type: schema.ToolPartTypeText,
								Text: dryRunGuidance,
							}},
						}, nil)
					}
					auditFn(ctx, safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						PolicyPass: true,
					})
				} else {
					auditFn(ctx, safety.AuditEvent{
						Timestamp:  time.Now(),
						ToolName:   toolName,
						CallID:     callID,
						Phase:      phase,
						Arguments:  json.RawMessage(args),
						Error:      err.Error(),
						PolicyPass: true,
					})
					// Surface the error as a final result chunk so the agent
					// sees the failure as tool output text and can react.
					// Without this the wrapped stream closes empty and
					// upstream concatenation fails with "stream reader is
					// empty, concat fail" instead of the real error.
					sw.Send(&schema.ToolResult{
						Parts: []schema.ToolOutputPart{{
							Type: schema.ToolPartTypeText,
							Text: fmt.Sprintf("tool call failed: %s", err),
						}},
					}, nil)
				}
				return
			}
			sw.Send(chunk, nil)
		}
	}()

	return out
}
