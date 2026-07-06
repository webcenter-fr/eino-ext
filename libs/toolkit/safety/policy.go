package safety

import (
	"context"

	"emperror.dev/errors"
	"github.com/google/cel-go/cel"
	"google.golang.org/protobuf/types/known/structpb"
)

// Policy evaluates whether a tool invocation is allowed.
// Implementations must be safe for concurrent use.
type Policy interface {
	// Evaluate returns nil if the invocation is allowed, or an error describing
	// why it was denied.
	Evaluate(ctx context.Context, toolName string, params map[string]any) error
}

// CELRule defines a single CEL-based policy rule.
// The expression receives the variables "toolName" (string) and "params" (map).
type CELRule struct {
	// Name is a human-readable identifier for this rule (used in error messages).
	Name string
	// Expression is a CEL expression that must evaluate to bool.
	// Variables available: "toolName" (string), "params" (map[string]any).
	Expression string
	// ToolNames restricts this rule to specific tools. If empty, applies to all tools.
	ToolNames []string

	program cel.Program
}

// CELPolicy evaluates CEL expressions against tool parameters.
// Rules are evaluated in order; the first failing rule stops evaluation.
type CELPolicy struct {
	rules []CELRule
	env   *cel.Env
}

// NewCELPolicy compiles the given CEL rules into a reusable policy.
// Compilation happens once at construction time, not per call.
func NewCELPolicy(rules []CELRule) (*CELPolicy, error) {
	if len(rules) == 0 {
		return nil, errors.New("CELPolicy requires at least one rule")
	}

	env, err := cel.NewEnv(
		cel.Variable("toolName", cel.StringType),
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create CEL environment")
	}

		// Pre-compile all expressions to validate them at construction time.
	for i, rule := range rules {
		ast, iss := env.Compile(rule.Expression)
		if iss.Err() != nil {
			return nil, errors.Wrapf(iss.Err(), "failed to compile CEL rule %q", rule.Name)
		}
		prog, err := env.Program(ast)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create program for CEL rule %q", rule.Name)
		}
		rules[i].program = prog
	}

	return &CELPolicy{rules: rules, env: env}, nil
}

// Evaluate checks all rules against the given tool invocation.
// Returns nil if all rules pass, or an error describing the first failed rule.
func (p *CELPolicy) Evaluate(_ context.Context, toolName string, params map[string]any) error {
	protoParams := &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	if err := mapToProtoStruct(protoParams, params); err != nil {
		return errors.Wrap(err, "failed to convert params to proto struct")
	}

	activation := map[string]interface{}{
		"toolName": toolName,
		"params":   protoParams,
	}

	for _, rule := range p.rules {
		if !matchesTool(rule.ToolNames, toolName) {
			continue
		}

		out, _, err := rule.program.Eval(activation)
		if err != nil {
			return errors.Wrapf(err, "CEL rule %q evaluation error", rule.Name)
		}

		allowed, ok := out.Value().(bool)
		if !ok {
			return errors.Errorf("CEL rule %q did not return bool", rule.Name)
		}
		if !allowed {
			return errors.Errorf("policy denied by rule %q", rule.Name)
		}
	}

	return nil
}

// matchesTool returns true if the rule applies to the given tool name.
// An empty ToolNames means the rule applies to all tools.
func matchesTool(toolNames []string, toolName string) bool {
	if len(toolNames) == 0 {
		return true
	}
	for _, t := range toolNames {
		if t == toolName {
			return true
		}
	}
	return false
}

// mapToProtoStruct converts a Go map[string]any into a protobuf Struct.
func mapToProtoStruct(s *structpb.Struct, m map[string]any) error {
	for k, v := range m {
		pv, err := structpb.NewValue(v)
		if err != nil {
			return errors.Wrapf(err, "key %q", k)
		}
		s.Fields[k] = pv
	}
	return nil
}

// PolicyChain evaluates multiple policies in order. The first failure stops
// evaluation and returns the error.
type PolicyChain []Policy

// Evaluate runs each policy in sequence. Returns nil if all pass, or the first
// denial error.
func (c PolicyChain) Evaluate(ctx context.Context, toolName string, params map[string]any) error {
	for i, p := range c {
		if err := p.Evaluate(ctx, toolName, params); err != nil {
			return errors.Wrapf(err, "policy chain[%d]", i)
		}
	}
	return nil
}

// compile-time interface check.
var _ Policy = (*CELPolicy)(nil)
var _ Policy = (PolicyChain)(nil)
