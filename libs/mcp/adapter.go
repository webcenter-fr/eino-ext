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

	mcpapproval "github.com/webcenter-fr/eino-ext/libs/mcp/approval"
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
		inputSchema, err := inputSchemaJSON(info, isWrite)
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
		args, approvalID, err := stripApprovalID(req.Params.Arguments)
		if err != nil {
			s.audit(ctx, safety.AuditEvent{
				Timestamp: time.Now(), ToolName: toolName, CallID: uuid.NewString(),
				Phase: safety.PhaseRejected, Arguments: req.Params.Arguments, Error: err.Error(),
			})
			return toolErrorResult(err), nil
		}
		argsStr := string(args)
		if argsStr == "" {
			argsStr = "{}"
		}
		callID := uuid.NewString()
		id := s.identityFromRequest(req)
		meta := identityMetadata(id)
		argsHash, argsTruncated := "", false
		if isWrite {
			argsHash, _ = mcpapproval.ArgsHash(args)
			argsTruncated = len(args) > ApprovalDisplayBudget
		}

		// reject audits a denied call (RBAC, policy, or gate) and packs the
		// error into a tool error result (IsError=true, not a protocol error).
		reject := func(policyPass bool, err error) *mcpsdk.CallToolResult {
			s.audit(ctx, safety.AuditEvent{
				Timestamp: time.Now(), ToolName: toolName, CallID: callID,
				Phase: safety.PhaseRejected, Arguments: args,
				Error: err.Error(), PolicyPass: policyPass, Metadata: meta,
				ArgsHash: argsHash, ArgsTruncated: argsTruncated,
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
			var approvalAuthorizer *ApprovalURLAuthorizer
			if authorizer == nil {
				if s.approvalClient != nil {
					approvalAuthorizer = &ApprovalURLAuthorizer{
						client:     s.approvalClient,
						serverName: s.cfg.Approval.Service.ServerName,
						session:    req.Session,
						identity:   id,
						instance:   instance,
						approvalID: approvalID,
						timeout:    s.approvalTimeout,
						ttl:        s.approvalTTL,
						preview:    previewFunc(t),
						onRequested: func(registeredID string) {
							s.audit(ctx, safety.AuditEvent{
								Timestamp: time.Now(), ToolName: toolName, CallID: callID,
								Phase: safety.PhaseApprovalRequested, Arguments: args,
								ApprovalID: registeredID, PolicyPass: true, Metadata: meta,
								ArgsHash: argsHash, ArgsTruncated: argsTruncated,
							})
						},
					}
					authorizer = approvalAuthorizer
				} else {
					authorizer = &ElicitationAuthorizer{
						session: req.Session, identity: id, instance: instance, timeout: s.approvalTimeout,
					}
				}
			}
			if err := safety.ShouldGateWithAuthorization(ctx, toolName, s.writeTools, gp, args, authorizer); err != nil {
				if !gp.DryRun {
					s.auditApproval(ctx, toolName, callID, args, approvalID, approvalAuthorizer, err)
				}
				return reject(true, err), nil
			}
			if gp.DryRun {
				phase = safety.PhaseDryRun
			} else {
				s.auditApproval(ctx, toolName, callID, args, approvalID, approvalAuthorizer, nil)
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
			ArgsHash: argsHash, ArgsTruncated: argsTruncated,
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

// auditApproval emits the approval lifecycle event. It is separate from the
// tool-call event so the approval outcome is auditable even when the tool is
// never executed.
func (s *Server) auditApproval(ctx context.Context, toolName, callID string, args json.RawMessage, approvalID string, authorizer *ApprovalURLAuthorizer, authErr error) {
	event := safety.AuditEvent{
		Timestamp: time.Now(), ToolName: toolName, CallID: callID,
		Arguments: args, PolicyPass: authErr == nil, ApprovalID: approvalID,
	}
	if authorizer != nil {
		if claims := authorizer.LastReceipt(); claims != nil {
			event.ApprovalID = claims.ApprovalID
			event.ApproverSub = claims.ApproverSub
			event.JTI = claims.ID
			event.Decision = claims.Decision
			event.Policy = claims.Policy
		}
	}
	switch {
	case authErr == nil:
		event.Phase = safety.PhaseApprovalGranted
	case authorizer != nil && authorizer.expired:
		event.Phase = safety.PhaseApprovalExpired
	default:
		event.Phase = safety.PhaseApprovalDenied
	}
	if authErr != nil {
		event.Error = authErr.Error()
	}
	s.audit(ctx, event)
}

// stripApprovalID removes the transport-only approvalId field before the tool
// sees the arguments. The field is published in the MCP input schema for write
// tools so the LLM can re-call with it after an out-of-band approval.
func stripApprovalID(args json.RawMessage) (json.RawMessage, string, error) {
	if len(args) == 0 {
		return args, "", nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return nil, "", emperrors.Wrap(err, "failed to parse tool arguments")
	}
	raw, ok := fields["approvalId"]
	if !ok {
		return args, "", nil
	}
	var approvalID string
	if err := json.Unmarshal(raw, &approvalID); err != nil {
		return nil, "", emperrors.Wrap(err, "approvalId must be a string")
	}
	delete(fields, "approvalId")
	stripped, err := json.Marshal(fields)
	if err != nil {
		return nil, "", emperrors.Wrap(err, "failed to strip approvalId")
	}
	return stripped, approvalID, nil
}

// previewFunc runs the tool's own dry-run preview for the approval UI. A
// failure is a warning, never an approval.
func previewFunc(t tool.InvokableTool) PreviewFunc {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(args, &fields); err != nil {
			return "", emperrors.Wrap(err, "failed to parse args for preview")
		}
		fields["dryRun"] = json.RawMessage("true")
		fields["confirmed"] = json.RawMessage("false")
		delete(fields, "approvalId")
		previewArgs, err := json.Marshal(fields)
		if err != nil {
			return "", emperrors.Wrap(err, "failed to build preview args")
		}
		return t.InvokableRun(ctx, string(previewArgs))
	}
}

// inputSchemaJSON converts a tool's ParamsOneOf to a JSON-schema map suitable
// for mcpsdk.Tool.InputSchema (low-level AddTool accepts any value that
// JSON-marshals to an object with type "object").
func inputSchemaJSON(info *schema.ToolInfo, isWrite bool) (any, error) {
	// ToJSONSchema is the promoted method of the embedded *ParamsOneOf; it
	// handles a nil ParamsOneOf (tool without params) by returning nil.
	js, err := info.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	m := map[string]any{"type": "object"}
	if js != nil {
		b, err := json.Marshal(js)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
	}
	if m["type"] != "object" {
		m["type"] = "object"
	}
	if isWrite {
		properties, _ := m["properties"].(map[string]any)
		if properties == nil {
			properties = map[string]any{}
			m["properties"] = properties
		}
		properties["approvalId"] = map[string]any{
			"type":        "string",
			"description": "Optional approval id returned by a previous call when out-of-band browser approval is required. The server strips it before invoking the tool.",
		}
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
