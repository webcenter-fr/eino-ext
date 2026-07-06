package argocd

import (
	_ "embed"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// listOutputGuidance is a shared guidance block appended to the description of all
// list tools. It instructs the model to narrow queries to avoid large responses.
//
//go:embed prompts/list_output_guidance.md
var listOutputGuidance string

// describeOutputGuidance is a shared guidance block appended to the description of all
// describe tools. It instructs the model to narrow the output to avoid large responses.
//
//go:embed prompts/describe_output_guidance.md
var describeOutputGuidance string

// MustMarshal marshals v to JSON. Panics on error.
// This is safe to use here because Go structs cannot fail JSON serialization in practice.
func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// instanceNotFoundError returns an error indicating the requested instance is unknown.
func instanceNotFoundError(instance string, known []string) error {
	return errors.Errorf("ArgoCD instance not found: %s. Instance must be one of: %s", instance, strings.Join(known, ", "))
}
