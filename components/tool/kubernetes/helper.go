package kubernetes

import (
	_ "embed"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// listOutputGuidance is a shared guidance block appended to the description of all
// list tools (generic and resource list). It instructs the model to narrow queries
// to avoid large responses that blow up the context window.
//
//go:embed prompts/list_output_guidance.md
var listOutputGuidance string

// describeOutputGuidance is a shared guidance block appended to the description of all
// describe tools. It instructs the model to narrow the output to avoid large responses.
//
//go:embed prompts/describe_output_guidance.md
var describeOutputGuidance string

// missingManifestFields returns the manifest fields required by the resource
// tools that are absent from obj. Used to produce an LLM-actionable error
// naming exactly which fields the caller must add.
func missingManifestFields(obj *unstructured.Unstructured) []string {
	var missing []string
	if obj.GetAPIVersion() == "" {
		missing = append(missing, "apiVersion")
	}
	if obj.GetKind() == "" {
		missing = append(missing, "kind")
	}
	if obj.GetName() == "" {
		missing = append(missing, "metadata.name")
	}
	return missing
}

// joinFields joins field names with ", " for use in error messages.
func joinFields(fields []string) string {
	return strings.Join(fields, ", ")
}
