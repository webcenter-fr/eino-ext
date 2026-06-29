// Package cachestab provides a deterministic, non-destructive normalization of
// tool definitions to maximize prompt-cache stability (port of headroom's
// tool_def_normalize). Its surface is the tool definitions passed to WithTools,
// NOT the message history.
//
// On WithTools it:
//   - sorts the tool list by name (alphabetical), and
//   - recursively sorts the keys of each tool's JSON Schema (properties, required,
//     $defs, …).
//
// The transformation is semantics-preserving (same tool names, same parameters)
// and modifies no message, so it is safe to compose with any other middleware.
//
// Out of scope (provider-specific, belongs in a proxy): injecting Anthropic
// cache_control or OpenAI prompt_cache_key.
package cachestab

import (
	"context"
	"sort"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

// ToolCallingChatModel decorates a model.ToolCallingChatModel, normalizing tool
// definitions on every WithTools call.
type ToolCallingChatModel struct {
	base model.ToolCallingChatModel
}

var _ model.ToolCallingChatModel = (*ToolCallingChatModel)(nil)

// NewToolCallingChatModel wraps base so that WithTools receives normalized,
// deterministically-ordered tool definitions.
func NewToolCallingChatModel(base model.ToolCallingChatModel) (*ToolCallingChatModel, error) {
	if base == nil {
		return nil, errors.New("cachestab: base model must not be nil")
	}
	return &ToolCallingChatModel{base: base}, nil
}

// Generate delegates to the wrapped model unchanged.
func (m *ToolCallingChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.base.Generate(ctx, input, opts...)
}

// Stream delegates to the wrapped model unchanged.
func (m *ToolCallingChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.base.Stream(ctx, input, opts...)
}

// WithTools normalizes tools, then binds them to the wrapped model, returning a
// new normalizing decorator.
func (m *ToolCallingChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	normalized, err := NormalizeTools(tools)
	if err != nil {
		return nil, err
	}
	bound, err := m.base.WithTools(normalized)
	if err != nil {
		return nil, err
	}
	return &ToolCallingChatModel{base: bound}, nil
}

// NormalizeTools returns a new slice of tools sorted by name, each with a
// key-sorted JSON Schema. Input tools are not mutated. Tools with a nil
// ParamsOneOf are left as-is (no parameters).
func NormalizeTools(tools []*schema.ToolInfo) ([]*schema.ToolInfo, error) {
	out := make([]*schema.ToolInfo, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			out = append(out, t)
			continue
		}
		clone := *t
		if t.ParamsOneOf != nil {
			sc, err := t.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, errors.Wrapf(err, "cachestab: schema for tool %q", t.Name)
			}
			normalizeSchema(sc)
			clone.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(sc)
		}
		out = append(out, &clone)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i] == nil || out[j] == nil {
			return out[j] != nil
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// normalizeSchema deterministically sorts a JSON Schema's keys in place:
// Properties (orderedmap), Required, and $defs, recursing into all subschemas.
func normalizeSchema(s *jsonschema.Schema) {
	if s == nil {
		return
	}

	if len(s.Required) > 0 {
		sort.Strings(s.Required)
	}

	if s.Properties != nil {
		s.Properties = sortProperties(s.Properties)
		for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
			normalizeSchema(pair.Value)
		}
	}

	normalizeSchema(s.Items)
	normalizeSchema(s.Contains)
	normalizeSchema(s.Not)
	normalizeSchema(s.AdditionalProperties)
	for _, sub := range s.PrefixItems {
		normalizeSchema(sub)
	}
	for _, sub := range s.AllOf {
		normalizeSchema(sub)
	}
	for _, sub := range s.AnyOf {
		normalizeSchema(sub)
	}
	for _, sub := range s.OneOf {
		normalizeSchema(sub)
	}
	for _, sub := range s.PatternProperties {
		normalizeSchema(sub)
	}
	for _, sub := range s.Definitions {
		normalizeSchema(sub)
	}
}

func sortProperties(in *orderedmap.OrderedMap[string, *jsonschema.Schema]) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
	keys := make([]string, 0, in.Len())
	for pair := in.Oldest(); pair != nil; pair = pair.Next() {
		keys = append(keys, pair.Key)
	}
	sort.Strings(keys)
	out := orderedmap.New[string, *jsonschema.Schema]()
	for _, k := range keys {
		v, _ := in.Get(k)
		out.Set(k, v)
	}
	return out
}
