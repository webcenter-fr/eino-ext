package mcp

import (
	"context"
	"encoding/json"
	"time"

	emperrors "emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// addTools registers the configured eino tools on srv, filtered by what id may
// use at the category level (tools/list). When s.authz is nil, all tools are
// registered (id is unused).
func (s *Server) addTools(ctx context.Context, srv *mcpsdk.Server, id *Identity) error {
	for _, t := range s.cfg.Tools {
		info, err := t.Info(ctx)
		if err != nil {
			return emperrors.Wrap(err, "failed to get tool info")
		}
		isWrite := s.writeTools[info.Name]
		if s.authz != nil && !s.authz.CanUse(id, info.Name, isWrite) {
			continue // hidden from this identity
		}
		inputSchema, err := inputSchemaJSON(info)
		if err != nil {
			return emperrors.Wrapf(err, "failed to build input schema for tool %q", info.Name)
		}
		srv.AddTool(&mcpsdk.Tool{
			Name:        info.Name,
			Description: info.Desc,
			InputSchema: inputSchema,
			Annotations: annotationsFor(isWrite),
		}, s.makeHandler(t, info.Name, isWrite))
	}
	return nil
}

// makeHandler wraps an eino tool as an MCP tool handler, reproducing the safety
// middleware's preflight: RBAC → CEL policy → gate (dry-run/confirmed +
// elicitation approval) → execute → audit.
func (s *Server) makeHandler(t tool.InvokableTool, toolName string, isWrite bool) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := req.Params.Arguments
		argsStr := string(args)
		if argsStr == "" {
			argsStr = "{}"
		}
		callID := uuid.NewString()
		id := s.identityFromRequest(req)
		meta := identityMetadata(id)

		// reject audits a denied call (RBAC, policy, or gate) and packs the
		// error into a tool error result (IsError=true, not a protocol error).
		reject := func(policyPass bool, err error) *mcpsdk.CallToolResult {
			s.audit(ctx, safety.AuditEvent{
				Timestamp: time.Now(), ToolName: toolName, CallID: callID,
				Phase: safety.PhaseRejected, Arguments: args,
				Error: err.Error(), PolicyPass: policyPass, Metadata: meta,
			})
			return toolErrorResult(err)
		}

		// 1. Authorization (RBAC): category + instance.
		instance := extractInstance(args, s.cfg.InstanceParam)
		if s.authz != nil {
			if err := s.authz.Authorize(id, toolName, instance, isWrite); err != nil {
				return reject(true, err), nil
			}
		}

		// 2. Policy (all calls).
		if s.cfg.Policy != nil {
			params, _ := parseArgs(argsStr)
			if err := s.cfg.Policy.Evaluate(ctx, toolName, params); err != nil {
				return reject(false, err), nil
			}
		}

		// 3. Gate (write tools only).
		phase := safety.PhaseRead
		execCtx := ctx
		if isWrite {
			gp, err := safety.ExtractGateParams(argsStr)
			if err != nil {
				return reject(true, err), nil
			}
			authorizer := s.cfg.ExecutionAuthorizer
			if authorizer == nil {
				authorizer = &ElicitationAuthorizer{
					session: req.Session, identity: id, instance: instance, timeout: s.approvalTimeout,
				}
			}
			if err := safety.ShouldGateWithAuthorization(ctx, toolName, s.writeTools, gp, args, authorizer); err != nil {
				return reject(true, err), nil
			}
			if gp.DryRun {
				phase = safety.PhaseDryRun
			} else {
				// Real execution authorized: mark the context so the per-tool
				// second layer (confirm.RequireConfirmationCtx) sees it.
				execCtx = safety.WithExecutionAuthorized(ctx, toolName)
				phase = safety.PhaseExecute
			}
		}

		// 4. Execute.
		result, err := t.InvokableRun(execCtx, argsStr)
		event := safety.AuditEvent{
			Timestamp: time.Now(), ToolName: toolName, CallID: callID,
			Phase: phase, Arguments: args, PolicyPass: true, Metadata: meta,
		}
		if err != nil {
			event.Error = err.Error()
		} else {
			event.Result = result
		}
		s.audit(ctx, event)
		if err != nil {
			return toolErrorResult(err), nil
		}
		if phase == safety.PhaseDryRun {
			result += safety.DryRunGuidance
		}
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: result}},
		}, nil
	}
}

// inputSchemaJSON converts a tool's ParamsOneOf to a JSON-schema map suitable
// for mcpsdk.Tool.InputSchema (low-level AddTool accepts any value that
// JSON-marshals to an object with type "object").
func inputSchemaJSON(info *schema.ToolInfo) (any, error) {
	// ToJSONSchema is the promoted method of the embedded *ParamsOneOf; it
	// handles a nil ParamsOneOf (tool without params) by returning nil.
	js, err := info.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	if js == nil {
		return map[string]any{"type": "object"}, nil
	}
	b, err := json.Marshal(js)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m["type"] != "object" {
		m["type"] = "object"
	}
	return m, nil
}

// annotationsFor returns MCP tool annotations. Write tools are marked
// destructive (advisory — the server-side gate is the enforcement).
func annotationsFor(isWrite bool) *mcpsdk.ToolAnnotations {
	if !isWrite {
		return &mcpsdk.ToolAnnotations{ReadOnlyHint: true}
	}
	destructive := true
	openWorld := true
	return &mcpsdk.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: &destructive,
		OpenWorldHint:   &openWorld,
	}
}

// extractInstance reads the instance argument (cluster/instance) from raw args.
// Returns "" when absent or when args are not a JSON object.
func extractInstance(args json.RawMessage, param string) string {
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	s, _ := m[param].(string)
	return s
}

// toolErrorResult packs a tool-originated error into a CallToolResult with
// IsError=true (a tool error, not a protocol error — the LLM must see it and
// self-correct).
func toolErrorResult(err error) *mcpsdk.CallToolResult {
	res := &mcpsdk.CallToolResult{}
	res.SetError(err)
	return res
}

func parseArgs(raw string) (map[string]any, error) {
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, emperrors.Wrap(err, "failed to parse tool arguments")
	}
	return params, nil
}
